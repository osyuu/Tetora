package webhook_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tetora/internal/webhook"
)

// --------------------------------------------------------------------------
// MatchesEvent
// --------------------------------------------------------------------------

func TestMatchesEvent(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		event  string
		want   bool
	}{
		{
			name:   "empty events list matches any event",
			events: []string{},
			event:  "success",
			want:   true,
		},
		{
			name:   "nil events list matches any event",
			events: nil,
			event:  "timeout",
			want:   true,
		},
		{
			name:   "events contains all matches any event",
			events: []string{"all"},
			event:  "error",
			want:   true,
		},
		{
			name:   "events contains all matches another event",
			events: []string{"all"},
			event:  "cancelled",
			want:   true,
		},
		{
			name:   "events contains specific event matches",
			events: []string{"success"},
			event:  "success",
			want:   true,
		},
		{
			name:   "events does not contain event returns false",
			events: []string{"success"},
			event:  "error",
			want:   false,
		},
		{
			name:   "multiple events list matches one of them",
			events: []string{"success", "timeout"},
			event:  "timeout",
			want:   true,
		},
		{
			name:   "multiple events list does not match absent event",
			events: []string{"success", "timeout"},
			event:  "error",
			want:   false,
		},
		{
			name:   "multiple events list with all matches any event",
			events: []string{"success", "all"},
			event:  "cancelled",
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wh := webhook.Config{Events: tc.events}
			got := webhook.MatchesEvent(wh, tc.event)
			if got != tc.want {
				t.Errorf("MatchesEvent(%v, %q) = %v, want %v", tc.events, tc.event, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Send — helpers
// --------------------------------------------------------------------------

// capture records a single HTTP request received by the test server.
type capture struct {
	method      string
	contentType string
	headers     http.Header
	body        []byte
}

// collectingServer starts an httptest.Server that records all incoming requests
// into the returned slice. The WaitGroup must be pre-added by the caller
// (wg.Add(n) where n is the number of expected requests).
func collectingServer(t *testing.T, wg *sync.WaitGroup) (*httptest.Server, *[]capture) {
	t.Helper()
	var mu sync.Mutex
	var captures []capture

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("server: reading body: %v", err)
		}
		mu.Lock()
		captures = append(captures, capture{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			headers:     r.Header.Clone(),
			body:        body,
		})
		mu.Unlock()
		wg.Done()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &captures
}

// waitWithTimeout waits for the WaitGroup to reach zero or times out.
func waitWithTimeout(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("timed out waiting for webhook deliveries")
	}
}

// --------------------------------------------------------------------------
// Send — tests
// --------------------------------------------------------------------------

func TestSend_EmptyWebhookListDoesNothing(t *testing.T) {
	// No server is started; if Send tried to make a request it would fail.
	webhook.Send(nil, "success", webhook.Payload{JobID: "j1"})
	// Nothing to assert — the test passes if it does not panic or block.
}

func TestSend_ContentTypeIsJSON(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	srv, caps := collectingServer(t, &wg)

	webhook.Send([]webhook.Config{{URL: srv.URL}}, "success", webhook.Payload{JobID: "j1"})
	waitWithTimeout(t, &wg, 3*time.Second)

	c := (*caps)[0]
	if c.method != "POST" {
		t.Errorf("method = %q, want POST", c.method)
	}
	if c.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", c.contentType)
	}
}

func TestSend_CustomHeadersAreSent(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	srv, caps := collectingServer(t, &wg)

	wh := webhook.Config{
		URL: srv.URL,
		Headers: map[string]string{
			"X-Secret-Token": "abc123",
			"X-Source":       "tetora",
		},
	}
	webhook.Send([]webhook.Config{wh}, "success", webhook.Payload{JobID: "j2"})
	waitWithTimeout(t, &wg, 3*time.Second)

	c := (*caps)[0]
	if got := c.headers.Get("X-Secret-Token"); got != "abc123" {
		t.Errorf("X-Secret-Token = %q, want %q", got, "abc123")
	}
	if got := c.headers.Get("X-Source"); got != "tetora" {
		t.Errorf("X-Source = %q, want %q", got, "tetora")
	}
}

func TestSend_FiltersWebhooksByEvent(t *testing.T) {
	// Two webhooks: one listens for "success" only, one for "error" only.
	// Sending "success" should fire exactly one request (to the success webhook).
	var wg sync.WaitGroup
	wg.Add(1) // only one delivery expected
	srv, caps := collectingServer(t, &wg)

	webhooks := []webhook.Config{
		{URL: srv.URL, Events: []string{"success"}},
		{URL: srv.URL, Events: []string{"error"}},
	}
	webhook.Send(webhooks, "success", webhook.Payload{JobID: "j3"})
	waitWithTimeout(t, &wg, 3*time.Second)

	// Give a moment for any unexpected second delivery to arrive.
	time.Sleep(50 * time.Millisecond)

	if got := len(*caps); got != 1 {
		t.Errorf("deliveries = %d, want 1 (only matching webhook should fire)", got)
	}
}

func TestSend_AllEventFilterFiresForAnyEvent(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	srv, caps := collectingServer(t, &wg)

	wh := webhook.Config{URL: srv.URL, Events: []string{"all"}}
	webhook.Send([]webhook.Config{wh}, "cancelled", webhook.Payload{JobID: "j4"})
	waitWithTimeout(t, &wg, 3*time.Second)

	if len(*caps) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(*caps))
	}
}

