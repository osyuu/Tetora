package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tetora/internal/audit"
	"tetora/internal/quiet"
	"tetora/internal/quickaction"
)

// ---------------------------------------------------------------------------
// isValidOutputFilename
// ---------------------------------------------------------------------------

func TestIsValidOutputFilename_Valid(t *testing.T) {
	cases := []string{
		"abc123.json",
		"task_20260221-120000.json",
		"a-b_c.txt",
		"README.md",
		"output.JSON",
		"a",
	}
	for _, name := range cases {
		if !isValidOutputFilename(name) {
			t.Errorf("isValidOutputFilename(%q) = false, want true", name)
		}
	}
}

func TestIsValidOutputFilename_Invalid(t *testing.T) {
	cases := []struct {
		name string
		desc string
	}{
		{"", "empty string"},
		{".hidden", "starts with dot"},
		{"../escape.json", "path traversal"},
		{"foo/bar.json", "path separator"},
		{"file name.json", "space"},
		{"alert('xss').json", "special chars"},
	}
	for _, tc := range cases {
		if isValidOutputFilename(tc.name) {
			t.Errorf("isValidOutputFilename(%q) [%s] = true, want false", tc.name, tc.desc)
		}
	}
}

func TestIsValidOutputFilename_TooLong(t *testing.T) {
	// 256 characters -> false
	long256 := strings.Repeat("a", 256)
	if isValidOutputFilename(long256) {
		t.Error("isValidOutputFilename(256 chars) = true, want false")
	}
}

func TestIsValidOutputFilename_ExactlyMaxLength(t *testing.T) {
	// 255 characters of valid chars -> true
	long255 := strings.Repeat("a", 255)
	if !isValidOutputFilename(long255) {
		t.Error("isValidOutputFilename(255 chars) = false, want true")
	}
}

// ---------------------------------------------------------------------------
// validateDashboardCookie
// ---------------------------------------------------------------------------

func TestValidateDashboardCookie_Valid(t *testing.T) {
	secret := "test-secret-key-42"
	cookie := dashboardAuthCookie(secret)
	if !validateDashboardCookie(cookie, secret) {
		t.Errorf("validateDashboardCookie(%q, %q) = false, want true", cookie, secret)
	}
}

func TestValidateDashboardCookie_Expired(t *testing.T) {
	secret := "test-secret-key-42"
	// Create a cookie with a timestamp from 25 hours ago.
	ts := fmt.Sprintf("%d", time.Now().Add(-25*time.Hour).Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	cookie := ts + ":" + sig

	if validateDashboardCookie(cookie, secret) {
		t.Error("validateDashboardCookie(expired) = true, want false")
	}
}

func TestValidateDashboardCookie_TamperedSignature(t *testing.T) {
	secret := "test-secret-key-42"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	// Use a wrong HMAC (signed with different key).
	wrongMac := hmac.New(sha256.New, []byte("wrong-secret"))
	wrongMac.Write([]byte(ts))
	wrongSig := hex.EncodeToString(wrongMac.Sum(nil))
	cookie := ts + ":" + wrongSig

	if validateDashboardCookie(cookie, secret) {
		t.Error("validateDashboardCookie(tampered sig) = true, want false")
	}
}

func TestValidateDashboardCookie_Malformed(t *testing.T) {
	// No colon separator.
	if validateDashboardCookie("not-a-cookie", "secret") {
		t.Error("validateDashboardCookie(\"not-a-cookie\") = true, want false")
	}
}

func TestValidateDashboardCookie_Empty(t *testing.T) {
	if validateDashboardCookie("", "secret") {
		t.Error("validateDashboardCookie(\"\") = true, want false")
	}
}

func TestValidateDashboardCookie_JustColon(t *testing.T) {
	// Timestamp part is empty -> parse fails.
	if validateDashboardCookie(":abc", "secret") {
		t.Error("validateDashboardCookie(\":abc\") = true, want false")
	}
}

// ---------------------------------------------------------------------------
// dashboardAuthCookie
// ---------------------------------------------------------------------------

func TestDashboardAuthCookie_NonEmpty(t *testing.T) {
	cookie := dashboardAuthCookie("my-secret")
	if cookie == "" {
		t.Error("dashboardAuthCookie returned empty string")
	}
}

func TestDashboardAuthCookie_Format(t *testing.T) {
	cookie := dashboardAuthCookie("my-secret")
	parts := strings.SplitN(cookie, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("dashboardAuthCookie format: expected \"timestamp:hex_signature\", got %q", cookie)
	}

	// Timestamp part should be a valid Unix timestamp.
	ts := parts[0]
	for _, c := range ts {
		if c < '0' || c > '9' {
			t.Errorf("timestamp part %q contains non-digit character %q", ts, string(c))
			break
		}
	}

	// Signature part should be valid hex.
	sig := parts[1]
	if _, err := hex.DecodeString(sig); err != nil {
		t.Errorf("signature part %q is not valid hex: %v", sig, err)
	}

	// HMAC-SHA256 produces 32 bytes = 64 hex characters.
	if len(sig) != 64 {
		t.Errorf("signature length = %d, want 64 hex chars", len(sig))
	}
}

func TestDashboardAuthCookie_ValidatesWithSameSecret(t *testing.T) {
	secret := "shared-secret"
	cookie := dashboardAuthCookie(secret)
	if !validateDashboardCookie(cookie, secret) {
		t.Error("cookie generated with secret does not validate with same secret")
	}
}

func TestDashboardAuthCookie_RejectsWithDifferentSecret(t *testing.T) {
	cookie := dashboardAuthCookie("secret-A")
	if validateDashboardCookie(cookie, "secret-B") {
		t.Error("cookie generated with secret-A validated with secret-B, want false")
	}
}

// ---------------------------------------------------------------------------
// clientIP
// ---------------------------------------------------------------------------

func TestClientIP_WithXForwardedFor(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{"X-Forwarded-For": []string{"1.2.3.4"}},
		RemoteAddr: "127.0.0.1:9999",
	}
	got := clientIP(r)
	if got != "1.2.3.4" {
		t.Errorf("clientIP with X-Forwarded-For = %q, want %q", got, "1.2.3.4")
	}
}

func TestClientIP_WithoutHeader(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{},
		RemoteAddr: "10.0.0.1:8080",
	}
	got := clientIP(r)
	if got != "10.0.0.1" {
		t.Errorf("clientIP without header = %q, want %q", got, "10.0.0.1")
	}
}

