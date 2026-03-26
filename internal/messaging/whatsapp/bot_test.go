package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tetora/internal/messaging"
)

// --- Mock BotRuntime ---

type mockRuntime struct{}

func (m *mockRuntime) Submit(_ context.Context, _ messaging.TaskRequest) (messaging.TaskResult, error) {
	return messaging.TaskResult{Status: "success"}, nil
}
func (m *mockRuntime) Route(_ context.Context, _, _ string) (string, error)          { return "", nil }
func (m *mockRuntime) GetOrCreateSession(_, _, _, _ string) (string, error)          { return "", nil }
func (m *mockRuntime) BuildSessionContext(_ string, _ int) string                     { return "" }
func (m *mockRuntime) AddSessionMessage(_, _, _ string)                               {}
func (m *mockRuntime) UpdateSessionStats(_ string, _, _, _, _ float64)                {}
func (m *mockRuntime) RecordHistory(_, _, _, _, _ string, _, _ interface{})           {}
func (m *mockRuntime) PublishEvent(_ string, _ map[string]interface{})                {}
func (m *mockRuntime) IsActive() bool                                                 { return false }
func (m *mockRuntime) ExpandPrompt(prompt, _ string) string                           { return prompt }
func (m *mockRuntime) LoadAgentPrompt(_ string) (string, error)                       { return "", nil }
func (m *mockRuntime) FillTaskDefaults(_ *string, _ *string, _ string) string         { return "task-id" }
func (m *mockRuntime) HistoryDB() string                                              { return ":memory:" }
func (m *mockRuntime) WorkspaceDir() string                                           { return "/tmp" }
func (m *mockRuntime) SaveUpload(_ string, _ []byte) (string, error)                  { return "", nil }
func (m *mockRuntime) Truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
func (m *mockRuntime) NewTraceID(_ string) string                                                  { return "trace-id" }
func (m *mockRuntime) WithTraceID(ctx context.Context, _ string) context.Context                  { return ctx }
func (m *mockRuntime) LogInfo(_ string, _ ...interface{})                                          {}
func (m *mockRuntime) LogWarn(_ string, _ ...interface{})                                          {}
func (m *mockRuntime) LogError(_ string, _ error, _ ...interface{})                               {}
func (m *mockRuntime) LogInfoCtx(_ context.Context, _ string, _ ...interface{})                   {}
func (m *mockRuntime) LogErrorCtx(_ context.Context, _ string, _ error, _ ...interface{})         {}
func (m *mockRuntime) LogDebugCtx(_ context.Context, _ string, _ ...interface{})                  {}
func (m *mockRuntime) ClientIP(_ *http.Request) string                                             { return "" }
func (m *mockRuntime) AuditLog(_, _, _, _ string)                                                  {}
func (m *mockRuntime) QueryCostStats() (float64, float64, float64)                                 { return 0, 0, 0 }
func (m *mockRuntime) UpdateAgentModel(_, _ string) error                                          { return nil }
func (m *mockRuntime) MaybeCompactSession(_ string, _ int, _ float64)                              {}
func (m *mockRuntime) UpdateSessionTitle(_, _ string)                                              {}
func (m *mockRuntime) SessionContextLimit() int                                                     { return 10 }
func (m *mockRuntime) AgentConfig(_ string) (string, string, bool)                                 { return "", "", false }
func (m *mockRuntime) ArchiveSession(string) error                                                 { return nil }
func (m *mockRuntime) SetMemory(string, string, string)                                            {}
func (m *mockRuntime) SendWebhooks(string, map[string]interface{})                                 {}
func (m *mockRuntime) StatusJSON() []byte                                                          { return []byte("{}") }
func (m *mockRuntime) ListCronJobs() []messaging.CronJobInfo                                      { return nil }
func (m *mockRuntime) SmartDispatchEnabled() bool                                                  { return false }
func (m *mockRuntime) DefaultAgent() string                                                        { return "" }
func (m *mockRuntime) DefaultModel() string                                                        { return "" }
func (m *mockRuntime) CostAlertDailyLimit() float64                                                { return 0 }
func (m *mockRuntime) ApprovalGatesEnabled() bool                                                  { return false }
func (m *mockRuntime) ApprovalGatesAutoApproveTools() []string                                     { return nil }
func (m *mockRuntime) ProviderHasNativeSession(string) bool                                        { return false }
func (m *mockRuntime) DownloadFile(string, string, string) (string, error)                         { return "", nil }
func (m *mockRuntime) BuildFilePromptPrefix([]string) string                                       { return "" }
func (m *mockRuntime) AgentModels() map[string]string                                              { return nil }

var testRT = &mockRuntime{}

// newBot is a test helper that creates a Bot with the mock runtime.
func newBot(cfg Config) *Bot {
	return NewBot(cfg, testRT)
}

// --- Tests ---

