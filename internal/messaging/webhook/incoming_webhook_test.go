package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
)

// --- Signature Verification Tests ---

func TestVerifySignature_NoSecret(t *testing.T) {
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	if !VerifySignature(r, []byte("body"), "") {
		t.Error("expected true when no secret configured")
	}
}

func TestVerifySignature_GitHub(t *testing.T) {
	secret := "mysecret"
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Hub-Signature-256", sig)

	if !VerifySignature(r, body, secret) {
		t.Error("expected true for valid GitHub signature")
	}

	// Wrong signature.
	r2 := httptest.NewRequest("POST", "/hooks/test", nil)
	r2.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	if VerifySignature(r2, body, secret) {
		t.Error("expected false for invalid GitHub signature")
	}
}

func TestVerifySignature_GitLab(t *testing.T) {
	secret := "gitlab-token"
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Gitlab-Token", secret)

	if !VerifySignature(r, []byte("body"), secret) {
		t.Error("expected true for valid GitLab token")
	}

	r2 := httptest.NewRequest("POST", "/hooks/test", nil)
	r2.Header.Set("X-Gitlab-Token", "wrong")
	if VerifySignature(r2, []byte("body"), secret) {
		t.Error("expected false for wrong GitLab token")
	}
}

func TestVerifySignature_Generic(t *testing.T) {
	secret := "genericsecret"
	body := []byte(`{"data":"test"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Webhook-Signature", sig)

	if !VerifySignature(r, body, secret) {
		t.Error("expected true for valid generic signature")
	}
}

func TestVerifySignature_SecretButNoHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	if VerifySignature(r, []byte("body"), "secret") {
		t.Error("expected false when secret is set but no signature header")
	}
}

// --- HMAC-SHA256 Tests ---

func TestVerifyHMACSHA256(t *testing.T) {
	secret := "test-secret"
	body := []byte("hello world")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	if !VerifyHMACSHA256(body, secret, sig) {
		t.Error("expected true for valid HMAC")
	}
	if VerifyHMACSHA256(body, secret, "badhex") {
		t.Error("expected false for invalid hex")
	}
	if VerifyHMACSHA256(body, "wrong-secret", sig) {
		t.Error("expected false for wrong secret")
	}
}

// --- Template Expansion Tests ---

func TestExpandTemplate_Simple(t *testing.T) {
	payload := map[string]any{
		"action": "opened",
		"title":  "Fix bug",
		"count":  float64(42),
	}

	result := ExpandTemplate("Action: {{payload.action}}, Title: {{payload.title}}", payload)
	if result != "Action: opened, Title: Fix bug" {
		t.Errorf("got %q", result)
	}
}

func TestExpandTemplate_Nested(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"title":    "Add feature",
			"html_url": "https://github.com/repo/pull/1",
		},
	}

	result := ExpandTemplate("PR: {{payload.pull_request.title}} - {{payload.pull_request.html_url}}", payload)
	expected := "PR: Add feature - https://github.com/repo/pull/1"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExpandTemplate_Missing(t *testing.T) {
	result := ExpandTemplate("{{payload.nonexistent}}", map[string]any{})
	if result != "{{payload.nonexistent}}" {
		t.Errorf("expected original placeholder for missing key, got %q", result)
	}
}

func TestExpandTemplate_Types(t *testing.T) {
	payload := map[string]any{
		"count":  float64(42),
		"rate":   float64(3.14),
		"active": true,
		"tags":   []any{"a", "b"},
	}

	tests := []struct {
		template string
		expected string
	}{
		{"{{payload.count}}", "42"},
		{"{{payload.rate}}", "3.14"},
		{"{{payload.active}}", "true"},
		{"{{payload.tags}}", `["a","b"]`},
	}

	for _, tt := range tests {
		result := ExpandTemplate(tt.template, payload)
		if result != tt.expected {
			t.Errorf("ExpandTemplate(%q) = %q, want %q", tt.template, result, tt.expected)
		}
	}
}

// --- Nested Value Tests ---

func TestGetNestedValue(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
		"top": "level",
	}

	if v := GetNestedValue(m, "top"); v != "level" {
		t.Errorf("got %v, want 'level'", v)
	}
	if v := GetNestedValue(m, "a.b.c"); v != "deep" {
		t.Errorf("got %v, want 'deep'", v)
	}
	if v := GetNestedValue(m, "a.b.missing"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
	if v := GetNestedValue(m, "nonexistent"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

// --- Filter Evaluation Tests ---

func TestEvaluateFilter_Empty(t *testing.T) {
	if !EvaluateFilter("", map[string]any{}) {
		t.Error("empty filter should accept all")
	}
}

func TestEvaluateFilter_Equal(t *testing.T) {
	payload := map[string]any{"action": "opened"}

	if !EvaluateFilter("payload.action == 'opened'", payload) {
		t.Error("expected true for matching ==")
	}
	if EvaluateFilter("payload.action == 'closed'", payload) {
		t.Error("expected false for non-matching ==")
	}
}

func TestEvaluateFilter_NotEqual(t *testing.T) {
	payload := map[string]any{"action": "opened"}

	if !EvaluateFilter("payload.action != 'closed'", payload) {
		t.Error("expected true for non-matching !=")
	}
	if EvaluateFilter("payload.action != 'opened'", payload) {
		t.Error("expected false for matching !=")
	}
}

func TestEvaluateFilter_Truthy(t *testing.T) {
	tests := []struct {
		payload  map[string]any
		filter   string
		expected bool
	}{
		{map[string]any{"active": true}, "payload.active", true},
		{map[string]any{"active": false}, "payload.active", false},
		{map[string]any{"name": "test"}, "payload.name", true},
		{map[string]any{"name": ""}, "payload.name", false},
		{map[string]any{"count": float64(5)}, "payload.count", true},
		{map[string]any{"count": float64(0)}, "payload.count", false},
		{map[string]any{}, "payload.missing", false},
	}

	for _, tt := range tests {
		result := EvaluateFilter(tt.filter, tt.payload)
		if result != tt.expected {
			t.Errorf("EvaluateFilter(%q) = %v, want %v", tt.filter, result, tt.expected)
		}
	}
}

func TestEvaluateFilter_NestedKey(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"state": "open",
		},
	}
	if !EvaluateFilter("payload.pull_request.state == 'open'", payload) {
		t.Error("expected true for nested key equality")
	}
}

func TestEvaluateFilter_DoubleQuotes(t *testing.T) {
	payload := map[string]any{"action": "opened"}
	if !EvaluateFilter(`payload.action == "opened"`, payload) {
		t.Error("expected true with double quotes")
	}
}

// --- IsTruthy Tests ---

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		val      any
		expected bool
	}{
		{nil, false},
		{true, true},
		{false, false},
		{"hello", true},
		{"", false},
		{float64(1), true},
		{float64(0), false},
		{map[string]any{}, true},  // non-nil non-basic type = true
		{[]any{"a"}, true},
	}
	for _, tt := range tests {
		if IsTruthy(tt.val) != tt.expected {
			t.Errorf("IsTruthy(%v) = %v, want %v", tt.val, !tt.expected, tt.expected)
		}
	}
}

// --- Config Tests ---

func TestConfig_IsEnabled(t *testing.T) {
	// Default (nil) → enabled.
	c := Config{}
	if !c.IsEnabled() {
		t.Error("expected enabled by default")
	}

	// Explicitly enabled.
	tr := true
	c.Enabled = &tr
	if !c.IsEnabled() {
		t.Error("expected enabled when set to true")
	}

	// Explicitly disabled.
	f := false
	c.Enabled = &f
	if c.IsEnabled() {
		t.Error("expected disabled when set to false")
	}
}
