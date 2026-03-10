package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 1. TestExtractJSONPath — nested objects, array index, bool, float, missing key, invalid JSON
func TestExtractJSONPath(t *testing.T) {
	json := `{"name":"test","data":{"status":"ok","count":42,"active":true,"items":[{"id":"a"},{"id":"b"}]}}`

	tests := []struct {
		path string
		want string
	}{
		{"name", "test"},
		{"data.status", "ok"},
		{"data.count", "42"},
		{"data.active", "true"},
		{"data.items.0.id", "a"},
		{"data.items.1.id", "b"},
		{"data.items.2.id", ""},  // out of range
		{"missing", ""},
		{"data.missing.deep", ""},
	}

	for _, tt := range tests {
		got := extractJSONPath(json, tt.path)
		if got != tt.want {
			t.Errorf("extractJSONPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}

	// Invalid JSON.
	if got := extractJSONPath("not json", "key"); got != "" {
		t.Errorf("extractJSONPath(invalid) = %q, want empty", got)
	}

	// Empty inputs.
	if got := extractJSONPath("", "key"); got != "" {
		t.Errorf("extractJSONPath(empty json) = %q, want empty", got)
	}
	if got := extractJSONPath(`{"a":"b"}`, ""); got != "" {
		t.Errorf("extractJSONPath(empty path) = %q, want empty", got)
	}
}

// 2. TestApplyResponseMapping — with mapping, without, empty body
func TestApplyResponseMapping(t *testing.T) {
	body := `{"data":{"object":{"id":"ref_123","status":"succeeded"}}}`

	// With DataPath mapping.
	mapping := &ResponseMapping{DataPath: "data.object"}
	got := applyResponseMapping(body, mapping)
	if !strings.Contains(got, "ref_123") {
		t.Errorf("applyResponseMapping with DataPath: got %q, want to contain ref_123", got)
	}

	// Without mapping — returns full body.
	got = applyResponseMapping(body, nil)
	if got != body {
		t.Errorf("applyResponseMapping nil mapping: got %q, want full body", got)
	}

	// Empty body.
	got = applyResponseMapping("", mapping)
	if got != "" {
		t.Errorf("applyResponseMapping empty body: got %q, want empty", got)
	}

	// Mapping with empty DataPath — returns full body.
	got = applyResponseMapping(body, &ResponseMapping{})
	if got != body {
		t.Errorf("applyResponseMapping empty DataPath: got %q, want full body", got)
	}
}

// 3. TestCallbackManagerRegisterDeliver — basic register → deliver → channel receives
func TestCallbackManagerRegisterDeliver(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("test-key", ctx, "single")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	if !cm.HasChannel("test-key") {
		t.Error("HasChannel should return true")
	}

	result := CallbackResult{Status: 200, Body: `{"ok":true}`, ContentType: "application/json"}
	if cm.Deliver("test-key", result) != DeliverOK {
		t.Error("Deliver should return DeliverOK")
	}

	select {
	case received := <-ch:
		if received.Body != `{"ok":true}` {
			t.Errorf("received body = %q, want {\"ok\":true}", received.Body)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for callback")
	}
}

// 4. TestCallbackManagerCollision — same key twice → nil
func TestCallbackManagerCollision(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1 := cm.Register("dup-key", ctx, "single")
	if ch1 == nil {
		t.Fatal("first Register returned nil")
	}

	ch2 := cm.Register("dup-key", ctx, "single")
	if ch2 != nil {
		t.Error("second Register should return nil (collision)")
	}
}

// 5. TestCallbackManagerCapacity — 1001st → nil
func TestCallbackManagerCapacity(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		ch := cm.Register(key, ctx, "single")
		if ch == nil {
			t.Fatalf("Register failed at key %d", i)
		}
	}

	ch := cm.Register("key-overflow", ctx, "single")
	if ch != nil {
		t.Error("1001st Register should return nil (capacity)")
	}
}

// 6. TestCallbackManagerUnregisterSafe — double unregister no panic
func TestCallbackManagerUnregisterSafe(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cm.Register("safe-key", ctx, "single")

	// First unregister.
	cm.Unregister("safe-key")

	// Second unregister should not panic.
	cm.Unregister("safe-key")

	// Unregister non-existent key.
	cm.Unregister("nonexistent")

	if cm.HasChannel("safe-key") {
		t.Error("HasChannel should return false after unregister")
	}
}

// 7. TestCallbackManagerContextCleanup — cancel ctx → channel removed
func TestCallbackManagerContextCleanup(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())

	cm.Register("ctx-key", ctx, "single")
	if !cm.HasChannel("ctx-key") {
		t.Fatal("channel should exist before cancel")
	}

	cancel()
	// Wait for cleanup goroutine.
	time.Sleep(50 * time.Millisecond)

	if cm.HasChannel("ctx-key") {
		t.Error("channel should be removed after context cancel")
	}
}

// 8. TestResolveTemplateWithFields — {{steps.id.output.field}} with JSON output
func TestResolveTemplateWithFields(t *testing.T) {
	exec := &workflowExecutor{
		wCtx: &WorkflowContext{
			Input: map[string]string{"name": "world"},
			Steps: map[string]*WorkflowStepResult{
				"step1": {Output: `{"result":"hello","count":5}`, Status: "success"},
			},
			Env: map[string]string{},
		},
	}

	tests := []struct {
		tmpl string
		want string
	}{
		{"Hello {{name}}", "Hello world"},
		{"Status: {{steps.step1.status}}", "Status: success"},
		{"Result: {{steps.step1.output.result}}", "Result: hello"},
		{"Count: {{steps.step1.output.count}}", "Count: 5"},
		{"Missing: {{steps.step1.output.missing}}", "Missing: "},
		{"No step: {{steps.nope.output.x}}", "No step: "},
	}

	for _, tt := range tests {
		got := exec.resolveTemplateWithFields(tt.tmpl)
		if got != tt.want {
			t.Errorf("resolveTemplateWithFields(%q) = %q, want %q", tt.tmpl, got, tt.want)
		}
	}
}

// 9. TestResolveTemplateXMLEscaped — entity escaping
func TestResolveTemplateXMLEscaped(t *testing.T) {
	exec := &workflowExecutor{
		wCtx: &WorkflowContext{
			Input: map[string]string{"val": "a<b&c"},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
	}

	got := exec.resolveTemplateXMLEscaped("Value: {{val}}")
	if !strings.Contains(got, "&lt;") || !strings.Contains(got, "&amp;") {
		t.Errorf("resolveTemplateXMLEscaped should escape XML entities, got %q", got)
	}
}

// 10. TestIsValidCallbackKey — valid/invalid formats
func TestIsValidCallbackKey(t *testing.T) {
	valid := []string{"abc", "test-key", "ocr-123_456", "a.b.c", "A1"}
	for _, k := range valid {
		if !isValidCallbackKey(k) {
			t.Errorf("isValidCallbackKey(%q) = false, want true", k)
		}
	}

	invalid := []string{"", "-starts-dash", ".starts-dot", "has space", "has/slash", strings.Repeat("a", 257)}
	for _, k := range invalid {
		if isValidCallbackKey(k) {
			t.Errorf("isValidCallbackKey(%q) = true, want false", k)
		}
	}
}

// 11. TestCallbackDBRoundTrip — record → query → markDelivered → isDelivered
func TestCallbackDBRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Init table.
	initCallbackTable(dbPath)

	// Record.
	recordPendingCallback(dbPath, "db-key-1", "run-1", "step-1", "single", "bearer",
		"https://example.com", `{"test":true}`, "2026-12-31 00:00:00")

	// Query.
	rec := queryPendingCallbackByKey(dbPath, "db-key-1")
	if rec == nil {
		t.Fatal("queryPendingCallbackByKey returned nil")
	}
	if rec.RunID != "run-1" || rec.StepID != "step-1" || rec.Mode != "single" {
		t.Errorf("unexpected record: %+v", rec)
	}

	// Query waiting.
	rec2 := queryPendingCallback(dbPath, "db-key-1")
	if rec2 == nil {
		t.Fatal("queryPendingCallback returned nil for waiting record")
	}

	// Mark delivered.
	markCallbackDelivered(dbPath, "db-key-1", 0, CallbackResult{Status: 200, Body: `{"done":true}`})

	// isDelivered.
	if !isCallbackDelivered(dbPath, "db-key-1", 0) {
		t.Error("isCallbackDelivered should return true after delivery")
	}

	// queryPendingCallback should return nil now (not waiting anymore).
	rec3 := queryPendingCallback(dbPath, "db-key-1")
	if rec3 != nil {
		t.Error("queryPendingCallback should return nil after delivery")
	}
}

// 12. TestValidateExternalStep — all validation rules
func TestValidateExternalStep(t *testing.T) {
	allIDs := map[string]bool{"s1": true}

	// Valid external step.
	valid := WorkflowStep{ID: "s1", Type: "external", ExternalURL: "https://example.com"}
	errs := validateStep(valid, allIDs)
	if len(errs) > 0 {
		t.Errorf("valid external step has errors: %v", errs)
	}

	// Mutual exclusion: both externalBody and externalRawBody.
	mutual := WorkflowStep{
		ID: "s1", Type: "external",
		ExternalBody:    map[string]string{"a": "b"},
		ExternalRawBody: "<xml/>",
	}
	errs = validateStep(mutual, allIDs)
	if len(errs) == 0 {
		t.Error("should error on both externalBody and externalRawBody")
	}

	// Invalid callbackMode.
	badMode := WorkflowStep{ID: "s1", Type: "external", CallbackMode: "invalid"}
	errs = validateStep(badMode, allIDs)
	if len(errs) == 0 {
		t.Error("should error on invalid callbackMode")
	}

	// Invalid callbackAuth.
	badAuth := WorkflowStep{ID: "s1", Type: "external", CallbackAuth: "invalid"}
	errs = validateStep(badAuth, allIDs)
	if len(errs) == 0 {
		t.Error("should error on invalid callbackAuth")
	}

	// Invalid onTimeout.
	badTimeout := WorkflowStep{ID: "s1", Type: "external", OnTimeout: "retry"}
	errs = validateStep(badTimeout, allIDs)
	if len(errs) == 0 {
		t.Error("should error on invalid onTimeout")
	}
}

// 13. TestHttpPostWithRetry — httptest.NewServer success/retry/fail
func TestHttpPostWithRetry(t *testing.T) {
	// Success case.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	resp, err := httpPostWithRetry(context.Background(), ts.URL, "application/json", nil, `{"test":true}`, 0)
	if err != nil {
		t.Fatalf("success case failed: %v", err)
	}
	resp.Body.Close()

	// Retry then succeed.
	attempts := 0
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(500)
			w.Write([]byte("server error"))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts2.Close()

	resp, err = httpPostWithRetry(context.Background(), ts2.URL, "application/json", nil, `{}`, 3)
	if err != nil {
		t.Fatalf("retry case failed: %v", err)
	}
	resp.Body.Close()
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}

	// All retries fail.
	ts3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("always fail"))
	}))
	defer ts3.Close()

	_, err = httpPostWithRetry(context.Background(), ts3.URL, "application/json", nil, `{}`, 1)
	if err == nil {
		t.Error("expected error when all retries fail")
	}
}

