package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAPIClient_ClientIDHeader verifies that APIClient sets X-Client-ID header
// when ClientID is non-empty, and omits it when empty.
func TestAPIClient_ClientIDHeader(t *testing.T) {
	var capturedClientID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClientID = r.Header.Get("X-Client-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Strip http:// from URL to match NewAPIClient expectations.
	listenAddr := strings.TrimPrefix(srv.URL, "http://")

	t.Run("sets header when ClientID is non-empty", func(t *testing.T) {
		api := NewAPIClient(listenAddr, "")
		api.ClientID = "cli_myapp"

		resp, err := api.Do("GET", "/test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if capturedClientID != "cli_myapp" {
			t.Errorf("X-Client-ID = %q, want %q", capturedClientID, "cli_myapp")
		}
	})

	t.Run("omits header when ClientID is empty", func(t *testing.T) {
		capturedClientID = ""
		api := NewAPIClient(listenAddr, "")
		// ClientID intentionally not set.

		resp, err := api.Do("GET", "/test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if capturedClientID != "" {
			t.Errorf("X-Client-ID should be absent, got %q", capturedClientID)
		}
	})

	t.Run("sends header with POST body", func(t *testing.T) {
		capturedClientID = ""
		api := NewAPIClient(listenAddr, "")
		api.ClientID = "cli_test"

		resp, err := api.Do("POST", "/test", strings.NewReader(`{"prompt":"hello"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if capturedClientID != "cli_test" {
			t.Errorf("X-Client-ID = %q, want %q", capturedClientID, "cli_test")
		}
	})
}
