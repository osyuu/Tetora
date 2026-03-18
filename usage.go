package main

import (
	"fmt"
	"strings"

	"tetora/internal/usage"
)

// --- Type Aliases ---

type UsageSummary = usage.UsageSummary
type ModelUsage = usage.ModelUsage
type AgentUsage = usage.AgentUsage
type ExpensiveSession = usage.ExpensiveSession
type DayUsage = usage.DayUsage

// --- Query Functions ---

func queryUsageSummary(dbPath, period string) (*UsageSummary, error) {
	return usage.QuerySummary(dbPath, period)
}

func queryUsageByModel(dbPath string, days int) ([]ModelUsage, error) {
	return usage.QueryByModel(dbPath, days)
}

func queryUsageByAgent(dbPath string, days int) ([]AgentUsage, error) {
	return usage.QueryByAgent(dbPath, days)
}

func queryExpensiveSessions(dbPath string, limit, days int) ([]ExpensiveSession, error) {
	return usage.QueryExpensiveSessions(dbPath, limit, days)
}

func queryCostTrend(dbPath string, days int) ([]DayUsage, error) {
	return usage.QueryCostTrend(dbPath, days)
}

// --- Format Functions ---

func formatUsageSummary(summary *UsageSummary) string {
	return usage.FormatSummary(summary)
}

func formatModelBreakdown(models []ModelUsage) string {
	return usage.FormatModelBreakdown(models)
}

func formatAgentBreakdown(roles []AgentUsage) string {
	return usage.FormatAgentBreakdown(roles)
}

// --- Cost Footer ---

// formatResponseCostFooter returns a cost footer string for channel responses.
func formatResponseCostFooter(cfg *Config, result *ProviderResult) string {
	if cfg == nil || !cfg.Usage.ShowFooter || result == nil {
		return ""
	}

	tmpl := cfg.Usage.FooterTemplate
	if tmpl == "" {
		tmpl = "{{.tokensIn}}in/{{.tokensOut}}out ~${{.cost}}"
	}

	footer := tmpl
	footer = strings.ReplaceAll(footer, "{{.tokensIn}}", fmt.Sprintf("%d", result.TokensIn))
	footer = strings.ReplaceAll(footer, "{{.tokensOut}}", fmt.Sprintf("%d", result.TokensOut))
	footer = strings.ReplaceAll(footer, "{{.cost}}", fmt.Sprintf("%.4f", result.CostUSD))

	return footer
}

// formatResultCostFooter returns a cost footer string from a TaskResult.
func formatResultCostFooter(cfg *Config, result *TaskResult) string {
	if cfg == nil || !cfg.Usage.ShowFooter || result == nil {
		return ""
	}
	pr := &ProviderResult{
		TokensIn:  result.TokensIn,
		TokensOut: result.TokensOut,
		CostUSD:   result.CostUSD,
	}
	return formatResponseCostFooter(cfg, pr)
}