func TestSend_PayloadJSONStructure(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	srv, caps := collectingServer(t, &wg)

	input := webhook.Payload{
		JobID:    "job-42",
		Name:     "nightly-report",
		Source:   "cron",
		Status:   "success",
		Cost:     0.012,
		Duration: 3400,
		Model:    "claude-3-5-sonnet",
		Output:   "done",
	}
	webhook.Send([]webhook.Config{{URL: srv.URL}}, "success", input)
	waitWithTimeout(t, &wg, 3*time.Second)

	var got webhook.Payload
	if err := json.Unmarshal((*caps)[0].body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	// Event must be set by Send, not the caller.
	if got.Event != "success" {
		t.Errorf("Event = %q, want %q", got.Event, "success")
	}
	if got.JobID != input.JobID {
		t.Errorf("JobID = %q, want %q", got.JobID, input.JobID)
	}
	if got.Name != input.Name {
		t.Errorf("Name = %q, want %q", got.Name, input.Name)
	}
	if got.Source != input.Source {
		t.Errorf("Source = %q, want %q", got.Source, input.Source)
	}
	if got.Cost != input.Cost {
		t.Errorf("Cost = %v, want %v", got.Cost, input.Cost)
	}
	if got.Duration != input.Duration {
		t.Errorf("Duration = %v, want %v", got.Duration, input.Duration)
	}
	if got.Model != input.Model {
		t.Errorf("Model = %q, want %q", got.Model, input.Model)
	}
	// Timestamp must be populated automatically.
	if got.Timestamp == "" {
		t.Error("Timestamp is empty; Send should auto-populate it")
	}
	if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
		t.Errorf("Timestamp %q is not RFC3339: %v", got.Timestamp, err)
	}
}

func TestSend_MultipleMatchingWebhooksAllFire(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(3) // three webhooks, all match "error"
	srv, caps := collectingServer(t, &wg)

	webhooks := []webhook.Config{
		{URL: srv.URL, Events: []string{"error"}},
		{URL: srv.URL},                            // empty = all
		{URL: srv.URL, Events: []string{"all"}},
	}
	webhook.Send(webhooks, "error", webhook.Payload{JobID: "j5"})
	waitWithTimeout(t, &wg, 3*time.Second)

	if got := len(*caps); got != 3 {
		t.Errorf("deliveries = %d, want 3", got)
	}
}

func TestSend_CallerTimestampIsPreserved(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	srv, caps := collectingServer(t, &wg)

	fixedTS := "2024-01-15T12:00:00Z"
	webhook.Send([]webhook.Config{{URL: srv.URL}}, "success", webhook.Payload{
		JobID:     "j6",
		Timestamp: fixedTS,
	})
	waitWithTimeout(t, &wg, 3*time.Second)

	var got webhook.Payload
	if err := json.Unmarshal((*caps)[0].body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if got.Timestamp != fixedTS {
		t.Errorf("Timestamp = %q, want caller-supplied %q", got.Timestamp, fixedTS)
	}
}
