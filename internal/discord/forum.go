package discord

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// Forum status constants.
const (
	ForumStatusBacklog = "backlog"
	ForumStatusTodo    = "todo"
	ForumStatusDoing   = "doing"
	ForumStatusReview  = "review"
	ForumStatusDone    = "done"
)

// ValidForumStatuses returns all recognized forum board statuses.
func ValidForumStatuses() []string {
	return []string{ForumStatusBacklog, ForumStatusTodo, ForumStatusDoing, ForumStatusReview, ForumStatusDone}
}

// IsValidForumStatus checks if a status string is recognized.
func IsValidForumStatus(status string) bool {
	for _, s := range ValidForumStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

// ForumBoardDeps holds external dependencies for the forum board.
type ForumBoardDeps struct {
	// ValidateAgent returns true if the agent name exists.
	ValidateAgent func(name string) bool
	// AvailableRoles returns available agent role names.
	AvailableRoles func() []string
	// BindThread binds a thread to an agent, returns session ID.
	BindThread func(guildID, threadID, role string) string
	// ThreadBindingsEnabled returns whether thread bindings are enabled.
	ThreadBindingsEnabled bool
}

// ForumBoard manages a Discord Forum channel as a task board.
type ForumBoard struct {
	client *Client
	cfg    config.DiscordForumBoardConfig
	deps   ForumBoardDeps
}

// NewForumBoard creates a new forum board manager.
func NewForumBoard(client *Client, cfg config.DiscordForumBoardConfig, deps ForumBoardDeps) *ForumBoard {
	return &ForumBoard{client: client, cfg: cfg, deps: deps}
}

// CreateThread creates a new forum thread with an initial status tag.
func (fb *ForumBoard) CreateThread(title, body, status string) (string, error) {
	if fb.cfg.ForumChannelID == "" {
		return "", fmt.Errorf("forum channel ID not configured")
	}
	if title == "" {
		return "", fmt.Errorf("thread title is required")
	}
	if status == "" {
		status = ForumStatusBacklog
	}
	if !IsValidForumStatus(status) {
		return "", fmt.Errorf("invalid status %q, valid: %s", status, strings.Join(ValidForumStatuses(), ", "))
	}
	if len(title) > 100 {
		title = title[:97] + "..."
	}

	appliedTags := fb.tagsForStatus(status)
	payload := map[string]any{
		"name": title,
		"message": map[string]any{
			"content": fb.formatThreadBody(body, status),
		},
	}
	if len(appliedTags) > 0 {
		payload["applied_tags"] = appliedTags
	}

	path := fmt.Sprintf("/channels/%s/threads", fb.cfg.ForumChannelID)
	statusCode, respBody := fb.client.RequestRaw("POST", path, payload)
	if statusCode == 0 && respBody == nil {
		return "", fmt.Errorf("discord API unavailable (bot not connected)")
	}
	if statusCode < 200 || statusCode >= 300 {
		return "", fmt.Errorf("create forum thread failed: status %d, body: %s", statusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse thread response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("created thread has no ID")
	}

	log.Info("discord forum thread created", "thread", result.ID, "title", title, "status", status)
	return result.ID, nil
}

func (fb *ForumBoard) formatThreadBody(body, status string) string {
	if body == "" {
		body = "(No description)"
	}
	return fmt.Sprintf("**Status:** `%s`\n\n%s\n\n_Created via Tetora at %s_",
		status, body, time.Now().Format(time.RFC3339))
}

// SetStatus updates the status tag on a forum thread.
func (fb *ForumBoard) SetStatus(threadID, status string) error {
	if threadID == "" {
		return fmt.Errorf("thread ID is required")
	}
	if !IsValidForumStatus(status) {
		return fmt.Errorf("invalid status %q, valid: %s", status, strings.Join(ValidForumStatuses(), ", "))
	}

	payload := map[string]any{"applied_tags": fb.tagsForStatus(status)}
	path := fmt.Sprintf("/channels/%s", threadID)
	statusCode, respBody := fb.client.RequestRaw("PATCH", path, payload)
	if statusCode == 0 && respBody == nil {
		return fmt.Errorf("discord API unavailable (bot not connected)")
	}
	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("set forum status failed: status %d, body: %s", statusCode, string(respBody))
	}

	log.Info("discord forum status updated", "thread", threadID, "status", status)
	return nil
}

func (fb *ForumBoard) tagsForStatus(status string) []string {
	if fb.cfg.Tags == nil {
		return nil
	}
	tagID, ok := fb.cfg.Tags[status]
	if !ok || tagID == "" {
		return nil
	}
	return []string{tagID}
}

// HandleAssign assigns an agent to a forum thread.
func (fb *ForumBoard) HandleAssign(threadID, guildID, role string) error {
	if threadID == "" {
		return fmt.Errorf("thread ID is required")
	}
	if role == "" {
		return fmt.Errorf("agent is required")
	}

	if fb.deps.ValidateAgent == nil || !fb.deps.ValidateAgent(role) {
		return fmt.Errorf("unknown agent %q", role)
	}

	if fb.deps.ThreadBindingsEnabled && fb.deps.BindThread != nil {
		sessionID := fb.deps.BindThread(guildID, threadID, role)
		log.Info("discord forum thread assigned", "thread", threadID, "agent", role, "session", sessionID)
	}

	if err := fb.SetStatus(threadID, ForumStatusDoing); err != nil {
		log.Warn("discord forum auto-status failed", "thread", threadID, "error", err)
	}

	return nil
}

// HandleCompletion updates a forum thread status when a task completes.
func (fb *ForumBoard) HandleCompletion(threadID string, success bool) {
	if threadID == "" {
		return
	}
	newStatus := ForumStatusDone
	if !success {
		newStatus = ForumStatusReview
	}
	if err := fb.SetStatus(threadID, newStatus); err != nil {
		log.Warn("discord forum completion status failed", "thread", threadID, "success", success, "error", err)
	}
}

// HandleAssignCommand processes the /assign <agent> command.
func (fb *ForumBoard) HandleAssignCommand(channelID, guildID, args string) string {
	role := strings.TrimSpace(strings.ToLower(args))
	if role == "" {
		var available []string
		if fb.deps.AvailableRoles != nil {
			available = fb.deps.AvailableRoles()
		}
		return fmt.Sprintf("Usage: `/assign <agent>` -- Available agents: %s", strings.Join(available, ", "))
	}
	if err := fb.HandleAssign(channelID, guildID, role); err != nil {
		return fmt.Sprintf("Failed to assign: %v", err)
	}
	return fmt.Sprintf("Thread assigned to agent **%s** and status set to `doing`.", role)
}

// HandleStatusCommand processes the /status <status> command.
func (fb *ForumBoard) HandleStatusCommand(channelID, args string) string {
	status := strings.TrimSpace(strings.ToLower(args))
	if status == "" {
		return fmt.Sprintf("Usage: `/status <status>` -- Valid statuses: %s", strings.Join(ValidForumStatuses(), ", "))
	}
	if !IsValidForumStatus(status) {
		return fmt.Sprintf("Invalid status `%s`. Valid: %s", status, strings.Join(ValidForumStatuses(), ", "))
	}
	if err := fb.SetStatus(channelID, status); err != nil {
		return fmt.Sprintf("Failed to update status: %v", err)
	}
	return fmt.Sprintf("Thread status updated to `%s`.", status)
}

// IsConfigured returns whether the forum board is configured and enabled.
func (fb *ForumBoard) IsConfigured() bool {
	return fb.cfg.Enabled && fb.cfg.ForumChannelID != ""
}

// ValidateForumBoardConfig checks the forum board configuration for common issues.
func ValidateForumBoardConfig(cfg config.DiscordForumBoardConfig) []string {
	var warnings []string
	if cfg.Enabled && cfg.ForumChannelID == "" {
		warnings = append(warnings, "discord.forumBoard.enabled=true but forumChannelId is empty")
	}
	if cfg.Tags != nil {
		for status, tagID := range cfg.Tags {
			if !IsValidForumStatus(status) {
				warnings = append(warnings, fmt.Sprintf("discord.forumBoard.tags: unknown status %q", status))
			}
			if tagID == "" {
				warnings = append(warnings, fmt.Sprintf("discord.forumBoard.tags.%s: empty tag ID", status))
			}
		}
	}
	return warnings
}
