package gchat

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"tetora/internal/messaging"
)

// --- Google Chat Message Types ---

// gchatEvent represents an incoming event from Google Chat.
type gchatEvent struct {
	Type      string        `json:"type"` // "MESSAGE", "ADDED_TO_SPACE", "REMOVED_FROM_SPACE", "CARD_CLICKED"
	EventTime string        `json:"eventTime"`
	Space     gchatSpace    `json:"space"`
	Message   *gchatMessage `json:"message,omitempty"`
	User      gchatUser     `json:"user"`
	Action    *gchatAction  `json:"action,omitempty"`
}

// gchatSpace represents a Google Chat space (room/DM).
type gchatSpace struct {
	Name        string `json:"name"`        // "spaces/{space_id}"
	Type        string `json:"type"`        // "ROOM", "DM"
	DisplayName string `json:"displayName"`
}

// gchatMessage represents a message in Google Chat.
type gchatMessage struct {
	Name         string            `json:"name"`         // "spaces/{space}/messages/{message}"
	Sender       gchatUser         `json:"sender"`
	CreateTime   string            `json:"createTime"`
	Text         string            `json:"text"`
	Thread       *gchatThread      `json:"thread,omitempty"`
	ArgumentText string            `json:"argumentText"` // text after @bot mention
	Annotations  []gchatAnnotation `json:"annotations,omitempty"`
	Attachment   []gchatAttachment `json:"attachment,omitempty"`
}

// gchatUser represents a Google Chat user.
type gchatUser struct {
	Name        string `json:"name"` // "users/{user_id}"
	DisplayName string `json:"displayName"`
	AvatarUrl   string `json:"avatarUrl,omitempty"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type"` // "HUMAN", "BOT"
}

// gchatThread represents a message thread.
type gchatThread struct {
	Name string `json:"name"` // "spaces/{space}/threads/{thread}"
}

// gchatAnnotation represents mentions or other annotations.
type gchatAnnotation struct {
	Type        string            `json:"type"` // "USER_MENTION"
	StartIndex  int               `json:"startIndex"`
	Length      int               `json:"length"`
	UserMention *gchatUserMention `json:"userMention,omitempty"`
}

// gchatUserMention represents a user mention.
type gchatUserMention struct {
	User gchatUser `json:"user"`
	Type string    `json:"type"` // "ADD", "MENTION"
}

// gchatAttachment represents a file attachment.
type gchatAttachment struct {
	Name        string `json:"name"`
	ContentName string `json:"contentName"`
	ContentType string `json:"contentType"`
	Source      string `json:"source"` // "UPLOADED_CONTENT", "DRIVE_FILE"
}

// gchatAction represents a card click action.
type gchatAction struct {
	ActionMethodName string                 `json:"actionMethodName"`
	Parameters       []gchatActionParameter `json:"parameters,omitempty"`
}

// gchatActionParameter represents a parameter for a card action.
type gchatActionParameter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// --- Google Chat Card Types ---

// gchatCard represents a Google Chat card message.
type gchatCard struct {
	Header   *gchatCardHeader   `json:"header,omitempty"`
	Sections []gchatCardSection `json:"sections"`
}

// gchatCardHeader represents a card header.
type gchatCardHeader struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	ImageUrl string `json:"imageUrl,omitempty"`
}

// gchatCardSection represents a card section.
type gchatCardSection struct {
	Header  string            `json:"header,omitempty"`
	Widgets []gchatCardWidget `json:"widgets"`
}

// gchatCardWidget represents a card widget.
type gchatCardWidget struct {
	TextParagraph *gchatTextParagraph `json:"textParagraph,omitempty"`
	KeyValue      *gchatKeyValue      `json:"keyValue,omitempty"`
	Buttons       []gchatButton       `json:"buttons,omitempty"`
}

// gchatTextParagraph represents a text paragraph widget.
type gchatTextParagraph struct {
	Text string `json:"text"`
}

