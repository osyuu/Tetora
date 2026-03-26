package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
	"tetora/internal/history"
	"tetora/internal/log"
	"tetora/internal/session"
	"tetora/internal/skill"
)

// BuildDigest assembles a structured markdown digest of recent activity
// for LLM analysis. days controls the lookback window (capped at 90).
func BuildDigest(cfg *config.Config, days int) string {
	if cfg == nil || cfg.HistoryDB == "" {
		return "(Review digest unavailable: no history database configured)"
	}

	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}

	cutoff := time.Now().AddDate(0, 0, -days).Format(time.RFC3339)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Activity Digest (last %d days)\n\n", days))

	// Section 1: Recent Conversations.
	b.WriteString("## Recent Conversations\n\n")
	msgs := queryMessages(cfg.HistoryDB, cutoff, 200)
	if len(msgs) == 0 {
		b.WriteString("(none in this period)\n\n")
	} else {
		for _, m := range msgs {
			content := m.Content
			if len(content) > 300 {
				content = content[:300] + "..."
			}
			content = strings.ReplaceAll(content, "\n", " ")
			date := m.CreatedAt
			if len(date) > 10 {
				date = date[:10]
			}
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", date, m.Role, content))
		}
		b.WriteString("\n")
	}

	// Section 2: Recent Reflections.
	b.WriteString("## Recent Reflections\n\n")
	refs := queryReflections(cfg.HistoryDB, cutoff, 50)
	if len(refs) == 0 {
		b.WriteString("(none in this period)\n\n")
	} else {
		for _, r := range refs {
			b.WriteString(fmt.Sprintf("- Score: %d/5 | Role: %s | %s | Improve: %s\n",
				r.Score, r.Agent, r.Feedback, r.Improvement))
		}
		b.WriteString("\n")
	}

	// Section 3: Recent Job Runs (failures detailed, successes counted).
	b.WriteString("## Recent Job Runs\n\n")
	runs := queryJobRuns(cfg.HistoryDB, cutoff, 50)
	if len(runs) == 0 {
		b.WriteString("(none in this period)\n\n")
	} else {
		successCount := 0
		var failures []history.JobRun
		for _, r := range runs {
			if r.Status == "success" {
				successCount++
			} else {
				failures = append(failures, r)
			}
		}
		b.WriteString(fmt.Sprintf("Successful runs: %d\n", successCount))
		if len(failures) == 0 {
			b.WriteString("Failed runs: 0\n\n")
		} else {
			b.WriteString(fmt.Sprintf("Failed runs: %d\n", len(failures)))
			for _, f := range failures {
				errMsg := f.Error
				if len(errMsg) > 200 {
					errMsg = errMsg[:200] + "..."
				}
				b.WriteString(fmt.Sprintf("- [%s] %s (%s): %s — %s\n",
					f.StartedAt[:10], f.Name, f.JobID, f.Status, errMsg))
			}
			b.WriteString("\n")
		}
	}

	// Section 4: Existing Skills.
	b.WriteString("## Existing Skills\n\n")
	skillCfg := &skill.AppConfig{
		Skills:       cfg.Skills,
		SkillStore:   cfg.SkillStore,
		WorkspaceDir: cfg.WorkspaceDir,
		HistoryDB:    cfg.HistoryDB,
		BaseDir:      cfg.BaseDir,
	}
	fileMetas := skill.LoadAllFileSkillMetas(skillCfg)
	configSkills := cfg.Skills
	if len(fileMetas) == 0 && len(configSkills) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, m := range fileMetas {
			b.WriteString(fmt.Sprintf("- %s: %s\n", m.Name, m.Description))
		}
		for _, s := range configSkills {
			b.WriteString(fmt.Sprintf("- %s (config): %s\n", s.Name, s.Description))
		}
		b.WriteString("\n")
	}

	// Section 5: Existing Rules.
	b.WriteString("## Existing Rules\n\n")
	rulesDir := filepath.Join(cfg.WorkspaceDir, "rules")
	if ruleEntries, err := os.ReadDir(rulesDir); err != nil || len(ruleEntries) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, e := range ruleEntries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			excerpt := ""
			if data, err := os.ReadFile(filepath.Join(rulesDir, e.Name())); err == nil {
				excerpt = string(data)
				if len(excerpt) > 200 {
					excerpt = excerpt[:200] + "..."
				}
				excerpt = strings.ReplaceAll(excerpt, "\n", " ")
			}
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", name, excerpt))
		}
		b.WriteString("\n")
	}

	// Section 6: Memory Status (with priority + last access).
	b.WriteString("## Memory Status\n\n")
	memories, err := listMemory(cfg)
	accessLog := loadMemoryAccessLog(cfg)
	now := time.Now()
	if err != nil || len(memories) == 0 {
		b.WriteString("(none)\n\n")
	} else {
		for _, m := range memories {
			lastAccess := "never"
			staleWarning := ""
			if ts, ok := accessLog[m.Key]; ok {
				lastAccess = ts
				if len(lastAccess) > 10 {
					lastAccess = lastAccess[:10]
				}
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					ageDays := int(now.Sub(t).Hours() / 24)
					if ageDays > 30 && m.Priority != "P0" {
						staleWarning = fmt.Sprintf(" — stale (%d days)", ageDays)
					}
				}
			}
			b.WriteString(fmt.Sprintf("- %s (%s) — last accessed: %s%s\n", m.Key, m.Priority, lastAccess, staleWarning))
		}
		b.WriteString("\n")
	}

	// Archived memory count.
	archiveDir := filepath.Join(cfg.WorkspaceDir, "memory", "archive")
	if entries, err := os.ReadDir(archiveDir); err == nil {
		archiveCount := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				archiveCount++
			}
		}
		if archiveCount > 0 {
			b.WriteString(fmt.Sprintf("Archived entries: %d (in memory/archive/)\n\n", archiveCount))
		}
	}

	return b.String()
}