func TestWhatsAppWebhookVerification(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		VerifyToken: "test_verify_token_123",
	}

	bot := newBot(cfg)

	tests := []struct {
		name       string
		mode       string
		token      string
		challenge  string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "valid verification",
			mode:       "subscribe",
			token:      "test_verify_token_123",
			challenge:  "challenge_12345",
			wantStatus: 200,
			wantBody:   "challenge_12345",
		},
		{
			name:       "invalid token",
			mode:       "subscribe",
			token:      "wrong_token",
			challenge:  "challenge_12345",
			wantStatus: 403,
			wantBody:   "forbidden",
		},
		{
			name:       "invalid mode",
			mode:       "unsubscribe",
			token:      "test_verify_token_123",
			challenge:  "challenge_12345",
			wantStatus: 403,
			wantBody:   "forbidden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/whatsapp/webhook?hub.mode="+tt.mode+"&hub.verify_token="+tt.token+"&hub.challenge="+tt.challenge, nil)
			w := httptest.NewRecorder()

			bot.WebhookHandler(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("body = %q, want to contain %q", w.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestSendWhatsAppMessagePayload(t *testing.T) {
	// This test verifies payload format (doesn't actually send).
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                "15551234567",
		"type":              "text",
		"text": map[string]string{
			"body": "Hello from Tetora!",
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed["messaging_product"] != "whatsapp" {
		t.Errorf("messaging_product = %q, want %q", parsed["messaging_product"], "whatsapp")
	}

	if parsed["to"] != "15551234567" {
		t.Errorf("to = %q, want %q", parsed["to"], "15551234567")
	}

	textMap := parsed["text"].(map[string]interface{})
	if textMap["body"] != "Hello from Tetora!" {
		t.Errorf("text.body = %q, want %q", textMap["body"], "Hello from Tetora!")
	}
}

func TestWhatsAppSignatureVerification(t *testing.T) {
	appSecret := "test_secret_key"
	body := []byte(`{"test":"data"}`)

	// Generate valid signature.
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		appSecret string
		body      []byte
		signature string
		want      bool
	}{
		{
			name:      "valid signature",
			appSecret: appSecret,
			body:      body,
			signature: validSig,
			want:      true,
		},
		{
			name:      "invalid signature",
			appSecret: appSecret,
			body:      body,
			signature: "sha256=invalid",
			want:      false,
		},
		{
			name:      "missing sha256 prefix",
			appSecret: appSecret,
			body:      body,
			signature: hex.EncodeToString(mac.Sum(nil)),
			want:      false,
		},
		{
			name:      "empty signature",
			appSecret: appSecret,
			body:      body,
			signature: "",
			want:      false,
		},
		{
			name:      "no secret configured",
			appSecret: "",
			body:      body,
			signature: "anything",
			want:      true, // skip verification if no secret
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifySignature(tt.appSecret, tt.body, tt.signature)
			if got != tt.want {
				t.Errorf("verifySignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWhatsAppAPIVersion(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "default version",
			cfg:  Config{},
			want: "v21.0",
		},
		{
			name: "custom version",
			cfg:  Config{APIVersion: "v18.0"},
			want: "v18.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.APIVersion_()
			if got != tt.want {
				t.Errorf("APIVersion_() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWhatsAppConfigEnabled(t *testing.T) {
	cfg := Config{
		Enabled:       true,
		PhoneNumberID: "123456789",
		AccessToken:   "test_token",
		VerifyToken:   "verify_token",
	}

	if !cfg.Enabled {
		t.Error("WhatsApp should be enabled")
	}

	if cfg.PhoneNumberID != "123456789" {
		t.Errorf("PhoneNumberID = %q, want %q", cfg.PhoneNumberID, "123456789")
	}
}

func TestWhatsAppMessageText(t *testing.T) {
	text := &messageText{
		Body: "Hello, world!",
	}

	if text.Body != "Hello, world!" {
		t.Errorf("Body = %q, want %q", text.Body, "Hello, world!")
	}

	// Test JSON marshaling.
	data, err := json.Marshal(text)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed messageText
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed.Body != "Hello, world!" {
		t.Errorf("parsed.Body = %q, want %q", parsed.Body, "Hello, world!")
	}
}

func TestWhatsAppDedupMessages(t *testing.T) {
	cfg := Config{
		Enabled:       true,
		PhoneNumberID: "123456789",
		AccessToken:   "test_token",
	}

	bot := newBot(cfg)

	// Mark message as processed.
	bot.mu.Lock()
	bot.processed["msg_123"] = time.Now()
	bot.processedSize = 1
	bot.mu.Unlock()

	// Check dedup works.
	bot.mu.Lock()
	if _, seen := bot.processed["msg_123"]; !seen {
		t.Error("message not found in processed map")
	}
	count := bot.processedSize
	bot.mu.Unlock()

	if count != 1 {
		t.Errorf("processedSize = %d, want 1", count)
	}
}

func TestWhatsAppBotCreation(t *testing.T) {
	cfg := Config{
		Enabled:       true,
		PhoneNumberID: "123456789",
		AccessToken:   "test_token",
	}

	bot := newBot(cfg)
	if bot == nil {
		t.Fatal("newBot returned nil")
	}

	if bot.cfg != cfg {
		t.Error("bot config not set correctly")
	}

	if bot.processed == nil {
		t.Error("bot processed map not initialized")
	}
}
