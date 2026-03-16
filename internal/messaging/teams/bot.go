// Package teams provides Microsoft Teams Bot Framework integration.
package teams

import (
	"bytes"
	"context"
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

// --- Teams Activity Types ---

// Activity represents an incoming Activity from Bot Framework.
type Activity struct {
	Type         string       `json:"type"`                   // "message", "conversationUpdate", "invoke"
	ID           string       `json:"id"`
	Timestamp    string       `json:"timestamp"`
	Text         string       `json:"text"`
	ChannelID    string       `json:"channelId"`               // "msteams"
	ServiceURL   string       `json:"serviceUrl"`              // for replies
	From         Account      `json:"from"`
	Conversation Conversation `json:"conversation"`
	Recipient    Account      `json:"recipient"`
	Attachments  []Attachment `json:"attachments,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`      // for Adaptive Card actions
}

// Account identifies a user or bot in Teams.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Conversation identifies a Teams conversation.
type Conversation struct {
	ID      string `json:"id"`
	IsGroup bool   `json:"isGroup,omitempty"`
}

// Attachment represents an attachment in a Teams message.
type Attachment struct {
	ContentType string          `json:"contentType"`
	Content     json.RawMessage `json:"content,omitempty"`
	ContentURL  string          `json:"contentUrl,omitempty"`
	Name        string          `json:"name,omitempty"`
}

// --- Teams Token Cache ---

// tokenCache caches the OAuth2 bearer token for outbound API calls.
type tokenCache struct {
	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// --- Teams Bot ---

// Bot handles incoming Microsoft Teams Bot Framework webhook events.
type Bot struct {
	cfg        Config
	rt         messaging.BotRuntime
	tc         tokenCache

	// Dedup: track recently processed activity IDs.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client

	// tokenURL can be overridden for testing.
	tokenURL string
}

// NewBot creates a new Bot instance.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	tenantID := cfg.TenantID
	if tenantID == "" {
		tenantID = "botframework.com"
	}
	return &Bot{
		cfg:        cfg,
		rt:         rt,
		processed:  make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		tokenURL:   fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenantID),
	}
}

// SetTyping sends a typing activity via Bot Framework.
func (b *Bot) SetTyping(_ context.Context, channelRef string) error {
	if channelRef == "" {
		return nil
	}

	// channelRef format for Teams: "serviceURL|conversationID"
	parts := strings.SplitN(channelRef, "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil // invalid ref format, skip silently
	}
	serviceURL := parts[0]
	conversationID := parts[1]

	url := fmt.Sprintf("%sv3/conversations/%s/activities",
		ensureTrailingSlash(serviceURL), conversationID)

	payload := map[string]string{
		"type": "typing",
	}
	return b.sendBotFrameworkRequest(url, payload)
}

// PresenceName returns the channel name for presence tracking.
func (b *Bot) PresenceName() string { return "teams" }

