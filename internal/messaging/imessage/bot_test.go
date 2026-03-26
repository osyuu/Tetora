package imessage

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestIMessageBotNewBot(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test-pass",
		AllowedChats: []string{"iMessage;-;+1234567890"},
	}

	bot := newBot(cfg)
	if bot == nil {
		t.Fatal("expected non-nil bot")
	}
	if bot.serverURL != "http://localhost:1234" {
		t.Errorf("serverURL = %q, want %q", bot.serverURL, "http://localhost:1234")
	}
	if bot.password != "test-pass" {
		t.Errorf("password = %q, want %q", bot.password, "test-pass")
	}
	if bot.client == nil {
		t.Error("expected non-nil client")
	}
	if bot.dedup == nil {
		t.Error("expected non-nil dedup map")
	}
}

func TestIMessageBotNewBotTrailingSlash(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234/",
		Password:  "pw",
	}
	bot := newBot(cfg)
	if bot.serverURL != "http://localhost:1234" {
		t.Errorf("serverURL = %q, want no trailing slash", bot.serverURL)
	}
}

func TestIMessageWebhookHandler(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/imessage/webhook", nil)
		w := httptest.NewRecorder()
		bot.WebhookHandler(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/imessage/webhook", strings.NewReader("not json"))
		w := httptest.NewRecorder()
		bot.WebhookHandler(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("non-message event", func(t *testing.T) {
		payload := `{"type": "chat-read-status-changed", "data": {}}`
		req := httptest.NewRequest("POST", "/api/imessage/webhook", strings.NewReader(payload))
		w := httptest.NewRecorder()
		bot.WebhookHandler(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("valid new-message event returns 200", func(t *testing.T) {
		msg := Message{
			GUID:     "msg-001",
			ChatGUID: "iMessage;-;+1234567890",
			Text:     "Hello",
			IsFromMe: true, // from self, should be skipped in HandleMessage
		}
		msgJSON, _ := json.Marshal(msg)
		payload := WebhookPayload{
			Type: "new-message",
			Data: json.RawMessage(msgJSON),
		}
		payloadJSON, _ := json.Marshal(payload)
		req := httptest.NewRequest("POST", "/api/imessage/webhook", strings.NewReader(string(payloadJSON)))
		w := httptest.NewRecorder()
		bot.WebhookHandler(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestIMessageHandleMessageDedup(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		ServerURL:    "http://localhost:1234",
		Password:     "test",
		AllowedChats: []string{}, // empty = allow all
	}
	bot := newBot(cfg)

	// Add a GUID to dedup.
	bot.mu.Lock()
	bot.dedup["msg-dup"] = time.Now()
	bot.mu.Unlock()

	// HandleMessage should skip duplicate (no panic, no crash).
	bot.HandleMessage(Message{
		GUID:     "msg-dup",
		ChatGUID: "chat-1",
		Text:     "duplicate",
		IsFromMe: false,
	})

	// The dedup entry should still exist.
	bot.mu.Lock()
	_, exists := bot.dedup["msg-dup"]
	bot.mu.Unlock()
	if !exists {
		t.Error("dedup entry should still exist")
	}
}

func TestIMessageHandleMessageIsFromMe(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	// HandleMessage should skip isFromMe (no panic, no dispatch).
	bot.HandleMessage(Message{
		GUID:     "msg-self",
		ChatGUID: "chat-1",
		Text:     "my own message",
		IsFromMe: true,
	})

	// Should not appear in dedup since it's skipped before dedup check.
	bot.mu.Lock()
	_, exists := bot.dedup["msg-self"]
	bot.mu.Unlock()
	if exists {
		t.Error("isFromMe message should not be added to dedup")
	}
}

func TestIMessageHandleMessageEmpty(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	bot.HandleMessage(Message{
		GUID:     "msg-empty",
		ChatGUID: "chat-1",
		Text:     "   ",
		IsFromMe: false,
	})

	bot.mu.Lock()
	_, exists := bot.dedup["msg-empty"]
	bot.mu.Unlock()
	if exists {
		t.Error("empty message should not be added to dedup")
	}
}

func TestIMessageHandleMessageAllowedChats(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		ServerURL:    "http://localhost:1234",
		Password:     "test",
		AllowedChats: []string{"chat-allowed"},
	}
	bot := newBot(cfg)

	// Message from non-allowed chat should be skipped after dedup.
	bot.HandleMessage(Message{
		GUID:     "msg-blocked",
		ChatGUID: "chat-other",
		Text:     "hello from unknown",
		IsFromMe: false,
	})

	// The dedup entry should exist (message was processed up to allowedChats check).
	bot.mu.Lock()
	_, exists := bot.dedup["msg-blocked"]
	bot.mu.Unlock()
	if !exists {
		t.Error("dedup entry should exist even for blocked chats")
	}
}

func TestIMessageSendMessage(t *testing.T) {
	var receivedBody map[string]string
	var receivedPassword string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPassword = r.URL.Query().Get("password")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": 200}`))
	}))
	defer ts.Close()

	cfg := Config{
		Enabled:   true,
		ServerURL: ts.URL,
		Password:  "test-pw",
	}
	bot := newBot(cfg)

	err := bot.SendMessage("iMessage;-;+1234567890", "Hello World")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	if receivedPassword != "test-pw" {
		t.Errorf("password = %q, want %q", receivedPassword, "test-pw")
	}
	if receivedBody["chatGuid"] != "iMessage;-;+1234567890" {
		t.Errorf("chatGuid = %q, want %q", receivedBody["chatGuid"], "iMessage;-;+1234567890")
	}
	if receivedBody["message"] != "Hello World" {
		t.Errorf("message = %q, want %q", receivedBody["message"], "Hello World")
	}
}

func TestIMessageSendMessageEmpty(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	if err := bot.SendMessage("", "text"); err == nil {
		t.Error("expected error for empty chatGUID")
	}
	if err := bot.SendMessage("chat", ""); err == nil {
		t.Error("expected error for empty text")
	}
}

func TestIMessageSendMessageHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server error"}`))
	}))
	defer ts.Close()

	cfg := Config{
		Enabled:   true,
		ServerURL: ts.URL,
		Password:  "test",
	}
	bot := newBot(cfg)

	err := bot.SendMessage("chat-1", "hello")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code, got: %s", err.Error())
	}
}