func TestClientIP_MultipleIPs(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.50, 70.41.3.18, 150.172.238.178"}},
		RemoteAddr: "127.0.0.1:9999",
	}
	got := clientIP(r)
	if got != "203.0.113.50" {
		t.Errorf("clientIP with multiple IPs = %q, want %q", got, "203.0.113.50")
	}
}

// ---------------------------------------------------------------------------
// loginLimiter
// ---------------------------------------------------------------------------

func TestLoginLimiter_NotLockedInitially(t *testing.T) {
	ll := newLoginLimiter()
	if ll.isLocked("1.2.3.4") {
		t.Error("new IP should not be locked")
	}
}

func TestLoginLimiter_LocksAfter5Failures(t *testing.T) {
	ll := newLoginLimiter()
	ip := "10.0.0.1"
	for i := 0; i < 5; i++ {
		ll.recordFailure(ip)
	}
	if !ll.isLocked(ip) {
		t.Error("IP should be locked after 5 failures")
	}
}

func TestLoginLimiter_NotLockedBefore5(t *testing.T) {
	ll := newLoginLimiter()
	ip := "10.0.0.2"
	for i := 0; i < 4; i++ {
		ll.recordFailure(ip)
	}
	if ll.isLocked(ip) {
		t.Error("IP should not be locked after only 4 failures")
	}
}

func TestLoginLimiter_SuccessClearsFailures(t *testing.T) {
	ll := newLoginLimiter()
	ip := "10.0.0.3"
	for i := 0; i < 4; i++ {
		ll.recordFailure(ip)
	}
	ll.recordSuccess(ip)
	// After success, failures are cleared — should not lock even with 1 more failure.
	ll.recordFailure(ip)
	if ll.isLocked(ip) {
		t.Error("IP should not be locked after success cleared failures")
	}
}

func TestLoginLimiter_DifferentIPsIndependent(t *testing.T) {
	ll := newLoginLimiter()
	for i := 0; i < 5; i++ {
		ll.recordFailure("bad-ip")
	}
	if ll.isLocked("good-ip") {
		t.Error("different IP should not be affected")
	}
	if !ll.isLocked("bad-ip") {
		t.Error("bad-ip should be locked")
	}
}

func TestLoginLimiter_Cleanup(t *testing.T) {
	ll := newLoginLimiter()
	// Manually insert an expired entry.
	ll.mu.Lock()
	ll.attempts["old-ip"] = &loginAttempt{
		failures: 3,
		lastFail: time.Now().Add(-loginLockoutDur - time.Minute),
	}
	ll.mu.Unlock()

	ll.cleanup()

	ll.mu.Lock()
	_, exists := ll.attempts["old-ip"]
	ll.mu.Unlock()
	if exists {
		t.Error("cleanup should remove expired entries")
	}
}

// ---------------------------------------------------------------------------
// IP Allowlist
// ---------------------------------------------------------------------------

func TestParseAllowlist_Nil(t *testing.T) {
	al := parseAllowlist(nil)
	if al != nil {
		t.Error("parseAllowlist(nil) should return nil")
	}
}

func TestParseAllowlist_Empty(t *testing.T) {
	al := parseAllowlist([]string{})
	if al != nil {
		t.Error("parseAllowlist([]) should return nil")
	}
}

func TestIPAllowlist_SingleIP(t *testing.T) {
	al := parseAllowlist([]string{"192.168.1.100"})
	if !al.contains("192.168.1.100") {
		t.Error("expected 192.168.1.100 to be allowed")
	}
	if al.contains("192.168.1.101") {
		t.Error("expected 192.168.1.101 to be blocked")
	}
}

func TestIPAllowlist_CIDR(t *testing.T) {
	al := parseAllowlist([]string{"10.0.0.0/8"})
	if !al.contains("10.1.2.3") {
		t.Error("expected 10.1.2.3 to be allowed (in 10.0.0.0/8)")
	}
	if al.contains("192.168.1.1") {
		t.Error("expected 192.168.1.1 to be blocked")
	}
}

func TestIPAllowlist_Mixed(t *testing.T) {
	al := parseAllowlist([]string{"127.0.0.1", "192.168.0.0/16"})
	if !al.contains("127.0.0.1") {
		t.Error("expected 127.0.0.1 to be allowed")
	}
	if !al.contains("192.168.1.100") {
		t.Error("expected 192.168.1.100 to be allowed (in 192.168.0.0/16)")
	}
	if al.contains("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be blocked")
	}
}

func TestIPAllowlist_Nil_AllowsAll(t *testing.T) {
	var al *ipAllowlist
	if !al.contains("any-ip") {
		t.Error("nil allowlist should allow all")
	}
}

func TestIPAllowlist_InvalidIP(t *testing.T) {
	al := parseAllowlist([]string{"127.0.0.1"})
	if al.contains("not-an-ip") {
		t.Error("invalid IP should not be allowed")
	}
}

func TestIPAllowlist_IPv6(t *testing.T) {
	al := parseAllowlist([]string{"::1"})
	if !al.contains("::1") {
		t.Error("expected ::1 to be allowed")
	}
	if al.contains("127.0.0.1") {
		t.Error("expected 127.0.0.1 to be blocked when only ::1 allowed")
	}
}

func TestIPAllowlist_InvalidEntry(t *testing.T) {
	// Invalid entries are silently skipped.
	al := parseAllowlist([]string{"not-valid", "127.0.0.1"})
	if !al.contains("127.0.0.1") {
		t.Error("expected 127.0.0.1 to be allowed")
	}
}

// ---------------------------------------------------------------------------
// API Rate Limiter
// ---------------------------------------------------------------------------

func TestAPIRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := newAPIRateLimiter(10)
	for i := 0; i < 10; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed (limit=10)", i+1)
		}
	}
}

func TestAPIRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := newAPIRateLimiter(5)
	for i := 0; i < 5; i++ {
		rl.allow("1.2.3.4")
	}
	if rl.allow("1.2.3.4") {
		t.Error("6th request should be blocked (limit=5)")
	}
}

func TestAPIRateLimiter_IndependentIPs(t *testing.T) {
	rl := newAPIRateLimiter(3)
	for i := 0; i < 3; i++ {
		rl.allow("ip-a")
	}
	// ip-a is at limit, ip-b should still be allowed.
	if !rl.allow("ip-b") {
		t.Error("different IP should not be affected by ip-a's limit")
	}
}

func TestAPIRateLimiter_Cleanup(t *testing.T) {
	rl := newAPIRateLimiter(10)
	// Add an old entry.
	rl.mu.Lock()
	rl.windows["old-ip"] = &ipWindow{
		timestamps: []time.Time{time.Now().Add(-2 * time.Minute)},
	}
	rl.mu.Unlock()

	rl.cleanup()

	rl.mu.Lock()
	_, exists := rl.windows["old-ip"]
	rl.mu.Unlock()
	if exists {
		t.Error("cleanup should remove expired entries")
	}
}

