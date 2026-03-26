package matrix

// bot.go implements the Matrix sync-loop bot extracted from the root package.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"tetora/internal/messaging"
)

// --- Matrix Sync Response Types ---

// matrixSyncResponse is the top-level response from /_matrix/client/v3/sync.
type matrixSyncResponse struct {
	NextBatch string          `json:"next_batch"`
	Rooms     matrixSyncRooms `json:"rooms"`
}

// matrixSyncRooms contains joined and invited room data.
type matrixSyncRooms struct {
	Join   map[string]matrixJoinedRoom  `json:"join"`
	Invite map[string]matrixInvitedRoom `json:"invite"`
}

// matrixJoinedRoom represents a room the bot has joined.
type matrixJoinedRoom struct {
	Timeline matrixTimeline `json:"timeline"`
}

// matrixTimeline contains timeline events for a room.
type matrixTimeline struct {
	Events []matrixEvent `json:"events"`
}

// matrixEvent represents a single Matrix event.
type matrixEvent struct {
	Type     string          `json:"type"`
	Sender   string          `json:"sender"`
	EventID  string          `json:"event_id"`
	Content  json.RawMessage `json:"content"`
	OriginTS int64           `json:"origin_server_ts"`
}

// matrixInvitedRoom represents a room the bot has been invited to.
type matrixInvitedRoom struct {
	InviteState matrixInviteState `json:"invite_state"`
}

// matrixInviteState contains state events for an invited room.
type matrixInviteState struct {
	Events []matrixEvent `json:"events"`
}

// matrixMessageContent is the content of an m.room.message event.
type matrixMessageContent struct {
	MsgType string `json:"msgtype"`
	Body    string `json:"body"`
	URL     string `json:"url,omitempty"` // for m.image, m.file, etc.
}

// matrixErrorResponse is a Matrix API error response.
type matrixErrorResponse struct {
	ErrCode string `json:"errcode"`
	Error   string `json:"error"`
}

// --- Matrix Bot ---

// Bot manages the Matrix sync loop and message handling.
type Bot struct {
	cfg        Config
	rt         messaging.BotRuntime
	apiBase    string // homeserver URL + /_matrix/client/v3
	sinceToken string // for incremental sync
	txnID      int64  // atomic counter for transaction IDs
	stopCh     chan struct{}
	httpClient *http.Client
}

// NewBot creates a new Bot instance.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	apiBase := strings.TrimRight(cfg.Homeserver, "/") + "/_matrix/client/v3"
	return &Bot{
		cfg:        cfg,
		rt:         rt,
		apiBase:    apiBase,
		stopCh:     make(chan struct{}),
		httpClient: &http.Client{Timeout: 60 * time.Second}, // long-poll needs longer timeout
	}
}