func TestIMessageSearchMessages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("password") != "test-pw" {
			t.Errorf("password = %q, want %q", r.URL.Query().Get("password"), "test-pw")
		}
		if r.URL.Query().Get("query") != "hello" {
			t.Errorf("query = %q, want %q", r.URL.Query().Get("query"), "hello")
		}
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"guid":        "msg-001",
					"chatGuid":    "chat-1",
					"text":        "hello world",
					"handle":      map[string]string{"address": "+1234567890"},
					"dateCreated": 1700000000000,
					"isFromMe":    false,
				},
				{
					"guid":        "msg-002",
					"chatGuid":    "chat-1",
					"text":        "hello again",
					"handle":      map[string]string{"address": "+0987654321"},
					"dateCreated": 1700000001000,
					"isFromMe":    true,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := Config{
		Enabled:   true,
		ServerURL: ts.URL,
		Password:  "test-pw",
	}
	bot := newBot(cfg)

	msgs, err := bot.SearchMessages("hello", 10)
	if err != nil {
		t.Fatalf("SearchMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].GUID != "msg-001" {
		t.Errorf("first message GUID = %q, want %q", msgs[0].GUID, "msg-001")
	}
	if msgs[0].Handle != "+1234567890" {
		t.Errorf("first message handle = %q, want %q", msgs[0].Handle, "+1234567890")
	}
	if msgs[1].IsFromMe != true {
		t.Error("second message should be isFromMe=true")
	}
}

func TestIMessageSearchMessagesEmpty(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	_, err := bot.SearchMessages("", 10)
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestIMessageReadRecentMessages(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check URL path contains chat GUID.
		if !strings.Contains(r.URL.Path, "chat-1") {
			t.Errorf("URL path should contain chat GUID, got: %s", r.URL.Path)
		}
		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"guid":        "msg-recent-1",
					"chatGuid":    "chat-1",
					"text":        "recent message",
					"handle":      map[string]string{"address": "+111"},
					"dateCreated": 1700000000000,
					"isFromMe":    false,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := Config{
		Enabled:   true,
		ServerURL: ts.URL,
		Password:  "test",
	}
	bot := newBot(cfg)

	msgs, err := bot.ReadRecentMessages("chat-1", 5)
	if err != nil {
		t.Fatalf("ReadRecentMessages failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Text != "recent message" {
		t.Errorf("text = %q, want %q", msgs[0].Text, "recent message")
	}
}

func TestIMessageReadRecentMessagesEmptyChatGUID(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	_, err := bot.ReadRecentMessages("", 5)
	if err == nil {
		t.Error("expected error for empty chatGUID")
	}
}

func TestIMessageSendTapback(t *testing.T) {
	var receivedBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "msg-001") {
			t.Errorf("URL path should contain message GUID, got: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := Config{
		Enabled:   true,
		ServerURL: ts.URL,
		Password:  "test",
	}
	bot := newBot(cfg)

	err := bot.SendTapback("chat-1", "msg-001", 2000)
	if err != nil {
		t.Fatalf("SendTapback failed: %v", err)
	}
	if receivedBody["chatGuid"] != "chat-1" {
		t.Errorf("chatGuid = %v, want %q", receivedBody["chatGuid"], "chat-1")
	}
	tapback, ok := receivedBody["tapback"].(float64) // JSON numbers are float64
	if !ok || int(tapback) != 2000 {
		t.Errorf("tapback = %v, want 2000", receivedBody["tapback"])
	}
}

func TestIMessageConfigWebhookPathOrDefault(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"default", Config{}, "/api/imessage/webhook"},
		{"custom", Config{WebhookPath: "/custom/hook"}, "/custom/hook"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.WebhookPathOrDefault()
			if got != tt.want {
				t.Errorf("WebhookPathOrDefault() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIMessageBotSendAndName(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		ServerURL:    "http://localhost:1234",
		Password:     "test",
		AllowedChats: []string{"chat-1"},
	}
	bot := newBot(cfg)

	// Test Name().
	if bot.Name() != "imessage" {
		t.Errorf("Name() = %q, want %q", bot.Name(), "imessage")
	}

	// Test Send() with no allowed chats.
	bot2 := newBot(Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	})
	err := bot2.Send("test")
	if err == nil {
		t.Error("expected error when no allowed chats configured")
	}

	// Test Send() with empty text.
	err = bot.Send("")
	if err != nil {
		t.Errorf("Send empty text should return nil, got: %v", err)
	}
}

func TestIMessagePresenceName(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		ServerURL: "http://localhost:1234",
		Password:  "test",
	}
	bot := newBot(cfg)

	if bot.PresenceName() != "imessage" {
		t.Errorf("PresenceName() = %q, want %q", bot.PresenceName(), "imessage")
	}

	// SetTyping should be a no-op.
	err := bot.SetTyping(context.Background(), "chat-1")
	if err != nil {
		t.Errorf("SetTyping should return nil, got: %v", err)
	}
}

func TestIMessageDedupCleanup(t *testing.T) {
	cfg := Config{
		Enabled:      true,
		ServerURL:    "http://localhost:1234",
		Password:     "test",
		AllowedChats: []string{"chat-allowed-only"}, // restrict to prevent dispatch
	}
	bot := newBot(cfg)

	// Pre-populate dedup with old entries.
	bot.mu.Lock()
	oldTime := time.Now().Add(-10 * time.Minute) // older than 5 min cutoff
	for i := 0; i < 5; i++ {
		bot.dedup[fmt.Sprintf("old-msg-%d", i)] = oldTime
	}
	bot.mu.Unlock()

	// Process a new message (which triggers dedup cleanup).
	// This message will be added to dedup then stopped by allowedChats filter.
	bot.HandleMessage(Message{
		GUID:     "new-msg",
		ChatGUID: "chat-not-allowed",
		Text:     "trigger cleanup",
		IsFromMe: false,
	})

	// Old entries should be cleaned up.
	bot.mu.Lock()
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("old-msg-%d", i)
		if _, exists := bot.dedup[key]; exists {
			t.Errorf("old dedup entry %q should have been cleaned up", key)
		}
	}
	// New message should be in dedup (it gets added before allowedChats check).
	if _, exists := bot.dedup["new-msg"]; !exists {
		t.Error("new message should be in dedup")
	}
	bot.mu.Unlock()
}
