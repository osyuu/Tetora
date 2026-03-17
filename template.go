package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tetora/internal/history"
)

// expandPrompt replaces template variables in a prompt string.
// Supported variables:
//
//	{{date}}          — current date (YYYY-MM-DD)
//	{{datetime}}      — current datetime (RFC3339)
//	{{weekday}}       — day of week (Monday, Tuesday, ...)
//	{{last_output}}   — output summary from the last run of this job
//	{{last_status}}   — status from the last run of this job
//	{{last_error}}    — error from the last run of this job
//	{{env.KEY}}       — environment variable value
//	{{memory.KEY}}    — agent memory value for the current role
//	{{knowledge_dir}} — path to knowledge base directory
//	{{skill.NAME}}    — output of named skill execution
//	{{review.digest}} — activity digest for last 7 days (default)
//	{{review.digest:N}} — activity digest for last N days (1-90)
func expandPrompt(prompt, jobID, dbPath, agentName, knowledgeDir string, cfg *Config) string {
	if !strings.Contains(prompt, "{{") {
		return prompt
	}

	now := time.Now()

	// Static replacements.
	r := strings.NewReplacer(
		"{{date}}", now.Format("2006-01-02"),
		"{{datetime}}", now.Format(time.RFC3339),
		"{{weekday}}", now.Weekday().String(),
		"{{knowledge_dir}}", knowledgeDir,
	)
	prompt = r.Replace(prompt)

	// Last job run replacements (only if jobID + dbPath are available).
	if jobID != "" && dbPath != "" &&
		(strings.Contains(prompt, "{{last_output}}") ||
			strings.Contains(prompt, "{{last_status}}") ||
			strings.Contains(prompt, "{{last_error}}")) {

		last := history.QueryLastRun(dbPath, jobID)
		lastOutput := ""
		lastStatus := ""
		lastError := ""
		if last != nil {
			lastOutput = last.OutputSummary
			lastStatus = last.Status
			lastError = last.Error
		}

		r2 := strings.NewReplacer(
			"{{last_output}}", lastOutput,
			"{{last_status}}", lastStatus,
			"{{last_error}}", lastError,
		)
		prompt = r2.Replace(prompt)
	}

	// Environment variable replacements: {{env.KEY}}
	envRe := regexp.MustCompile(`\{\{env\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
	prompt = envRe.ReplaceAllStringFunc(prompt, func(match string) string {
		parts := envRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		return os.Getenv(parts[1])
	})

	// Agent memory replacements: {{memory.KEY}}
	if agentName != "" && cfg != nil {
		memRe := regexp.MustCompile(`\{\{memory\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		prompt = memRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := memRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			val, _ := getMemory(cfg, agentName, parts[1])
			if val != "" {
				recordMemoryAccess(cfg, parts[1])
			}
			return val
		})
	}

	// Rules file on-demand loading: {{rules.FILENAME}}
	if cfg != nil && strings.Contains(prompt, "{{rules.") {
		rulesRe := regexp.MustCompile(`\{\{rules\.([A-Za-z_][A-Za-z0-9_\-]*)\}\}`)
		prompt = rulesRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := rulesRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			path := filepath.Join(cfg.WorkspaceDir, "rules", parts[1]+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				return "(rule not found: " + parts[1] + ")"
			}
			return string(data)
		})
	}

	// Skill output replacements: {{skill.NAME}}
	if cfg != nil && strings.Contains(prompt, "{{skill.") {
		skillRe := regexp.MustCompile(`\{\{skill\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)
		prompt = skillRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := skillRe.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			skill := getSkill(cfg, parts[1])
			if skill == nil {
				return match
			}
			result, err := executeSkill(context.Background(), *skill, nil)
			if err != nil || result.Status != "success" {
				return "(skill error)"
			}
			return strings.TrimSpace(result.Output)
		})
	}

	// Review digest: {{review.digest}} or {{review.digest:N}}
	if cfg != nil && strings.Contains(prompt, "{{review.digest") {
		reviewRe := regexp.MustCompile(`\{\{review\.digest(?::(\d+))?\}\}`)
		prompt = reviewRe.ReplaceAllStringFunc(prompt, func(match string) string {
			parts := reviewRe.FindStringSubmatch(match)
			days := 7 // default
			if len(parts) >= 2 && parts[1] != "" {
				if d, err := strconv.Atoi(parts[1]); err == nil && d > 0 && d <= 90 {
					days = d
				}
			}
			return buildReviewDigest(cfg, days)
		})
	}

	return prompt
}

// PromptInfo represents a prompt template file.
type PromptInfo struct {
	Name    string `json:"name"`
	Preview string `json:"preview,omitempty"` // first ~100 chars
	Content string `json:"content,omitempty"` // full content (only when requested)
}

// promptsDir returns the prompts directory path, creating it if needed.
func promptsDir(cfg *Config) string {
	dir := filepath.Join(cfg.BaseDir, "prompts")
	os.MkdirAll(dir, 0o755)
	return dir
}

// listPrompts returns all .md files in the prompts directory.
func listPrompts(cfg *Config) ([]PromptInfo, error) {
	dir := promptsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var prompts []PromptInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		preview := ""
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil {
			preview = string(data)
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			// Collapse newlines for preview.
			preview = strings.ReplaceAll(preview, "\n", " ")
		}
		prompts = append(prompts, PromptInfo{Name: name, Preview: preview})
	}

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})
	return prompts, nil
}

// readPrompt reads a prompt file by name.
func readPrompt(cfg *Config, name string) (string, error) {
	path := filepath.Join(promptsDir(cfg), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt %q not found", name)
	}
	return string(data), nil
}

// writePrompt creates or updates a prompt file.
func writePrompt(cfg *Config, name, content string) error {
	if name == "" {
		return fmt.Errorf("prompt name is required")
	}
	// Sanitize name: only allow alphanumeric, hyphens, underscores.
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in prompt name (use a-z, 0-9, -, _)", string(r))
		}
	}
	path := filepath.Join(promptsDir(cfg), name+".md")
	return os.WriteFile(path, []byte(content), 0o644)
}

// deletePrompt removes a prompt file.
func deletePrompt(cfg *Config, name string) error {
	path := filepath.Join(promptsDir(cfg), name+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("prompt %q not found", name)
	}
	return os.Remove(path)
}

// resolvePromptFile reads a prompt from the prompts directory.
// Used by cron to resolve CronTaskConfig.PromptFile.
func resolvePromptFile(cfg *Config, promptFile string) (string, error) {
	if promptFile == "" {
		return "", nil
	}
	// Allow with or without .md extension.
	name := strings.TrimSuffix(promptFile, ".md")
	return readPrompt(cfg, name)
}