// --- Query helpers ---

// ReflectionRow holds the fields read from the reflections table.
type ReflectionRow struct {
	TaskID      string
	Agent       string
	Score       int
	Feedback    string
	Improvement string
	CostUSD     float64
	CreatedAt   string
}

func queryMessages(dbPath, cutoff string, limit int) []session.SessionMessage {
	sql := fmt.Sprintf(
		`SELECT id, session_id, role, content, cost_usd, tokens_in, tokens_out, model, task_id, created_at
		 FROM session_messages
		 WHERE created_at >= '%s' AND role IN ('user','assistant')
		 ORDER BY created_at DESC LIMIT %d`,
		db.Escape(cutoff), limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		log.Warn("review: query messages failed", "error", err)
		return nil
	}

	var msgs []session.SessionMessage
	for _, row := range rows {
		msgs = append(msgs, session.SessionMessageFromRow(row))
	}
	return msgs
}

func queryReflections(dbPath, cutoff string, limit int) []ReflectionRow {
	sql := fmt.Sprintf(
		`SELECT task_id, agent, score, feedback, improvement, cost_usd, created_at
		 FROM reflections
		 WHERE created_at >= '%s'
		 ORDER BY created_at DESC LIMIT %d`,
		db.Escape(cutoff), limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		log.Warn("review: query reflections failed", "error", err)
		return nil
	}

	var results []ReflectionRow
	for _, row := range rows {
		results = append(results, ReflectionRow{
			TaskID:      jsonStr(row["task_id"]),
			Agent:       jsonStr(row["agent"]),
			Score:       jsonInt(row["score"]),
			Feedback:    jsonStr(row["feedback"]),
			Improvement: jsonStr(row["improvement"]),
			CostUSD:     jsonFloat(row["cost_usd"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return results
}

func queryJobRuns(dbPath, cutoff string, limit int) []history.JobRun {
	sql := fmt.Sprintf(
		`SELECT id, job_id, name, source, started_at, finished_at, status, exit_code, cost_usd, output_summary, error, model, session_id, COALESCE(output_file,'') as output_file, COALESCE(tokens_in,0) as tokens_in, COALESCE(tokens_out,0) as tokens_out, COALESCE(agent,'') as agent
		 FROM job_runs
		 WHERE started_at >= '%s'
		 ORDER BY started_at DESC LIMIT %d`,
		db.Escape(cutoff), limit)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		log.Warn("review: query job runs failed", "error", err)
		return nil
	}

	var runs []history.JobRun
	for _, row := range rows {
		runs = append(runs, history.RunFromRow(row))
	}
	return runs
}

// --- Memory helpers ---

type memoryEntry struct {
	Key      string
	Priority string
}

func listMemory(cfg *config.Config) ([]memoryEntry, error) {
	dir := filepath.Join(cfg.WorkspaceDir, "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []memoryEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		priority := parseMemoryPriority(data)
		result = append(result, memoryEntry{Key: key, Priority: priority})
	}
	return result, nil
}

func loadMemoryAccessLog(cfg *config.Config) map[string]string {
	result := make(map[string]string)
	if cfg == nil || cfg.WorkspaceDir == "" {
		return result
	}
	path := filepath.Join(cfg.WorkspaceDir, "memory", ".access.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	_ = json.Unmarshal(data, &result)
	return result
}

func parseMemoryPriority(data []byte) string {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return "P1"
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return "P1"
	}
	front := s[4 : 4+end]
	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "priority:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "priority:"))
			if val == "P0" || val == "P1" || val == "P2" {
				return val
			}
		}
	}
	return "P1"
}

// --- JSON field helpers ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var i int
		fmt.Sscanf(x, "%d", &i)
		return i
	default:
		return 0
	}
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	default:
		return 0
	}
}