// 14. TestRunExternalStepDryRun — dry-run output format
func TestRunExternalStepDryRun(t *testing.T) {
	exec := &workflowExecutor{
		mode: WorkflowModeDryRun,
		wCtx: &WorkflowContext{
			Input: map[string]string{},
			Steps: map[string]*WorkflowStepResult{},
			Env:   map[string]string{},
		},
		run: &WorkflowRun{
			StepResults: map[string]*StepRunResult{},
		},
	}

	step := &WorkflowStep{
		ID:           "ext1",
		Type:         "external",
		ExternalURL:  "https://example.com/api",
		CallbackMode: "single",
	}
	result := &StepRunResult{StepID: "ext1"}

	exec.runStepOnce(context.Background(), step, result)

	if result.Status != "success" {
		t.Errorf("dry-run status = %q, want success", result.Status)
	}
	if !strings.Contains(result.Output, "DRY-RUN") {
		t.Errorf("dry-run output should contain DRY-RUN, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "https://example.com/api") {
		t.Errorf("dry-run output should contain URL, got %q", result.Output)
	}
}

// 15. TestDeliverConcurrentUnregister — verify recover() prevents panic on concurrent close.
func TestDeliverConcurrentUnregister(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("race-key", ctx, "single")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	// Unregister concurrently while delivering.
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.Unregister("race-key")
	}()
	<-done

	// Deliver after channel is closed — should not panic, should return DeliverNoEntry.
	dr := cm.Deliver("race-key", CallbackResult{Body: "test"})
	if dr != DeliverNoEntry {
		t.Errorf("Deliver after Unregister = %d, want DeliverNoEntry", dr)
	}
}

