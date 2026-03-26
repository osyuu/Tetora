package proactive

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
)

// newTestEngine creates a minimal Engine wired to a temp SQLite DB.
func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	cfg := &config.Config{HistoryDB: dbPath}
	e := &Engine{
		cfg:       cfg,
		cooldowns: make(map[string]cooldownEntry),
	}
	return e, dbPath
}

// TestSetCooldown_PersistsToDB verifies that SetCooldown writes the entry to SQLite.
func TestSetCooldown_PersistsToDB(t *testing.T) {
	e, dbPath := newTestEngine(t)

	// Ensure table exists before writing.
	if err := db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS proactive_cooldowns (
		rule_name TEXT PRIMARY KEY,
		last_triggered TEXT NOT NULL,
		duration_ns INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	e.SetCooldown("daily-report", 24*time.Hour)

	rows, err := db.Query(dbPath, "SELECT rule_name, duration_ns FROM proactive_cooldowns")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if got := db.Str(rows[0]["rule_name"]); got != "daily-report" {
		t.Errorf("rule_name = %q, want %q", got, "daily-report")
	}
	wantNs := int((24 * time.Hour).Nanoseconds())
	if got := db.Int(rows[0]["duration_ns"]); got != wantNs {
		t.Errorf("duration_ns = %d, want %d", got, wantNs)
	}
}

// TestSetCooldown_InMemory verifies that SetCooldown populates the in-memory map.
func TestSetCooldown_InMemory(t *testing.T) {
	e, dbPath := newTestEngine(t)

	// Pre-create table so persist doesn't silently fail.
	_ = db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS proactive_cooldowns (
		rule_name TEXT PRIMARY KEY,
		last_triggered TEXT NOT NULL,
		duration_ns INTEGER NOT NULL
	)`)

	if e.CheckCooldown("rule-x") {
		t.Fatal("expected no cooldown before SetCooldown")
	}

	e.SetCooldown("rule-x", time.Hour)

	if !e.CheckCooldown("rule-x") {
		t.Error("expected cooldown to be active after SetCooldown")
	}
}

// TestLoadCooldownsFromDB_RestoresActive verifies that a non-expired cooldown
// is restored when loadCooldownsFromDB is called on a fresh engine.
func TestLoadCooldownsFromDB_RestoresActive(t *testing.T) {
	e, dbPath := newTestEngine(t)

	// Seed the DB directly — simulate what a previous process wrote.
	if err := db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS proactive_cooldowns (
		rule_name TEXT PRIMARY KEY,
		last_triggered TEXT NOT NULL,
		duration_ns INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	triggered := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	durationNs := int64(2 * time.Hour)
	sql := `INSERT INTO proactive_cooldowns (rule_name, last_triggered, duration_ns)
	        VALUES ('market-open', '` + triggered + `', ` + itoa(durationNs) + `)`
	if err := db.Exec(dbPath, sql); err != nil {
		t.Fatalf("seed DB: %v", err)
	}

	// Load into a fresh engine — simulates daemon restart.
	e2 := &Engine{cfg: e.cfg, cooldowns: make(map[string]cooldownEntry)}
	e2.loadCooldownsFromDB()

	if !e2.CheckCooldown("market-open") {
		t.Error("expected cooldown to be active after loadCooldownsFromDB")
	}
}

// TestLoadCooldownsFromDB_SkipsExpired verifies that expired cooldowns are not restored.
func TestLoadCooldownsFromDB_SkipsExpired(t *testing.T) {
	e, dbPath := newTestEngine(t)

	if err := db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS proactive_cooldowns (
		rule_name TEXT PRIMARY KEY,
		last_triggered TEXT NOT NULL,
		duration_ns INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Triggered 2 hours ago with a 1-hour cooldown → already expired.
	triggered := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	durationNs := int64(time.Hour)
	sql := `INSERT INTO proactive_cooldowns (rule_name, last_triggered, duration_ns)
	        VALUES ('old-rule', '` + triggered + `', ` + itoa(durationNs) + `)`
	if err := db.Exec(dbPath, sql); err != nil {
		t.Fatalf("seed DB: %v", err)
	}

	e2 := &Engine{cfg: e.cfg, cooldowns: make(map[string]cooldownEntry)}
	e2.loadCooldownsFromDB()

	if e2.CheckCooldown("old-rule") {
		t.Error("expired cooldown should NOT be restored")
	}
}

// TestLoadCooldownsFromDB_CreatesTableIfMissing verifies that calling
// loadCooldownsFromDB on a fresh DB (no table yet) does not panic or error.
func TestLoadCooldownsFromDB_CreatesTableIfMissing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fresh.db")

	// Verify the file doesn't exist yet.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("expected DB file to not exist yet")
	}

	e := &Engine{
		cfg:       &config.Config{HistoryDB: dbPath},
		cooldowns: make(map[string]cooldownEntry),
	}
	// Should not panic or return an error (errors are logged, not returned).
	e.loadCooldownsFromDB()

	// Table should now exist.
	rows, err := db.Query(dbPath, "SELECT name FROM sqlite_master WHERE type='table' AND name='proactive_cooldowns'")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if len(rows) != 1 {
		t.Error("expected proactive_cooldowns table to be created by loadCooldownsFromDB")
	}
}

// TestSetCooldown_Upsert verifies that calling SetCooldown twice on the same rule
// updates the existing DB row (no duplicate rows).
func TestSetCooldown_Upsert(t *testing.T) {
	e, dbPath := newTestEngine(t)

	_ = db.Exec(dbPath, `CREATE TABLE IF NOT EXISTS proactive_cooldowns (
		rule_name TEXT PRIMARY KEY,
		last_triggered TEXT NOT NULL,
		duration_ns INTEGER NOT NULL
	)`)

	e.SetCooldown("rule-dup", time.Hour)
	e.SetCooldown("rule-dup", 2*time.Hour)

	rows, err := db.Query(dbPath, "SELECT count(*) as n FROM proactive_cooldowns WHERE rule_name='rule-dup'")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if db.Int(rows[0]["n"]) != 1 {
		t.Errorf("expected 1 row after upsert, got %d", db.Int(rows[0]["n"]))
	}
}

// itoa converts int64 to string without importing strconv in test body.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
