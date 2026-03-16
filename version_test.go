package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupVersionTestDB is a helper used by tests that exercise root-level wrappers
// or functions that depend on root package types (Workflow, Config, etc.).
func setupVersionTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initVersionDB(dbPath); err != nil {
		t.Fatalf("initVersionDB: %v", err)
	}
	return dbPath
}

// TestHandleConfigVersionSubcommands verifies the root-level CLI dispatch
// function that depends on root types (Config, etc.).
func TestHandleConfigVersionSubcommands(t *testing.T) {
	// Just test that unknown actions return false.
	if handleConfigVersionSubcommands("unknown-action", nil) {
		t.Error("unknown action should return false")
	}
}

func TestHandleWorkflowVersionSubcommands(t *testing.T) {
	if handleWorkflowVersionSubcommands("unknown-action", nil) {
		t.Error("unknown action should return false")
	}
}

// TestRestoreWorkflowVersion exercises restoreWorkflowVersion, which stays in
// the root package because it depends on Workflow, Config, loadWorkflowByName,
// and saveWorkflow.
func TestRestoreWorkflowVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	initVersionDB(dbPath)

	cfg := &Config{
		BaseDir:   dir,
		HistoryDB: dbPath,
	}

	// Create workflow dir.
	os.MkdirAll(filepath.Join(dir, "workflows"), 0o755)

	// Write initial workflow.
	wf1 := &Workflow{Name: "test-wf", Steps: []WorkflowStep{{ID: "s1", Prompt: "v1"}}}
	saveWorkflow(cfg, wf1)

	// Get v1 ID.
	versions, _ := queryVersions(dbPath, "workflow", "test-wf", 10)
	if len(versions) == 0 {
		t.Fatal("no workflow versions")
	}
	v1ID := versions[0].VersionID

	// Update workflow.
	wf2 := &Workflow{Name: "test-wf", Steps: []WorkflowStep{{ID: "s1", Prompt: "v2"}, {ID: "s2", Prompt: "new"}}}
	saveWorkflow(cfg, wf2)

	// Restore to v1.
	if err := restoreWorkflowVersion(dbPath, cfg, v1ID); err != nil {
		t.Fatalf("restoreWorkflowVersion: %v", err)
	}

	// Verify restored content.
	restored, err := loadWorkflowByName(cfg, "test-wf")
	if err != nil {
		t.Fatalf("loadWorkflowByName: %v", err)
	}
	if len(restored.Steps) != 1 {
		t.Errorf("expected 1 step after restore, got %d", len(restored.Steps))
	}
	if restored.Steps[0].Prompt != "v1" {
		t.Errorf("prompt: got %q, want %q", restored.Steps[0].Prompt, "v1")
	}
}

// TestRestoreConfigVersionInvalidType is kept here because it uses the root
// wrapper, which exercises the full call path including the type alias.
func TestRestoreConfigVersionInvalidType(t *testing.T) {
	dbPath := setupVersionTestDB(t)

	snapshotEntity(dbPath, "workflow", "my-wf", `{"name":"my-wf"}`, "test", "")
	versions, _ := queryVersions(dbPath, "workflow", "my-wf", 10)
	vid := versions[0].VersionID

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{}`), 0o644)

	_, err := restoreConfigVersion(dbPath, configPath, vid)
	if err == nil {
		t.Error("expected error for wrong entity type")
	}
	if !strings.Contains(err.Error(), "not a config") {
		t.Errorf("error should mention type mismatch: %v", err)
	}
}

// TestSnapshotEntityEmptyDB verifies the empty-dbPath short-circuit through
// the root wrappers (snapshotConfig, snapshotWorkflow, snapshotPrompt).
func TestSnapshotEntityEmptyDB(t *testing.T) {
	if err := snapshotConfig("", "/nonexistent/config.json", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
	if err := snapshotWorkflow("", "wf", "{}", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
	if err := snapshotPrompt("", "prompt", "hello", "test", ""); err != nil {
		t.Errorf("expected nil error for empty dbPath, got %v", err)
	}
}
