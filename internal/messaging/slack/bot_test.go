package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tetora/internal/messaging"
)

// mockRuntime implements messaging.BotRuntime with no-op methods for testing.
type mockRuntime struct{}

func (m *mockRuntime) Submit(ctx context.Context, req messaging.TaskRequest) (messaging.TaskResult, error) {
	return messaging.TaskResult{}, nil
}
func (m *mockRuntime) Route(ctx context.Context, prompt, source string) (string, error) {
	return "", nil
}
func (m *mockRuntime) GetOrCreateSession(platform, key, agent, title string) (string, error) {
	return "", nil
}
func (m *mockRuntime) BuildSessionContext(sessionID string, limit int) string { return "" }
func (m *mockRuntime) AddSessionMessage(sessionID, role, content string)      {}
func (m *mockRuntime) UpdateSessionStats(sessionID string, cost, tokensIn, tokensOut, msgCount float64) {
}
func (m *mockRuntime) RecordHistory(taskID, name, source, agent, outputFile string, task, result interface{}) {
}
func (m *mockRuntime) PublishEvent(eventType string, data map[string]interface{}) {}
func (m *mockRuntime) IsActive() bool                                            { return false }
func (m *mockRuntime) ExpandPrompt(prompt, agent string) string                  { return prompt }
func (m *mockRuntime) LoadAgentPrompt(agent string) (string, error)              { return "", nil }
func (m *mockRuntime) FillTaskDefaults(agent *string, name *string, source string) string {
	return ""
}
func (m *mockRuntime) HistoryDB() string   { return "" }
func (m *mockRuntime) WorkspaceDir() string { return "" }
func (m *mockRuntime) SaveUpload(filename string, data []byte) (string, error) {
	return "", nil
}
func (m *mockRuntime) Truncate(s string, maxLen int) string { return s }
func (m *mockRuntime) NewTraceID(source string) string      { return "" }
func (m *mockRuntime) WithTraceID(ctx context.Context, traceID string) context.Context {
	return ctx
}
func (m *mockRuntime) LogInfo(msg string, args ...interface{})                              {}
func (m *mockRuntime) LogWarn(msg string, args ...interface{})                              {}
func (m *mockRuntime) LogError(msg string, err error, args ...interface{})                  {}
func (m *mockRuntime) LogInfoCtx(ctx context.Context, msg string, args ...interface{})      {}
func (m *mockRuntime) LogErrorCtx(ctx context.Context, msg string, err error, args ...interface{}) {
}
func (m *mockRuntime) LogDebugCtx(ctx context.Context, msg string, args ...interface{}) {}
func (m *mockRuntime) ClientIP(r *http.Request) string                                  { return "" }
func (m *mockRuntime) AuditLog(action, source, target, ip string)                       {}
func (m *mockRuntime) QueryCostStats() (today, week, month float64)                     { return 0, 0, 0 }
func (m *mockRuntime) UpdateAgentModel(agent, model string) error                       { return nil }
func (m *mockRuntime) MaybeCompactSession(sessionID string, msgCount int, tokenCount float64) {}
func (m *mockRuntime) UpdateSessionTitle(sessionID, title string)                             {}
func (m *mockRuntime) SessionContextLimit() int                                               { return 10 }
func (m *mockRuntime) AgentConfig(agent string) (model, permMode string, found bool) {
	return "", "", false
}
func (m *mockRuntime) ArchiveSession(channelKey string) error { return nil }
func (m *mockRuntime) SetMemory(agent, key, value string)     {}
func (m *mockRuntime) SendWebhooks(status string, payload map[string]interface{}) {}
func (m *mockRuntime) StatusJSON() []byte                                         { return nil }
func (m *mockRuntime) ListCronJobs() []messaging.CronJobInfo                      { return nil }
func (m *mockRuntime) SmartDispatchEnabled() bool                                 { return false }
func (m *mockRuntime) DefaultAgent() string                                       { return "" }
func (m *mockRuntime) DefaultModel() string                                       { return "" }
func (m *mockRuntime) CostAlertDailyLimit() float64                               { return 0 }
func (m *mockRuntime) ApprovalGatesEnabled() bool                                 { return false }
func (m *mockRuntime) ApprovalGatesAutoApproveTools() []string                    { return nil }
func (m *mockRuntime) ProviderHasNativeSession(agent string) bool                 { return false }
func (m *mockRuntime) DownloadFile(url, filename, authHeader string) (string, error) {
	return "", nil
}
func (m *mockRuntime) BuildFilePromptPrefix(filePaths []string) string     { return "" }
func (m *mockRuntime) AgentModels() map[string]string                      { return nil }

// --- StripMentions ---

