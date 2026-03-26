package proactive

import (
	"context"
	"testing"
	"time"

	"tetora/internal/config"
	"tetora/internal/dispatch"
)

func newEngineWithRules(rules []config.ProactiveRule, deps Deps) *Engine {
	cfg := &config.Config{
		Proactive: config.ProactiveConfig{
			Enabled: true,
			Rules:   rules,
		},
	}
	return New(cfg, nil, nil, nil, deps)
}

// TestThresholdExplicitCooldown verifies that a threshold rule with an explicit
// cooldown fires once and then blocks via CheckCooldown.
func TestThresholdExplicitCooldown(t *testing.T) {
	metricValue := 10.0 // above threshold of 5
	actionCalled := 0

	rule := config.ProactiveRule{
		Name:     "cost-alert",
		Cooldown: "10m",
		Trigger: config.ProactiveTrigger{
			Type:   "threshold",
			Metric: "daily_cost_usd",
			Op:     ">",
			Value:  5.0,
		},
		Action: config.ProactiveAction{
			Type:    "dispatch",
			Prompt:  "check cost",
			Agent:   "ruri",
		},
		Delivery: config.ProactiveDelivery{
			Channel: "dashboard",
		},
	}

	deps := Deps{
		RunTask: func(ctx context.Context, task dispatch.Task, sem, childSem chan struct{}, agentName string) dispatch.TaskResult {
			actionCalled++
			return dispatch.TaskResult{Status: "success", Output: "ok"}
		},
		RecordHistory: func(dbPath string, task dispatch.Task, result dispatch.TaskResult, agentName, startedAt, finishedAt, outputFile string) {
		},
		FillDefaults: func(cfg *config.Config, t *dispatch.Task) {},
	}

	e := newEngineWithRules([]config.ProactiveRule{rule}, deps)

	// Patch getMetricValue by directly calling executeAction (we test cooldown mechanics, not metric resolution).
	// Verify not in cooldown before first fire.
	if e.CheckCooldown(rule.Name) {
		t.Fatal("expected no cooldown before first trigger")
	}

	// Manually call executeAction (same path as checkThresholdRules would take).
	_ = e.executeAction(context.Background(), rule)

	// Verify cooldown is now active.
	if !e.CheckCooldown(rule.Name) {
		t.Error("expected cooldown to be active after executeAction")
	}

	// Verify the stored duration matches the explicit cooldown.
	e.mu.RLock()
	entry, ok := e.cooldowns[rule.Name]
	e.mu.RUnlock()

	if !ok {
		t.Fatal("expected cooldown entry to exist")
	}

	want := 10 * time.Minute
	if entry.duration != want {
		t.Errorf("expected cooldown duration %v, got %v", want, entry.duration)
	}

	// Verify the metric check respects the cooldown (simulate second check pass).
	if e.CompareThreshold(metricValue, rule.Trigger.Op, rule.Trigger.Value) {
		// Would fire — but CheckCooldown should block it.
		if e.CheckCooldown(rule.Name) {
			// Correct: rule is blocked.
		} else {
			t.Error("rule should be in cooldown but CheckCooldown returned false")
		}
	}
}

// TestHeartbeatCooldownSetToInterval verifies that a heartbeat rule without an
// explicit cooldown gets a cooldown equal to its Trigger.Interval after firing.
func TestHeartbeatCooldownSetToInterval(t *testing.T) {
	rule := config.ProactiveRule{
		Name: "heartbeat-check",
		// No Cooldown field — engine should derive it from Trigger.Interval.
		Trigger: config.ProactiveTrigger{
			Type:     "heartbeat",
			Interval: "5m",
		},
		Action: config.ProactiveAction{
			Type:   "notify",
			Message: "heartbeat ping",
		},
		Delivery: config.ProactiveDelivery{
			Channel: "dashboard",
		},
	}

	e := newEngineWithRules([]config.ProactiveRule{rule}, Deps{})

	if e.CheckCooldown(rule.Name) {
		t.Fatal("expected no cooldown before first trigger")
	}

	_ = e.executeAction(context.Background(), rule)

	// Cooldown should now be set.
	if !e.CheckCooldown(rule.Name) {
		t.Error("expected cooldown to be active after heartbeat executeAction")
	}

	e.mu.RLock()
	entry, ok := e.cooldowns[rule.Name]
	e.mu.RUnlock()

	if !ok {
		t.Fatal("expected cooldown entry to exist after heartbeat trigger")
	}

	want := 5 * time.Minute
	if entry.duration != want {
		t.Errorf("expected heartbeat cooldown duration %v (from Interval), got %v", want, entry.duration)
	}
}