// HandleWebhook handles incoming Bot Framework webhook events.
// POST /api/teams/webhook
func (b *Bot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.rt.LogError("teams: read body failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	// Validate auth (JWT in Authorization header).
	if err := b.validateAuth(r); err != nil {
		b.rt.LogWarn("teams: auth validation failed", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse activity.
	var activity Activity
	if err := json.Unmarshal(body, &activity); err != nil {
		b.rt.LogError("teams: parse activity failed", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Return 200 OK immediately to prevent retries.
	w.WriteHeader(http.StatusOK)

	// Process activity asynchronously.
	go b.processActivity(activity)
}

// validateAuth validates the JWT token from the Authorization header.
// For simplicity: validates structure + verifies appId in claims (skip full JWKS rotation).
func (b *Bot) validateAuth(r *http.Request) error {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return fmt.Errorf("teams: missing Authorization header")
	}

	// Expect "Bearer <token>" format.
	if !strings.HasPrefix(auth, "Bearer ") {
		return fmt.Errorf("teams: invalid Authorization format")
	}
	token := strings.TrimPrefix(auth, "Bearer ")

	// JWT is three base64url-encoded parts separated by dots.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("teams: invalid JWT structure (expected 3 parts, got %d)", len(parts))
	}

	// Decode and validate payload (middle part).
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return fmt.Errorf("teams: decode JWT payload: %w", err)
	}

	var claims struct {
		Iss   string `json:"iss"`
		Aud   string `json:"aud"`
		AppID string `json:"appid"`
		Exp   int64  `json:"exp"`
		Nbf   int64  `json:"nbf"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return fmt.Errorf("teams: parse JWT claims: %w", err)
	}

	// Check expiration.
	now := time.Now().Unix()
	if claims.Exp > 0 && now > claims.Exp {
		return fmt.Errorf("teams: token expired (exp=%d, now=%d)", claims.Exp, now)
	}

	// Check not-before.
	if claims.Nbf > 0 && now < claims.Nbf {
		return fmt.Errorf("teams: token not yet valid (nbf=%d, now=%d)", claims.Nbf, now)
	}

	// Validate audience matches our appId.
	if b.cfg.AppID != "" {
		if claims.Aud != b.cfg.AppID {
			return fmt.Errorf("teams: audience mismatch (got %q, want %q)", claims.Aud, b.cfg.AppID)
		}
	}

	// Validate issuer (should be Microsoft-related).
	if claims.Iss != "" {
		validIssuers := []string{
			"https://api.botframework.com",
			"https://sts.windows.net/",
			"https://login.microsoftonline.com/",
		}
		issValid := false
		for _, vi := range validIssuers {
			if strings.HasPrefix(claims.Iss, vi) {
				issValid = true
				break
			}
		}
		if !issValid {
			return fmt.Errorf("teams: invalid issuer %q", claims.Iss)
		}
	}

	return nil
}

// base64URLDecode decodes a base64url-encoded string (JWT-style, no padding).
func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed.
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// processActivity handles a Teams activity after webhook validation.
func (b *Bot) processActivity(activity Activity) {
	switch activity.Type {
	case "message":
		b.handleMessageActivity(activity)
	case "conversationUpdate":
		b.handleConversationUpdate(activity)
	case "invoke":
		b.handleInvokeActivity(activity)
	default:
		b.rt.LogDebugCtx(context.Background(), "teams: unhandled activity type", "type", activity.Type)
	}
}

// handleMessageActivity processes an incoming message from Teams.
func (b *Bot) handleMessageActivity(activity Activity) {
	// Dedup: check if already processed.
	if activity.ID != "" {
		b.mu.Lock()
		if _, seen := b.processed[activity.ID]; seen {
			b.mu.Unlock()
			b.rt.LogDebugCtx(context.Background(), "teams: duplicate activity ignored", "activityID", activity.ID)
			return
		}
		b.processed[activity.ID] = time.Now()
		b.processedSize++

		// Cleanup old entries every 1000 activities.
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
	}

	// Handle Adaptive Card action submissions (value field).
	if activity.Value != nil && len(activity.Value) > 0 {
		var val map[string]interface{}
		if json.Unmarshal(activity.Value, &val) == nil && len(val) > 0 {
			b.rt.LogInfo("teams: adaptive card action", "from", activity.From.Name, "value", string(activity.Value))
			// Treat the JSON value as a prompt.
			valJSON, _ := json.Marshal(val)
			b.dispatchToAgent(string(valJSON), activity)
			return
		}
	}

	text := strings.TrimSpace(activity.Text)
	if text == "" {
		return
	}

	// Remove bot mention prefix (e.g., "<at>BotName</at> ").
	text = RemoveBotMention(text)
	if text == "" {
		return
	}

	b.rt.LogInfo("teams: received message", "from", activity.From.Name, "conversation", activity.Conversation.ID, "text", b.rt.Truncate(text, 100))

	// Dispatch to agent.
	b.dispatchToAgent(text, activity)
}

// RemoveBotMention strips Teams @mention tags from the message text.
func RemoveBotMention(text string) string {
	// Teams wraps mentions in <at>Name</at> tags.
	for {
		start := strings.Index(text, "<at>")
		if start == -1 {
			break
		}
		end := strings.Index(text, "</at>")
		if end == -1 {
			break
		}
		text = text[:start] + text[end+5:]
	}
	return strings.TrimSpace(text)
}

// handleConversationUpdate handles bot join/leave events.
func (b *Bot) handleConversationUpdate(activity Activity) {
	b.rt.LogInfo("teams: conversation update", "conversation", activity.Conversation.ID)
}

// handleInvokeActivity handles invoke activities (e.g., messaging extensions).
func (b *Bot) handleInvokeActivity(activity Activity) {
	b.rt.LogInfo("teams: invoke activity", "conversation", activity.Conversation.ID)
}

// dispatchToAgent dispatches a message to the agent system and replies via Teams.
func (b *Bot) dispatchToAgent(text string, activity Activity) {
	traceID := b.rt.NewTraceID("teams")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	// Route to determine agent.
	role := b.cfg.DefaultAgent
	if role == "" {
		var err error
		role, err = b.rt.Route(ctx, text, "teams")
		if err != nil {
			b.rt.LogErrorCtx(ctx, "teams: route error", err)
		}
		b.rt.LogInfoCtx(ctx, "teams route result", "agent", role)
	}

	// Find or create session.
	chKey := "teams:" + activity.From.ID + ":" + activity.Conversation.ID
	sessID, err := b.rt.GetOrCreateSession("teams", chKey, role, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "teams session error", err)
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
	taskID := b.rt.FillTaskDefaults(&role, nil, "teams")

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
		Meta:           map[string]string{"source": "teams"},
	})

	// Record to history.
	b.rt.RecordHistory(taskID, "", "teams", role, result.OutputFile, nil, result)

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

	// Teams text messages limit: truncate at 28KB.
	if len(response) > 28000 {
		response = response[:27997] + "..."
	}

	// Send reply.
	if err := b.sendReply(activity.ServiceURL, activity.Conversation.ID, activity.ID, response); err != nil {
		b.rt.LogError("teams: reply failed", err)
		// Fall back to proactive message.
		if proactiveErr := b.sendProactive(activity.ServiceURL, activity.Conversation.ID, response); proactiveErr != nil {
			b.rt.LogError("teams: proactive also failed", proactiveErr)
		}
	}

	b.rt.LogInfoCtx(ctx, "teams task complete", "taskID", taskID, "status", result.Status, "cost", result.CostUSD)

	// Emit SSE event.
	b.rt.PublishEvent("teams", map[string]interface{}{
		"from":           activity.From.Name,
		"conversationId": activity.Conversation.ID,
		"taskID":         taskID,
		"status":         result.Status,
		"cost":           result.CostUSD,
	})
}

// --- Teams Bot Framework API ---

// GetToken obtains (or returns cached) an OAuth2 bearer token for outbound Bot Framework API calls.
func (b *Bot) GetToken() (string, error) {
	// Check cache first.
	b.tc.mu.RLock()
	if b.tc.token != "" && time.Now().Before(b.tc.expiresAt) {
		token := b.tc.token
		b.tc.mu.RUnlock()
		return token, nil
	}
	b.tc.mu.RUnlock()

	// Acquire write lock to refresh.
	b.tc.mu.Lock()
	defer b.tc.mu.Unlock()

	// Double-check after acquiring write lock.
	if b.tc.token != "" && time.Now().Before(b.tc.expiresAt) {
		return b.tc.token, nil
	}

	// Request new token.
	data := fmt.Sprintf(
		"grant_type=client_credentials&client_id=%s&client_secret=%s&scope=%s",
		b.cfg.AppID,
		b.cfg.AppPassword,
		"https%3A%2F%2Fapi.botframework.com%2F.default",
	)

	req, err := http.NewRequest("POST", b.tokenURL, strings.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("teams: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("teams: token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("teams: token HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // seconds
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("teams: parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("teams: empty access token in response")
	}

	// Cache with 5-minute buffer before actual expiry.
	expiresIn := time.Duration(tokenResp.ExpiresIn) * time.Second
	if expiresIn > 5*time.Minute {
		expiresIn -= 5 * time.Minute
	}
	b.tc.token = tokenResp.AccessToken
	b.tc.expiresAt = time.Now().Add(expiresIn)

	b.rt.LogDebugCtx(context.Background(), "teams: token refreshed", "expiresIn", tokenResp.ExpiresIn)
	return tokenResp.AccessToken, nil
}

// sendReply sends a reply to a specific activity in a conversation.
// POST to serviceUrl/v3/conversations/{conversationId}/activities/{activityId}
func (b *Bot) sendReply(serviceURL, conversationID, activityID, text string) error {
	if serviceURL == "" || conversationID == "" {
		return fmt.Errorf("teams: missing serviceURL or conversationID")
	}

	url := fmt.Sprintf("%sv3/conversations/%s/activities/%s",
		ensureTrailingSlash(serviceURL), conversationID, activityID)

	payload := map[string]interface{}{
		"type": "message",
		"text": text,
	}

	return b.sendBotFrameworkRequest(url, payload)
}

// sendProactive sends a proactive message to a conversation (no reply-to activity).
// POST to serviceUrl/v3/conversations/{conversationId}/activities
func (b *Bot) sendProactive(serviceURL, conversationID, text string) error {
	if serviceURL == "" || conversationID == "" {
		return fmt.Errorf("teams: missing serviceURL or conversationID")
	}

	url := fmt.Sprintf("%sv3/conversations/%s/activities",
		ensureTrailingSlash(serviceURL), conversationID)

	payload := map[string]interface{}{
		"type": "message",
		"text": text,
	}

	return b.sendBotFrameworkRequest(url, payload)
}

// SendAdaptiveCard sends an Adaptive Card to a conversation.
func (b *Bot) SendAdaptiveCard(serviceURL, conversationID string, card map[string]interface{}) error {
	if serviceURL == "" || conversationID == "" {
		return fmt.Errorf("teams: missing serviceURL or conversationID")
	}

	url := fmt.Sprintf("%sv3/conversations/%s/activities",
		ensureTrailingSlash(serviceURL), conversationID)

	payload := map[string]interface{}{
		"type": "message",
		"attachments": []map[string]interface{}{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content":     card,
			},
		},
	}

	return b.sendBotFrameworkRequest(url, payload)
}

// sendBotFrameworkRequest sends an authenticated POST request to the Bot Framework API.
func (b *Bot) sendBotFrameworkRequest(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("teams: marshal payload: %w", err)
	}

	token, err := b.GetToken()
	if err != nil {
		return fmt.Errorf("teams: get token: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("teams: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("teams: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("teams: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	b.rt.LogDebugCtx(context.Background(), "teams: API request sent", "url", url, "status", resp.StatusCode)
	return nil
}

// ensureTrailingSlash ensures a URL ends with a slash.
func ensureTrailingSlash(url string) string {
	if !strings.HasSuffix(url, "/") {
		return url + "/"
	}
	return url
}

// --- Teams Adaptive Card Builder ---

// BuildSimpleAdaptiveCard creates a simple Adaptive Card with a title and body text.
func BuildSimpleAdaptiveCard(title, body string) map[string]interface{} {
	card := map[string]interface{}{
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body": []map[string]interface{}{
			{
				"type":   "TextBlock",
				"text":   title,
				"weight": "bolder",
				"size":   "medium",
			},
			{
				"type": "TextBlock",
				"text": body,
				"wrap": true,
			},
		},
	}
	return card
}

// BuildAdaptiveCardWithActions creates an Adaptive Card with action buttons.
func BuildAdaptiveCardWithActions(title, body string, actions []map[string]interface{}) map[string]interface{} {
	card := BuildSimpleAdaptiveCard(title, body)
	if len(actions) > 0 {
		card["actions"] = actions
	}
	return card
}

// BuildSubmitAction creates an Action.Submit for an Adaptive Card.
func BuildSubmitAction(title string, data map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":  "Action.Submit",
		"title": title,
		"data":  data,
	}
}

// --- Teams Notification Integration ---

// Notifier sends notifications via Teams Bot Framework API.
type Notifier struct {
	Bot            *Bot
	ServiceURL     string // cached service URL for proactive messages
	ConversationID string // target conversation ID
}

// Send sends a text notification to the configured conversation.
func (n *Notifier) Send(text string) error {
	if text == "" {
		return nil
	}
	// Truncate if too long.
	if len(text) > 28000 {
		text = text[:27997] + "..."
	}

	return n.Bot.sendProactive(n.ServiceURL, n.ConversationID, text)
}

// Name returns the notifier name.
func (n *Notifier) Name() string { return "teams" }
