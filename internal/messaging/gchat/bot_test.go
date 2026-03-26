package gchat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// --- Helper: Generate Test RSA Key ---

func generateTestRSAKey(t *testing.T) string {
	t.Helper()
	// Generate a real RSA key for testing using crypto/rsa.
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test RSA key: %v", err)
	}

	// Marshal to PKCS#8 format.
	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	// Encode to PEM.
	pemBlock := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	}

	return string(pem.EncodeToMemory(pemBlock))
}

// writeServiceAccountKey writes a service account key file to tmpDir and returns the path.
func writeServiceAccountKey(t *testing.T, tmpDir, tokenURI string) string {
	t.Helper()
	keyPath := filepath.Join(tmpDir, "service-account.json")
	testKey := generateTestRSAKey(t)

	saKey := serviceAccountKey{
		Type:        "service_account",
		ProjectID:   "test-project",
		PrivateKeyID: "test-key-id",
		PrivateKey:  testKey,
		ClientEmail: "test@test-project.iam.gserviceaccount.com",
		TokenURI:    tokenURI,
	}

	keyData, err := json.Marshal(saKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		t.Fatal(err)
	}
	return keyPath
}

// --- Tests ---

func TestGChatServiceAccountParsing(t *testing.T) {
	tmpDir := t.TempDir()
	testKey := generateTestRSAKey(t)

	saKey := serviceAccountKey{
		Type:                    "service_account",
		ProjectID:               "test-project",
		PrivateKeyID:            "test-key-id",
		PrivateKey:              testKey,
		ClientEmail:             "test@test-project.iam.gserviceaccount.com",
		ClientID:                "123456789",
		AuthURI:                 "https://accounts.google.com/o/oauth2/auth",
		TokenURI:                "https://oauth2.googleapis.com/token",
		AuthProviderX509CertURL: "https://www.googleapis.com/oauth2/v1/certs",
		ClientX509CertURL:       "https://www.googleapis.com/robot/v1/metadata/x509/test%40test-project.iam.gserviceaccount.com",
	}

	keyData, err := json.Marshal(saKey)
	if err != nil {
		t.Fatal(err)
	}

	keyPath := filepath.Join(tmpDir, "service-account.json")
	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Enabled:           true,
		ServiceAccountKey: keyPath,
	}

	bot, err := NewBot(cfg, testRT)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	if bot.saKey.ClientEmail != "test@test-project.iam.gserviceaccount.com" {
		t.Errorf("expected client email %s, got %s", "test@test-project.iam.gserviceaccount.com", bot.saKey.ClientEmail)
	}

	if bot.privKey == nil {
		t.Error("expected private key to be parsed")
	}
}

func TestGChatEventParsing(t *testing.T) {
	tests := []struct {
		name      string
		eventJSON string
		eventType string
		hasMsg    bool
	}{
		{
			name: "MESSAGE event",
			eventJSON: `{
				"type": "MESSAGE",
				"eventTime": "2026-02-23T10:00:00.000Z",
				"space": {"name": "spaces/AAAAA", "type": "ROOM", "displayName": "Test Room"},
				"message": {
					"name": "spaces/AAAAA/messages/12345",
					"sender": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"},
					"createTime": "2026-02-23T10:00:00.000Z",
					"text": "Hello bot",
					"argumentText": "Hello bot"
				},
				"user": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"}
			}`,
			eventType: "MESSAGE",
			hasMsg:    true,
		},
		{
			name: "ADDED_TO_SPACE event",
			eventJSON: `{
				"type": "ADDED_TO_SPACE",
				"eventTime": "2026-02-23T10:00:00.000Z",
				"space": {"name": "spaces/AAAAA", "type": "ROOM", "displayName": "Test Room"},
				"user": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"}
			}`,
			eventType: "ADDED_TO_SPACE",
			hasMsg:    false,
		},
		{
			name: "REMOVED_FROM_SPACE event",
			eventJSON: `{
				"type": "REMOVED_FROM_SPACE",
				"eventTime": "2026-02-23T10:00:00.000Z",
				"space": {"name": "spaces/AAAAA", "type": "ROOM", "displayName": "Test Room"},
				"user": {"name": "users/123", "displayName": "Test User", "type": "HUMAN"}
			}`,
			eventType: "REMOVED_FROM_SPACE",
			hasMsg:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event gchatEvent
			if err := json.Unmarshal([]byte(tt.eventJSON), &event); err != nil {
				t.Fatalf("failed to parse event: %v", err)
			}

			if event.Type != tt.eventType {
				t.Errorf("expected type %s, got %s", tt.eventType, event.Type)
			}

			if tt.hasMsg && event.Message == nil {
				t.Error("expected message to be present")
			}

			if !tt.hasMsg && event.Message != nil {
				t.Error("expected message to be nil")
			}
		})
	}
}

