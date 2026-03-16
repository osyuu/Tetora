// Package line provides LINE Messaging API bot integration.
package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/messaging"
)

// --- LINE Webhook Event Types ---

// WebhookBody is the top-level webhook payload from LINE Platform.
type WebhookBody struct {
	Destination string  `json:"destination"`
	Events      []Event `json:"events"`
}

// Event represents a single webhook event from LINE.
type Event struct {
	Type       string   `json:"type"` // "message", "follow", "unfollow", "join", "leave", "postback"
	Timestamp  int64    `json:"timestamp"`
	ReplyToken string   `json:"replyToken,omitempty"`
	Source     Source   `json:"source"`
	Message    *Msg     `json:"message,omitempty"`
	Postback   *Postback `json:"postback,omitempty"`
}

// Source identifies the source of an event.
type Source struct {
	Type    string `json:"type"`              // "user", "group", "room"
	UserID  string `json:"userId,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	RoomID  string `json:"roomId,omitempty"`
}

// Msg represents an incoming message.
type Msg struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "text", "image", "video", "audio", "sticker"
	Text      string `json:"text,omitempty"`
	StickerID string `json:"stickerId,omitempty"`
	PackageID string `json:"packageId,omitempty"`
}

// Postback represents a postback event.
type Postback struct {
	Data string `json:"data"`
}

// --- LINE Message Types ---

// Message is a message to send via LINE API.
type Message struct {
	Type               string          `json:"type"`                         // "text", "image", "flex"
	Text               string          `json:"text,omitempty"`               // for text messages
	AltText            string          `json:"altText,omitempty"`            // for flex messages
	Contents           json.RawMessage `json:"contents,omitempty"`           // for flex messages
	OriginalContentURL string          `json:"originalContentUrl,omitempty"` // for image/video/audio
	PreviewImageURL    string          `json:"previewImageUrl,omitempty"`    // for image/video
	QuickReply         *QuickReply     `json:"quickReply,omitempty"`         // quick reply buttons
}

// QuickReply holds quick reply items.
type QuickReply struct {
	Items []QuickReplyItem `json:"items"`
}

// QuickReplyItem is a single quick reply button.
type QuickReplyItem struct {
	Type   string      `json:"type"` // "action"
	Action QuickAction `json:"action"`
}

// QuickAction is the action of a quick reply item.
type QuickAction struct {
	Type  string `json:"type"`           // "message", "postback", "uri"
	Label string `json:"label"`
	Text  string `json:"text,omitempty"` // for "message" type
	Data  string `json:"data,omitempty"` // for "postback" type
	URI   string `json:"uri,omitempty"`  // for "uri" type
}

// Profile represents a user profile from LINE API.
type Profile struct {
	DisplayName   string `json:"displayName"`
	UserID        string `json:"userId"`
	PictureURL    string `json:"pictureUrl,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	Language      string `json:"language,omitempty"`
}

// --- LINE Bot ---

// Bot handles incoming LINE Messaging API webhook events.
type Bot struct {
	cfg     Config
	rt      messaging.BotRuntime
	apiBase string // "https://api.line.me/v2/bot"

	// Dedup: track recently processed message IDs.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client
}

// NewBot creates a new Bot instance.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	return &Bot{
		cfg:        cfg,
		rt:         rt,
		apiBase:    "https://api.line.me/v2/bot",
		processed:  make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetTyping is a no-op; LINE Messaging API does not support typing indicators.
func (b *Bot) SetTyping(_ context.Context, _ string) error {
	return nil
}

// PresenceName returns the channel name for presence tracking.
func (b *Bot) PresenceName() string { return "line" }

