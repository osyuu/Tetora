package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"tetora/internal/log"
)

// --- Default Phase Emojis ---

const (
	ReactionPhaseQueued   = "queued"
	ReactionPhaseThinking = "thinking"
	ReactionPhaseTool     = "tool"
	ReactionPhaseDone     = "done"
	ReactionPhaseError    = "error"
)

// DefaultReactionEmojis returns the default phase-to-emoji mapping.
func DefaultReactionEmojis() map[string]string {
	return map[string]string{
		ReactionPhaseQueued:   "\u23F3",     // hourglass
		ReactionPhaseThinking: "\U0001F914", // thinking face
		ReactionPhaseTool:     "\U0001F527", // wrench
		ReactionPhaseDone:     "\u2705",     // white check mark
		ReactionPhaseError:    "\u274C",     // cross mark
	}
}

// ValidReactionPhases returns all valid lifecycle phase names.
func ValidReactionPhases() []string {
	return []string{
		ReactionPhaseQueued,
		ReactionPhaseThinking,
		ReactionPhaseTool,
		ReactionPhaseDone,
		ReactionPhaseError,
	}
}

// --- Reaction Manager ---

// ReactionManager manages lifecycle emoji reactions on Discord messages.
type ReactionManager struct {
	client       *Client
	defaultEmoji map[string]string
	overrides    map[string]string
	mu           sync.Mutex
	current      map[string]string // "channelID:messageID" -> current phase
}

// NewReactionManager creates a new reaction manager with optional emoji overrides.
func NewReactionManager(client *Client, overrides map[string]string) *ReactionManager {
	return &ReactionManager{
		client:       client,
		defaultEmoji: DefaultReactionEmojis(),
		overrides:    overrides,
		current:      make(map[string]string),
	}
}

func reactionKey(channelID, messageID string) string {
	return channelID + ":" + messageID
}

// EmojiForPhase returns the emoji string for a given phase, checking overrides first.
func (rm *ReactionManager) EmojiForPhase(phase string) string {
	if rm.overrides != nil {
		if emoji, ok := rm.overrides[phase]; ok && emoji != "" {
			return emoji
		}
	}
	if emoji, ok := rm.defaultEmoji[phase]; ok {
		return emoji
	}
	return ""
}

// SetPhase transitions a message to a new lifecycle phase.
func (rm *ReactionManager) SetPhase(channelID, messageID, phase string) {
	if channelID == "" || messageID == "" || phase == "" {
		return
	}

	newEmoji := rm.EmojiForPhase(phase)
	if newEmoji == "" {
		log.Debug("discord reactions: unknown phase", "phase", phase)
		return
	}

	key := reactionKey(channelID, messageID)

	rm.mu.Lock()
	prevPhase := rm.current[key]
	rm.current[key] = phase
	rm.mu.Unlock()

	if prevPhase != "" && prevPhase != phase {
		prevEmoji := rm.EmojiForPhase(prevPhase)
		if prevEmoji != "" {
			rm.removeReaction(channelID, messageID, prevEmoji)
		}
	}

	rm.addReaction(channelID, messageID, newEmoji)

	log.Debug("discord reaction phase set",
		"channel", channelID, "message", messageID,
		"phase", phase, "emoji", newEmoji,
		"prevPhase", prevPhase)
}

// ClearPhase removes tracking for a message.
func (rm *ReactionManager) ClearPhase(channelID, messageID string) {
	key := reactionKey(channelID, messageID)
	rm.mu.Lock()
	delete(rm.current, key)
	rm.mu.Unlock()
}

// GetCurrentPhase returns the current phase for a message.
func (rm *ReactionManager) GetCurrentPhase(channelID, messageID string) string {
	key := reactionKey(channelID, messageID)
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.current[key]
}

// --- Discord API Calls ---

func (rm *ReactionManager) addReaction(channelID, messageID, emoji string) {
	if rm.client == nil {
		return
	}
	encoded := url.PathEscape(emoji)
	path := fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", channelID, messageID, encoded)
	rm.request("PUT", path, nil)
}

func (rm *ReactionManager) removeReaction(channelID, messageID, emoji string) {
	if rm.client == nil {
		return
	}
	encoded := url.PathEscape(emoji)
	path := fmt.Sprintf("/channels/%s/messages/%s/reactions/%s/@me", channelID, messageID, encoded)
	rm.request("DELETE", path, nil)
}

// request performs a generic HTTP request to the Discord API.
func (rm *ReactionManager) request(method, path string, payload any) (int, []byte) {
	if rm.client == nil || rm.client.HTTPClient == nil {
		return 0, nil
	}
	var bodyStr string
	if payload != nil {
		body, _ := json.Marshal(payload)
		bodyStr = string(body)
	}

	reqBody := strings.NewReader(bodyStr)
	req, err := http.NewRequest(method, APIBase+path, reqBody)
	if err != nil {
		log.Error("discord api request error", "method", method, "path", path, "error", err)
		return 0, nil
	}
	if bodyStr != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bot "+rm.client.Token)

	resp, err := rm.client.HTTPClient.Do(req)
	if err != nil {
		log.Error("discord api send failed", "method", method, "path", path, "error", err)
		return 0, nil
	}
	defer resp.Body.Close()

	var respBody []byte
	if resp.Body != nil {
		respBody, _ = io.ReadAll(io.LimitReader(resp.Body, 8192))
	}

	if resp.StatusCode >= 400 {
		log.Warn("discord api error", "method", method, "path", path,
			"status", resp.StatusCode, "body", string(respBody))
	}

	return resp.StatusCode, respBody
}

// --- Lifecycle Integration Helpers ---

func (rm *ReactionManager) ReactQueued(channelID, messageID string) {
	rm.SetPhase(channelID, messageID, ReactionPhaseQueued)
}

func (rm *ReactionManager) ReactThinking(channelID, messageID string) {
	rm.SetPhase(channelID, messageID, ReactionPhaseThinking)
}

func (rm *ReactionManager) ReactTool(channelID, messageID string) {
	rm.SetPhase(channelID, messageID, ReactionPhaseTool)
}

func (rm *ReactionManager) ReactDone(channelID, messageID string) {
	rm.SetPhase(channelID, messageID, ReactionPhaseDone)
	rm.ClearPhase(channelID, messageID)
}

func (rm *ReactionManager) ReactError(channelID, messageID string) {
	rm.SetPhase(channelID, messageID, ReactionPhaseError)
	rm.ClearPhase(channelID, messageID)
}