func TestAPIRateLimiter_DefaultLimit(t *testing.T) {
	rl := newAPIRateLimiter(0)
	if rl.limit != 60 {
		t.Errorf("default limit = %d, want 60", rl.limit)
	}
}

// ---------------------------------------------------------------------------
// clientIP port stripping
// ---------------------------------------------------------------------------

func TestClientIP_StripsPort(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{},
		RemoteAddr: "192.168.1.1:54321",
	}
	got := clientIP(r)
	if got != "192.168.1.1" {
		t.Errorf("clientIP = %q, want %q", got, "192.168.1.1")
	}
}

func TestClientIP_XForwardedForTrimmed(t *testing.T) {
	r := &http.Request{
		Header:     http.Header{"X-Forwarded-For": []string{"  1.2.3.4 , 5.6.7.8"}},
		RemoteAddr: "127.0.0.1:9999",
	}
	got := clientIP(r)
	if got != "1.2.3.4" {
		t.Errorf("clientIP = %q, want %q", got, "1.2.3.4")
	}
}

// ---------------------------------------------------------------------------
// parseRouteDetail
// ---------------------------------------------------------------------------

func TestParseRouteDetail_Full(t *testing.T) {
	detail := "role=琉璃 method=keyword confidence=high prompt=review this code"
	role, method, confidence, prompt := audit.ParseRouteDetail(detail)
	if role != "琉璃" {
		t.Errorf("role = %q, want %q", role, "琉璃")
	}
	if method != "keyword" {
		t.Errorf("method = %q, want %q", method, "keyword")
	}
	if confidence != "high" {
		t.Errorf("confidence = %q, want %q", confidence, "high")
	}
	if prompt != "review this code" {
		t.Errorf("prompt = %q, want %q", prompt, "review this code")
	}
}

func TestParseRouteDetail_NoPrompt(t *testing.T) {
	detail := "role=黒曜 method=llm confidence=medium"
	role, method, confidence, prompt := audit.ParseRouteDetail(detail)
	if role != "黒曜" {
		t.Errorf("role = %q, want %q", role, "黒曜")
	}
	if method != "llm" {
		t.Errorf("method = %q, want %q", method, "llm")
	}
	if confidence != "medium" {
		t.Errorf("confidence = %q, want %q", confidence, "medium")
	}
	if prompt != "" {
		t.Errorf("prompt = %q, want empty", prompt)
	}
}

func TestParseRouteDetail_Empty(t *testing.T) {
	role, method, confidence, prompt := audit.ParseRouteDetail("")
	if role != "" || method != "" || confidence != "" || prompt != "" {
		t.Errorf("audit.ParseRouteDetail(\"\") = (%q,%q,%q,%q), want all empty", role, method, confidence, prompt)
	}
}

func TestParseRouteDetail_PromptWithSpaces(t *testing.T) {
	detail := "role=翡翠 method=keyword confidence=high prompt=check the deployment status for all services"
	_, _, _, prompt := audit.ParseRouteDetail(detail)
	if prompt != "check the deployment status for all services" {
		t.Errorf("prompt = %q, want %q", prompt, "check the deployment status for all services")
	}
}

// ---------------------------------------------------------------------------
// cleanupRouteResults
// ---------------------------------------------------------------------------

func TestCleanupRouteResults_RemovesExpired(t *testing.T) {
	routeResultsMu.Lock()
	routeResults["old-id"] = &routeResultEntry{
		Status:    "done",
		CreatedAt: time.Now().Add(-31 * time.Minute),
	}
	routeResults["new-id"] = &routeResultEntry{
		Status:    "done",
		CreatedAt: time.Now(),
	}
	routeResultsMu.Unlock()

	cleanupRouteResults()

	routeResultsMu.Lock()
	_, oldExists := routeResults["old-id"]
	_, newExists := routeResults["new-id"]
	routeResultsMu.Unlock()

	if oldExists {
		t.Error("expired route result should be cleaned up")
	}
	if !newExists {
		t.Error("recent route result should NOT be cleaned up")
	}

	// Cleanup test state.
	routeResultsMu.Lock()
	delete(routeResults, "new-id")
	routeResultsMu.Unlock()
}

func TestCleanupRouteResults_KeepsRunning(t *testing.T) {
	routeResultsMu.Lock()
	routeResults["running-id"] = &routeResultEntry{
		Status:    "running",
		CreatedAt: time.Now().Add(-5 * time.Minute),
	}
	routeResultsMu.Unlock()

	cleanupRouteResults()

	routeResultsMu.Lock()
	_, exists := routeResults["running-id"]
	routeResultsMu.Unlock()

	if !exists {
		t.Error("running route result within TTL should NOT be cleaned up")
	}

	// Cleanup test state.
	routeResultsMu.Lock()
	delete(routeResults, "running-id")
	routeResultsMu.Unlock()
}

// ---------------------------------------------------------------------------
// recoveryMiddleware
// ---------------------------------------------------------------------------

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	panicky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	handler := recoveryMiddleware(panicky)
	req, _ := http.NewRequest("GET", "/boom", nil)
	rr := &httpResponseRecorder{code: 200, header: http.Header{}}
	handler.ServeHTTP(rr, req)
	if rr.code != http.StatusInternalServerError {
		t.Errorf("recoveryMiddleware status = %d, want 500", rr.code)
	}
}

func TestRecoveryMiddleware_PassesThrough(t *testing.T) {
	normal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler := recoveryMiddleware(normal)
	req, _ := http.NewRequest("GET", "/ok", nil)
	rr := &httpResponseRecorder{code: 200, header: http.Header{}}
	handler.ServeHTTP(rr, req)
	if rr.code != http.StatusOK {
		t.Errorf("recoveryMiddleware normal status = %d, want 200", rr.code)
	}
}

// ---------------------------------------------------------------------------
// bodySizeMiddleware
// ---------------------------------------------------------------------------

func TestBodySizeMiddleware_AllowsSmallBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := bodySizeMiddleware(inner)
	body := strings.NewReader("small body")
	req, _ := http.NewRequest("POST", "/test", body)
	rr := &httpResponseRecorder{code: 200, header: http.Header{}}
	handler.ServeHTTP(rr, req)
	if rr.code != http.StatusOK {
		t.Errorf("bodySizeMiddleware small body status = %d, want 200", rr.code)
	}
}

