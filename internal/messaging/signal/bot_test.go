package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestSignalWebhookParsing(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		APIBaseURL:  "http://localhost:8080",
		PhoneNumber: "+1234567890",
	}
	bot := newBot(cfg)

	// Sample webhook payload.
	payload := receivePayload{
		Envelope: envelope{
			Source:     "+0987654321",
			SourceName: "Test User",
			SourceUUID: "test-uuid",
			Timestamp:  time.Now().UnixMilli(),
			DataMessage: &dataMessage{
				Timestamp: time.Now().UnixMilli(),
				Message:   "Hello from Signal",
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/signal/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)

	// Check dedup tracking.
	bot.mu.Lock()
	// Check if the message was tracked (processedSize > 0).
	if bot.processedSize == 0 {
		t.Error("expected message to be tracked in dedup map")
	}
	bot.mu.Unlock()
}

func TestSignalGroupMessageHandling(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		APIBaseURL:  "http://localhost:8080",
		PhoneNumber: "+1234567890",
	}
	bot := newBot(cfg)

	// Sample group message.
	payload := receivePayload{
		Envelope: envelope{
			Source:     "+0987654321",
			SourceName: "Group Member",
			Timestamp:  time.Now().UnixMilli(),
			DataMessage: &dataMessage{
				Timestamp: time.Now().UnixMilli(),
				Message:   "Group message test",
				GroupInfo: &groupInfo{
					GroupID: "group-123",
					Type:    "DELIVER",
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/signal/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)

	// Verify group message was tracked.
	bot.mu.Lock()
	if bot.processedSize == 0 {
		t.Error("expected group message to be tracked")
	}
	bot.mu.Unlock()
}

func TestSignalMessageDedup(t *testing.T) {
	cfg := Config{
		Enabled: true,
	}
	bot := newBot(cfg)

	env := envelope{
		Source:    "+0987654321",
		Timestamp: time.Now().UnixMilli(),
		DataMessage: &dataMessage{
			Timestamp: time.Now().UnixMilli(),
			Message:   "Duplicate test",
		},
	}

	// Process once.
	bot.processEnvelope(env)
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	firstSize := bot.processedSize
	bot.mu.Unlock()

	// Process again (should be deduped).
	bot.processEnvelope(env)
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	secondSize := bot.processedSize
	bot.mu.Unlock()

	if firstSize != secondSize {
		t.Errorf("expected dedup to prevent duplicate processing, got first=%d second=%d", firstSize, secondSize)
	}
}

func TestSignalSendMessage(t *testing.T) {
	// Mock HTTP server.
	var capturedRequest *http.Request
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRequest = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"timestamp": 123456789}`))
	}))
	defer server.Close()

	cfg := Config{
		Enabled:     true,
		APIBaseURL:  server.URL,
		PhoneNumber: "+1234567890",
	}
	bot := newBot(cfg)

	err := bot.SendMessage("+0987654321", "Test message")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Verify request.
	if capturedRequest.Method != "POST" {
		t.Errorf("expected POST, got %s", capturedRequest.Method)
	}

	if !strings.Contains(capturedRequest.URL.Path, "/v2/send") {
		t.Errorf("expected /v2/send path, got %s", capturedRequest.URL.Path)
	}

	// Verify payload.
	var payload sendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.Number != "+0987654321" {
		t.Errorf("expected number +0987654321, got %s", payload.Number)
	}

	if payload.Message != "Test message" {
		t.Errorf("expected message 'Test message', got %s", payload.Message)
	}
}

func TestSignalSendGroupMessage(t *testing.T) {
	// Mock HTTP server.
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"timestamp": 123456789}`))
	}))
	defer server.Close()

	cfg := Config{
		Enabled:    true,
		APIBaseURL: server.URL,
	}
	bot := newBot(cfg)

	err := bot.SendGroupMessage("group-abc123", "Test group message")
	if err != nil {
		t.Fatalf("SendGroupMessage failed: %v", err)
	}

	// Verify payload.
	var payload sendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.GroupID != "group-abc123" {
		t.Errorf("expected groupID group-abc123, got %s", payload.GroupID)
	}

	if payload.Message != "Test group message" {
		t.Errorf("expected message 'Test group message', got %s", payload.Message)
	}
}

func TestSignalNotifier(t *testing.T) {
	// Mock HTTP server.
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &Notifier{
		Config: Config{
			APIBaseURL: server.URL,
		},
		Recipient: "+1234567890",
		IsGroup:   false,
	}

	err := notifier.Send("Notification test")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify payload.
	var payload sendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.Number != "+1234567890" {
		t.Errorf("expected number +1234567890, got %s", payload.Number)
	}

	if payload.Message != "Notification test" {
		t.Errorf("expected message 'Notification test', got %s", payload.Message)
	}
}

func TestSignalNotifierGroupMode(t *testing.T) {
	// Mock HTTP server.
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &Notifier{
		Config: Config{
			APIBaseURL: server.URL,
		},
		Recipient: "group-xyz",
		IsGroup:   true,
	}

	err := notifier.Send("Group notification")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify payload.
	var payload sendRequest
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if payload.GroupID != "group-xyz" {
		t.Errorf("expected groupID group-xyz, got %s", payload.GroupID)
	}

	if payload.Message != "Group notification" {
		t.Errorf("expected message 'Group notification', got %s", payload.Message)
	}
}

func TestSignalEmptyMessageIgnored(t *testing.T) {
	cfg := Config{
		Enabled: true,
	}
	bot := newBot(cfg)

	env := envelope{
		Source:    "+0987654321",
		Timestamp: time.Now().UnixMilli(),
		DataMessage: &dataMessage{
			Timestamp: time.Now().UnixMilli(),
			Message:   "",
		},
	}

	bot.processEnvelope(env)
	time.Sleep(50 * time.Millisecond)

	bot.mu.Lock()
	if bot.processedSize != 0 {
		t.Error("expected empty message to be ignored")
	}
	bot.mu.Unlock()
}