// HandleWebhook handles incoming LINE webhook events.
// POST = incoming events from LINE Platform.
func (b *Bot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body for signature verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.rt.LogError("line: read body failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Verify HMAC-SHA256 signature.
	if b.cfg.ChannelSecret != "" {
		sig := r.Header.Get("X-Line-Signature")
		if !VerifySignature(b.cfg.ChannelSecret, body, sig) {
			b.rt.LogWarn("line: signature verification failed")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	// Parse webhook payload.
	var hook WebhookBody
	if err := json.Unmarshal(body, &hook); err != nil {
		b.rt.LogError("line: parse webhook failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// LINE expects 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process events asynchronously.
	go b.processEvents(hook.Events)
}

// processEvents processes incoming LINE webhook events.
func (b *Bot) processEvents(events []Event) {
	for _, event := range events {
		switch event.Type {
		case "message":
			b.handleMessageEvent(event)
		case "postback":
			b.handlePostbackEvent(event)
		case "follow":
			b.handleFollowEvent(event)
		case "join":
			b.handleJoinEvent(event)
		case "unfollow", "leave":
			b.rt.LogInfo("line: user/group event", "type", event.Type, "source", event.Source.UserID)
		default:
			b.rt.LogDebugCtx(context.Background(), "line: unhandled event type", "type", event.Type)
		}
	}
}

// handleMessageEvent processes a message event.
func (b *Bot) handleMessageEvent(event Event) {
	if event.Message == nil {
		return
	}

	// Dedup: check if already processed.
	b.mu.Lock()
	if _, seen := b.processed[event.Message.ID]; seen {
		b.mu.Unlock()
		b.rt.LogDebugCtx(context.Background(), "line: duplicate message ignored", "msgID", event.Message.ID)
		return
	}
	b.processed[event.Message.ID] = time.Now()
	b.processedSize++

	// Cleanup old entries every 1000 messages.
	if b.processedSize > 1000 {
		cutoff := time.Now().Add(-1 * time.Hour)
		for id, t := range b.processed {
			if t.Before(cutoff) {
				delete(b.processed, id)
				b.processedSize--
			}
		}
	}
	b.mu.Unlock()

	// Only handle text messages; skip unsupported types.
	if event.Message.Type != "text" {
		b.rt.LogDebugCtx(context.Background(), "line: unsupported message type ignored", "msgID", event.Message.ID, "type", event.Message.Type)
		return
	}

	text := strings.TrimSpace(event.Message.Text)
	if text == "" {
		return
	}

	// Determine conversation target (user or group).
	targetID := b.resolveTargetID(event.Source)
	b.rt.LogInfo("line: received message", "from", event.Source.UserID, "target", targetID, "text", b.rt.Truncate(text, 100))

	// Dispatch to agent.
	b.dispatchToAgent(text, event.Source.UserID, targetID, event.ReplyToken)
}

// handlePostbackEvent processes a postback event.
func (b *Bot) handlePostbackEvent(event Event) {
	if event.Postback == nil {
		return
	}

	b.rt.LogInfo("line: postback received", "data", event.Postback.Data, "from", event.Source.UserID)

	// Treat postback data as a prompt.
	targetID := b.resolveTargetID(event.Source)
	b.dispatchToAgent(event.Postback.Data, event.Source.UserID, targetID, event.ReplyToken)
}

// handleFollowEvent sends a welcome message when a user adds the bot.
func (b *Bot) handleFollowEvent(event Event) {
	b.rt.LogInfo("line: new follower", "userID", event.Source.UserID)

	if event.ReplyToken != "" {
		msgs := []Message{{
			Type: "text",
			Text: "Welcome to Tetora! Send me a message and I'll help you.",
		}}
		if err := b.sendReply(event.ReplyToken, msgs); err != nil {
			b.rt.LogError("line: welcome message failed", err)
		}
	}
}

// handleJoinEvent logs when the bot joins a group/room.
func (b *Bot) handleJoinEvent(event Event) {
	b.rt.LogInfo("line: joined group/room", "groupID", event.Source.GroupID, "roomID", event.Source.RoomID)
}

// dispatchToAgent dispatches a message to the agent system and replies.
func (b *Bot) dispatchToAgent(text, userID, targetID, replyToken string) {
	traceID := b.rt.NewTraceID("line")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	// Route to determine agent.
	role := b.cfg.DefaultAgent
	if role == "" {
		var err error
		role, err = b.rt.Route(ctx, text, "line")
		if err != nil {
			b.rt.LogErrorCtx(ctx, "line: route error", err)
		}
		b.rt.LogInfoCtx(ctx, "line route result", "agent", role)
	}

	// Find or create session.
	chKey := "line:" + userID + ":" + targetID
	sessID, err := b.rt.GetOrCreateSession("line", chKey, role, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "line session error", err)
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
	taskID := b.rt.FillTaskDefaults(&role, nil, "line")

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
		Meta:           map[string]string{"source": "line"},
	})

	// Record to history.
	b.rt.RecordHistory(taskID, "", "line", role, result.OutputFile, nil, result)

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

	// LINE text messages have a 5000 character limit.
	if len(response) > 5000 {
		response = response[:4997] + "..."
	}

	// Send response: try reply first (free), fall back to push (paid).
	msgs := []Message{{Type: "text", Text: response}}
	if replyToken != "" {
		if err := b.sendReply(replyToken, msgs); err != nil {
			b.rt.LogWarn("line: reply failed, trying push", "error", err)
			if pushErr := b.sendPush(targetID, msgs); pushErr != nil {
				b.rt.LogError("line: push also failed", pushErr)
			}
		}
	} else {
		if err := b.sendPush(targetID, msgs); err != nil {
			b.rt.LogError("line: push failed", err)
		}
	}

	b.rt.LogInfoCtx(ctx, "line task complete", "taskID", taskID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	b.rt.PublishEvent("line", map[string]interface{}{
		"from":   userID,
		"target": targetID,
		"taskID": taskID,
		"status": result.Status,
		"cost":   result.CostUSD,
	})
}

// resolveTargetID determines the reply target (group/room/user).
func (b *Bot) resolveTargetID(src Source) string {
	if src.GroupID != "" {
		return src.GroupID
	}
	if src.RoomID != "" {
		return src.RoomID
	}
	return src.UserID
}

// --- LINE Messaging API ---

// sendReply sends reply messages using a reply token (free, within 3-minute window).
func (b *Bot) sendReply(replyToken string, messages []Message) error {
	if replyToken == "" {
		return fmt.Errorf("line: empty reply token")
	}

	payload := map[string]interface{}{
		"replyToken": replyToken,
		"messages":   messages,
	}

	return b.sendAPIRequest(b.apiBase+"/message/reply", payload)
}

// sendPush sends push messages to a user/group/room (costs money per message).
func (b *Bot) sendPush(to string, messages []Message) error {
	if to == "" {
		return fmt.Errorf("line: empty push target")
	}

	payload := map[string]interface{}{
		"to":       to,
		"messages": messages,
	}

	return b.sendAPIRequest(b.apiBase+"/message/push", payload)
}

// GetProfile fetches a user's LINE profile.
func (b *Bot) GetProfile(userID string) (*Profile, error) {
	if userID == "" {
		return nil, fmt.Errorf("line: empty user ID")
	}

	url := fmt.Sprintf("%s/profile/%s", b.apiBase, userID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("line: create profile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.cfg.ChannelAccessToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("line: profile request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("line: profile HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var profile Profile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("line: parse profile: %w", err)
	}

	return &profile, nil
}

// sendAPIRequest sends a POST request to LINE Messaging API.
func (b *Bot) sendAPIRequest(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("line: marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("line: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+b.cfg.ChannelAccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("line: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("line: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	b.rt.LogDebugCtx(context.Background(), "line: API request sent", "url", url, "status", resp.StatusCode)
	return nil
}

// --- LINE Signature Verification ---

// VerifySignature verifies the X-Line-Signature header using HMAC-SHA256.
// The signature is base64-encoded HMAC-SHA256 of the request body with the channel secret.
func VerifySignature(channelSecret string, body []byte, signature string) bool {
	if channelSecret == "" {
		return true // skip if no secret configured
	}

	if signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(channelSecret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

// --- LINE Flex Message Builder ---

// BuildFlexText creates a simple flex message with text content.
func BuildFlexText(altText, text string) Message {
	contents, _ := json.Marshal(map[string]interface{}{
		"type": "bubble",
		"body": map[string]interface{}{
			"type":   "box",
			"layout": "vertical",
			"contents": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
					"wrap": true,
				},
			},
		},
	})

	return Message{
		Type:     "flex",
		AltText:  altText,
		Contents: contents,
	}
}

// BuildQuickReplyMessage attaches quick reply buttons to a text message.
func BuildQuickReplyMessage(text string, options []string) Message {
	items := make([]QuickReplyItem, 0, len(options))
	for _, opt := range options {
		items = append(items, QuickReplyItem{
			Type: "action",
			Action: QuickAction{
				Type:  "message",
				Label: opt,
				Text:  opt,
			},
		})
	}

	return Message{
		Type: "text",
		Text: text,
		QuickReply: &QuickReply{
			Items: items,
		},
	}
}

// --- LINE Notification Integration ---

// Notifier sends notifications via LINE Push API.
type Notifier struct {
	Config Config
	ChatID string // user/group ID to send to
}

// Send sends a notification message to the configured chat ID.
func (n *Notifier) Send(text string) error {
	if text == "" {
		return nil
	}
	// Truncate if too long.
	if len(text) > 5000 {
		text = text[:4997] + "..."
	}

	payload := map[string]interface{}{
		"to": n.ChatID,
		"messages": []Message{
			{Type: "text", Text: text},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("line: marshal notification: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.line.me/v2/bot/message/push", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("line: create notification request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+n.Config.ChannelAccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("line: notification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("line: notification HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// Name returns the notifier name.
func (n *Notifier) Name() string { return "line" }
