// Package telemetry tracks detailed token usage breakdown per task for cost
// optimization analysis. Records system prompt, context, tool definition,
// input, and output token counts alongside cost and duration data.
package telemetry

import (
	"fmt"
	"strings"
	"time"

	"tetora/internal/db"
	tlog "tetora/internal/log"
)

// Entry holds the token breakdown for a single task execution.
type Entry struct {
	TaskID             string
	Agent              string
	Complexity         string
	Provider           string
	Model              string
	SystemPromptTokens int
	ContextTokens      int
	ToolDefsTokens     int
	InputTokens        int
	OutputTokens       int
	CostUSD            float64
	DurationMs         int64
	Source             string
	CreatedAt          string
}

// Init creates the token_telemetry table if it doesn't exist.
func Init(dbPath string) error {
	if dbPath == "" {
		return nil
	}
	// Migration: rename role -> agent in token_telemetry.
	_ = db.Exec(dbPath, `ALTER TABLE token_telemetry RENAME COLUMN role TO agent;`)

	sql := `CREATE TABLE IF NOT EXISTS token_telemetry (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT,
		agent TEXT,
		complexity TEXT,
		provider TEXT,
		model TEXT,
		system_prompt_tokens INTEGER DEFAULT 0,
		context_tokens INTEGER DEFAULT 0,
		tool_defs_tokens INTEGER DEFAULT 0,
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		cost_usd REAL DEFAULT 0,
		duration_ms INTEGER DEFAULT 0,
		source TEXT,
		created_at TEXT
	);`
	return db.Exec(dbPath, sql)
}

// Record stores token usage data for a completed task.
// Called asynchronously (goroutine) to avoid blocking task execution.
func Record(dbPath string, entry Entry) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT INTO token_telemetry
			(task_id, agent, complexity, provider, model, system_prompt_tokens, context_tokens,
			 tool_defs_tokens, input_tokens, output_tokens, cost_usd, duration_ms, source, created_at)
		 VALUES ('%s', '%s', '%s', '%s', '%s', %d, %d, %d, %d, %d, %.6f, %d, '%s', '%s');`,
		db.Escape(entry.TaskID),
		db.Escape(entry.Agent),
		db.Escape(entry.Complexity),
		db.Escape(entry.Provider),
		db.Escape(entry.Model),
		entry.SystemPromptTokens,
		entry.ContextTokens,
		entry.ToolDefsTokens,
		entry.InputTokens,
		entry.OutputTokens,
		entry.CostUSD,
		entry.DurationMs,
		db.Escape(entry.Source),
		db.Escape(entry.CreatedAt),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		tlog.Warn("record token telemetry failed", "error", err, "taskId", entry.TaskID)
	}
}

// QueryUsageSummary returns a summary of token usage grouped by complexity.
func QueryUsageSummary(dbPath string, days int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT
			complexity,
			COUNT(*) as request_count,
			COALESCE(SUM(system_prompt_tokens), 0) as total_system_prompt,
			COALESCE(SUM(context_tokens), 0) as total_context,
			COALESCE(SUM(tool_defs_tokens), 0) as total_tool_defs,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(output_tokens), 0) as total_output,
			COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(AVG(input_tokens), 0) as avg_input,
			COALESCE(AVG(output_tokens), 0) as avg_output
		 FROM token_telemetry
		 WHERE date(created_at) >= '%s'
		 GROUP BY complexity
		 ORDER BY total_cost DESC;`, since)
	return db.Query(dbPath, sql)
}

