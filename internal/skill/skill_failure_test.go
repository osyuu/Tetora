package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendSkillFailure(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &AppConfig{
		WorkspaceDir: tmpDir,
	}

	// First failure.
	AppendSkillFailure(cfg, "go-backend", "Fix login bug", "kokuyou", "connection refused")

	fpath := filepath.Join(tmpDir, "skills", "go-backend", SkillFailuresFile)
	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("expected failures.md to exist: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "# Skill Failures") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "Fix login bug") {
		t.Error("missing task title")
	}
	if !strings.Contains(content, "kokuyou") {
		t.Error("missing agent name")
	}
	if !strings.Contains(content, "connection refused") {
		t.Error("missing error message")
	}
}

func TestAppendSkillFailureFIFO(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &AppConfig{
		WorkspaceDir: tmpDir,
	}

	// Append more than max entries.
	for i := 0; i < SkillFailuresMaxCount+3; i++ {
		AppendSkillFailure(cfg, "test-skill", "Task "+string(rune('A'+i)), "agent", "error "+string(rune('A'+i)))
	}

	fpath := filepath.Join(tmpDir, "skills", "test-skill", SkillFailuresFile)
	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("expected failures.md to exist: %v", err)
	}
	content := string(data)

	// Count entries (## headers).
	entries := strings.Count(content, "\n## ")
	// The first entry also matches "## " at the start (after header line).
	if entries > SkillFailuresMaxCount {
		t.Errorf("expected at most %d entries, got %d", SkillFailuresMaxCount, entries)
	}

	// Most recent entry should be present (the last one appended).
	lastChar := rune('A' + SkillFailuresMaxCount + 2)
	if !strings.Contains(content, "Task "+string(lastChar)) {
		t.Errorf("most recent entry (Task %s) should be present", string(lastChar))
	}

	// Oldest entry should be gone.
	if strings.Contains(content, "Task A") {
		t.Error("oldest entry (Task A) should have been evicted")
	}
}

func TestAppendSkillFailureTruncatesError(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &AppConfig{
		WorkspaceDir: tmpDir,
	}

	longErr := strings.Repeat("x", SkillFailuresMaxChars+100)
	AppendSkillFailure(cfg, "test-skill", "Long error task", "agent", longErr)

	fpath := filepath.Join(tmpDir, "skills", "test-skill", SkillFailuresFile)
	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// The error should be truncated.
	if strings.Contains(content, longErr) {
		t.Error("error message should be truncated")
	}
	if !strings.Contains(content, "...") {
		t.Error("truncated error should end with ...")
	}
}

func TestLoadSkillFailuresEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	// No file.
	result := LoadSkillFailures(tmpDir)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}

	// Empty file.
	os.WriteFile(filepath.Join(tmpDir, SkillFailuresFile), []byte(""), 0o644)
	result = LoadSkillFailures(tmpDir)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}

	// Header only.
	os.WriteFile(filepath.Join(tmpDir, SkillFailuresFile), []byte("# Skill Failures"), 0o644)
	result = LoadSkillFailures(tmpDir)
	if result != "" {
		t.Errorf("expected empty for header-only, got %q", result)
	}
}

func TestLoadSkillFailuresContent(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &AppConfig{
		WorkspaceDir: tmpDir,
	}

	AppendSkillFailure(cfg, "my-skill", "Task A", "ruri", "some error")
	AppendSkillFailure(cfg, "my-skill", "Task B", "hisui", "another error")

	result := LoadSkillFailuresByName(cfg, "my-skill")
	if result == "" {
		t.Fatal("expected non-empty failures")
	}
	if !strings.Contains(result, "Task B") {
		t.Error("should contain most recent failure")
	}
	if !strings.Contains(result, "Task A") {
		t.Error("should contain older failure")
	}
}

func TestLoadSkillFailuresTruncatesLargeContent(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "large-skill")
	os.MkdirAll(skillDir, 0o755)

	// Write a large failures file.
	large := "# Skill Failures\n\n" + strings.Repeat("## 2026-01-01T00:00:00Z — Big task (agent: x)\n"+strings.Repeat("e", 300)+"\n\n", 20)
	os.WriteFile(filepath.Join(skillDir, SkillFailuresFile), []byte(large), 0o644)

	result := LoadSkillFailures(skillDir)
	if len(result) > SkillFailuresMaxInject+50 { // +50 for "... (truncated)"
		t.Errorf("result too large: %d chars", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("should indicate truncation")
	}
}

func TestParseFailureEntries(t *testing.T) {
	tmpDir := t.TempDir()
	fpath := filepath.Join(tmpDir, "failures.md")

	content := `# Skill Failures

## 2026-03-08T12:00:00Z — Task B (agent: hisui)
error B

## 2026-03-08T10:00:00Z — Task A (agent: ruri)
error A
`
	os.WriteFile(fpath, []byte(content), 0o644)

	entries := ParseFailureEntries(fpath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if !strings.Contains(entries[0], "Task B") {
		t.Error("first entry should be Task B")
	}
	if !strings.Contains(entries[1], "Task A") {
		t.Error("second entry should be Task A")
	}
}
