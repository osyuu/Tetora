package whatsapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"tetora/internal/messaging"
)

// --- Webhook Types ---

// webhook is the top-level payload sent by the WhatsApp Cloud API.
type webhook struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				Messages []struct {
					ID        string       `json:"id"`
					From      string       `json:"from"` // sender phone number
					Timestamp string       `json:"timestamp"`
					Type      string       `json:"type"` // "text", "image", "audio", etc.
					Text      *messageText `json:"text,omitempty"`
				} `json:"messages,omitempty"`
				Statuses []struct {
					ID        string `json:"id"`
					Status    string `json:"status"` // "sent", "delivered", "read"
					Timestamp string `json:"timestamp"`
				} `json:"statuses,omitempty"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

// messageText represents the text field in a WhatsApp message.
type messageText struct {
	Body string `json:"body"`
}

// --- Bot ---

// Bot handles incoming WhatsApp Cloud API webhook events.
type Bot struct {
	cfg Config
	rt  messaging.BotRuntime

	// Dedup: track recently processed message IDs to handle retries.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex
}

// NewBot creates a new WhatsApp bot with the given config and runtime.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	return &Bot{
		cfg:       cfg,
		rt:        rt,
		processed: make(map[string]time.Time),
	}
}

// SetTyping is a no-op; WhatsApp Cloud API does not support typing indicators for bots.
func (b *Bot) SetTyping(_ context.Context, _ string) error {
	return nil
}

// PresenceName returns the channel name for presence tracking.
func (b *Bot) PresenceName() string { return "whatsapp" }

// --- Webhook Handler ---

// WebhookHandler handles incoming WhatsApp webhook events.
// GET = verification challenge, POST = incoming messages.
func (b *Bot) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		b.handleVerification(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body for signature verification.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.rt.LogError("whatsapp: read body failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Verify signature if AppSecret is configured.
	if b.cfg.AppSecret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifySignature(b.cfg.AppSecret, body, sig) {
			b.rt.LogWarn("whatsapp: signature verification failed", "signature", sig)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	// Parse webhook payload.
	var hook webhook
	if err := json.Unmarshal(body, &hook); err != nil {
		b.rt.LogError("whatsapp: parse webhook failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// WhatsApp expects 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process messages asynchronously.
	go b.processWebhook(&hook)
}

// handleVerification handles the webhook verification challenge.
// GET /api/whatsapp/webhook?hub.mode=subscribe&hub.verify_token=xxx&hub.challenge=xxx
func (b *Bot) handleVerification(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode == "subscribe" && token == b.cfg.VerifyToken {
		b.rt.LogInfo("whatsapp: webhook verified", "challenge", challenge)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(challenge))
		return
	}

	b.rt.LogWarn("whatsapp: verification failed", "mode", mode, "token", token)
	http.Error(w, "forbidden", http.StatusForbidden)
}

// processWebhook processes incoming webhook messages.
func (b *Bot) processWebhook(hook *webhook) {
	for _, entry := range hook.Entry {
		for _, change := range entry.Changes {
			// Process incoming messages.
			for _, msg := range change.Value.Messages {
				b.handleMessage(msg.From, msg.ID, msg.Text, msg.Type)
			}

			// Silently ignore status updates (sent, delivered, read).
			if len(change.Value.Statuses) > 0 {
				b.rt.LogInfo("whatsapp: ignoring status updates", "count", len(change.Value.Statuses))
			}
		}
	}
}

// handleMessage processes a single WhatsApp message.
func (b *Bot) handleMessage(from, msgID string, textPtr *messageText, msgType string) {
	// Dedup: check if we've already processed this message.
	b.mu.Lock()
	if _, seen := b.processed[msgID]; seen {
		b.mu.Unlock()
		b.rt.LogInfo("whatsapp: duplicate message ignored", "msgID", msgID)
		return
	}
	b.processed[msgID] = time.Now()
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

	// Only process text messages for now.
	if msgType != "text" || textPtr == nil {
		b.rt.LogInfo("whatsapp: non-text message ignored", "msgID", msgID, "type", msgType)
		return
	}

	text := strings.TrimSpace(textPtr.Body)
	if text == "" {
		return
	}

	traceID := b.rt.NewTraceID("whatsapp")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	b.rt.LogInfo("whatsapp: received message", "from", from, "text", messaging.TruncateStr(text, 100))

	// Route to determine agent.
	agent, err := b.rt.Route(ctx, text, "whatsapp")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "whatsapp: route failed", err, "from", from)
	}
	b.rt.LogInfoCtx(ctx, "whatsapp route result", "from", from, "agent", agent)

	// Find or create session for this phone number.
	chKey := fmt.Sprintf("whatsapp:%s", from)
	sessID, err := b.rt.GetOrCreateSession("whatsapp", chKey, agent, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "whatsapp: session error", err)
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

	// Set up task defaults.
	taskAgent := agent
	taskID := b.rt.FillTaskDefaults(&taskAgent, nil, "whatsapp")

	// Apply agent-specific config.
	var systemPrompt, model, permMode string
	if taskAgent != "" {
		if soulPrompt, err := b.rt.LoadAgentPrompt(taskAgent); err == nil && soulPrompt != "" {
			systemPrompt = soulPrompt
		}
		if m, pm, ok := b.rt.AgentConfig(taskAgent); ok {
			model = m
			permMode = pm
		}
	}

	// Expand prompt variables.
	contextPrompt = b.rt.ExpandPrompt(contextPrompt, taskAgent)

	// Run task asynchronously.
	go func() {
		result, err := b.rt.Submit(ctx, messaging.TaskRequest{
			AgentRole:      taskAgent,
			Content:        contextPrompt,
			SessionID:      sessID,
			SystemPrompt:   systemPrompt,
			Model:          model,
			PermissionMode: permMode,
			Meta: map[string]string{
				"taskID": taskID,
				"source": "whatsapp",
				"from":   from,
			},
		})
		if err != nil {
			b.rt.LogErrorCtx(ctx, "whatsapp: submit failed", err, "taskID", taskID)
		}

		// Record assistant response to session.
		if sessID != "" {
			msgRole := "assistant"
			content := messaging.TruncateStr(result.Output, 5000)
			if result.Status != "success" && result.Status != "" {
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

		// Send response back to WhatsApp.
		response := result.Output
		if result.Error != "" {
			response = fmt.Sprintf("❌ Error: %s", result.Error)
		}

		// Truncate response if too long.
		if len(response) > 4000 {
			response = response[:3997] + "..."
		}

		if err := b.sendMessage(from, response); err != nil {
			b.rt.LogError("whatsapp: send response failed", err, "taskID", taskID)
		}

		b.rt.LogInfoCtx(ctx, "whatsapp task complete", "taskID", taskID, "status", result.Status, "cost", result.CostUSD)

		// Emit SSE event.
		b.rt.PublishEvent("whatsapp", map[string]interface{}{
			"from":   from,
			"taskID": taskID,
			"status": result.Status,
			"cost":   result.CostUSD,
		})
	}()
}

// sendMessage sends a text message via WhatsApp Cloud API.
func (b *Bot) sendMessage(to, text string) error {
	return SendMessage(b.cfg, to, text)
}

// SendNotify sends a notification to a specific WhatsApp number.
// This is the bot-level method used for notification chain integration.
func (b *Bot) SendNotify(to, text string) {
	if err := SendMessage(b.cfg, to, text); err != nil {
		b.rt.LogError("whatsapp: notification send failed", err, "to", to)
	}
}

// --- WhatsApp Cloud API Functions ---

// SendMessage sends a text message via WhatsApp Cloud API.
func SendMessage(cfg Config, to string, text string) error {
	if text == "" {
		return nil
	}

	// WhatsApp has a message length limit; truncate if needed.
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"text": map[string]string{
			"body": text,
		},
	}

	return sendAPIRequest(cfg, payload)
}

// SendReply sends a reply to a specific message.
func SendReply(cfg Config, to string, text string, messageID string) error {
	if text == "" {
		return nil
	}

	if len(text) > 4096 {
		text = text[:4093] + "..."
	}

	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "text",
		"context": map[string]string{
			"message_id": messageID,
		},
		"text": map[string]string{
			"body": text,
		},
	}

	return sendAPIRequest(cfg, payload)
}

// sendAPIRequest sends a request to WhatsApp Cloud API.
func sendAPIRequest(cfg Config, payload interface{}) error {
	url := fmt.Sprintf("https://graph.facebook.com/%s/%s/messages",
		cfg.APIVersion_(), cfg.PhoneNumberID)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("whatsapp: marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("whatsapp: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("whatsapp: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// verifySignature verifies the X-Hub-Signature-256 header.
func verifySignature(appSecret string, body []byte, signature string) bool {
	if appSecret == "" {
		return true // skip if no secret configured
	}

	if signature == "" {
		return false
	}

	// Signature format: "sha256=<hex>"
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	providedSig := signature[7:]

	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSig), []byte(providedSig))
}

// --- Notifier ---

// Notifier implements a notification sender for WhatsApp.
// It is used by the notification chain to push messages to a specific recipient.
type Notifier struct {
	Cfg       Config
	Recipient string
}

// Send sends a text notification to the configured recipient.
func (n *Notifier) Send(text string) error {
	return SendMessage(n.Cfg, n.Recipient, text)
}

// Name returns the notifier channel name.
func (n *Notifier) Name() string { return "whatsapp" }