// Run starts the sync loop. Blocks until ctx is cancelled or Stop is called.
func (b *Bot) Run(ctx context.Context) {
	b.rt.LogInfo("matrix bot starting sync loop", "homeserver", b.cfg.Homeserver, "userId", b.cfg.UserID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		default:
		}

		if err := b.sync(); err != nil {
			b.rt.LogWarn("matrix sync error", "error", err)
			// Backoff on error.
			select {
			case <-ctx.Done():
				return
			case <-b.stopCh:
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// Stop signals the bot to stop the sync loop.
func (b *Bot) Stop() {
	select {
	case <-b.stopCh:
	default:
		close(b.stopCh)
	}
}

// sync performs a single sync iteration.
func (b *Bot) sync() error {
	url := b.apiBase + "/sync?timeout=30000"
	if b.sinceToken != "" {
		url += "&since=" + b.sinceToken
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("matrix: create sync request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.AccessToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: sync request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("matrix: unauthorized (401), check access token")
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: sync HTTP %d: %s", resp.StatusCode, string(body))
	}

	var syncResp matrixSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return fmt.Errorf("matrix: decode sync response: %w", err)
	}

	// Update since token for next sync.
	if syncResp.NextBatch != "" {
		b.sinceToken = syncResp.NextBatch
	}

	// Process invited rooms (auto-join).
	if b.cfg.AutoJoin {
		for roomID := range syncResp.Rooms.Invite {
			b.rt.LogInfo("matrix: auto-joining invited room", "roomID", roomID)
			if err := b.joinRoom(roomID); err != nil {
				b.rt.LogWarn("matrix: auto-join failed", "roomID", roomID, "error", err)
			}
		}
	}

	// Process joined room events.
	for roomID, room := range syncResp.Rooms.Join {
		for _, event := range room.Timeline.Events {
			b.handleRoomEvent(roomID, event)
		}
	}

	return nil
}

// handleRoomEvent processes a single event from a room timeline.
func (b *Bot) handleRoomEvent(roomID string, event matrixEvent) {
	// Only process m.room.message events.
	if event.Type != "m.room.message" {
		return
	}

	// Ignore own messages.
	if event.Sender == b.cfg.UserID {
		return
	}

	// Parse message content.
	var content matrixMessageContent
	if err := json.Unmarshal(event.Content, &content); err != nil {
		b.rt.LogDebugCtx(context.Background(), "matrix: failed to parse message content", "eventID", event.EventID, "error", err)
		return
	}

	// Only process text messages.
	if content.MsgType != "m.text" {
		b.rt.LogDebugCtx(context.Background(), "matrix: ignoring non-text message", "msgtype", content.MsgType, "eventID", event.EventID)
		return
	}

	text := strings.TrimSpace(content.Body)
	if text == "" {
		return
	}

	b.rt.LogInfo("matrix: received message", "from", event.Sender, "room", roomID, "text", b.rt.Truncate(text, 100))

	// Dispatch to agent asynchronously.
	go b.dispatchToAgent(text, event.Sender, roomID)
}

// dispatchToAgent dispatches a message to the agent system and sends a reply.
func (b *Bot) dispatchToAgent(text, sender, roomID string) {
	traceID := b.rt.NewTraceID("matrix")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	// Route to determine agent.
	role := b.cfg.DefaultAgent
	if role == "" {
		routed, err := b.rt.Route(ctx, text, "matrix")
		if err != nil {
			b.rt.LogErrorCtx(ctx, "matrix: route error", err)
		} else {
			role = routed
			b.rt.LogInfoCtx(ctx, "matrix route result", "agent", role)
		}
	}

	// Find or create session.
	chKey := "matrix:" + sender + ":" + roomID
	sessID, err := b.rt.GetOrCreateSession("matrix", chKey, role, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "matrix session error", err)
	}

	// Build context-aware prompt.
	contextPrompt := text
	if sessID != "" {
		sessionCtx := b.rt.BuildSessionContext(sessID, b.rt.SessionContextLimit())
		if sessionCtx != "" {
			contextPrompt = sessionCtx + "\n\n---\n\n" + text
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

	// Expand prompt with agent knowledge and context variables.
	contextPrompt = b.rt.ExpandPrompt(contextPrompt, role)

	// Load agent-specific config.
	soulPrompt, _ := b.rt.LoadAgentPrompt(role)
	model, permMode, _ := b.rt.AgentConfig(role)

	// Fill task defaults (generates task ID, resolves agent name).
	taskID := b.rt.FillTaskDefaults(&role, nil, "matrix")

	// Run task.
	result, _ := b.rt.Submit(ctx, messaging.TaskRequest{
		AgentRole:      role,
		Content:        contextPrompt,
		SessionID:      sessID,
		SystemPrompt:   soulPrompt,
		Model:          model,
		PermissionMode: permMode,
		Meta:           map[string]string{"source": "matrix"},
	})

	// Record to history.
	b.rt.RecordHistory(taskID, "", "matrix", role, result.OutputFile, nil, result)

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

	// Matrix has no strict message length limit, but truncate very long messages.
	if len(response) > 32000 {
		response = response[:31997] + "..."
	}

	// Send response.
	if err := b.sendMessage(roomID, response); err != nil {
		b.rt.LogErrorCtx(ctx, "matrix: send reply failed", err, "roomID", roomID)
	}

	b.rt.LogInfoCtx(ctx, "matrix task complete", "taskID", taskID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	b.rt.PublishEvent("matrix", map[string]interface{}{
		"from":   sender,
		"room":   roomID,
		"taskID": taskID,
		"status": result.Status,
		"cost":   result.CostUSD,
	})
}

// --- Matrix REST API ---

// sendMessage sends a text message to a Matrix room.
func (b *Bot) sendMessage(roomID, text string) error {
	if roomID == "" {
		return fmt.Errorf("matrix: empty room ID")
	}
	if text == "" {
		return nil
	}

	txnID := atomic.AddInt64(&b.txnID, 1)
	url := fmt.Sprintf("%s/rooms/%s/send/m.room.message/%d",
		b.apiBase, roomID, txnID)

	payload := map[string]string{
		"msgtype": "m.text",
		"body":    text,
	}

	return b.matrixPUT(url, payload)
}

// joinRoom joins a Matrix room by room ID or alias.
func (b *Bot) joinRoom(roomIDOrAlias string) error {
	if roomIDOrAlias == "" {
		return fmt.Errorf("matrix: empty room ID/alias")
	}

	url := fmt.Sprintf("%s/join/%s", b.apiBase, roomIDOrAlias)
	return b.matrixPOST(url, map[string]string{})
}

// leaveRoom leaves a Matrix room.
func (b *Bot) leaveRoom(roomID string) error {
	if roomID == "" {
		return fmt.Errorf("matrix: empty room ID")
	}

	url := fmt.Sprintf("%s/rooms/%s/leave", b.apiBase, roomID)
	return b.matrixPOST(url, map[string]string{})
}

// --- HTTP Helpers ---

// matrixPUT sends a PUT request to the Matrix API.
func (b *Bot) matrixPUT(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("matrix: marshal payload: %w", err)
	}

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("matrix: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// matrixPOST sends a POST request to the Matrix API.
func (b *Bot) matrixPOST(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("matrix: marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("matrix: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// --- Matrix Notification Integration ---

// MatrixNotifier sends notifications via Matrix room messages.
type MatrixNotifier struct {
	Config Config
	RoomID string // room ID to send notifications to
}

// Send sends a notification message to the configured Matrix room.
func (n *MatrixNotifier) Send(text string) error {
	if text == "" {
		return nil
	}
	if n.RoomID == "" {
		return fmt.Errorf("matrix: no room ID configured for notifications")
	}

	// Truncate very long messages.
	if len(text) > 32000 {
		text = text[:31997] + "..."
	}

	apiBase := strings.TrimRight(n.Config.Homeserver, "/") + "/_matrix/client/v3"
	txnID := time.Now().UnixNano()
	url := fmt.Sprintf("%s/rooms/%s/send/m.room.message/%d", apiBase, n.RoomID, txnID)

	payload := map[string]string{
		"msgtype": "m.text",
		"body":    text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("matrix: marshal notification: %w", err)
	}

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("matrix: create notification request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.Config.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("matrix: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("matrix: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the notifier name.
func (n *MatrixNotifier) Name() string { return "matrix" }
