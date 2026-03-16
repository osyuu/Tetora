// Package imessage provides iMessage integration via BlueBubbles.
package imessage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/messaging"
)

// --- BlueBubbles Message Types ---

// WebhookPayload represents an incoming webhook event from BlueBubbles.
type WebhookPayload struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Message represents a message from BlueBubbles.
type Message struct {
	GUID     string `json:"guid"`
	ChatGUID string `json:"chatGuid"`
	Text     string `json:"text"`
	Handle   struct {
		Address string `json:"address"`
	} `json:"handle"`
	DateCreated int64 `json:"dateCreated"`
	IsFromMe    bool  `json:"isFromMe"`
}

// BBMessage is a simplified message for search/read results.
type BBMessage struct {
	GUID        string `json:"guid"`
	ChatGUID    string `json:"chatGuid"`
	Text        string `json:"text"`
	Handle      string `json:"handle"`
	DateCreated int64  `json:"dateCreated"`
	IsFromMe    bool   `json:"isFromMe"`
}

// --- iMessage Bot ---

// Bot handles incoming iMessage messages via BlueBubbles.
type Bot struct {
	cfg       Config
	rt        messaging.BotRuntime
	serverURL string
	password  string
	dedup     map[string]time.Time // message GUID -> timestamp for dedup
	mu        sync.Mutex
	client    *http.Client
}

// NewBot creates a new Bot instance.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	serverURL := strings.TrimRight(cfg.ServerURL, "/")
	return &Bot{
		cfg:       cfg,
		rt:        rt,
		serverURL: serverURL,
		password:  cfg.Password,
		dedup:     make(map[string]time.Time),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// SetTyping is a no-op; BlueBubbles API does not support typing indicators.
func (b *Bot) SetTyping(_ context.Context, _ string) error {
	return nil
}

// PresenceName returns the channel name for presence tracking.
func (b *Bot) PresenceName() string { return "imessage" }

// --- Webhook Handler ---

// WebhookHandler handles incoming BlueBubbles webhook POST events.
func (b *Bot) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.rt.LogError("imessage: read webhook body failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		b.rt.LogError("imessage: parse webhook payload failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Return 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Only handle new-message events.
	if payload.Type != "new-message" {
		b.rt.LogDebugCtx(context.Background(), "imessage: non-message event ignored", "type", payload.Type)
		return
	}

	var msg Message
	if err := json.Unmarshal(payload.Data, &msg); err != nil {
		b.rt.LogError("imessage: parse message data failed", err)
		return
	}

	go b.HandleMessage(msg)
}

// HandleMessage processes an incoming BlueBubbles message.
func (b *Bot) HandleMessage(msg Message) {
	// Skip messages from self.
	if msg.IsFromMe {
		b.rt.LogDebugCtx(context.Background(), "imessage: skipping own message", "guid", msg.GUID)
		return
	}

	// Skip empty messages.
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		b.rt.LogDebugCtx(context.Background(), "imessage: empty message ignored", "guid", msg.GUID)
		return
	}

	// Dedup check.
	b.mu.Lock()
	if _, seen := b.dedup[msg.GUID]; seen {
		b.mu.Unlock()
		b.rt.LogDebugCtx(context.Background(), "imessage: duplicate message ignored", "guid", msg.GUID)
		return
	}
	b.dedup[msg.GUID] = time.Now()

	// Cleanup old dedup entries (older than 5 minutes).
	cutoff := time.Now().Add(-5 * time.Minute)
	for key, t := range b.dedup {
		if t.Before(cutoff) {
			delete(b.dedup, key)
		}
	}
	b.mu.Unlock()

	// Check allowed chats.
	if len(b.cfg.AllowedChats) > 0 {
		allowed := false
		for _, chat := range b.cfg.AllowedChats {
			if chat == msg.ChatGUID || chat == msg.Handle.Address {
				allowed = true
				break
			}
		}
		if !allowed {
			b.rt.LogDebugCtx(context.Background(), "imessage: chat not in allowedChats", "chatGuid", msg.ChatGUID, "handle", msg.Handle.Address)
			return
		}
	}

	b.rt.LogInfo("imessage: received message", "from", msg.Handle.Address, "chatGuid", msg.ChatGUID, "text", b.rt.Truncate(text, 100))

	// Dispatch to agent.
	b.dispatchToAgent(text, msg)
}