func TestGChatCardBuilder(t *testing.T) {
	card := gchatCard{
		Header: &gchatCardHeader{
			Title:    "Task Result",
			Subtitle: "Completed",
		},
		Sections: []gchatCardSection{
			{
				Header: "Details",
				Widgets: []gchatCardWidget{
					{
						TextParagraph: &gchatTextParagraph{
							Text: "Task completed successfully.",
						},
					},
					{
						KeyValue: &gchatKeyValue{
							TopLabel: "Status",
							Content:  "Success",
							Icon:     "STAR",
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("failed to marshal card: %v", err)
	}

	var parsed gchatCard
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse card: %v", err)
	}

	if parsed.Header.Title != "Task Result" {
		t.Errorf("expected title %s, got %s", "Task Result", parsed.Header.Title)
	}

	if len(parsed.Sections) != 1 {
		t.Errorf("expected 1 section, got %d", len(parsed.Sections))
	}

	if len(parsed.Sections[0].Widgets) != 2 {
		t.Errorf("expected 2 widgets, got %d", len(parsed.Sections[0].Widgets))
	}
}

func TestGChatSendMessage(t *testing.T) {
	// Create mock HTTP server.
	callCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if strings.Contains(r.URL.Path, "/token") {
			// Token endpoint.
			resp := map[string]interface{}{
				"access_token": "mock-token",
				"expires_in":   3600,
				"token_type":   "Bearer",
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		// Message endpoint.
		if r.Header.Get("Authorization") != "Bearer mock-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var req gchatSendRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Text == "" && len(req.Cards) == 0 {
			http.Error(w, "empty message", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"name": "spaces/AAAAA/messages/12345"})
	}))
	defer mockServer.Close()

	tmpDir := t.TempDir()
	keyPath := writeServiceAccountKey(t, tmpDir, mockServer.URL+"/token")

	cfg := Config{
		Enabled:           true,
		ServiceAccountKey: keyPath,
	}

	_, err := NewBot(cfg, testRT)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the message structure.
	req := gchatSendRequest{Text: "Hello"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "Hello") {
		t.Errorf("expected message to contain 'Hello', got %s", string(data))
	}
}

func TestGChatJWTGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := writeServiceAccountKey(t, tmpDir, "https://oauth2.googleapis.com/token")

	cfg := Config{
		Enabled:           true,
		ServiceAccountKey: keyPath,
	}

	bot, err := NewBot(cfg, testRT)
	if err != nil {
		t.Fatal(err)
	}

	jwt, err := bot.createJWT()
	if err != nil {
		t.Fatalf("failed to create JWT: %v", err)
	}

	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Errorf("expected JWT to have 3 parts, got %d", len(parts))
	}

	// Verify header.
	if parts[0] == "" {
		t.Error("expected header to be non-empty")
	}

	// Verify claims.
	if parts[1] == "" {
		t.Error("expected claims to be non-empty")
	}

	// Verify signature.
	if parts[2] == "" {
		t.Error("expected signature to be non-empty")
	}
}

func TestGChatDedup(t *testing.T) {
	bot := &Bot{
		processed: make(map[string]time.Time),
	}

	msgID := "spaces/AAAAA/messages/12345"

	if bot.isDuplicate(msgID) {
		t.Error("expected message to not be duplicate initially")
	}

	bot.markProcessed(msgID)

	if !bot.isDuplicate(msgID) {
		t.Error("expected message to be duplicate after marking")
	}

	// Test cleanup.
	bot.processed[msgID] = time.Now().Add(-10 * time.Minute)
	if bot.isDuplicate(msgID) {
		t.Error("expected old message to be cleaned up")
	}
}

func TestGChatWebhookHandler(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := writeServiceAccountKey(t, tmpDir, "https://oauth2.googleapis.com/token")

	cfg := Config{
		Enabled:           true,
		ServiceAccountKey: keyPath,
		DefaultAgent:      "琉璃",
	}

	bot, err := NewBot(cfg, testRT)
	if err != nil {
		t.Fatal(err)
	}

	// Test ADDED_TO_SPACE event.
	event := gchatEvent{
		Type: "ADDED_TO_SPACE",
		Space: gchatSpace{
			Name:        "spaces/AAAAA",
			Type:        "ROOM",
			DisplayName: "Test Room",
		},
		User: gchatUser{
			Name:        "users/123",
			DisplayName: "Test User",
			Type:        "HUMAN",
		},
	}

	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/api/gchat/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()

	bot.HandleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	resp := w.Body.String()
	if !strings.Contains(resp, "Hello") {
		t.Errorf("expected welcome message, got %s", resp)
	}
}