// httpResponseRecorder is a minimal http.ResponseWriter for tests.
type httpResponseRecorder struct {
	code   int
	header http.Header
	body   []byte
}

func (r *httpResponseRecorder) Header() http.Header         { return r.header }
func (r *httpResponseRecorder) Write(b []byte) (int, error)  { r.body = append(r.body, b...); return len(b), nil }
func (r *httpResponseRecorder) WriteHeader(code int)          { r.code = code }

// --- from security_test.go ---

// ---------------------------------------------------------------------------
// securityMonitor
// ---------------------------------------------------------------------------

func newTestSecurityMonitor(threshold, windowMin int) (*securityMonitor, *[]string) {
	var alerts []string
	var mu sync.Mutex
	notifyFn := func(msg string) {
		mu.Lock()
		alerts = append(alerts, msg)
		mu.Unlock()
	}
	sm := &securityMonitor{
		events:        make(map[string][]time.Time),
		lastAlert:     make(map[string]time.Time),
		threshold:     threshold,
		windowMin:     windowMin,
		alertCooldown: 15 * time.Minute,
		notifyFn:      notifyFn,
	}
	return sm, &alerts
}

func TestSecurityMonitor_NilSafe(t *testing.T) {
	// Should not panic.
	var sm *securityMonitor
	sm.recordEvent("1.2.3.4", "test")
	sm.cleanup()
}

func TestSecurityMonitor_NoAlertBelowThreshold(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(5, 5)

	for i := 0; i < 4; i++ {
		sm.recordEvent("1.2.3.4", "auth.fail")
	}

	// Give goroutine a moment if it were to fire (it shouldn't).
	time.Sleep(50 * time.Millisecond)
	if len(*alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(*alerts))
	}
}

func TestSecurityMonitor_AlertAtThreshold(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(3, 5)

	for i := 0; i < 3; i++ {
		sm.recordEvent("1.2.3.4", "auth.fail")
	}

	// Wait for async notification.
	time.Sleep(100 * time.Millisecond)
	if len(*alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(*alerts))
	}
	if !strings.Contains((*alerts)[0], "1.2.3.4") {
		t.Errorf("alert should contain IP, got %q", (*alerts)[0])
	}
	if !strings.Contains((*alerts)[0], "[Security]") {
		t.Errorf("alert should contain [Security], got %q", (*alerts)[0])
	}
}

func TestSecurityMonitor_DedupSameIP(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(2, 5)

	// First burst: 2 events -> alert.
	sm.recordEvent("1.2.3.4", "auth.fail")
	sm.recordEvent("1.2.3.4", "auth.fail")

	// Second burst: 2 more events -> should be deduped.
	sm.recordEvent("1.2.3.4", "auth.fail")
	sm.recordEvent("1.2.3.4", "auth.fail")

	time.Sleep(100 * time.Millisecond)
	if len(*alerts) != 1 {
		t.Errorf("expected 1 alert (dedup), got %d", len(*alerts))
	}
}

func TestSecurityMonitor_DifferentIPsSeparate(t *testing.T) {
	sm, alerts := newTestSecurityMonitor(2, 5)

	sm.recordEvent("1.1.1.1", "auth.fail")
	sm.recordEvent("1.1.1.1", "auth.fail")
	sm.recordEvent("2.2.2.2", "auth.fail")
	sm.recordEvent("2.2.2.2", "auth.fail")

	time.Sleep(100 * time.Millisecond)
	if len(*alerts) != 2 {
		t.Errorf("expected 2 alerts (different IPs), got %d", len(*alerts))
	}
}

func TestSecurityMonitor_Cleanup(t *testing.T) {
	sm, _ := newTestSecurityMonitor(10, 1) // 1 minute window

	// Add old events.
	sm.mu.Lock()
	sm.events["old-ip"] = []time.Time{time.Now().Add(-5 * time.Minute)}
	sm.lastAlert["old-ip"] = time.Now().Add(-20 * time.Minute)
	sm.mu.Unlock()

	sm.cleanup()

	sm.mu.Lock()
	_, eventsExist := sm.events["old-ip"]
	_, alertsExist := sm.lastAlert["old-ip"]
	sm.mu.Unlock()

	if eventsExist {
		t.Error("cleanup should remove expired events")
	}
	if alertsExist {
		t.Error("cleanup should remove expired alert dedup entries")
	}
}

func TestNewSecurityMonitor_Disabled(t *testing.T) {
	cfg := &Config{SecurityAlert: SecurityAlertConfig{Enabled: false}}
	sm := newSecurityMonitor(cfg, func(s string) {})
	if sm != nil {
		t.Error("expected nil when disabled")
	}
}

func TestNewSecurityMonitor_NilNotify(t *testing.T) {
	cfg := &Config{SecurityAlert: SecurityAlertConfig{Enabled: true}}
	sm := newSecurityMonitor(cfg, nil)
	if sm != nil {
		t.Error("expected nil when notifyFn is nil")
	}
}

func TestNewSecurityMonitor_Defaults(t *testing.T) {
	cfg := &Config{SecurityAlert: SecurityAlertConfig{Enabled: true}}
	sm := newSecurityMonitor(cfg, func(s string) {})
	if sm.threshold != 10 {
		t.Errorf("threshold = %d, want 10", sm.threshold)
	}
	if sm.windowMin != 5 {
		t.Errorf("windowMin = %d, want 5", sm.windowMin)
	}
}

// --- from pwa_test.go ---

// ---------------------------------------------------------------------------
// Dashboard HTML integration tests
// ---------------------------------------------------------------------------

func TestDashboardHTML_ManifestLink(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, `rel="manifest"`) {
		t.Error("dashboard.html missing manifest link")
	}
	if !strings.Contains(html, `/dashboard/manifest.json`) {
		t.Error("dashboard.html manifest link has wrong href")
	}
}

func TestDashboardHTML_SWRegistration(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, "serviceWorker") {
		t.Error("dashboard.html missing service worker registration")
	}
	if !strings.Contains(html, "/dashboard/sw.js") {
		t.Error("dashboard.html SW registration has wrong path")
	}
}

func TestDashboardHTML_ThemeColor(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, `name="theme-color"`) {
		t.Error("dashboard.html missing theme-color meta tag")
	}
}

func TestDashboardHTML_InstallButton(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, "pwa-install-btn") {
		t.Error("dashboard.html missing PWA install button")
	}
	if !strings.Contains(html, "pwaInstall") {
		t.Error("dashboard.html missing pwaInstall function")
	}
}