// dispatchToAgent dispatches a message to the agent system and replies via iMessage.
func (b *Bot) dispatchToAgent(text string, msg Message) {
	traceID := b.rt.NewTraceID("imessage")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	// Route to determine agent.
	role := b.cfg.DefaultAgent
	if role == "" {
		var err error
		role, err = b.rt.Route(ctx, text, "imessage")
		if err != nil {
			b.rt.LogErrorCtx(ctx, "imessage: route error", err)
		}
		b.rt.LogInfoCtx(ctx, "imessage route result", "agent", role)
	}

	// Find or create session.
	chKey := "imessage:" + msg.Handle.Address + ":" + msg.ChatGUID
	sessID, err := b.rt.GetOrCreateSession("imessage", chKey, role, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "imessage session error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sessID != "" {
		sessionCtx := b.rt.BuildSessionContext(sessID, b.rt.SessionContextLimit())
		if sessionCtx != "" {
			contextPrompt = sessionCtx + "\n\n" + text
		}

		// Record user message to session.
		b.rt.AddSessionMessage(sessID, "user", messaging.TruncateStr(text, 5000))
		b.rt.UpdateSessionStats(sessID, 0, 0, 0, 1)

		title := text
		if len(title) > 100 {
			title = title[:100]
		}
		b.rt.UpdateSessionTitle(sessID, title)
	}

	// Fill task defaults and apply agent-specific config.
	taskID := b.rt.FillTaskDefaults(&role, nil, "imessage")

	soulPrompt, _ := b.rt.LoadAgentPrompt(role)
	model, permMode, _ := b.rt.AgentConfig(role)

	// Expand prompt variables.
	contextPrompt = b.rt.ExpandPrompt(contextPrompt, role)

	// Run task.
	result, _ := b.rt.Submit(ctx, messaging.TaskRequest{
		AgentRole:      role,
		Content:        contextPrompt,
		SessionID:      sessID,
		SystemPrompt:   soulPrompt,
		Model:          model,
		PermissionMode: permMode,
		Meta:           map[string]string{"source": "imessage"},
	})

	// Record to history.
	b.rt.RecordHistory(taskID, "", "imessage", role, result.OutputFile, nil, result)

	// Record assistant response to session.
	if sessID != "" {
		msgRole := "assistant"
		content := messaging.TruncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, messaging.TruncateStr(errMsg, 2000))
		}
		b.rt.AddSessionMessage(sessID, msgRole, content)
		b.rt.UpdateSessionStats(sessID, result.CostUSD, result.TokensIn, result.TokensOut, 0)
	}

	// Build response.
	response := result.Output
	if result.Error != "" {
		response = fmt.Sprintf("Error: %s", result.Error)
	}

	// iMessage has no strict length limit but keep reasonable.
	if len(response) > 4000 {
		response = response[:3997] + "..."
	}

	// Send response.
	if err := b.SendMessage(msg.ChatGUID, response); err != nil {
		b.rt.LogError("imessage: send response failed", err, "chatGuid", msg.ChatGUID)
	}

	b.rt.LogInfoCtx(ctx, "imessage task complete", "taskID", taskID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	b.rt.PublishEvent("imessage", map[string]interface{}{
		"from":     msg.Handle.Address,
		"chatGuid": msg.ChatGUID,
		"taskID":   taskID,
		"status":   result.Status,
		"cost":     result.CostUSD,
	})
}

// --- BlueBubbles API Methods ---

