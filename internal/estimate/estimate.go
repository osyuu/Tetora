// Package estimate provides pure computation helpers for cost estimation.
// It has no dependency on root package types or *Config.
package estimate

import (
	"fmt"
	"strings"

	"tetora/internal/db"
)

// ModelPricing defines per-model pricing rates.
type ModelPricing struct {
	Model           string  `json:"model"`
	InputPer1M      float64 `json:"inputPer1M"`               // USD per 1M input tokens
	OutputPer1M     float64 `json:"outputPer1M"`              // USD per 1M output tokens
	CacheReadPer1M  float64 `json:"cacheReadPer1M,omitempty"`  // USD per 1M cache read tokens
	CacheWritePer1M float64 `json:"cacheWritePer1M,omitempty"` // USD per 1M cache write tokens
}

// CostEstimate is the result for a single task estimation.
type CostEstimate struct {
	Name               string  `json:"name"`
	Provider           string  `json:"provider"`
	Model              string  `json:"model"`
	EstimatedCostUSD   float64 `json:"estimatedCostUsd"`
	EstimatedTokensIn  int     `json:"estimatedTokensIn"`
	EstimatedTokensOut int     `json:"estimatedTokensOut"`
	Breakdown          string  `json:"breakdown,omitempty"`
}

// EstimateResult is the full response for POST /dispatch/estimate.
type EstimateResult struct {
	Tasks              []CostEstimate `json:"tasks"`
	TotalEstimatedCost float64        `json:"totalEstimatedCostUsd"`
	ClassifyCost       float64        `json:"classifyCostUsd,omitempty"`
}

// DefaultPricing returns built-in pricing for well-known models.
func DefaultPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		// Claude models (cacheRead: 10% of input, cacheWrite: 125% of input)
		"opus":   {Model: "opus", InputPer1M: 15.00, OutputPer1M: 75.00, CacheReadPer1M: 1.50, CacheWritePer1M: 18.75},
		"sonnet": {Model: "sonnet", InputPer1M: 3.00, OutputPer1M: 15.00, CacheReadPer1M: 0.30, CacheWritePer1M: 3.75},
		"haiku":  {Model: "haiku", InputPer1M: 0.25, OutputPer1M: 1.25, CacheReadPer1M: 0.025, CacheWritePer1M: 0.3125},
		// OpenAI models
		"gpt-4o":      {Model: "gpt-4o", InputPer1M: 2.50, OutputPer1M: 10.00},
		"gpt-4o-mini": {Model: "gpt-4o-mini", InputPer1M: 0.15, OutputPer1M: 0.60},
		"gpt-4-turbo": {Model: "gpt-4-turbo", InputPer1M: 10.00, OutputPer1M: 30.00},
		"o1":          {Model: "o1", InputPer1M: 15.00, OutputPer1M: 60.00},
	}
}

// InputTokens estimates input tokens using the len/4 heuristic.
// For mixed content (English, CJK, code), this is accurate within ~20%.
func InputTokens(prompt, systemPrompt string) int {
	total := len(prompt) + len(systemPrompt)
	tokens := total / 4
	if tokens < 10 {
		tokens = 10
	}
	return tokens
}

// ContextWindow returns the context window size (in tokens) for known models.
func ContextWindow(model string) int {
	lm := strings.ToLower(model)
	switch {
	case strings.Contains(lm, "opus"):
		return 200000
	case strings.Contains(lm, "sonnet"):
		return 200000
	case strings.Contains(lm, "haiku"):
		return 200000
	case strings.Contains(lm, "gpt-4o"):
		return 128000
	case strings.Contains(lm, "gpt-4-turbo"):
		return 128000
	case strings.Contains(lm, "o1"):
		return 200000
	default:
		return 200000
	}
}

// ResolvePricing looks up pricing for a model.
// Chain: cfgPricing[exact] → cfgPricing[prefix] → defaults[exact] → defaults[prefix] → fallback.
func ResolvePricing(cfgPricing map[string]ModelPricing, model string) ModelPricing {
	// Exact match in config.
	if cfgPricing != nil {
		if p, ok := cfgPricing[model]; ok {
			return p
		}
		// Prefix match in config.
		lm := strings.ToLower(model)
		for key, p := range cfgPricing {
			if strings.Contains(lm, strings.ToLower(key)) {
				return p
			}
		}
	}

	// Exact match in defaults.
	defaults := DefaultPricing()
	if p, ok := defaults[model]; ok {
		return p
	}

	// Prefix match in defaults (e.g., "claude-3-5-sonnet-20241022" matches "sonnet").
	lm := strings.ToLower(model)
	for key, p := range defaults {
		if strings.Contains(lm, strings.ToLower(key)) {
			return p
		}
	}

	// Fallback: GPT-4o rates.
	return ModelPricing{Model: model, InputPer1M: 2.50, OutputPer1M: 10.00}
}

// QueryModelAvgOutput returns the average output tokens for a model from the history DB.
// Uses the last 10 successful runs with that model that have tokens_out > 0.
func QueryModelAvgOutput(dbPath, model string) int {
	if dbPath == "" || model == "" {
		return 0
	}
	sql := fmt.Sprintf(
		`SELECT COALESCE(AVG(tokens_out), 0) as avg_out
		 FROM (SELECT tokens_out FROM job_runs
		       WHERE model = '%s' AND status = 'success' AND tokens_out > 0
		       ORDER BY id DESC LIMIT 10)`,
		db.Escape(model))
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return db.Int(rows[0]["avg_out"])
}