// ---------------------------------------------------------------------------
// Auth middleware bypass test
// ---------------------------------------------------------------------------

func TestDashboardAuthMiddleware_AllowsPWAAssets(t *testing.T) {
	cfg := &Config{
		DashboardAuth: DashboardAuthConfig{
			Enabled:  true,
			Password: "secret",
		},
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := dashboardAuthMiddleware(cfg, inner)

	paths := []string{"/dashboard/manifest.json", "/dashboard/sw.js", "/dashboard/icon.svg"}
	for _, p := range paths {
		req := httptest.NewRequest("GET", p, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path %s returned %d with auth enabled, expected 200 (bypass)", p, rr.Code)
		}
	}
}

// --- from quiet_test.go ---

func TestIsQuietHours_Disabled(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: false,
			Start:   "23:00",
			End:     "08:00",
		},
	}
	if quiet.IsQuietHours(toQuietCfg(cfg)) {
		t.Error("isQuietHours should return false when disabled")
	}
}

func TestIsQuietHours_EmptyStart(t *testing.T) {
	cfg := &Config{
		QuietHours: QuietHoursConfig{
			Enabled: true,
			Start:   "",
			End:     "08:00",
		},
	}
	if quiet.IsQuietHours(toQuietCfg(cfg)) {
		t.Error("isQuietHours should return false when start is empty")
	}
}

// --- from quickaction_test.go ---

func TestQuickAction_List(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
			{Name: "review", Label: "Code review"},
		},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)
	actions := engine.List()
	if len(actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(actions))
	}
}

func TestQuickAction_Get(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
			{Name: "review", Label: "Code review"},
		},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	action, err := engine.Get("deploy")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if action.Name != "deploy" {
		t.Errorf("expected name 'deploy', got %s", action.Name)
	}
}

func TestQuickAction_Get_NotFound(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
		},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	_, err := engine.Get("unknown")
	if err == nil {
		t.Error("expected error for missing action, got nil")
	}
}

func TestQuickAction_BuildPrompt_Static(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "hello", Prompt: "Say hello", Agent: "琉璃"},
		},
		SmartDispatch: SmartDispatchConfig{DefaultAgent: "琉璃"},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	prompt, role, err := engine.BuildPrompt("hello", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Say hello" {
		t.Errorf("expected prompt 'Say hello', got %s", prompt)
	}
	if role != "琉璃" {
		t.Errorf("expected role '琉璃', got %s", role)
	}
}

func TestQuickAction_BuildPrompt_Template(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{
				Name:           "greet",
				PromptTemplate: "Hello {{.name}}!",
				Agent:           "琉璃",
			},
		},
		SmartDispatch: SmartDispatchConfig{DefaultAgent: "琉璃"},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	params := map[string]any{"name": "Alice"}
	prompt, role, err := engine.BuildPrompt("greet", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Hello Alice!" {
		t.Errorf("expected prompt 'Hello Alice!', got %s", prompt)
	}
	if role != "琉璃" {
		t.Errorf("expected role '琉璃', got %s", role)
	}
}

func TestQuickAction_BuildPrompt_Defaults(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{
				Name:           "greet",
				PromptTemplate: "Hello {{.name}}, you are {{.age}} years old!",
				Params: map[string]QuickActionParam{
					"name": {Type: "string", Default: "Guest"},
					"age":  {Type: "number", Default: 18},
				},
				Agent: "琉璃",
			},
		},
		SmartDispatch: SmartDispatchConfig{DefaultAgent: "琉璃"},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	// Only override name, age should use default.
	params := map[string]any{"name": "Bob"}
	prompt, _, err := engine.BuildPrompt("greet", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Hello Bob, you are 18 years old!" {
		t.Errorf("expected 'Hello Bob, you are 18 years old!', got %s", prompt)
	}

	// No params, should use all defaults.
	prompt2, _, err := engine.BuildPrompt("greet", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt2 != "Hello Guest, you are 18 years old!" {
		t.Errorf("expected 'Hello Guest, you are 18 years old!', got %s", prompt2)
	}
}

func TestQuickAction_Search(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production", Shortcut: "d"},
			{Name: "review", Label: "Code review", Shortcut: "r"},
			{Name: "test", Label: "Run tests", Shortcut: "t"},
		},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	// Search by name.
	results := engine.Search("deploy")
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("expected 1 result 'deploy', got %d results", len(results))
	}

	// Search by label substring.
	results = engine.Search("code")
	if len(results) != 1 || results[0].Name != "review" {
		t.Errorf("expected 1 result 'review', got %d results", len(results))
	}

	// Search by label substring (case insensitive).
	results = engine.Search("PRODUCTION")
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("expected 1 result 'deploy', got %d results", len(results))
	}
}

func TestQuickAction_Search_NoMatch(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "deploy", Label: "Deploy to production"},
		},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	results := engine.Search("unknown")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestQuickAction_Search_Shortcut(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{
			{Name: "build", Label: "Build project", Shortcut: "b"},
			{Name: "test", Label: "Run tests", Shortcut: "t"},
		},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	results := engine.Search("b")
	if len(results) != 1 || results[0].Name != "build" {
		t.Errorf("expected 1 result 'build', got %d results", len(results))
	}
}

func TestQuickAction_EmptyConfig(t *testing.T) {
	cfg := &Config{
		QuickActions: []QuickAction{},
	}
	engine := quickaction.NewEngine(cfg.QuickActions, cfg.SmartDispatch.DefaultAgent)

	actions := engine.List()
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}

	_, err := engine.Get("any")
	if err == nil {
		t.Error("expected error for missing action, got nil")
	}
}

// --- from incoming_webhook_test.go ---

// --- Signature Verification Tests ---

func TestVerifyWebhookSignature_NoSecret(t *testing.T) {
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	if !verifyWebhookSignature(r, []byte("body"), "") {
		t.Error("expected true when no secret configured")
	}
}