// gchatKeyValue represents a key-value widget.
type gchatKeyValue struct {
	TopLabel    string `json:"topLabel,omitempty"`
	Content     string `json:"content"`
	BottomLabel string `json:"bottomLabel,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// gchatButton represents a button widget.
type gchatButton struct {
	TextButton  *gchatTextButton  `json:"textButton,omitempty"`
	ImageButton *gchatImageButton `json:"imageButton,omitempty"`
}

// gchatTextButton represents a text button.
type gchatTextButton struct {
	Text    string       `json:"text"`
	OnClick gchatOnClick `json:"onClick"`
}

// gchatImageButton represents an image button.
type gchatImageButton struct {
	Icon    string       `json:"icon"` // "STAR", "BOOKMARK", etc.
	OnClick gchatOnClick `json:"onClick"`
}

// gchatOnClick represents a button click action.
type gchatOnClick struct {
	Action   *gchatAction   `json:"action,omitempty"`
	OpenLink *gchatOpenLink `json:"openLink,omitempty"`
}

// gchatOpenLink represents a link to open.
type gchatOpenLink struct {
	Url string `json:"url"`
}

// --- Google Chat Send Request ---

// gchatSendRequest represents a request to send a message to Google Chat.
type gchatSendRequest struct {
	Text   string       `json:"text,omitempty"`
	Cards  []gchatCard  `json:"cards,omitempty"`
	Thread *gchatThread `json:"thread,omitempty"`
}

// --- Service Account Types ---

// serviceAccountKey represents a Google service account key JSON.
type serviceAccountKey struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id"`
	AuthURI                 string `json:"auth_uri"`
	TokenURI                string `json:"token_uri"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
	ClientX509CertURL       string `json:"client_x509_cert_url"`
}

// --- Bot ---

// Bot handles incoming Google Chat messages and sends responses.
type Bot struct {
	cfg     Config
	rt      messaging.BotRuntime
	saKey   *serviceAccountKey
	privKey *rsa.PrivateKey

	// Token cache.
	tokenCache  string
	tokenExpiry time.Time
	tokenMu     sync.Mutex

	// Dedup: track recently processed message IDs.
	processed     map[string]time.Time
	processedSize int
	mu            sync.Mutex

	// httpClient for API calls (replaceable for testing).
	httpClient *http.Client
}

// NewBot creates a new Bot instance, loading and parsing the service account key.
func NewBot(cfg Config, rt messaging.BotRuntime) (*Bot, error) {
	b := &Bot{
		cfg:        cfg,
		rt:         rt,
		processed:  make(map[string]time.Time),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}

	keyPath := cfg.ServiceAccountKey
	if keyPath == "" {
		return nil, fmt.Errorf("gchat: serviceAccountKey not configured")
	}

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("gchat: failed to read service account key: %w", err)
	}

	var saKey serviceAccountKey
	if err := json.Unmarshal(keyData, &saKey); err != nil {
		return nil, fmt.Errorf("gchat: failed to parse service account key: %w", err)
	}
	b.saKey = &saKey

	// Parse RSA private key.
	block, _ := pem.Decode([]byte(saKey.PrivateKey))
	if block == nil {
		return nil, fmt.Errorf("gchat: failed to decode PEM private key")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 format.
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("gchat: failed to parse private key: %w", err)
		}
	}

	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("gchat: private key is not RSA")
	}
	b.privKey = rsaKey

	rt.LogInfo("gchat: initialized", "clientEmail", saKey.ClientEmail)
	return b, nil
}

// HandleWebhook handles incoming Google Chat events.
func (b *Bot) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.rt.LogError("gchat: failed to read webhook body", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var event gchatEvent
	if err := json.Unmarshal(body, &event); err != nil {
		b.rt.LogError("gchat: failed to parse webhook event", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	b.rt.LogDebugCtx(r.Context(), "gchat: received event", "type", event.Type, "space", event.Space.Name)

	switch event.Type {
	case "MESSAGE":
		b.handleMessage(w, &event)
	case "ADDED_TO_SPACE":
		b.handleAddedToSpace(w, &event)
	case "REMOVED_FROM_SPACE":
		b.handleRemovedFromSpace(w, &event)
	case "CARD_CLICKED":
		b.handleCardClicked(w, &event)
	default:
		b.rt.LogWarn("gchat: unknown event type", "type", event.Type)
		w.WriteHeader(http.StatusOK)
	}
}

// handleMessage processes a MESSAGE event.
func (b *Bot) handleMessage(w http.ResponseWriter, event *gchatEvent) {
	if event.Message == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Dedup check.
	msgID := event.Message.Name
	if b.isDuplicate(msgID) {
		b.rt.LogDebugCtx(context.Background(), "gchat: duplicate message", "msgID", msgID)
		w.WriteHeader(http.StatusOK)
		return
	}
	b.markProcessed(msgID)

	// Ignore bot messages.
	if event.Message.Sender.Type == "BOT" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Extract text (use argumentText if available, else text).
	text := strings.TrimSpace(event.Message.ArgumentText)
	if text == "" {
		text = strings.TrimSpace(event.Message.Text)
	}
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Determine agent.
	role := b.cfg.DefaultAgent

	// Resolve thread name.
	spaceName := event.Space.Name
	threadName := ""
	if event.Message.Thread != nil {
		threadName = event.Message.Thread.Name
	}

	go b.dispatchTask(spaceName, threadName, role, text, event.User.DisplayName)

	// Send immediate acknowledgment.
	resp := gchatSendRequest{Text: "Processing..."}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleAddedToSpace processes an ADDED_TO_SPACE event.
func (b *Bot) handleAddedToSpace(w http.ResponseWriter, event *gchatEvent) {
	b.rt.LogInfo("gchat: added to space", "space", event.Space.Name, "type", event.Space.Type)

	resp := gchatSendRequest{Text: "Hello! I'm Tetora bot. Send me a message to get started."}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// handleRemovedFromSpace processes a REMOVED_FROM_SPACE event.
func (b *Bot) handleRemovedFromSpace(w http.ResponseWriter, event *gchatEvent) {
	b.rt.LogInfo("gchat: removed from space", "space", event.Space.Name)
	w.WriteHeader(http.StatusOK)
}

// handleCardClicked processes a CARD_CLICKED event.
func (b *Bot) handleCardClicked(w http.ResponseWriter, event *gchatEvent) {
	b.rt.LogDebugCtx(context.Background(), "gchat: card clicked", "action", event.Action)
	w.WriteHeader(http.StatusOK)
}

// dispatchTask dispatches a task to the agent and sends the result back.
// Google Chat does not use session management.
func (b *Bot) dispatchTask(spaceName, threadName, role, text, userName string) {
	ctx := context.Background()

	model, permMode, exists := b.rt.AgentConfig(role)
	if !exists {
		b.sendTextMessage(spaceName, threadName, fmt.Sprintf("Unknown agent: %s", role)) //nolint:errcheck
		return
	}

	prompt := b.rt.ExpandPrompt(text, role)
	soulPrompt, _ := b.rt.LoadAgentPrompt(role)

	result, _ := b.rt.Submit(ctx, messaging.TaskRequest{
		AgentRole:      role,
		Content:        prompt,
		SystemPrompt:   soulPrompt,
		Model:          model,
		PermissionMode: permMode,
		Meta:           map[string]string{"source": "gchat", "user": userName},
	})

	var responseText string
	if result.Error != "" {
		responseText = fmt.Sprintf("Error: %s", result.Error)
	} else {
		responseText = result.Output
	}

	if err := b.sendTextMessage(spaceName, threadName, responseText); err != nil {
		b.rt.LogError("gchat: failed to send response", err, "space", spaceName)
	}
}

// sendTextMessage sends a text message to a Google Chat space.
func (b *Bot) sendTextMessage(spaceName, threadName, text string) error {
	req := gchatSendRequest{Text: text}
	if threadName != "" {
		req.Thread = &gchatThread{Name: threadName}
	}
	return b.sendMessage(spaceName, req)
}

// sendCardMessage sends a card message to a Google Chat space.
func (b *Bot) sendCardMessage(spaceName, threadName string, card gchatCard) error {
	req := gchatSendRequest{Cards: []gchatCard{card}}
	if threadName != "" {
		req.Thread = &gchatThread{Name: threadName}
	}
	return b.sendMessage(spaceName, req)
}

// sendMessage sends a message to a Google Chat space via the REST API.
func (b *Bot) sendMessage(spaceName string, req gchatSendRequest) error {
	token, err := b.getAccessToken()
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("https://chat.googleapis.com/v1/%s/messages", spaceName)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error: HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// getAccessToken returns a valid OAuth2 access token, generating a new one if needed.
func (b *Bot) getAccessToken() (string, error) {
	b.tokenMu.Lock()
	defer b.tokenMu.Unlock()

	// Return cached token if still valid.
	if b.tokenCache != "" && time.Now().Before(b.tokenExpiry) {
		return b.tokenCache, nil
	}

	jwt, err := b.createJWT()
	if err != nil {
		return "", fmt.Errorf("failed to create JWT: %w", err)
	}

	token, expiresIn, err := b.exchangeJWT(jwt)
	if err != nil {
		return "", fmt.Errorf("failed to exchange JWT: %w", err)
	}

	b.tokenCache = token
	b.tokenExpiry = time.Now().Add(time.Duration(expiresIn-60) * time.Second) // 60s buffer

	return token, nil
}

// createJWT creates a signed JWT for service account authentication.
func (b *Bot) createJWT() (string, error) {
	now := time.Now().Unix()
	claims := map[string]interface{}{
		"iss":   b.saKey.ClientEmail,
		"scope": "https://www.googleapis.com/auth/chat.bot",
		"aud":   "https://oauth2.googleapis.com/token",
		"exp":   now + 3600,
		"iat":   now,
	}

	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	claimsJSON, _ := json.Marshal(claims)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signInput := headerB64 + "." + claimsB64
	hash := sha256.Sum256([]byte(signInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, b.privKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signInput + "." + signatureB64, nil
}

// exchangeJWT exchanges a signed JWT for an OAuth2 access token.
func (b *Bot) exchangeJWT(jwt string) (token string, expiresIn int, err error) {
	payload := fmt.Sprintf("grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=%s", jwt)

	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token", strings.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("token exchange failed: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse token response: %w", err)
	}

	return result.AccessToken, result.ExpiresIn, nil
}

// isDuplicate checks if a message ID has been processed recently.
func (b *Bot) isDuplicate(msgID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Clean old entries (older than 5 minutes).
	cutoff := time.Now().Add(-5 * time.Minute)
	for id, t := range b.processed {
		if t.Before(cutoff) {
			delete(b.processed, id)
			b.processedSize--
		}
	}

	// Limit map size to prevent unbounded growth.
	if b.processedSize > 10000 {
		b.processed = make(map[string]time.Time)
		b.processedSize = 0
	}

	_, exists := b.processed[msgID]
	return exists
}

// markProcessed marks a message ID as processed.
func (b *Bot) markProcessed(msgID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.processed[msgID] = time.Now()
	b.processedSize++
}

// SetTyping is a no-op; Google Chat does not support typing indicators.
func (b *Bot) SetTyping(_ context.Context, _ string) error {
	return nil
}

// PresenceName returns the platform identifier.
func (b *Bot) PresenceName() string {
	return "gchat"
}

// --- Notifier ---

// Notifier sends notifications to a fixed Google Chat space.
type Notifier struct {
	Bot       *Bot
	SpaceName string // "spaces/{space_id}"
}

// Send sends a text message to the configured space.
func (n *Notifier) Send(text string) error {
	return n.Bot.sendTextMessage(n.SpaceName, "", text)
}

// Name returns the notifier identifier.
func (n *Notifier) Name() string {
	return "gchat"
}
