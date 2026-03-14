package main

import "tetora/internal/audit"

// Type aliases so the rest of the codebase continues to compile unchanged.
type AuditEntry = audit.Entry
type RoutingHistoryEntry = audit.RoutingHistoryEntry
type AgentRoutingStats = audit.AgentRoutingStats

func startAuditWriter()                      { audit.StartWriter() }
func initAuditLog(dbPath string) error       { return audit.Init(dbPath) }
func auditLog(dbPath, action, source, detail, ip string) {
	audit.Log(dbPath, action, source, detail, ip)
}
func queryAuditLog(dbPath string, limit, offset int) ([]AuditEntry, int, error) {
	return audit.Query(dbPath, limit, offset)
}
func queryRoutingStats(dbPath string, limit int) ([]RoutingHistoryEntry, map[string]*AgentRoutingStats, error) {
	return audit.QueryRoutingStats(dbPath, limit)
}
func parseRouteDetail(detail string) (string, string, string, string) {
	return audit.ParseRouteDetail(detail)
}
func cleanupAuditLog(dbPath string, days int) error { return audit.Cleanup(dbPath, days) }