func TestVerifyWebhookSignature_GitHub(t *testing.T) {
	secret := "mysecret"
	body := []byte(`{"action":"opened"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Hub-Signature-256", sig)

	if !verifyWebhookSignature(r, body, secret) {
		t.Error("expected true for valid GitHub signature")
	}

	// Wrong signature.
	r2 := httptest.NewRequest("POST", "/hooks/test", nil)
	r2.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	if verifyWebhookSignature(r2, body, secret) {
		t.Error("expected false for invalid GitHub signature")
	}
}

func TestVerifyWebhookSignature_GitLab(t *testing.T) {
	secret := "gitlab-token"
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Gitlab-Token", secret)

	if !verifyWebhookSignature(r, []byte("body"), secret) {
		t.Error("expected true for valid GitLab token")
	}

	r2 := httptest.NewRequest("POST", "/hooks/test", nil)
	r2.Header.Set("X-Gitlab-Token", "wrong")
	if verifyWebhookSignature(r2, []byte("body"), secret) {
		t.Error("expected false for wrong GitLab token")
	}
}

func TestVerifyWebhookSignature_Generic(t *testing.T) {
	secret := "genericsecret"
	body := []byte(`{"data":"test"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest("POST", "/hooks/test", nil)
	r.Header.Set("X-Webhook-Signature", sig)

	if !verifyWebhookSignature(r, body, secret) {
		t.Error("expected true for valid generic signature")
	}
}

func TestVerifyWebhookSignature_SecretButNoHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/hooks/test", nil)
	if verifyWebhookSignature(r, []byte("body"), "secret") {
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

	if !verifyHMACSHA256(body, secret, sig) {
		t.Error("expected true for valid HMAC")
	}
	if verifyHMACSHA256(body, secret, "badhex") {
		t.Error("expected false for invalid hex")
	}
	if verifyHMACSHA256(body, "wrong-secret", sig) {
		t.Error("expected false for wrong secret")
	}
}

// --- Template Expansion Tests ---

func TestExpandPayloadTemplate_Simple(t *testing.T) {
	payload := map[string]any{
		"action": "opened",
		"title":  "Fix bug",
		"count":  float64(42),
	}

	result := expandPayloadTemplate("Action: {{payload.action}}, Title: {{payload.title}}", payload)
	if result != "Action: opened, Title: Fix bug" {
		t.Errorf("got %q", result)
	}
}

func TestExpandPayloadTemplate_Nested(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"title":    "Add feature",
			"html_url": "https://github.com/repo/pull/1",
		},
	}

	result := expandPayloadTemplate("PR: {{payload.pull_request.title}} - {{payload.pull_request.html_url}}", payload)
	expected := "PR: Add feature - https://github.com/repo/pull/1"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestExpandPayloadTemplate_Missing(t *testing.T) {
	result := expandPayloadTemplate("{{payload.nonexistent}}", map[string]any{})
	if result != "{{payload.nonexistent}}" {
		t.Errorf("expected original placeholder for missing key, got %q", result)
	}
}

func TestExpandPayloadTemplate_Types(t *testing.T) {
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
		result := expandPayloadTemplate(tt.template, payload)
		if result != tt.expected {
			t.Errorf("expandPayloadTemplate(%q) = %q, want %q", tt.template, result, tt.expected)
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

	if v := getNestedValue(m, "top"); v != "level" {
		t.Errorf("got %v, want 'level'", v)
	}
	if v := getNestedValue(m, "a.b.c"); v != "deep" {
		t.Errorf("got %v, want 'deep'", v)
	}
	if v := getNestedValue(m, "a.b.missing"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
	if v := getNestedValue(m, "nonexistent"); v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

// --- Filter Evaluation Tests ---

func TestEvaluateFilter_Empty(t *testing.T) {
	if !evaluateFilter("", map[string]any{}) {
		t.Error("empty filter should accept all")
	}
}

func TestEvaluateFilter_Equal(t *testing.T) {
	payload := map[string]any{"action": "opened"}

	if !evaluateFilter("payload.action == 'opened'", payload) {
		t.Error("expected true for matching ==")
	}
	if evaluateFilter("payload.action == 'closed'", payload) {
		t.Error("expected false for non-matching ==")
	}
}

func TestEvaluateFilter_NotEqual(t *testing.T) {
	payload := map[string]any{"action": "opened"}

	if !evaluateFilter("payload.action != 'closed'", payload) {
		t.Error("expected true for non-matching !=")
	}
	if evaluateFilter("payload.action != 'opened'", payload) {
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
		result := evaluateFilter(tt.filter, tt.payload)
		if result != tt.expected {
			t.Errorf("evaluateFilter(%q) = %v, want %v", tt.filter, result, tt.expected)
		}
	}
}

func TestEvaluateFilter_NestedKey(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"state": "open",
		},
	}
	if !evaluateFilter("payload.pull_request.state == 'open'", payload) {
		t.Error("expected true for nested key equality")
	}
}

func TestEvaluateFilter_DoubleQuotes(t *testing.T) {
	payload := map[string]any{"action": "opened"}
	if !evaluateFilter(`payload.action == "opened"`, payload) {
		t.Error("expected true with double quotes")
	}
}

// --- isTruthy Tests ---

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
		if isTruthy(tt.val) != tt.expected {
			t.Errorf("isTruthy(%v) = %v, want %v", tt.val, !tt.expected, tt.expected)
		}
	}
}

// --- IncomingWebhookConfig Tests ---

func TestIncomingWebhookConfig_IsEnabled(t *testing.T) {
	// Default (nil) → enabled.
	c := IncomingWebhookConfig{}
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

// --- Handler Integration Tests ---

// testWebhookConfig creates a Config with a minimal provider registry for webhook tests.
func testWebhookConfig(webhooks map[string]IncomingWebhookConfig) *Config {
	cfg := &Config{
		IncomingWebhooks: webhooks,
		BaseDir:          "/tmp/tetora-test",
	}
	return cfg
}

func TestHandleIncomingWebhook_NotFound(t *testing.T) {
	cfg := testWebhookConfig(nil)
	r := httptest.NewRequest("POST", "/hooks/missing", strings.NewReader(`{}`))
	result := handleIncomingWebhook(context.Background(), cfg, "missing", r, nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "not found") {
		t.Errorf("expected 'not found' in message, got %q", result.Message)
	}
}

func TestHandleIncomingWebhook_Disabled(t *testing.T) {
	f := false
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Enabled: &f},
	})
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(`{}`))
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, nil, nil)
	if result.Status != "disabled" {
		t.Errorf("expected disabled status, got %q", result.Status)
	}
}

func TestHandleIncomingWebhook_SignatureFail(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Secret: "mysecret"},
	})
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(`{"test":true}`))
	// No signature header.
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "signature") {
		t.Errorf("expected signature error, got %q", result.Message)
	}
}

func TestHandleIncomingWebhook_BadJSON(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜"},
	})
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(`not json`))
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "parse payload") {
		t.Errorf("expected parse error, got %q", result.Message)
	}
}

func TestHandleIncomingWebhook_Filtered(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"gh": {Agent: "黒曜", Filter: "payload.action == 'opened'"},
	})
	body := `{"action":"closed"}`
	r := httptest.NewRequest("POST", "/hooks/gh", strings.NewReader(body))
	result := handleIncomingWebhook(context.Background(), cfg, "gh", r, nil, nil, nil)
	if result.Status != "filtered" {
		t.Errorf("expected filtered status, got %q", result.Status)
	}
}