func TestStripSlackMentions(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello world", "hello world"},
		{"<@U12345> hello", "hello"},
		{"<@U12345|user> hello", "hello"},
		{"<@U12345> <@U67890> hello", "hello"},
		{"hello <@U12345> world", "hello  world"},
		{"<@U12345>", ""},
		{"", ""},
		{"no mentions here", "no mentions here"},
		{"<@UABC123> run tests <@UDEF456>", "run tests"},
	}

	for _, tt := range tests {
		got := StripMentions(tt.input)
		if got != tt.expected {
			t.Errorf("StripMentions(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- threadTS ---

func TestThreadTS(t *testing.T) {
	// When ThreadTS is set, use it.
	e1 := event{TS: "123.456", ThreadTS: "100.200"}
	if got := threadTS(e1); got != "100.200" {
		t.Errorf("threadTS with ThreadTS = %q, want %q", got, "100.200")
	}

	// When ThreadTS is empty, use TS.
	e2 := event{TS: "123.456", ThreadTS: ""}
	if got := threadTS(e2); got != "123.456" {
		t.Errorf("threadTS without ThreadTS = %q, want %q", got, "123.456")
	}
}

// --- VerifySignature ---

func TestVerifySlackSignature(t *testing.T) {
	secret := "test-signing-secret"
	body := []byte(`{"type":"event_callback"}`)
	ts := fmt.Sprintf("%d", time.Now().Unix())

	// Compute valid signature.
	baseStr := fmt.Sprintf("v0:%s:%s", ts, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(baseStr))
	validSig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	// Valid signature.
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", validSig)
	if !VerifySignature(req, body, secret) {
		t.Error("expected valid signature to pass")
	}

	// Invalid signature.
	req2 := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	req2.Header.Set("X-Slack-Request-Timestamp", ts)
	req2.Header.Set("X-Slack-Signature", "v0=deadbeef")
	if VerifySignature(req2, body, secret) {
		t.Error("expected invalid signature to fail")
	}

	// Missing headers.
	req3 := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	if VerifySignature(req3, body, secret) {
		t.Error("expected missing headers to fail")
	}

	// Expired timestamp (>5 min old).
	oldTS := fmt.Sprintf("%d", time.Now().Unix()-400)
	baseStr2 := fmt.Sprintf("v0:%s:%s", oldTS, string(body))
	mac2 := hmac.New(sha256.New, []byte(secret))
	mac2.Write([]byte(baseStr2))
	oldSig := "v0=" + hex.EncodeToString(mac2.Sum(nil))

	req4 := httptest.NewRequest("POST", "/slack/events", strings.NewReader(string(body)))
	req4.Header.Set("X-Slack-Request-Timestamp", oldTS)
	req4.Header.Set("X-Slack-Signature", oldSig)
	if VerifySignature(req4, body, secret) {
		t.Error("expected expired timestamp to fail")
	}
}

// --- EventHandler URL verification ---

func TestSlackEventHandler_URLVerification(t *testing.T) {
	rt := &mockRuntime{}
	b := NewBot(Config{Enabled: true, BotToken: "xoxb-test"}, rt)

	payload := `{"type":"url_verification","challenge":"test-challenge-123"}`
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(payload))
	w := httptest.NewRecorder()

	b.EventHandler(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["challenge"] != "test-challenge-123" {
		t.Errorf("challenge = %q, want %q", resp["challenge"], "test-challenge-123")
	}
}

// --- EventHandler method check ---

func TestSlackEventHandler_MethodNotAllowed(t *testing.T) {
	rt := &mockRuntime{}
	b := NewBot(Config{Enabled: true, BotToken: "xoxb-test"}, rt)

	req := httptest.NewRequest("GET", "/slack/events", nil)
	w := httptest.NewRecorder()

	b.EventHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// --- EventHandler signature verification ---

func TestSlackEventHandler_InvalidSignature(t *testing.T) {
	rt := &mockRuntime{}
	b := NewBot(Config{Enabled: true, BotToken: "xoxb-test", SigningSecret: "my-secret"}, rt)

	payload := `{"type":"url_verification","challenge":"test"}`
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(payload))
	req.Header.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	req.Header.Set("X-Slack-Signature", "v0=invalid")
	w := httptest.NewRecorder()

	b.EventHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- isDuplicate ---

func TestSlackBotDeduplicate(t *testing.T) {
	rt := &mockRuntime{}
	b := NewBot(Config{Enabled: true, BotToken: "xoxb-test"}, rt)

	// First call: not duplicate.
	if b.isDuplicate("event-001") {
		t.Error("first call should not be duplicate")
	}
	// Second call: duplicate.
	if !b.isDuplicate("event-001") {
		t.Error("second call should be duplicate")
	}
	// Different event: not duplicate.
	if b.isDuplicate("event-002") {
		t.Error("different event should not be duplicate")
	}
}

// --- Config JSON ---

func TestSlackBotConfigJSON(t *testing.T) {
	raw := `{
		"enabled": true,
		"botToken": "$SLACK_BOT_TOKEN",
		"signingSecret": "$SLACK_SIGNING_SECRET",
		"defaultChannel": "C12345"
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !cfg.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.BotToken != "$SLACK_BOT_TOKEN" {
		t.Errorf("botToken = %q, want %q", cfg.BotToken, "$SLACK_BOT_TOKEN")
	}
	if cfg.SigningSecret != "$SLACK_SIGNING_SECRET" {
		t.Errorf("signingSecret = %q", cfg.SigningSecret)
	}
	if cfg.DefaultChannel != "C12345" {
		t.Errorf("defaultChannel = %q", cfg.DefaultChannel)
	}
}

// --- Event callback with bot message (should be ignored) ---

func TestSlackEventHandler_IgnoresBotMessages(t *testing.T) {
	rt := &mockRuntime{}
	b := NewBot(Config{Enabled: true, BotToken: "xoxb-test"}, rt)

	ev := event{
		Type:  "message",
		Text:  "hello from bot",
		BotID: "B12345",
	}
	eventJSON, _ := json.Marshal(ev)

	payload := fmt.Sprintf(`{"type":"event_callback","event_id":"ev1","event":%s}`, string(eventJSON))
	req := httptest.NewRequest("POST", "/slack/events", strings.NewReader(payload))
	w := httptest.NewRecorder()

	b.EventHandler(w, req)

	// Should acknowledge immediately.
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
