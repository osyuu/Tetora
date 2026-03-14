package audit_test

import (
	"strings"
	"testing"
	"time"

	"tetora/internal/audit"
)

// ---- ParseRouteDetail tests ----

func TestParseRouteDetail_Full(t *testing.T) {
	role, method, confidence, prompt := audit.ParseRouteDetail(
		"role=dev method=keyword confidence=high prompt=fix the bug",
	)
	if role != "dev" {
		t.Errorf("role: want %q, got %q", "dev", role)
	}
	if method != "keyword" {
		t.Errorf("method: want %q, got %q", "keyword", method)
	}
	if confidence != "high" {
		t.Errorf("confidence: want %q, got %q", "high", confidence)
	}
	if prompt != "fix the bug" {
		t.Errorf("prompt: want %q, got %q", "fix the bug", prompt)
	}
}

func TestParseRouteDetail_NumericConfidence(t *testing.T) {
	role, method, confidence, prompt := audit.ParseRouteDetail(
		"role=kokuyou method=llm confidence=0.9 prompt=build API",
	)
	if role != "kokuyou" {
		t.Errorf("role: want %q, got %q", "kokuyou", role)
	}
	if method != "llm" {
		t.Errorf("method: want %q, got %q", "llm", method)
	}
	if confidence != "0.9" {
		t.Errorf("confidence: want %q, got %q", "0.9", confidence)
	}
	if prompt != "build API" {
		t.Errorf("prompt: want %q, got %q", "build API", prompt)
	}
}

func TestParseRouteDetail_Empty(t *testing.T) {
	role, method, confidence, prompt := audit.ParseRouteDetail("")
	if role != "" || method != "" || confidence != "" || prompt != "" {
		t.Errorf("all fields should be empty for empty input, got role=%q method=%q confidence=%q prompt=%q",
			role, method, confidence, prompt)
	}
}

func TestParseRouteDetail_MissingFields(t *testing.T) {
	// Only role is present — no method, confidence, or prompt.
	role, method, confidence, prompt := audit.ParseRouteDetail("role=ruri")
	if role != "ruri" {
		t.Errorf("role: want %q, got %q", "ruri", role)
	}
	if method != "" {
		t.Errorf("method: want empty, got %q", method)
	}
	if confidence != "" {
		t.Errorf("confidence: want empty, got %q", confidence)
	}
	if prompt != "" {
		t.Errorf("prompt: want empty, got %q", prompt)
	}
}

func TestParseRouteDetail_PromptWithSpaces(t *testing.T) {
	// Prompt contains multiple words — everything after "prompt=" must be preserved.
	input := "role=hisui method=llm confidence=medium prompt=analyze the market and report back"
	_, _, _, prompt := audit.ParseRouteDetail(input)
	want := "analyze the market and report back"
	if prompt != want {
		t.Errorf("prompt: want %q, got %q", want, prompt)
	}
}

func TestParseRouteDetail_OnlyPrompt(t *testing.T) {
	// Degenerate: only the prompt= key, nothing before it.
	role, method, confidence, prompt := audit.ParseRouteDetail(" prompt=hello world")
	if prompt != "hello world" {
		t.Errorf("prompt: want %q, got %q", "hello world", prompt)
	}
	if role != "" || method != "" || confidence != "" {
		t.Errorf("unexpected non-empty fields: role=%q method=%q confidence=%q", role, method, confidence)
	}
}

func TestParseRouteDetail_ExtraWhitespace(t *testing.T) {
	// Extra spaces between tokens — strings.Fields handles them.
	role, _, _, _ := audit.ParseRouteDetail("role=kohaku  method=keyword confidence=low prompt=write a poem")
	if role != "kohaku" {
		t.Errorf("role: want %q, got %q", "kohaku", role)
	}
}

func TestParseRouteDetail_PromptContainsPromptKeyword(t *testing.T) {
	// SplitN(..., 2) means only the first " prompt=" is split on;
	// any later "prompt=" occurrences remain part of the prompt value.
	input := "role=x method=y confidence=z prompt=set prompt=value"
	_, _, _, prompt := audit.ParseRouteDetail(input)
	if !strings.HasPrefix(prompt, "set prompt=") {
		t.Errorf("prompt should preserve inner 'prompt=', got %q", prompt)
	}
}

// ---- Log tests ----

func TestLog_EmptyDBPath_NoPanic(t *testing.T) {
	// Log with an empty dbPath must return immediately without panicking
	// or sending anything to Chan.
	before := len(audit.Chan)
	audit.Log("", "action", "source", "detail", "127.0.0.1")
	after := len(audit.Chan)
	if after != before {
		t.Errorf("Chan length changed: before=%d after=%d; Log with empty dbPath should not enqueue",
			before, after)
	}
}

func TestLog_NonEmptyDBPath_SendsToChannel(t *testing.T) {
	// Drain any pre-existing entries from Chan first.
	drainChan()

	audit.Log("/fake/db.sqlite", "test.action", "testsource", "some detail", "10.0.0.1")

	// Log is synchronous in its select path, so no real sleep is needed.
	// The short poll below is purely defensive.
	var queued bool
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(audit.Chan) > 0 {
			queued = true
			break
		}
		time.Sleep(time.Millisecond)
	}

	if !queued {
		t.Error("expected Log to enqueue an entry in Chan, but Chan is empty")
	}

	drainChan()
}

// ---- Channel behavior: drop when full ----

func TestLog_ChannelFull_DoesNotBlock(t *testing.T) {
	// Fill Chan to capacity via Log calls.
	drainChan()
	fillChanViaLog()

	// The next Log call must hit the default branch and return immediately.
	done := make(chan struct{})
	go func() {
		defer close(done)
		audit.Log("/fake/db.sqlite", "overflow.action", "src", "dropped detail", "")
	}()

	select {
	case <-done:
		// Good — returned without blocking.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Log blocked when channel was full; expected immediate drop")
	}

	drainChan()
}

// ---- helpers ----

// drainChan discards all entries currently in Chan without blocking.
func drainChan() {
	for {
		select {
		case <-audit.Chan:
		default:
			return
		}
	}
}

// fillChanViaLog calls Log repeatedly until Chan reaches capacity.
// It uses a non-empty dbPath so Log actually enqueues (not the early-return path).
// Once Chan is full the select-default branch silently drops, so calling Log
// one more time is safe — we just stop when len == cap.
func fillChanViaLog() {
	for len(audit.Chan) < cap(audit.Chan) {
		audit.Log("/fake/db.sqlite", "fill.action", "fill", "fill", "")
	}
}