func TestHandleIncomingWebhook_Accepted(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Template: "Process: {{payload.action}}"},
	})
	body := `{"action":"opened"}`
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(body))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted status, got %q", result.Status)
	}
	if result.Agent != "黒曜" {
		t.Errorf("expected role 黒曜, got %q", result.Agent)
	}
	if result.TaskID == "" {
		t.Error("expected non-empty taskID")
	}
}

func TestHandleIncomingWebhook_WithValidSignature(t *testing.T) {
	secret := "webhook-secret"
	payload := `{"action":"opened","title":"Test PR"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"gh": {Agent: "黒曜", Secret: secret, Template: "Review: {{payload.title}}"},
	})

	r := httptest.NewRequest("POST", "/hooks/gh", strings.NewReader(payload))
	r.Header.Set("X-Hub-Signature-256", sig)
	sem := make(chan struct{}, 5)

	result := handleIncomingWebhook(context.Background(), cfg, "gh", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q: %s", result.Status, result.Message)
	}
}

func TestHandleIncomingWebhook_DefaultPrompt(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜"}, // no template
	})
	body := `{"key":"value"}`
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(body))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q", result.Status)
	}
}

// --- HTTP Endpoint Tests ---

func TestIncomingWebhookHTTPEndpoint(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜", Template: "Test: {{payload.msg}}"},
	})
	sem := make(chan struct{}, 5)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/hooks/")
		ctx := r.Context()
		result := handleIncomingWebhook(ctx, cfg, name, r, nil, sem, nil)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	body := `{"msg":"hello"}`
	req := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var result IncomingWebhookResult
	json.NewDecoder(rr.Body).Decode(&result)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q", result.Status)
	}
}

func TestIncomingWebhookHTTPEndpoint_NotFound(t *testing.T) {
	cfg := testWebhookConfig(nil)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/hooks/")
		result := handleIncomingWebhook(r.Context(), cfg, name, r, nil, nil, nil)
		w.Header().Set("Content-Type", "application/json")
		if result.Status == "error" {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(result)
	})

	req := httptest.NewRequest("POST", "/hooks/nonexistent", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- Webhook List API Test ---

func TestWebhookListEndpoint(t *testing.T) {
	cfg := &Config{
		IncomingWebhooks: map[string]IncomingWebhookConfig{
			"gh-pr":  {Agent: "黒曜", Secret: "s", Filter: "payload.action == 'opened'"},
			"sentry": {Agent: "黒曜", Template: "Alert: {{payload.title}}"},
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		type webhookInfo struct {
			Name      string `json:"name"`
			Agent     string `json:"agent"`
			Enabled   bool   `json:"enabled"`
			HasSecret bool   `json:"hasSecret"`
		}
		var list []webhookInfo
		for name, wh := range cfg.IncomingWebhooks {
			list = append(list, webhookInfo{
				Name:      name,
				Agent:     wh.Agent,
				Enabled:   wh.IsEnabled(),
				HasSecret: wh.Secret != "",
			})
		}
		json.NewEncoder(w).Encode(list)
	})

	req := httptest.NewRequest("GET", "/webhooks/incoming", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var list []map[string]any
	json.NewDecoder(rr.Body).Decode(&list)
	if len(list) != 2 {
		t.Errorf("expected 2 webhooks, got %d", len(list))
	}
}

// --- Workflow Trigger Tests ---

func TestTriggerWebhookWorkflow_NotFound(t *testing.T) {
	cfg := &Config{
		BaseDir: t.TempDir(),
	}
	whCfg := IncomingWebhookConfig{
		Agent:    "黒曜",
		Workflow: "nonexistent",
	}
	result := triggerWebhookWorkflow(context.Background(), cfg, "test", whCfg,
		map[string]any{}, "prompt", nil, nil, nil)
	if result.Status != "error" {
		t.Errorf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "nonexistent") {
		t.Errorf("expected workflow name in error, got %q", result.Message)
	}
}

// --- Body Size Limit Test ---

func TestHandleIncomingWebhook_LargeBody(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"test": {Agent: "黒曜"},
	})
	// Create a valid JSON that's large but under 1MB.
	largePayload := `{"data":"` + strings.Repeat("x", 500000) + `"}`
	r := httptest.NewRequest("POST", "/hooks/test", strings.NewReader(largePayload))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "test", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted for large payload, got %q: %s", result.Status, result.Message)
	}
}

// --- Filter + Template Combo Test ---

func TestHandleIncomingWebhook_FilterPassAndTemplateExpand(t *testing.T) {
	cfg := testWebhookConfig(map[string]IncomingWebhookConfig{
		"gh": {
			Agent:    "黒曜",
			Filter:   "payload.action == 'opened'",
			Template: "Review PR: {{payload.pull_request.title}} ({{payload.pull_request.html_url}})",
		},
	})

	payload := map[string]any{
		"action": "opened",
		"pull_request": map[string]any{
			"title":    "Add feature X",
			"html_url": "https://github.com/repo/pull/42",
		},
	}
	body, _ := json.Marshal(payload)

	r := httptest.NewRequest("POST", "/hooks/gh", bytes.NewReader(body))
	sem := make(chan struct{}, 5)
	result := handleIncomingWebhook(context.Background(), cfg, "gh", r, nil, sem, nil)
	if result.Status != "accepted" {
		t.Errorf("expected accepted, got %q: %s", result.Status, result.Message)
	}
	if result.Agent != "黒曜" {
		t.Errorf("expected role 黒曜, got %q", result.Agent)
	}
}

func testConfig() *Config {
	return &Config{
		ListenAddr: "127.0.0.1:7777",
	}
}

func TestBuildOpenAPISpec_HasRequiredFields(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())

	requiredKeys := []string{"openapi", "info", "paths", "components", "tags"}
	for _, key := range requiredKeys {
		if _, ok := spec[key]; !ok {
			t.Errorf("spec missing required key %q", key)
		}
	}

	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatal("info is not a map")
	}
	for _, key := range []string{"title", "description", "version"} {
		if _, ok := info[key]; !ok {
			t.Errorf("info missing required key %q", key)
		}
	}
}

func TestBuildOpenAPISpec_Version(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	info := spec["info"].(map[string]any)
	version, ok := info["version"].(string)
	if !ok {
		t.Fatal("version is not a string")
	}
	if version != tetoraVersion {
		t.Errorf("expected version %q, got %q", tetoraVersion, version)
	}
}

func TestBuildOpenAPISpec_OpenAPIVersion(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	v, ok := spec["openapi"].(string)
	if !ok || v != "3.0.3" {
		t.Errorf("expected openapi 3.0.3, got %v", spec["openapi"])
	}
}

func TestBuildOpenAPISpec_AllEndpoints(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths is not a map")
	}

	expectedPaths := []string{
		"/dispatch",
		"/dispatch/estimate",
		"/cancel",
		"/healthz",
		"/history",
		"/history/{id}",
		"/sessions",
		"/sessions/{id}",
		"/sessions/{id}/message",
		"/sessions/{id}/stream",
		"/workflows",
		"/workflows/{name}",
		"/workflows/{name}/run",
		"/workflow-runs",
		"/workflow-runs/{id}",
		"/knowledge",
		"/knowledge/search",
		"/circuits",
		"/circuits/{provider}/reset",
		"/queue",
		"/budget",
		"/budget/pause",
		"/budget/resume",
		"/cron",
		"/cron/{id}/trigger",
		"/agent-messages",
		"/handoffs",
		"/roles",
		"/roles/{name}",
		"/route",
		"/audit",
		"/backup",
		"/stats/cost",
		"/stats/sla",
	}

	for _, path := range expectedPaths {
		if _, ok := paths[path]; !ok {
			t.Errorf("missing path: %s", path)
		}
	}
}

func TestBuildOpenAPISpec_Schemas(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatal("components is not a map")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("schemas is not a map")
	}

	expectedSchemas := []string{
		"Task",
		"TaskResult",
		"DispatchResult",
		"Session",
		"SessionDetail",
		"SessionMessage",
		"Workflow",
		"WorkflowStep",
		"WorkflowRun",
		"StepRunResult",
		"CostEstimate",
		"EstimateResult",
		"KnowledgeFile",
		"SearchResult",
		"Handoff",
		"AgentMessage",
		"QueueItem",
		"SLAMetrics",
		"HealthResult",
		"CronJob",
		"AgentConfig",
		"SmartDispatchResult",
		"Error",
		"JobRun",
		"AuditEntry",
	}

	for _, name := range expectedSchemas {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing schema: %s", name)
		}
	}
}

func TestBuildOpenAPISpec_ValidJSON(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal spec to JSON: %v", err)
	}
	if len(data) == 0 {
		t.Error("marshaled spec is empty")
	}

	// Verify it round-trips.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal spec JSON: %v", err)
	}
	if parsed["openapi"] != "3.0.3" {
		t.Error("round-tripped spec missing openapi field")
	}
}

func TestBuildOpenAPISpec_SecurityScheme(t *testing.T) {
	// With token: security should be present.
	cfgWithToken := &Config{
		ListenAddr: "127.0.0.1:7777",
		APIToken:   "test-secret-token",
	}
	spec := buildOpenAPISpec(cfgWithToken)
	if _, ok := spec["security"]; !ok {
		t.Error("security should be present when APIToken is set")
	}

	// Without token: security should be absent.
	cfgNoToken := &Config{
		ListenAddr: "127.0.0.1:7777",
	}
	specNoToken := buildOpenAPISpec(cfgNoToken)
	if _, ok := specNoToken["security"]; ok {
		t.Error("security should not be present when APIToken is empty")
	}

	// Security scheme should always be in components.
	components := spec["components"].(map[string]any)
	secSchemes, ok := components["securitySchemes"].(map[string]any)
	if !ok {
		t.Fatal("securitySchemes not found in components")
	}
	if _, ok := secSchemes["bearerAuth"]; !ok {
		t.Error("bearerAuth security scheme not found")
	}
}

func TestBuildOpenAPISpec_ServerURL(t *testing.T) {
	cfg := &Config{ListenAddr: "0.0.0.0:9999"}
	spec := buildOpenAPISpec(cfg)
	servers, ok := spec["servers"].([]map[string]any)
	if !ok || len(servers) == 0 {
		t.Fatal("servers is missing or empty")
	}
	url, ok := servers[0]["url"].(string)
	if !ok || url != "http://0.0.0.0:9999" {
		t.Errorf("expected server url http://0.0.0.0:9999, got %v", url)
	}
}

func TestBuildOpenAPISpec_DispatchEndpointShape(t *testing.T) {
	spec := buildOpenAPISpec(testConfig())
	paths := spec["paths"].(map[string]any)
	dispatch := paths["/dispatch"].(map[string]any)
	post, ok := dispatch["post"].(map[string]any)
	if !ok {
		t.Fatal("/dispatch POST operation not found")
	}

	// Should have tags, summary, description, requestBody, responses.
	for _, key := range []string{"tags", "summary", "description", "requestBody", "responses"} {
		if _, ok := post[key]; !ok {
			t.Errorf("/dispatch POST missing %q", key)
		}
	}

	// Tags should include "Core".
	tags, ok := post["tags"].([]string)
	if !ok || len(tags) == 0 || tags[0] != "Core" {
		t.Errorf("/dispatch POST should be tagged Core, got %v", post["tags"])
	}
}

func TestBuildPaths_MethodTypes(t *testing.T) {
	paths := buildPaths()

	// /dispatch should only have POST.
	dispatch := paths["/dispatch"].(map[string]any)
	if _, ok := dispatch["post"]; !ok {
		t.Error("/dispatch missing post")
	}
	if _, ok := dispatch["get"]; ok {
		t.Error("/dispatch should not have get")
	}

	// /healthz should only have GET.
	healthz := paths["/healthz"].(map[string]any)
	if _, ok := healthz["get"]; !ok {
		t.Error("/healthz missing get")
	}

	// /sessions/{id} should have GET and DELETE.
	sessId := paths["/sessions/{id}"].(map[string]any)
	if _, ok := sessId["get"]; !ok {
		t.Error("/sessions/{id} missing get")
	}
	if _, ok := sessId["delete"]; !ok {
		t.Error("/sessions/{id} missing delete")
	}
}

func TestBuildComponents_ErrorSchema(t *testing.T) {
	components := buildComponents()
	schemas := components["schemas"].(map[string]any)
	errSchema := schemas["Error"].(map[string]any)

	props, ok := errSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("Error schema missing properties")
	}
	if _, ok := props["error"]; !ok {
		t.Error("Error schema missing 'error' property")
	}

	required, ok := errSchema["required"].([]string)
	if !ok || len(required) == 0 || required[0] != "error" {
		t.Error("Error schema 'error' field should be required")
	}
}
