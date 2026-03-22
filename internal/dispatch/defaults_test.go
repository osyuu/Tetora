package dispatch

import (
	"testing"

	"tetora/internal/config"
)

func TestFillDefaults_EmptyModelWithEmptyGlobalDefault(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "",
		Agents:       map[string]config.AgentConfig{},
	}
	task := &Task{
		Prompt: "test prompt",
	}

	FillDefaults(cfg, task)

	if task.Model != DefaultFallbackModel {
		t.Errorf("expected fallback model %q, got %q", DefaultFallbackModel, task.Model)
	}
}

func TestFillDefaults_EmptyModelWithAgentNoModel(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "",
		Agents: map[string]config.AgentConfig{
			"kokuyou": {Model: ""},
		},
	}
	task := &Task{
		Agent:  "kokuyou",
		Prompt: "review spec",
	}

	FillDefaults(cfg, task)

	if task.Model != DefaultFallbackModel {
		t.Errorf("expected fallback model %q, got %q", DefaultFallbackModel, task.Model)
	}
}

func TestFillDefaults_ExplicitModelNotOverridden(t *testing.T) {
	cfg := &config.Config{
		DefaultModel: "claude-opus-4-6",
		Agents:       map[string]config.AgentConfig{},
	}
	task := &Task{
		Model:  "claude-haiku-4-5-20251001",
		Prompt: "quick check",
	}

	FillDefaults(cfg, task)

	if task.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("explicit model should not be overridden, got %q", task.Model)
	}
}
