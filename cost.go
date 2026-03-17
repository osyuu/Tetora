package main

import (
	"encoding/json"
	"fmt"
	"os"

	"tetora/internal/cost"
	"tetora/internal/estimate"
)

// Type aliases — root code continues to use bare names.
// BudgetConfig and AutoDowngradeConfig are aliased in config.go via internal/config.
type GlobalBudget = cost.GlobalBudget
type AgentBudget = cost.AgentBudget
type WorkflowBudget = cost.WorkflowBudget
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

// --- Cost Estimation Types (aliases to internal/estimate) ---
// ModelPricing is aliased in config.go via internal/config.

type CostEstimate = estimate.CostEstimate
type EstimateResult = estimate.EstimateResult

// estimateRequestTokens estimates the total input tokens for a provider request.
// Uses the len/4 heuristic for all text components.
func estimateRequestTokens(req ProviderRequest) int {
	total := len(req.Prompt)/4 + len(req.SystemPrompt)/4
	for _, m := range req.Messages {
		total += len(m.Content) / 4
	}
	for _, t := range req.Tools {
		total += (len(t.Name) + len(t.Description) + len(string(t.InputSchema))) / 4
	}
	if total < 10 {
		total = 10
	}
	return total
}

// compressMessages truncates old messages to reduce context window usage.
// Keeps the most recent keepRecent message pairs intact.
func compressMessages(messages []Message, keepRecent int) []Message {
	keepMsgs := keepRecent * 2
	if len(messages) <= keepMsgs {
		return messages
	}

	result := make([]Message, len(messages))
	compressEnd := len(messages) - keepMsgs

	for i, msg := range messages {
		if i < compressEnd && len(msg.Content) > 256 {
			// Replace large old messages with a compact summary.
			summary := fmt.Sprintf(`[{"type":"text","text":"[prior tool exchange, %d bytes compressed]"}]`, len(msg.Content))
			result[i] = Message{Role: msg.Role, Content: json.RawMessage(summary)}
		} else {
			result[i] = msg
		}
	}
	return result
}

// --- Cost Estimation ---

// estimateTaskCost estimates the cost of a single task without executing it.
func estimateTaskCost(cfg *Config, task Task, agentName string) CostEstimate {
	providerName := resolveProviderName(cfg, task, agentName)

	model := task.Model
	if model == "" {
		if pc, ok := cfg.Providers[providerName]; ok && pc.Model != "" {
			model = pc.Model
		}
	}
	if model == "" {
		model = cfg.DefaultModel
	}

	// Inject agent model if applicable.
	if agentName != "" {
		if rc, ok := cfg.Agents[agentName]; ok && rc.Model != "" {
			if task.Model == "" || task.Model == cfg.DefaultModel {
				model = rc.Model
			}
		}
	}

	// Estimate input tokens.
	tokensIn := estimate.InputTokens(task.Prompt, task.SystemPrompt)

	// Estimate output tokens from history, fallback to config default.
	tokensOut := estimate.QueryModelAvgOutput(cfg.HistoryDB, model)
	if tokensOut == 0 {
		tokensOut = cfg.Estimate.DefaultOutputTokensOrDefault()
	}

	pricing := estimate.ResolvePricing(cfg.Pricing, model)

	costUSD := float64(tokensIn)*pricing.InputPer1M/1_000_000 +
		float64(tokensOut)*pricing.OutputPer1M/1_000_000

	return CostEstimate{
		Name:               task.Name,
		Provider:           providerName,
		Model:              model,
		EstimatedCostUSD:   costUSD,
		EstimatedTokensIn:  tokensIn,
		EstimatedTokensOut: tokensOut,
		Breakdown: fmt.Sprintf("~%d in + ~%d out @ $%.2f/$%.2f per 1M",
			tokensIn, tokensOut, pricing.InputPer1M, pricing.OutputPer1M),
	}
}

// estimateTasks estimates cost for multiple tasks.
// If smart dispatch is enabled and tasks have no explicit agent, includes classification cost.
func estimateTasks(cfg *Config, tasks []Task) *EstimateResult {
	result := &EstimateResult{}

	for _, task := range tasks {
		fillDefaults(cfg, &task)
		agentName := task.Agent

		// If no agent and smart dispatch enabled, classification will happen.
		if agentName == "" && cfg.SmartDispatch.Enabled {
			// Estimate classification cost.
			classifyModel := cfg.DefaultModel
			if rc, ok := cfg.Agents[cfg.SmartDispatch.Coordinator]; ok && rc.Model != "" {
				classifyModel = rc.Model
			}
			classifyPricing := estimate.ResolvePricing(cfg.Pricing, classifyModel)
			// Classification prompt ~500 tokens in, ~50 tokens out.
			classifyCost := float64(500)*classifyPricing.InputPer1M/1_000_000 +
				float64(50)*classifyPricing.OutputPer1M/1_000_000
			result.ClassifyCost += classifyCost

			// Use keyword classification to guess likely agent (no LLM call).
			if kr := classifyByKeywords(cfg, task.Prompt); kr != nil {
				agentName = kr.Agent
			} else {
				agentName = cfg.SmartDispatch.DefaultAgent
			}
		}

		est := estimateTaskCost(cfg, task, agentName)
		result.Tasks = append(result.Tasks, est)
		result.TotalEstimatedCost += est.EstimatedCostUSD
	}

	result.TotalEstimatedCost += result.ClassifyCost
	return result
}
