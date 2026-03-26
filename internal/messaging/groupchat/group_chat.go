// Package groupchat implements group chat state management for messaging platforms.
package groupchat

import (
	"strings"
	"sync"
	"time"
)

// RateLimitConfig configures group chat rate limiting.
type RateLimitConfig struct {
	MaxPerMin int  `json:"maxPerMin,omitempty"` // default 5
	PerGroup  bool `json:"perGroup,omitempty"`  // per-group vs global rate limit
}

// Config holds group chat intelligence configuration.
type Config struct {
	Activation    string              `json:"activation,omitempty"`    // "mention", "keyword", "all"
	Keywords      []string            `json:"keywords,omitempty"`      // trigger keywords for "keyword" mode
	ContextWindow int                 `json:"contextWindow,omitempty"` // messages to include for context (default 10)
	RateLimit     RateLimitConfig     `json:"rateLimit,omitempty"`
	AllowedGroups map[string][]string `json:"allowedGroups,omitempty"` // platform → group IDs
	ThreadReply   bool                `json:"threadReply,omitempty"`   // reply in threads
	MentionNames  []string            `json:"mentionNames,omitempty"`  // names that trigger activation
	AgentNames    []string            `json:"agentNames,omitempty"`    // agent names used to populate MentionNames defaults
}

// Message represents a single group chat message.
type Message struct {
	Platform  string
	GroupID   string
	SenderID  string
	Text      string
	Timestamp time.Time
}

// Status represents the current group chat status for dashboard.
type Status struct {
	ActiveGroups  int            `json:"activeGroups"`
	TotalMessages int            `json:"totalMessages"`
	RateLimits    map[string]int `json:"rateLimits"` // groupID → remaining
}

// Engine manages group chat state across platforms.
type Engine struct {
	cfg *Config

	mu            sync.RWMutex
	messages      map[string][]Message      // platform:groupID → messages (ring buffer)
	rateLimitData map[string][]time.Time    // platform:groupID or "global" → timestamps (sliding window)
}

// New creates a new GroupChatEngine with the given config.
func New(cfg *Config) *Engine {
	if cfg == nil {
		return nil
	}

	// Apply defaults if not set.
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = 10
	}
	if cfg.RateLimit.MaxPerMin <= 0 {
		cfg.RateLimit.MaxPerMin = 5
	}
	if cfg.Activation == "" {
		cfg.Activation = "mention"
	}

	// Default mention names = agent names (if not already set).
	if len(cfg.MentionNames) == 0 && len(cfg.AgentNames) > 0 {
		cfg.MentionNames = append(cfg.MentionNames, cfg.AgentNames...)
		cfg.MentionNames = append(cfg.MentionNames, "tetora", "テトラ")
	}

	return &Engine{
		cfg:           cfg,
		messages:      make(map[string][]Message),
		rateLimitData: make(map[string][]time.Time),
	}
}

// ShouldRespond decides whether to respond based on activation mode.
func (e *Engine) ShouldRespond(platform, groupID, senderID, messageText string) bool {
	if e == nil || e.cfg == nil {
		return false
	}

	// Check if group is allowed (if whitelist configured).
	if !e.IsAllowedGroup(platform, groupID) {
		return false
	}

	// Check rate limit.
	if !e.CheckRateLimit(platform, groupID) {
		return false
	}

	// Check activation mode.
	mode := e.cfg.Activation
	switch mode {
	case "all":
		return true
	case "keyword":
		return e.matchesKeyword(messageText)
	case "mention":
		return e.matchesMention(messageText)
	default:
		// Unknown mode → default to mention.
		return e.matchesMention(messageText)
	}
}

// matchesMention checks if message contains any of the configured mention names (case insensitive).
func (e *Engine) matchesMention(messageText string) bool {
	lowerText := strings.ToLower(messageText)
	for _, name := range e.cfg.MentionNames {
		if strings.Contains(lowerText, strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// matchesKeyword checks if message contains any of the configured keywords (case insensitive).
func (e *Engine) matchesKeyword(messageText string) bool {
	lowerText := strings.ToLower(messageText)
	for _, kw := range e.cfg.Keywords {
		if strings.Contains(lowerText, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// CheckRateLimit checks if the rate limit allows a new message.
// Returns true if allowed, false if rate limited.
func (e *Engine) CheckRateLimit(platform, groupID string) bool {
	if e == nil {
		return false
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	key := e.rateLimitKey(platform, groupID)
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	// Remove timestamps older than 60s (sliding window).
	timestamps := e.rateLimitData[key]
	var valid []time.Time
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	e.rateLimitData[key] = valid

	// Check if under limit.
	maxPerMin := e.cfg.RateLimit.MaxPerMin
	if len(valid) >= maxPerMin {
		return false
	}

	// Record this message timestamp.
	e.rateLimitData[key] = append(valid, now)
	return true
}

// rateLimitKey returns the key for rate limit tracking.
// If PerGroup=true, key is platform:groupID, else "global".
func (e *Engine) rateLimitKey(platform, groupID string) string {
	if e.cfg.RateLimit.PerGroup {
		return platform + ":" + groupID
	}
	return "global"
}

// IsAllowedGroup checks if a group is whitelisted (if whitelist configured).
func (e *Engine) IsAllowedGroup(platform, groupID string) bool {
	if e == nil || e.cfg == nil {
		return false
	}

	// If no whitelist configured, allow all.
	if len(e.cfg.AllowedGroups) == 0 {
		return true
	}

	// Check if platform has whitelist.
	allowed, ok := e.cfg.AllowedGroups[platform]
	if !ok {
		return false
	}

	// Check if groupID is in the whitelist.
	for _, id := range allowed {
		if id == groupID {
			return true
		}
	}
	return false
}

// RecordMessage records a message for context window.
func (e *Engine) RecordMessage(platform, groupID, senderID, messageText string) {
	if e == nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	key := platform + ":" + groupID
	msg := Message{
		Platform:  platform,
		GroupID:   groupID,
		SenderID:  senderID,
		Text:      messageText,
		Timestamp: time.Now(),
	}

	messages := e.messages[key]
	// Ring buffer: keep last N messages.
	maxSize := e.cfg.ContextWindow
	if len(messages) >= maxSize {
		// Shift left (remove oldest).
		messages = messages[1:]
	}
	messages = append(messages, msg)
	e.messages[key] = messages
}

// GetContextMessages returns recent messages for context (up to limit).
func (e *Engine) GetContextMessages(platform, groupID string, limit int) []Message {
	if e == nil {
		return nil
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	key := platform + ":" + groupID
	messages := e.messages[key]

	if limit <= 0 || limit > len(messages) {
		limit = len(messages)
	}

	// Return last N messages.
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	return messages[start:]
}

// Status returns the current group chat status for dashboard.
func (e *Engine) Status() Status {
	if e == nil {
		return Status{
			ActiveGroups:  0,
			TotalMessages: 0,
			RateLimits:    make(map[string]int),
		}
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	status := Status{
		ActiveGroups:  len(e.messages),
		TotalMessages: 0,
		RateLimits:    make(map[string]int),
	}

	// Count total messages.
	for _, msgs := range e.messages {
		status.TotalMessages += len(msgs)
	}

	// Calculate rate limits remaining.
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)
	maxPerMin := e.cfg.RateLimit.MaxPerMin

	for key, timestamps := range e.rateLimitData {
		count := 0
		for _, ts := range timestamps {
			if ts.After(cutoff) {
				count++
			}
		}
		remaining := maxPerMin - count
		if remaining < 0 {
			remaining = 0
		}
		status.RateLimits[key] = remaining
	}

	return status
}
