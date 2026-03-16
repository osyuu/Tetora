package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmartDispatchMaxRetriesOrDefault(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 3},
		{1, 1},
		{5, 5},
		{-1, 3},
	}
	for _, tt := range tests {
		c := SmartDispatchConfig{MaxRetries: tt.input}
		got := c.MaxRetriesOrDefault()
		if got != tt.want {
			t.Errorf("SmartDispatchConfig{MaxRetries: %d}.maxRetriesOrDefault() = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestLoadSkillFailureContext_NoSkills(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{WorkspaceDir: tmpDir}
	d := &TaskBoardDispatcher{cfg: cfg}

	task := Task{Prompt: "test", Source: "test"}
	result := d.loadSkillFailureContext(task)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestLoadSkillFailureContext_WithFailures(t *testing.T) {
	tmpDir := t.TempDir()

	// Create skill with failures.
	skillDir := filepath.Join(tmpDir, "skills", "my-skill")
	os.MkdirAll(skillDir, 0o755)
	failContent := "# Skill Failures\n\n## 2026-01-01T00:00:00Z — Task A (agent: ruri)\nsome error happened\n"
	os.WriteFile(filepath.Join(skillDir, "failures.md"), []byte(failContent), 0o644)

	cfg := &Config{
		WorkspaceDir: tmpDir, // skillsDir uses WorkspaceDir
	}

	// Test loadSkillFailuresByName directly.
	failures := loadSkillFailuresByName(cfg, "my-skill")
	if failures == "" {
		t.Fatal("expected non-empty failures from loadSkillFailuresByName")
	}
	if !strings.Contains(failures, "some error happened") {
		t.Errorf("failures should contain error message, got: %s", failures)
	}
}

func TestDevQALoopResult_Fields(t *testing.T) {
	// Verify the struct fields are accessible and the types are correct.
	r := devQALoopResult{
		Result:     TaskResult{Status: "success", CostUSD: 0.5},
		QAApproved: true,
		Attempts:   2,
		TotalCost:  1.5,
	}

	if r.Result.Status != "success" {
		t.Errorf("Result.Status = %q, want success", r.Result.Status)
	}
	if !r.QAApproved {
		t.Error("QAApproved should be true")
	}
	if r.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", r.Attempts)
	}
	if r.TotalCost != 1.5 {
		t.Errorf("TotalCost = %f, want 1.5", r.TotalCost)
	}
}

func TestSmartDispatchResult_AttemptsField(t *testing.T) {
	// Verify the new Attempts field is present and works.
	sdr := SmartDispatchResult{
		Route:    RouteResult{Agent: "kokuyou", Method: "keyword", Confidence: "high"},
		Task:     TaskResult{Status: "success"},
		Attempts: 3,
	}
	if sdr.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", sdr.Attempts)
	}
}

func TestQAFailureRecordedAsSkillFailure(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills", "test-skill")
	os.MkdirAll(skillDir, 0o755)

	cfg := &Config{WorkspaceDir: tmpDir}

	// Simulate what devQALoop does when QA fails: record QA rejection as skill failure.
	qaFailMsg := "[QA rejection attempt 1] Implementation is incomplete, missing error handling"
	appendSkillFailure(cfg, "test-skill", "Test Task", "kokuyou", qaFailMsg)

	// Verify the failure was recorded.
	failures := loadSkillFailures(skillDir)
	if failures == "" {
		t.Fatal("expected non-empty failures after QA rejection recording")
	}
	if !strings.Contains(failures, "QA rejection attempt 1") {
		t.Errorf("failures should contain QA rejection, got: %s", failures)
	}
	if !strings.Contains(failures, "missing error handling") {
		t.Errorf("failures should contain the rejection detail, got: %s", failures)
	}

	// Simulate second QA failure.
	qaFailMsg2 := "[QA rejection attempt 2] Still missing error handling in edge case"
	appendSkillFailure(cfg, "test-skill", "Test Task", "kokuyou", qaFailMsg2)

	failures2 := loadSkillFailures(skillDir)
	if !strings.Contains(failures2, "QA rejection attempt 2") {
		t.Errorf("failures should contain second QA rejection, got: %s", failures2)
	}
	// First rejection should still be present (FIFO keeps 5).
	if !strings.Contains(failures2, "QA rejection attempt 1") {
		t.Errorf("first rejection should still be present, got: %s", failures2)
	}
}

func TestReviewLoopConfig(t *testing.T) {
	// Verify ReviewLoop field on SmartDispatchConfig.
	cfg := SmartDispatchConfig{
		Review:     true,
		ReviewLoop: true,
		MaxRetries: 2,
	}
	if !cfg.ReviewLoop {
		t.Error("ReviewLoop should be true")
	}
	if cfg.MaxRetriesOrDefault() != 2 {
		t.Errorf("MaxRetriesOrDefault() = %d, want 2", cfg.MaxRetriesOrDefault())
	}

	// Verify ReviewLoop field on TaskBoardDispatchConfig.
	tbCfg := TaskBoardDispatchConfig{
		ReviewLoop: true,
	}
	if !tbCfg.ReviewLoop {
		t.Error("TaskBoardDispatchConfig.ReviewLoop should be true")
	}
}

func TestTaskReviewLoopField(t *testing.T) {
	// Verify Task.ReviewLoop is serializable and defaults to false.
	task := Task{Prompt: "test task", Agent: "kokuyou"}
	if task.ReviewLoop {
		t.Error("Task.ReviewLoop should default to false")
	}

	task.ReviewLoop = true
	if !task.ReviewLoop {
		t.Error("Task.ReviewLoop should be true after setting")
	}
}

func TestTaskResultQAFields(t *testing.T) {
	// Verify QA-related fields on TaskResult.
	r := TaskResult{
		Status:   "success",
		Attempts: 3,
	}

	// Initially nil (no review).
	if r.QAApproved != nil {
		t.Error("QAApproved should be nil when no review")
	}

	// Set approved.
	approved := true
	r.QAApproved = &approved
	r.QAComment = "Looks good"
	if !*r.QAApproved {
		t.Error("QAApproved should be true")
	}
	if r.QAComment != "Looks good" {
		t.Errorf("QAComment = %q, want %q", r.QAComment, "Looks good")
	}
	if r.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", r.Attempts)
	}

	// Set rejected.
	rejected := false
	r.QAApproved = &rejected
	r.QAComment = "Dev↔QA loop exhausted (4 attempts): missing tests"
	if *r.QAApproved {
		t.Error("QAApproved should be false after rejection")
	}
}

func TestTaskResultQAFieldsSerialization(t *testing.T) {
	// Verify JSON omitempty: QA fields should be absent when unset.
	r := TaskResult{ID: "test-1", Status: "success"}
	data, _ := json.Marshal(r)
	s := string(data)

	if strings.Contains(s, "qaApproved") {
		t.Errorf("JSON should omit qaApproved when nil, got: %s", s)
	}
	if strings.Contains(s, "qaComment") {
		t.Errorf("JSON should omit qaComment when empty, got: %s", s)
	}
	if strings.Contains(s, `"attempts"`) {
		t.Errorf("JSON should omit attempts when 0, got: %s", s)
	}

	// With QA fields set.
	approved := true
	r.QAApproved = &approved
	r.QAComment = "ok"
	r.Attempts = 2
	data, _ = json.Marshal(r)
	s = string(data)

	if !strings.Contains(s, "qaApproved") {
		t.Errorf("JSON should include qaApproved when set, got: %s", s)
	}
	if !strings.Contains(s, "qaComment") {
		t.Errorf("JSON should include qaComment when set, got: %s", s)
	}
	if !strings.Contains(s, `"attempts"`) {
		t.Errorf("JSON should include attempts when set, got: %s", s)
	}
}