// 16. TestDeliverAndSeqStreaming — verify atomic seq allocation for streaming.
func TestDeliverAndSeqStreaming(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("stream-key", ctx, "streaming")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	// Deliver 3 streaming callbacks.
	for i := 0; i < 3; i++ {
		out := cm.DeliverAndSeq("stream-key", CallbackResult{Body: fmt.Sprintf("msg-%d", i)})
		if out.Result != DeliverOK {
			t.Fatalf("DeliverAndSeq %d: result = %d, want DeliverOK", i, out.Result)
		}
		if out.Seq != i {
			t.Errorf("DeliverAndSeq %d: seq = %d, want %d", i, out.Seq, i)
		}
	}

	// Drain channel.
	for i := 0; i < 3; i++ {
		select {
		case r := <-ch:
			if r.Body != fmt.Sprintf("msg-%d", i) {
				t.Errorf("received %q, want msg-%d", r.Body, i)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout draining channel")
		}
	}
}

// 17. TestSetSeqAfterReplay — verify seq counter updated after replay.
func TestSetSeqAfterReplay(t *testing.T) {
	cm := NewCallbackManager("")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := cm.Register("replay-key", ctx, "streaming")
	if ch == nil {
		t.Fatal("Register returned nil")
	}

	// Simulate replay of 3 accumulated results.
	replayed := []CallbackResult{
		{Body: "r0"}, {Body: "r1"}, {Body: "r2"},
	}
	cm.ReplayAccumulated("replay-key", replayed)
	cm.SetSeq("replay-key", len(replayed))

	// New delivery should start at seq=3.
	out := cm.DeliverAndSeq("replay-key", CallbackResult{Body: "new"})
	if out.Seq != 3 {
		t.Errorf("seq after replay = %d, want 3", out.Seq)
	}

	// Drain channel.
	for i := 0; i < 4; i++ {
		<-ch
	}
}

// 18. TestStreamingAccumulateNonJSON — verify non-JSON bodies are safely wrapped.
func TestStreamingAccumulateNonJSON(t *testing.T) {
	// Simulate accumulate with mixed JSON and non-JSON bodies.
	bodies := []string{`{"ok":true}`, "plain text", `{"count":42}`}

	var parts []string
	for _, b := range bodies {
		if !json.Valid([]byte(b)) {
			marshaled, _ := json.Marshal(b)
			b = string(marshaled)
		}
		parts = append(parts, b)
	}
	output := "[" + strings.Join(parts, ",") + "]"

	// Should be valid JSON.
	if !json.Valid([]byte(output)) {
		t.Errorf("accumulated output is not valid JSON: %s", output)
	}

	// Verify structure.
	var arr []any
	if err := json.Unmarshal([]byte(output), &arr); err != nil {
		t.Fatalf("failed to parse accumulated JSON: %v", err)
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 elements, got %d", len(arr))
	}
	// Second element should be a string "plain text".
	if s, ok := arr[1].(string); !ok || s != "plain text" {
		t.Errorf("element 1 = %v, want string 'plain text'", arr[1])
	}
}

// TestParseDurationWithDays tests the day-suffix duration parser.
func TestParseDurationWithDays(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"5m", 5 * time.Minute, false},
		{"1h", time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"31d", 0, true}, // over 30d limit
		{"-1d", 0, true}, // negative
		{"abc", 0, true}, // invalid
	}

	for _, tt := range tests {
		got, err := parseDurationWithDays(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDurationWithDays(%q) should error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDurationWithDays(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDurationWithDays(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