// SendMessage sends a text message to a chat via BlueBubbles API.
// POST /api/v1/message/text?password=...
func (b *Bot) SendMessage(chatGUID, text string) error {
	if chatGUID == "" || text == "" {
		return fmt.Errorf("imessage: empty chatGUID or message")
	}

	payload := map[string]string{
		"chatGuid": chatGUID,
		"message":  text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("imessage: marshal send request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/message/text?password=%s", b.serverURL, b.password)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("imessage: create send request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("imessage: send request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imessage: send HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	b.rt.LogDebugCtx(context.Background(), "imessage: message sent", "chatGuid", chatGUID, "status", resp.StatusCode)
	return nil
}

// SearchMessages searches messages via BlueBubbles API.
// GET /api/v1/message/search?password=...&query=...&limit=...
func (b *Bot) SearchMessages(query string, limit int) ([]BBMessage, error) {
	if query == "" {
		return nil, fmt.Errorf("imessage: empty search query")
	}
	if limit <= 0 {
		limit = 10
	}

	url := fmt.Sprintf("%s/api/v1/message/search?password=%s&query=%s&limit=%d",
		b.serverURL, b.password, query, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("imessage: create search request: %w", err)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imessage: search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("imessage: search HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []Message `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("imessage: parse search response: %w", err)
	}

	messages := make([]BBMessage, 0, len(result.Data))
	for _, m := range result.Data {
		messages = append(messages, BBMessage{
			GUID:        m.GUID,
			ChatGUID:    m.ChatGUID,
			Text:        m.Text,
			Handle:      m.Handle.Address,
			DateCreated: m.DateCreated,
			IsFromMe:    m.IsFromMe,
		})
	}
	return messages, nil
}

// ReadRecentMessages reads recent messages from a chat via BlueBubbles API.
// GET /api/v1/chat/{chatGUID}/message?password=...&limit=...
func (b *Bot) ReadRecentMessages(chatGUID string, limit int) ([]BBMessage, error) {
	if chatGUID == "" {
		return nil, fmt.Errorf("imessage: empty chatGUID")
	}
	if limit <= 0 {
		limit = 20
	}

	url := fmt.Sprintf("%s/api/v1/chat/%s/message?password=%s&limit=%d",
		b.serverURL, chatGUID, b.password, limit)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("imessage: create read request: %w", err)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imessage: read request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("imessage: read HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []Message `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("imessage: parse read response: %w", err)
	}

	messages := make([]BBMessage, 0, len(result.Data))
	for _, m := range result.Data {
		messages = append(messages, BBMessage{
			GUID:        m.GUID,
			ChatGUID:    m.ChatGUID,
			Text:        m.Text,
			Handle:      m.Handle.Address,
			DateCreated: m.DateCreated,
			IsFromMe:    m.IsFromMe,
		})
	}
	return messages, nil
}

// SendTapback sends a tapback reaction on a message via BlueBubbles API.
// POST /api/v1/message/{guid}/tapback?password=...
func (b *Bot) SendTapback(chatGUID, messageGUID string, tapback int) error {
	if messageGUID == "" {
		return fmt.Errorf("imessage: empty message GUID for tapback")
	}

	payload := map[string]interface{}{
		"chatGuid": chatGUID,
		"tapback":  tapback,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("imessage: marshal tapback request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/message/%s/tapback?password=%s", b.serverURL, messageGUID, b.password)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("imessage: create tapback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("imessage: tapback request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imessage: tapback HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	b.rt.LogDebugCtx(context.Background(), "imessage: tapback sent", "messageGuid", messageGUID, "tapback", tapback)
	return nil
}

// --- Notifier Interfaces ---

// Send sends a notification to the first allowed chat (Notifier interface).
func (b *Bot) Send(text string) error {
	if text == "" {
		return nil
	}
	if len(text) > 4000 {
		text = text[:3997] + "..."
	}
	// Send to first allowed chat.
	if len(b.cfg.AllowedChats) > 0 {
		return b.SendMessage(b.cfg.AllowedChats[0], text)
	}
	return fmt.Errorf("imessage: no allowed chats configured for notification")
}

// Name returns the notifier name (Notifier interface).
func (b *Bot) Name() string { return "imessage" }

// --- Notifier ---

// Notifier sends notifications via BlueBubbles iMessage API (standalone, for buildNotifiers).
type Notifier struct {
	Config   Config
	ChatGUID string // target chat GUID
}

// Send sends a text notification to the configured chat.
func (n *Notifier) Send(text string) error {
	if text == "" {
		return nil
	}
	if len(text) > 4000 {
		text = text[:3997] + "..."
	}

	serverURL := strings.TrimRight(n.Config.ServerURL, "/")
	payload := map[string]string{
		"chatGuid": n.ChatGUID,
		"message":  text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("imessage: marshal notification: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/message/text?password=%s", serverURL, n.Config.Password)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("imessage: create notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("imessage: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imessage: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the notifier name.
func (n *Notifier) Name() string { return "imessage" }
