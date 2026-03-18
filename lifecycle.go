package main

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/lifecycle"
	"tetora/internal/log"
)

// LifecycleEngine wraps the internal lifecycle engine for package main.
type LifecycleEngine struct {
	cfg    *Config
	engine *lifecycle.Engine
}

// globalLifecycleEngine is the singleton lifecycle engine.
var globalLifecycleEngine *LifecycleEngine

// newLifecycleEngine creates a new LifecycleEngine, wiring current globals.
func newLifecycleEngine(cfg *Config) *LifecycleEngine {
	le := &LifecycleEngine{cfg: cfg}
	le.rebuildEngine()
	return le
}

// rebuildEngine constructs the internal engine from current global services.
func (le *LifecycleEngine) rebuildEngine() {
	lcCfg := lifecycle.Config{
		Lifecycle: lifecycle.LifecycleConfig{
			AutoHabitSuggest:   le.cfg.Lifecycle.AutoHabitSuggest,
			AutoInsightAction:  le.cfg.Lifecycle.AutoInsightAction,
			AutoBirthdayRemind: le.cfg.Lifecycle.AutoBirthdayRemind,
		},
	}
	if le.cfg.Notes.Enabled {
		lcCfg.NotesEnabled = true
		lcCfg.VaultPath = le.cfg.Notes.VaultPathResolved(le.cfg.BaseDir)
	}
	le.engine = lifecycle.New(lcCfg, globalInsightsEngine, globalContactsService, globalGoalsService, globalReminderEngine)
}

// SuggestHabitForGoal returns habit suggestions based on goal title and category.
func (le *LifecycleEngine) SuggestHabitForGoal(title, category string) []string {
	le.rebuildEngine()
	return le.engine.SuggestHabitForGoal(title, category)
}

// RunInsightActions detects anomalies and creates reminders/notifications.
func (le *LifecycleEngine) RunInsightActions() ([]string, error) {
	le.rebuildEngine()
	return le.engine.RunInsightActions()
}

// SyncBirthdayReminders creates annual reminders for contact birthdays.
func (le *LifecycleEngine) SyncBirthdayReminders() (int, error) {
	le.rebuildEngine()
	return le.engine.SyncBirthdayReminders()
}

// OnGoalCompleted logs a celebration note when a goal is completed.
func (le *LifecycleEngine) OnGoalCompleted(goalID string) error {
	le.rebuildEngine()
	return le.engine.OnGoalCompleted(goalID)
}

// --- Tool Handlers ---

func toolLifecycleSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Lifecycle == nil {
		return "", fmt.Errorf("lifecycle engine not initialized")
	}

	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Action == "" {
		args.Action = "all"
	}

	result := map[string]any{}

	switch args.Action {
	case "birthdays":
		n, err := app.Lifecycle.SyncBirthdayReminders()
		if err != nil {
			return "", err
		}
		result["birthdays_synced"] = n

	case "insights":
		actions, err := app.Lifecycle.RunInsightActions()
		if err != nil {
			return "", err
		}
		result["insight_actions"] = actions

	case "all":
		if cfg.Lifecycle.AutoBirthdayRemind {
			n, err := app.Lifecycle.SyncBirthdayReminders()
			if err != nil {
				result["birthday_error"] = err.Error()
			} else {
				result["birthdays_synced"] = n
			}
		}
		if cfg.Lifecycle.AutoInsightAction {
			actions, err := app.Lifecycle.RunInsightActions()
			if err != nil {
				result["insight_error"] = err.Error()
			} else {
				result["insight_actions"] = actions
			}
		}

	default:
		return "", fmt.Errorf("unknown action: %s (use birthdays, insights, or all)", args.Action)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}

func toolLifecycleSuggest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Lifecycle == nil {
		return "", fmt.Errorf("lifecycle engine not initialized")
	}

	var args struct {
		GoalTitle    string `json:"goal_title"`
		GoalCategory string `json:"goal_category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.GoalTitle == "" {
		return "", fmt.Errorf("goal_title is required")
	}

	suggestions := app.Lifecycle.SuggestHabitForGoal(args.GoalTitle, args.GoalCategory)
	result := map[string]any{
		"goal_title":  args.GoalTitle,
		"suggestions": suggestions,
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return string(out), nil
}

// suppress unused import warning — log is used transitively via internal/lifecycle
var _ = log.Info