// QueryUsageByRole returns token usage grouped by agent and complexity.
func QueryUsageByRole(dbPath string, days int) ([]map[string]any, error) {
	if dbPath == "" {
		return nil, nil
	}
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT
			CASE WHEN agent = '' THEN '(unassigned)' ELSE agent END as agent,
			complexity,
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens), 0) as total_input,
			COALESCE(SUM(output_tokens), 0) as total_output,
			COALESCE(SUM(cost_usd), 0) as total_cost
		 FROM token_telemetry
		 WHERE date(created_at) >= '%s'
		 GROUP BY agent, complexity
		 ORDER BY total_cost DESC;`, since)
	return db.Query(dbPath, sql)
}

// --- Summary Types (for CLI + API) ---

// SummaryRow is a parsed row from QueryUsageSummary.
type SummaryRow struct {
	Complexity        string  `json:"complexity"`
	RequestCount      int     `json:"requestCount"`
	TotalSystemPrompt int     `json:"totalSystemPrompt"`
	TotalContext      int     `json:"totalContext"`
	TotalToolDefs     int     `json:"totalToolDefs"`
	TotalInput        int     `json:"totalInput"`
	TotalOutput       int     `json:"totalOutput"`
	TotalCost         float64 `json:"totalCost"`
	AvgInput          int     `json:"avgInput"`
	AvgOutput         int     `json:"avgOutput"`
}

// AgentRow is a parsed row from QueryUsageByRole.
type AgentRow struct {
	Agent        string  `json:"agent"`
	Complexity   string  `json:"complexity"`
	RequestCount int     `json:"requestCount"`
	TotalInput   int     `json:"totalInput"`
	TotalOutput  int     `json:"totalOutput"`
	TotalCost    float64 `json:"totalCost"`
}

// ParseSummaryRows converts raw DB rows to typed structs.
func ParseSummaryRows(rows []map[string]any) []SummaryRow {
	var result []SummaryRow
	for _, row := range rows {
		result = append(result, SummaryRow{
			Complexity:        db.Str(row["complexity"]),
			RequestCount:      db.Int(row["request_count"]),
			TotalSystemPrompt: db.Int(row["total_system_prompt"]),
			TotalContext:      db.Int(row["total_context"]),
			TotalToolDefs:     db.Int(row["total_tool_defs"]),
			TotalInput:        db.Int(row["total_input"]),
			TotalOutput:       db.Int(row["total_output"]),
			TotalCost:         db.Float(row["total_cost"]),
			AvgInput:          db.Int(row["avg_input"]),
			AvgOutput:         db.Int(row["avg_output"]),
		})
	}
	return result
}

// ParseAgentRows converts raw DB rows to typed structs.
func ParseAgentRows(rows []map[string]any) []AgentRow {
	var result []AgentRow
	for _, row := range rows {
		result = append(result, AgentRow{
			Agent:        db.Str(row["agent"]),
			Complexity:   db.Str(row["complexity"]),
			RequestCount: db.Int(row["request_count"]),
			TotalInput:   db.Int(row["total_input"]),
			TotalOutput:  db.Int(row["total_output"]),
			TotalCost:    db.Float(row["total_cost"]),
		})
	}
	return result
}

// FormatSummary formats token telemetry summary for CLI display.
func FormatSummary(rows []SummaryRow) string {
	if len(rows) == 0 {
		return "  (no data)"
	}

	lines := []string{
		fmt.Sprintf("  %-12s %6s %10s %10s %10s %10s",
			"Complexity", "Reqs", "Avg In", "Avg Out", "Total Cost", "Sys Prompt"),
		fmt.Sprintf("  %s", "--------------------------------------------------------------------"),
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %-12s %6d %10d %10d $%9.4f %10d",
			r.Complexity, r.RequestCount, r.AvgInput, r.AvgOutput, r.TotalCost, r.TotalSystemPrompt))
	}
	return strings.Join(lines, "\n")
}

// FormatByRole formats token usage by agent for CLI display.
func FormatByRole(rows []AgentRow) string {
	if len(rows) == 0 {
		return "  (no data)"
	}

	lines := []string{
		fmt.Sprintf("  %-15s %-12s %6s %10s %10s %10s",
			"Agent", "Complexity", "Reqs", "Total In", "Total Out", "Cost"),
		fmt.Sprintf("  %s", "-------------------------------------------------------------------"),
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %-15s %-12s %6d %10d %10d $%9.4f",
			db.Truncate(r.Agent, 15), r.Complexity, r.RequestCount, r.TotalInput, r.TotalOutput, r.TotalCost))
	}
	return strings.Join(lines, "\n")
}
