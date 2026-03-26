// Package signal provides Signal bot integration via signal-cli-rest-api.
package signal

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

// --- Signal Message Types ---

// receivePayload represents an incoming message from signal-cli-rest-api webhook.
type receivePayload struct {
	Envelope envelope `json:"envelope"`
}

// envelope contains the message envelope.
type envelope struct {
	Source        string         `json:"source"`      // sender phone number
	SourceName    string         `json:"sourceName"`  // sender name
	SourceUUID    string         `json:"sourceUuid"`  // sender UUID
	Timestamp     int64          `json:"timestamp"`   // message timestamp (ms)
	DataMessage   *dataMessage   `json:"dataMessage,omitempty"`
	SyncMessage   *syncMessage   `json:"syncMessage,omitempty"`
	CallMessage   *callMessage   `json:"callMessage,omitempty"`
	TypingMessage *typingMessage `json:"typingMessage,omitempty"`
}

// dataMessage represents a text/attachment message.
type dataMessage struct {
	Timestamp        int64        `json:"timestamp"`
	Message          string       `json:"message"`
	ExpiresInSeconds int          `json:"expiresInSeconds"`
	GroupInfo        *groupInfo   `json:"groupInfo,omitempty"`
	Attachments      []attachment `json:"attachments,omitempty"`
	Mentions         []mention    `json:"mentions,omitempty"`
	Quote            *quote       `json:"quote,omitempty"`
	Reaction         *reaction    `json:"reaction,omitempty"`
}

// groupInfo contains group message context.
type groupInfo struct {
	GroupID string `json:"groupId"`
	Type    string `json:"type"` // "DELIVER", "UPDATE", "QUIT"
}

// attachment represents a file attachment.
type attachment struct {
	ContentType string `json:"contentType"`
	Filename    string `json:"filename,omitempty"`
	ID          string `json:"id"`
	Size        int    `json:"size"`
}

