package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- NotebookLM Skill Tests ---

func TestNotebookLMImport_NoRelay(t *testing.T) {
	cfg := &AppConfig{Browser: nil}

	input, _ := json.Marshal(map[string]any{
		"notebook_url": "https://notebooklm.google.com/notebook/abc",
		"urls":         []string{"https://example.com"},
	})
	_, err := ToolNotebookLMImport(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when relay is nil")
	}
	if !strings.Contains(err.Error(), "browser extension not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMListSources_NoRelay(t *testing.T) {
	cfg := &AppConfig{Browser: nil}

	_, err := ToolNotebookLMListSources(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when relay is nil")
	}
	if !strings.Contains(err.Error(), "browser extension not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMQuery_NoRelay(t *testing.T) {
	cfg := &AppConfig{Browser: nil}

	input, _ := json.Marshal(map[string]any{
		"question": "What is the summary?",
	})
	_, err := ToolNotebookLMQuery(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when relay is nil")
	}
	if !strings.Contains(err.Error(), "browser extension not connected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMQuery_EmptyQuestion(t *testing.T) {
	// Empty question is validated before the browser relay check.
	cfg := &AppConfig{Browser: nil}

	input, _ := json.Marshal(map[string]any{
		"question": "",
	})
	_, err := ToolNotebookLMQuery(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty question")
	}
	if !strings.Contains(err.Error(), "question is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMDeleteSource_NoArgs(t *testing.T) {
	// Missing source args are validated before the browser relay check.
	cfg := &AppConfig{Browser: nil}

	input, _ := json.Marshal(map[string]any{})
	_, err := ToolNotebookLMDeleteSource(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when neither source_name nor source_id provided")
	}
	if !strings.Contains(err.Error(), "source_name or source_id is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMImport_NoURLs(t *testing.T) {
	// Empty urls validated before relay check.
	cfg := &AppConfig{Browser: nil}

	input, _ := json.Marshal(map[string]any{
		"notebook_url": "https://notebooklm.google.com/notebook/abc",
		"urls":         []string{},
	})
	_, err := ToolNotebookLMImport(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty urls")
	}
	if !strings.Contains(err.Error(), "urls is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNotebookLMImport_NoNotebookURL(t *testing.T) {
	// Empty notebook_url validated before relay check.
	cfg := &AppConfig{Browser: nil}

	input, _ := json.Marshal(map[string]any{
		"notebook_url": "",
		"urls":         []string{"https://example.com"},
	})
	_, err := ToolNotebookLMImport(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for empty notebook_url")
	}
	if !strings.Contains(err.Error(), "notebook_url is required") {
		t.Errorf("unexpected error: %v", err)
	}
}
