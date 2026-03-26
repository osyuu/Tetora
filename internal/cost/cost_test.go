package cost

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tetora/internal/db"
)

// setupTestDB creates a temporary SQLite DB with job_runs table and returns the path.
func setupTestDB(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	sql := `
CREATE TABLE IF NOT EXISTS job_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL,
  name TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL,
  status TEXT NOT NULL,
  exit_code INTEGER DEFAULT 0,
  cost_usd REAL DEFAULT 0,
  output_summary TEXT DEFAULT '',
  error TEXT DEFAULT '',
  model TEXT DEFAULT '',
  session_id TEXT DEFAULT '',
  output_file TEXT DEFAULT '',
  tokens_in INTEGER DEFAULT 0,
  tokens_out INTEGER DEFAULT 0,
  agent TEXT DEFAULT '',
  parent_id TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS workflow_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  workflow_name TEXT NOT NULL,
  cost_usd REAL DEFAULT 0
);`
	if err := db.Exec(dbPath, sql); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// insertTestJobRun inserts a cost record into the test DB.
func insertTestJobRun(t *testing.T, dbPath, jobID, agent string, costUSD float64) {
	t.Helper()
	now := time.Now()
	sql := fmt.Sprintf(
		`INSERT INTO job_runs (job_id, name, source, started_at, finished_at, status, cost_usd, agent)
		 VALUES ('%s','test','test','%s','%s','success',%f,'%s')`,
		db.Escape(jobID),
		db.Escape(now.Format(time.RFC3339)),
		db.Escape(now.Add(time.Minute).Format(time.RFC3339)),
		costUSD,
		db.Escape(agent),
	)
	if err := db.Exec(dbPath, sql); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDowngradeModel(t *testing.T) {
	ad := AutoDowngradeConfig{
		Enabled: true,
		Thresholds: []DowngradeThreshold{
			{At: 0.7, Model: "sonnet"},
			{At: 0.9, Model: "haiku"},
		},
	}

	tests := []struct {
		utilization float64
		want        string
	}{
		{0.5, ""},       // below all thresholds
		{0.7, "sonnet"}, // exactly at 70%
		{0.8, "sonnet"}, // between 70-90%
		{0.9, "haiku"},  // exactly at 90%
		{0.95, "haiku"}, // above 90%
		{1.0, "haiku"},  // at 100%
		{0.0, ""},       // zero
	}

	for _, tt := range tests {
		got := ResolveDowngradeModel(ad, tt.utilization)
		if got != tt.want {
			t.Errorf("ResolveDowngradeModel(%.2f) = %q, want %q", tt.utilization, got, tt.want)
		}
	}
}

func TestCheckBudgetPaused(t *testing.T) {
	budgets := BudgetConfig{Paused: true}
	result := CheckBudget(budgets, "", "", "", 0)
	if result.Allowed {
		t.Error("expected not allowed when paused")
	}
	if !result.Paused {
		t.Error("expected paused flag")
	}
	if result.AlertLevel != "paused" {
		t.Errorf("expected alertLevel=paused, got %s", result.AlertLevel)
	}
}

func TestCheckBudgetNoBudgets(t *testing.T) {
	result := CheckBudget(BudgetConfig{}, "", "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed when no budgets configured")
	}
	if result.AlertLevel != "ok" {
		t.Errorf("expected alertLevel=ok, got %s", result.AlertLevel)
	}
}

func TestCheckBudgetWithDB(t *testing.T) {
	dbPath := setupTestDB(t)
	insertTestJobRun(t, dbPath, "test1", "翡翠", 5.0)
	insertTestJobRun(t, dbPath, "test2", "黒曜", 3.0)

	// Test global daily budget exceeded.
	budgets := BudgetConfig{
		Global: GlobalBudget{Daily: 5.0}, // $5 limit, $8 spent
	}
	result := CheckBudget(budgets, dbPath, "", "", 0)
	if result.Allowed {
		t.Error("expected not allowed when budget exceeded")
	}
	if !result.Exceeded {
		t.Error("expected exceeded flag")
	}
	if result.AlertLevel != "exceeded" {
		t.Errorf("expected alertLevel=exceeded, got %s", result.AlertLevel)
	}

	// Test global budget within limits.
	budgets.Global.Daily = 20.0
	result = CheckBudget(budgets, dbPath, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed when within budget")
	}
	if result.AlertLevel != "ok" {
		t.Errorf("expected alertLevel=ok, got %s", result.AlertLevel)
	}

	// Test global budget at warning level (70%).
	budgets.Global.Daily = 10.0 // $8/$10 = 80% -> warning
	result = CheckBudget(budgets, dbPath, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed at warning level")
	}
	if result.AlertLevel != "warning" {
		t.Errorf("expected alertLevel=warning, got %s", result.AlertLevel)
	}

	// Test global budget at critical level (90%).
	budgets.Global.Daily = 8.5 // $8/$8.5 = 94% -> critical
	result = CheckBudget(budgets, dbPath, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed at critical level")
	}
	if result.AlertLevel != "critical" {
		t.Errorf("expected alertLevel=critical, got %s", result.AlertLevel)
	}

	// Test per-role budget exceeded.
	budgets.Global.Daily = 100.0 // global OK
	budgets.Agents = map[string]AgentBudget{
		"翡翠": {Daily: 3.0}, // $5 spent by 翡翠, limit $3
	}
	result = CheckBudget(budgets, dbPath, "翡翠", "", 0)
	if result.Allowed {
		t.Error("expected not allowed when role budget exceeded")
	}
	if !result.Exceeded {
		t.Error("expected exceeded flag for role")
	}

	// Test per-role budget OK for different role.
	result = CheckBudget(budgets, dbPath, "黒曜", "", 0)
	if !result.Allowed {
		t.Error("expected allowed for role without budget config")
	}
}

func TestCheckBudgetAutoDowngrade(t *testing.T) {
	dbPath := setupTestDB(t)
	insertTestJobRun(t, dbPath, "test1", "", 7.5)

	budgets := BudgetConfig{
		Global: GlobalBudget{Daily: 10.0}, // 75% utilized
		AutoDowngrade: AutoDowngradeConfig{
			Enabled: true,
			Thresholds: []DowngradeThreshold{
				{At: 0.7, Model: "sonnet"},
				{At: 0.9, Model: "haiku"},
			},
		},
	}

	result := CheckBudget(budgets, dbPath, "", "", 0)
	if !result.Allowed {
		t.Error("expected allowed with auto-downgrade")
	}
	if result.DowngradeModel != "sonnet" {
		t.Errorf("expected downgradeModel=sonnet, got %q", result.DowngradeModel)
	}
}

func TestQuerySpend(t *testing.T) {
	dbPath := setupTestDB(t)
	insertTestJobRun(t, dbPath, "t1", "翡翠", 2.5)
	insertTestJobRun(t, dbPath, "t2", "黒曜", 1.5)

	// Total spend.
	daily, weekly, monthly := QuerySpend(dbPath, "")
	if daily < 3.9 || daily > 4.1 {
		t.Errorf("expected daily ~4.0, got %.2f", daily)
	}
	if weekly < 3.9 || weekly > 4.1 {
		t.Errorf("expected weekly ~4.0, got %.2f", weekly)
	}
	if monthly < 3.9 || monthly > 4.1 {
		t.Errorf("expected monthly ~4.0, got %.2f", monthly)
	}

	// Per-role spend.
	daily, _, _ = QuerySpend(dbPath, "翡翠")
	if daily < 2.4 || daily > 2.6 {
		t.Errorf("expected role daily ~2.5, got %.2f", daily)
	}
}

func TestBudgetAlertTracker(t *testing.T) {
	tracker := NewBudgetAlertTracker()
	tracker.Cooldown = 100 * time.Millisecond

	// First alert should fire.
	if !tracker.ShouldAlert("test:daily:warning") {
		t.Error("expected first alert to fire")
	}

	// Immediate second alert should be suppressed.
	if tracker.ShouldAlert("test:daily:warning") {
		t.Error("expected second alert to be suppressed")
	}

	// Different key should fire.
	if !tracker.ShouldAlert("test:daily:critical") {
		t.Error("expected different key to fire")
	}

	// After cooldown, same key should fire again.
	time.Sleep(150 * time.Millisecond)
	if !tracker.ShouldAlert("test:daily:warning") {
		t.Error("expected alert to fire after cooldown")
	}
}

func TestSetBudgetPaused(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	os.WriteFile(configPath, []byte(`{"maxConcurrent": 3}`), 0644)

	// Pause.
	if err := SetBudgetPaused(configPath, true); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(configPath)
	if !containsStr(string(data), `"paused": true`) {
		t.Error("expected paused=true in config")
	}

	// Resume.
	if err := SetBudgetPaused(configPath, false); err != nil {
		t.Fatal(err)
	}

	data, _ = os.ReadFile(configPath)
	if !containsStr(string(data), `"paused": false`) {
		t.Error("expected paused=false in config")
	}
}

func TestQueryBudgetStatus(t *testing.T) {
	dbPath := setupTestDB(t)

	budgets := BudgetConfig{
		Global: GlobalBudget{Daily: 10.0, Weekly: 50.0},
		Agents: map[string]AgentBudget{
			"翡翠": {Daily: 3.0},
		},
	}

	status := QueryBudgetStatus(budgets, dbPath)
	if status.Global == nil {
		t.Fatal("expected global meter")
	}
	if status.Global.DailyLimit != 10.0 {
		t.Errorf("expected daily limit 10.0, got %.2f", status.Global.DailyLimit)
	}
	if status.Global.WeeklyLimit != 50.0 {
		t.Errorf("expected weekly limit 50.0, got %.2f", status.Global.WeeklyLimit)
	}
	if len(status.Agents) != 1 {
		t.Errorf("expected 1 role meter, got %d", len(status.Agents))
	}
}

func TestFormatBudgetSummary(t *testing.T) {
	dbPath := setupTestDB(t)

	budgets := BudgetConfig{
		Global: GlobalBudget{Daily: 10.0},
	}

	summary := FormatBudgetSummary(QueryBudgetStatus(budgets, dbPath))
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !containsStr(summary, "Today:") {
		t.Errorf("expected 'Today:' in summary, got: %s", summary)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