// mention represents an @mention in a message.
type mention struct {
	Name   string `json:"name"`
	Number string `json:"number"`
	UUID   string `json:"uuid"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
}

// quote represents a quoted/replied message.
type quote struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// reaction represents a reaction to a message.
type reaction struct {
	Emoji           string `json:"emoji"`
	TargetAuthor    string `json:"targetAuthor"`
	TargetTimestamp int64  `json:"targetTimestamp"`
	IsRemove        bool   `json:"isRemove"`
}

// syncMessage is for multi-device sync.
type syncMessage struct {
	SentMessage *dataMessage `json:"sentMessage,omitempty"`
}

// callMessage is for voice/video calls.
type callMessage struct {
	OfferMessage  json.RawMessage `json:"offerMessage,omitempty"`
	AnswerMessage json.RawMessage `json:"answerMessage,omitempty"`
	BusyMessage   json.RawMessage `json:"busyMessage,omitempty"`
	HangupMessage json.RawMessage `json:"hangupMessage,omitempty"`
}

// typingMessage is for typing indicators.
type typingMessage struct {
	Action    string `json:"action"` // "STARTED", "STOPPED"
	Timestamp int64  `json:"timestamp"`
	GroupID   string `json:"groupId,omitempty"`
}

// sendRequest is the payload for sending a message via signal-cli-rest-api.
type sendRequest struct {
	Number      string   `json:"number,omitempty"`      // recipient phone number (for DM)
	Recipients  []string `json:"recipients,omitempty"`  // multiple recipients
	GroupID     string   `json:"groupId,omitempty"`     // group ID (for group message)
	Message     string   `json:"message"`
	Attachments []string `json:"attachments,omitempty"` // base64-encoded or file paths
}

// --- Bot ---

// Bot handles incoming Signal messages via signal-cli-rest-api.
type Bot struct {
	cfg  Config
	rt   messaging.BotRuntime
	apiBase string // signal-cli-rest-api base URL

	// Dedup: track recently processed message timestamps.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client

	// Polling state.
	stopPolling chan struct{}
	pollingWg   sync.WaitGroup
}

// NewBot creates a new Bot instance.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	return &Bot{
		cfg:         cfg,
		rt:          rt,
		apiBase:     cfg.APIBaseURLOrDefault(),
		processed:   make(map[string]time.Time),
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		stopPolling: make(chan struct{}),
	}
}

// Start starts the polling loop if polling mode is enabled.
func (b *Bot) Start() {
	if !b.cfg.PollingMode {
		return
	}

	b.rt.LogInfo("signal: starting polling mode", "interval", b.cfg.PollIntervalOrDefault())
	b.pollingWg.Add(1)
	go b.pollLoop()
}

// Stop stops the polling loop.
func (b *Bot) Stop() {
	if !b.cfg.PollingMode {
		return
	}

	b.rt.LogInfo("signal: stopping polling")
	close(b.stopPolling)
	b.pollingWg.Wait()
	b.rt.LogInfo("signal: polling stopped")
}

// pollLoop continuously polls signal-cli-rest-api for new messages.
func (b *Bot) pollLoop() {
	defer b.pollingWg.Done()

	interval := time.Duration(b.cfg.PollIntervalOrDefault()) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopPolling:
			return
		case <-ticker.C:
			if err := b.fetchMessages(); err != nil {
				ctx := b.rt.WithTraceID(context.Background(), b.rt.NewTraceID("signal"))
				b.rt.LogDebugCtx(ctx, "signal: poll fetch failed", "error", err)
			}
		}
	}
}

// fetchMessages fetches new messages from signal-cli-rest-api polling endpoint.
// GET /v1/receive/{number}
func (b *Bot) fetchMessages() error {
	url := fmt.Sprintf("%s/v1/receive/%s", b.apiBase, b.cfg.PhoneNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("signal: create poll request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal: poll request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// No new messages.
		return nil
	}

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signal: poll HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response: array of envelopes.
	var envelopes []receivePayload
	if err := json.NewDecoder(resp.Body).Decode(&envelopes); err != nil {
		return fmt.Errorf("signal: parse poll response: %w", err)
	}

	// Process each envelope.
	for _, payload := range envelopes {
		b.processEnvelope(payload.Envelope)
	}

	return nil
}

// HandleWebhook handles incoming Signal webhook events.
// POST /api/signal/webhook
func (b *Bot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.rt.LogError("signal: read webhook body failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Parse payload.
	var payload receivePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		b.rt.LogError("signal: parse webhook payload failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Return 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process envelope asynchronously.
	go b.processEnvelope(payload.Envelope)
}

// processEnvelope processes a signal envelope.
func (b *Bot) processEnvelope(env envelope) {
	// Only handle data messages (text/attachments).
	if env.DataMessage == nil {
		ctx := b.rt.WithTraceID(context.Background(), b.rt.NewTraceID("signal"))
		b.rt.LogDebugCtx(ctx, "signal: non-data message ignored", "source", env.Source)
		return
	}

	msg := env.DataMessage
	if msg.Message == "" {
		ctx := b.rt.WithTraceID(context.Background(), b.rt.NewTraceID("signal"))
		b.rt.LogDebugCtx(ctx, "signal: empty message ignored", "source", env.Source)
		return
	}

	// Dedup: check if already processed using timestamp + source.
	dedupKey := fmt.Sprintf("%s:%d", env.Source, msg.Timestamp)
	b.mu.Lock()
	if _, seen := b.processed[dedupKey]; seen {
		b.mu.Unlock()
		ctx := b.rt.WithTraceID(context.Background(), b.rt.NewTraceID("signal"))
		b.rt.LogDebugCtx(ctx, "signal: duplicate message ignored", "key", dedupKey)
		return
	}
	b.processed[dedupKey] = time.Now()
	b.processedSize++

	// Cleanup old entries every 1000 messages.
	if b.processedSize > 1000 {
		cutoff := time.Now().Add(-1 * time.Hour)
		for key, t := range b.processed {
			if t.Before(cutoff) {
				delete(b.processed, key)
				b.processedSize--
			}
		}
	}
	b.mu.Unlock()

	text := strings.TrimSpace(msg.Message)
	if text == "" {
		return
	}

	// Determine if this is a group message or DM.
	isGroup := msg.GroupInfo != nil && msg.GroupInfo.GroupID != ""
	targetID := env.Source
	if isGroup {
		targetID = msg.GroupInfo.GroupID
	}

	b.rt.LogInfo("signal: received message",
		"from", env.SourceName,
		"source", env.Source,
		"group", isGroup,
		"text", b.rt.Truncate(text, 100),
	)

	// Dispatch to agent.
	b.dispatchToAgent(text, env, targetID, isGroup)
}

// dispatchToAgent dispatches a message to the agent system and replies via Signal.
func (b *Bot) dispatchToAgent(text string, env envelope, targetID string, isGroup bool) {
	traceID := b.rt.NewTraceID("signal")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	// Route to determine agent.
	role := b.cfg.DefaultAgent
	if role == "" {
		var err error
		role, err = b.rt.Route(ctx, text, "signal")
		if err != nil {
			b.rt.LogErrorCtx(ctx, "signal: route error", err)
		}
		b.rt.LogInfoCtx(ctx, "signal route result", "agent", role)
	}

	// Find or create session.
	chKey := "signal:" + env.Source + ":" + targetID
	sessID, err := b.rt.GetOrCreateSession("signal", chKey, role, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "signal session error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sessID != "" {
		sessionCtx := b.rt.BuildSessionContext(sessID, b.rt.SessionContextLimit())

		// Inline wrapWithContext: prepend session context if non-empty.
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
	taskID := b.rt.FillTaskDefaults(&role, nil, "signal")

	systemPrompt := ""
	if role != "" {
		if soulPrompt, err := b.rt.LoadAgentPrompt(role); err == nil && soulPrompt != "" {
			systemPrompt = soulPrompt
		}
	}

	model := ""
	permMode := ""
	if role != "" {
		if m, p, ok := b.rt.AgentConfig(role); ok {
			model = m
			permMode = p
		}
	}

	expandedPrompt := b.rt.ExpandPrompt(contextPrompt, role)

	// Submit task.
	taskStart := time.Now()
	result, submitErr := b.rt.Submit(ctx, messaging.TaskRequest{
		AgentRole:      role,
		Content:        expandedPrompt,
		SessionID:      sessID,
		SystemPrompt:   systemPrompt,
		Model:          model,
		PermissionMode: permMode,
		Meta: map[string]string{
			"source": "signal",
		},
	})
	if submitErr != nil {
		b.rt.LogErrorCtx(ctx, "signal: submit error", submitErr)
		result.Error = submitErr.Error()
		result.Status = "error"
	}

	// Use the task ID from FillTaskDefaults (Submit may override via its own fill).
	if result.TaskID == "" {
		result.TaskID = taskID
	}

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

	// Record to history.
	b.rt.RecordHistory(result.TaskID, "", "signal", role, result.OutputFile, nil, result)

	// Build response.
	response := result.Output
	if result.Error != "" {
		response = fmt.Sprintf("Error: %s", result.Error)
	}

	// Signal has no strict message length limit, but keep reasonable (2000 chars for safety).
	if len(response) > 2000 {
		response = response[:1997] + "..."
	}

	// Send response.
	if isGroup {
		if err := b.SendGroupMessage(targetID, response); err != nil {
			b.rt.LogError("signal: group message send failed", err)
		}
	} else {
		if err := b.SendMessage(env.Source, response); err != nil {
			b.rt.LogError("signal: DM send failed", err)
		}
	}

	b.rt.LogInfoCtx(ctx, "signal task complete",
		"taskID", result.TaskID,
		"status", result.Status,
		"cost", result.CostUSD,
		"duration_ms", time.Since(taskStart).Milliseconds(),
	)

	// Emit SSE event.
	b.rt.PublishEvent("signal", map[string]interface{}{
		"from":   env.SourceName,
		"source": env.Source,
		"group":  isGroup,
		"taskID": result.TaskID,
		"status": result.Status,
		"cost":   result.CostUSD,
	})
}

// --- Signal API Methods ---

// SendMessage sends a text message to a recipient via signal-cli-rest-api.
// POST /v2/send
func (b *Bot) SendMessage(to, text string) error {
	if to == "" || text == "" {
		return fmt.Errorf("signal: empty recipient or message")
	}

	payload := sendRequest{
		Number:  to,
		Message: text,
	}

	return b.sendSignalAPIRequest("/v2/send", payload)
}

// SendGroupMessage sends a text message to a group via signal-cli-rest-api.
// POST /v2/send
func (b *Bot) SendGroupMessage(groupID, text string) error {
	if groupID == "" || text == "" {
		return fmt.Errorf("signal: empty group ID or message")
	}

	payload := sendRequest{
		GroupID: groupID,
		Message: text,
	}

	return b.sendSignalAPIRequest("/v2/send", payload)
}

// sendSignalAPIRequest sends a POST request to signal-cli-rest-api.
func (b *Bot) sendSignalAPIRequest(path string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("signal: marshal payload: %w", err)
	}

	url := b.apiBase + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("signal: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signal: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	ctx := b.rt.WithTraceID(context.Background(), b.rt.NewTraceID("signal"))
	b.rt.LogDebugCtx(ctx, "signal: API request sent", "path", path, "status", resp.StatusCode)
	return nil
}

// --- PresenceSetter ---

// SetTyping is a no-op: signal-cli-rest-api does not expose typing indicators.
func (b *Bot) SetTyping(_ context.Context, _ string) error {
	return nil
}

// PresenceName returns the channel name.
func (b *Bot) PresenceName() string { return "signal" }

// --- Notifier ---

// Notifier sends notifications via signal-cli-rest-api.
type Notifier struct {
	Config    Config
	Recipient string // phone number or group ID to send to
	IsGroup   bool   // true if Recipient is a group ID
}

// Send sends a notification message.
func (n *Notifier) Send(text string) error {
	if text == "" {
		return nil
	}
	// Truncate if too long.
	if len(text) > 2000 {
		text = text[:1997] + "..."
	}

	var payload sendRequest
	if n.IsGroup {
		payload = sendRequest{
			GroupID: n.Recipient,
			Message: text,
		}
	} else {
		payload = sendRequest{
			Number:  n.Recipient,
			Message: text,
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("signal: marshal notification: %w", err)
	}

	url := n.Config.APIBaseURLOrDefault() + "/v2/send"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("signal: create notification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("signal: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("signal: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the notifier name.
func (n *Notifier) Name() string { return "signal" }
