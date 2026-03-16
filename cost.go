package main

import (
	"encoding/json"
	"fmt"
	"os"

	"tetora/internal/cost"
)

// Type aliases — root code continues to use bare names.
type BudgetConfig = cost.BudgetConfig
type GlobalBudget = cost.GlobalBudget
type AgentBudget = cost.AgentBudget
type WorkflowBudget = cost.WorkflowBudget
type AutoDowngradeConfig = cost.AutoDowngradeConfig
type DowngradeThreshold = cost.DowngradeThreshold
type BudgetCheckResult = cost.BudgetCheckResult
type BudgetStatus = cost.BudgetStatus
type BudgetMeter = cost.BudgetMeter
type AgentBudgetMeter = cost.AgentBudgetMeter
type budgetAlertTracker = cost.BudgetAlertTracker

func newBudgetAlertTracker() *budgetAlertTracker { return cost.NewBudgetAlertTracker() }

func querySpend(dbPath, role string) (daily, weekly, monthly float64) {
	return cost.QuerySpend(dbPath, role)
}

func queryWorkflowRunSpend(dbPath string, runID int) float64 {
	return cost.QueryWorkflowRunSpend(dbPath, runID)
}

func checkBudget(cfg *Config, agentName, workflowName string, workflowRunID int) *BudgetCheckResult {
	return cost.CheckBudget(cfg.Budgets, cfg.HistoryDB, agentName, workflowName, workflowRunID)
}

func resolveDowngradeModel(ad AutoDowngradeConfig, utilization float64) string {
	return cost.ResolveDowngradeModel(ad, utilization)
}

func queryBudgetStatus(cfg *Config) *BudgetStatus {
	return cost.QueryBudgetStatus(cfg.Budgets, cfg.HistoryDB)
}

func checkAndNotifyBudgetAlerts(cfg *Config, notifyFn func(string), tracker *budgetAlertTracker) {
	cost.CheckAndNotifyBudgetAlerts(cfg.Budgets, cfg.HistoryDB, notifyFn, tracker)
}

func checkPeriodAlert(notifyFn func(string), tracker *budgetAlertTracker, scope, period string, spend, limit float64) {
	cost.CheckPeriodAlert(notifyFn, tracker, scope, period, spend, limit)
}

func formatBudgetSummary(cfg *Config) string {
	return cost.FormatBudgetSummary(queryBudgetStatus(cfg))
}

// --- Kill Switch ---

// setBudgetPaused updates the budgets.paused field in config.json.
// Stays in root: performs raw config file I/O against the project config schema.
func setBudgetPaused(configPath string, paused bool) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Parse existing budgets.
	var budgets map[string]json.RawMessage
	if budgetsRaw, ok := raw["budgets"]; ok {
		json.Unmarshal(budgetsRaw, &budgets)
	}
	if budgets == nil {
		budgets = make(map[string]json.RawMessage)
	}

	pausedJSON, _ := json.Marshal(paused)
	budgets["paused"] = pausedJSON

	budgetsJSON, err := json.Marshal(budgets)
	if err != nil {
		return err
	}
	raw["budgets"] = budgetsJSON

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, append(out, '\n'), 0o644)
}
