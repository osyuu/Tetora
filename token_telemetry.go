package main

import "tetora/internal/telemetry"

// TokenTelemetryEntry holds the token breakdown for a single task execution.
type TokenTelemetryEntry = telemetry.Entry

// TokenSummaryRow is a parsed row from queryTokenUsageSummary.
type TokenSummaryRow = telemetry.SummaryRow

// TokenAgentRow is a parsed row from queryTokenUsageByRole.
type TokenAgentRow = telemetry.AgentRow

func initTokenTelemetry(dbPath string) error { return telemetry.Init(dbPath) }
func recordTokenTelemetry(dbPath string, entry TokenTelemetryEntry) {
	telemetry.Record(dbPath, entry)
}
func queryTokenUsageSummary(dbPath string, days int) ([]map[string]any, error) {
	return telemetry.QueryUsageSummary(dbPath, days)
}
func queryTokenUsageByRole(dbPath string, days int) ([]map[string]any, error) {
	return telemetry.QueryUsageByRole(dbPath, days)
}
func parseTokenSummaryRows(rows []map[string]any) []TokenSummaryRow {
	return telemetry.ParseSummaryRows(rows)
}
func parseTokenAgentRows(rows []map[string]any) []TokenAgentRow {
	return telemetry.ParseAgentRows(rows)
}
func formatTokenSummary(rows []TokenSummaryRow) string  { return telemetry.FormatSummary(rows) }
func formatTokenByRole(rows []TokenAgentRow) string     { return telemetry.FormatByRole(rows) }
