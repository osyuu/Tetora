package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// --- Skill Failure Injection ---
//
// Records task failures to skill-specific failures.md files.
// On subsequent executions of the same skill, the failure context
// is injected into the prompt so the agent avoids repeating mistakes.

const (
	SkillFailuresFile     = "failures.md"
	SkillFailuresMaxCount = 5    // FIFO: keep only the most recent N entries
	SkillFailuresMaxChars = 500  // max chars per error message
	SkillFailuresMaxInject = 2048 // max chars to inject into prompt per skill
)

// AppendSkillFailure appends a failure entry to skills/<skillName>/failures.md.
// Maintains a FIFO of at most SkillFailuresMaxCount entries.
func AppendSkillFailure(cfg *AppConfig, skillName, taskTitle, agentName, errMsg string) {
	dir := filepath.Join(SkillsDir(cfg), skillName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logWarn("skill failure: mkdir failed", "skill", skillName, "error", err)
		return
	}

	fpath := filepath.Join(dir, SkillFailuresFile)

	// Truncate error message.
	if len(errMsg) > SkillFailuresMaxChars {
		errMsg = errMsg[:SkillFailuresMaxChars] + "..."
	}

	// Build new entry.
	ts := time.Now().UTC().Format(time.RFC3339)
	entry := fmt.Sprintf("## %s — %s (agent: %s)\n%s\n", ts, taskTitle, agentName, errMsg)

	// Read existing entries, parse, and prepend the new one.
	existing := ParseFailureEntries(fpath)
	entries := append([]string{entry}, existing...)
	if len(entries) > SkillFailuresMaxCount {
		entries = entries[:SkillFailuresMaxCount]
	}

	content := "# Skill Failures\n\n" + strings.Join(entries, "\n")
	if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
		logWarn("skill failure: write failed", "skill", skillName, "error", err)
	}
}

// ParseFailureEntries reads failures.md and splits into individual entries.
// Each entry starts with "## ".
func ParseFailureEntries(fpath string) []string {
	data, err := os.ReadFile(fpath)
	if err != nil {
		return nil
	}

	content := string(data)
	// Split on entry headers.
	parts := strings.Split(content, "\n## ")
	var entries []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "# Skill Failures") {
			continue
		}
		entries = append(entries, "## "+p+"\n")
	}
	return entries
}

// LoadSkillFailures reads the failures.md for a skill directory
// and returns the content (truncated to budget).
// Returns empty string if no failures file or empty.
func LoadSkillFailures(skillDir string) string {
	fpath := filepath.Join(skillDir, SkillFailuresFile)
	data, err := os.ReadFile(fpath)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" || content == "# Skill Failures" {
		return ""
	}
	if len(content) > SkillFailuresMaxInject {
		content = content[:SkillFailuresMaxInject] + "\n... (truncated)"
	}
	return content
}

// LoadSkillFailuresByName loads failures for a skill by name using the config's skills directory.
func LoadSkillFailuresByName(cfg *AppConfig, skillName string) string {
	return LoadSkillFailures(filepath.Join(SkillsDir(cfg), skillName))
}
