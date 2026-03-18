package main

import (
	"regexp"

	"tetora/internal/retention"
	"tetora/internal/session"
)

// RetentionResult is a type alias for use across the root package.
type RetentionResult = retention.Result

// ReflectionRow is a type alias for use across the root package.
type ReflectionRow = retention.ReflectionRow

// DataExport is a type alias for use across the root package.
type DataExport = retention.DataExport

func retentionHooks(cfg *Config) retention.Hooks {
	return retention.Hooks{
		CleanupSessions:     cleanupSessions,
		CleanupOldQueueItems: cleanupOldQueueItems,
		CleanupOutputs:      cleanupOutputs,
		ListMemory: func(workspaceDir string) ([]retention.MemoryEntry, error) {
			entries, err := listMemory(cfg, "")
			if err != nil {
				return nil, err
			}
			out := make([]retention.MemoryEntry, len(entries))
			for i, e := range entries {
				out[i] = retention.MemoryEntry{
					Key:       e.Key,
					Value:     e.Value,
					Priority:  e.Priority,
					UpdatedAt: e.UpdatedAt,
				}
			}
			return out, nil
		},
		QuerySessions: func(dbPath string, limit int) ([]session.Session, error) {
			sessions, _, err := querySessions(dbPath, SessionQuery{Limit: limit})
			return sessions, err
		},
		LoadMemoryAccessLog:    func(workspaceDir string) map[string]string { return loadMemoryAccessLog(cfg) },
		SaveMemoryAccessLog:    func(workspaceDir string, log map[string]string) { saveMemoryAccessLog(cfg, log) },
		ParseMemoryFrontmatter: parseMemoryFrontmatter,
		BuildMemoryFrontmatter: buildMemoryFrontmatter,
	}
}

func retentionDays(configured, fallback int) int {
	return retention.Days(configured, fallback)
}

func runRetention(cfg *Config) []RetentionResult {
	return retention.Run(cfg, retentionHooks(cfg))
}

func compilePIIPatterns(patterns []string) []*regexp.Regexp {
	return retention.CompilePIIPatterns(patterns)
}

func redactPII(text string, patterns []*regexp.Regexp) string {
	return retention.RedactPII(text, patterns)
}

func queryRetentionStats(dbPath string) map[string]int {
	return retention.QueryStats(dbPath)
}

func exportData(cfg *Config) ([]byte, error) {
	return retention.Export(cfg, retentionHooks(cfg))
}

func queryReflectionsForExport(dbPath string) []ReflectionRow {
	return retention.QueryReflectionsForExport(dbPath)
}

func purgeDataBefore(cfg *Config, before string) ([]RetentionResult, error) {
	return retention.PurgeBefore(cfg.HistoryDB, before)
}

func cleanupWorkflowRuns(dbPath string, days int) (int, error) {
	return retention.CleanupWorkflowRuns(dbPath, days)
}

func cleanupHandoffs(dbPath string, days int) (int, error) {
	return retention.CleanupHandoffs(dbPath, days)
}

func cleanupReflections(dbPath string, days int) (int, error) {
	return retention.CleanupReflections(dbPath, days)
}

func cleanupSLAChecks(dbPath string, days int) (int, error) {
	return retention.CleanupSLAChecks(dbPath, days)
}

func cleanupTrustEvents(dbPath string, days int) (int, error) {
	return retention.CleanupTrustEvents(dbPath, days)
}

func cleanupLogFiles(logDir string, days int) int {
	return retention.CleanupLogFiles(logDir, days)
}

func cleanupClaudeSessions(days int) int {
	return retention.CleanupClaudeSessions(days)
}

func cleanupStaleMemory(cfg *Config, days int) (int, error) {
	return retention.CleanupStaleMemory(cfg.WorkspaceDir, days, retentionHooks(cfg))
}
