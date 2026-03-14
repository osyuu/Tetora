package sprite_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"tetora/internal/sprite"
)

// --- DefaultConfig ---

func TestDefaultConfig_CellDimensions(t *testing.T) {
	cfg := sprite.DefaultConfig()
	if cfg.CellWidth != 32 {
		t.Errorf("expected CellWidth 32, got %d", cfg.CellWidth)
	}
	if cfg.CellHeight != 32 {
		t.Errorf("expected CellHeight 32, got %d", cfg.CellHeight)
	}
}

func TestDefaultConfig_StateCount(t *testing.T) {
	cfg := sprite.DefaultConfig()
	if len(cfg.States) != 11 {
		t.Errorf("expected 11 states, got %d", len(cfg.States))
	}
}

func TestDefaultConfig_StateNames(t *testing.T) {
	cfg := sprite.DefaultConfig()

	want := []string{
		sprite.WalkDown,
		sprite.WalkUp,
		sprite.WalkLeft,
		sprite.WalkRight,
		sprite.Idle,
		sprite.Work,
		sprite.Think,
		sprite.Talk,
		sprite.Review,
		sprite.Celebrate,
		sprite.Error,
	}

	got := make(map[string]bool, len(cfg.States))
	for _, s := range cfg.States {
		got[s.Name] = true
	}

	for _, name := range want {
		if !got[name] {
			t.Errorf("expected state %q in DefaultConfig, not found", name)
		}
	}
}

func TestDefaultConfig_AgentsEmpty(t *testing.T) {
	cfg := sprite.DefaultConfig()
	if cfg.Agents == nil {
		t.Error("expected Agents map to be non-nil")
	}
	if len(cfg.Agents) != 0 {
		t.Errorf("expected empty Agents map, got %d entries", len(cfg.Agents))
	}
}

// --- LoadConfig ---

func TestLoadConfig_NonExistentDir_ReturnsDefaults(t *testing.T) {
	cfg := sprite.LoadConfig("/nonexistent/path/that/does/not/exist", nil)
	def := sprite.DefaultConfig()

	if cfg.CellWidth != def.CellWidth {
		t.Errorf("expected CellWidth %d, got %d", def.CellWidth, cfg.CellWidth)
	}
	if cfg.CellHeight != def.CellHeight {
		t.Errorf("expected CellHeight %d, got %d", def.CellHeight, cfg.CellHeight)
	}
	if len(cfg.States) != len(def.States) {
		t.Errorf("expected %d states, got %d", len(def.States), len(cfg.States))
	}
}

func TestLoadConfig_ValidConfigJSON(t *testing.T) {
	dir := t.TempDir()

	custom := sprite.Config{
		CellWidth:  64,
		CellHeight: 48,
		States: []sprite.StateDef{
			{Name: sprite.Idle, Row: 0, Frames: 2},
		},
		Agents: map[string]sprite.AgentDef{
			"ruri": {Sheet: "ruri.png"},
		},
	}
	data, err := json.Marshal(custom)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	cfg := sprite.LoadConfig(dir, nil)

	if cfg.CellWidth != 64 {
		t.Errorf("expected CellWidth 64, got %d", cfg.CellWidth)
	}
	if cfg.CellHeight != 48 {
		t.Errorf("expected CellHeight 48, got %d", cfg.CellHeight)
	}
	if len(cfg.States) != 1 {
		t.Errorf("expected 1 state, got %d", len(cfg.States))
	}
	if cfg.Agents["ruri"].Sheet != "ruri.png" {
		t.Errorf("expected ruri sheet ruri.png, got %q", cfg.Agents["ruri"].Sheet)
	}
}

func TestLoadConfig_InvalidJSON_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("failed to write invalid config.json: %v", err)
	}

	cfg := sprite.LoadConfig(dir, nil)
	def := sprite.DefaultConfig()

	if cfg.CellWidth != def.CellWidth {
		t.Errorf("expected CellWidth %d after invalid JSON, got %d", def.CellWidth, cfg.CellWidth)
	}
	if len(cfg.States) != len(def.States) {
		t.Errorf("expected %d states after invalid JSON, got %d", len(def.States), len(cfg.States))
	}
}

func TestLoadConfig_ZeroCellWidth_FilledFromDefaults(t *testing.T) {
	dir := t.TempDir()

	// Write config with CellWidth omitted (zero value).
	raw := `{"cellWidth": 0, "cellHeight": 0}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	cfg := sprite.LoadConfig(dir, nil)
	def := sprite.DefaultConfig()

	if cfg.CellWidth != def.CellWidth {
		t.Errorf("expected CellWidth filled from defaults (%d), got %d", def.CellWidth, cfg.CellWidth)
	}
	if cfg.CellHeight != def.CellHeight {
		t.Errorf("expected CellHeight filled from defaults (%d), got %d", def.CellHeight, cfg.CellHeight)
	}
}

func TestLoadConfig_AgentKeys_AutoRegistered(t *testing.T) {
	dir := t.TempDir()

	// No config file — uses defaults, agents come from agentKeys.
	keys := []string{"ruri", "hisui", "kokuyou"}
	cfg := sprite.LoadConfig(dir, keys)

	for _, k := range keys {
		if _, ok := cfg.Agents[k]; !ok {
			t.Errorf("expected agent %q to be auto-registered, not found", k)
		}
	}
}

func TestLoadConfig_AgentKeys_AutoRegistered_WithExistingConfig(t *testing.T) {
	dir := t.TempDir()

	// Config has one agent already; agentKeys adds a new one and doesn't overwrite the existing.
	custom := sprite.Config{
		CellWidth:  32,
		CellHeight: 32,
		States:     sprite.DefaultConfig().States,
		Agents: map[string]sprite.AgentDef{
			"ruri": {Sheet: "ruri.png"},
		},
	}
	data, err := json.Marshal(custom)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := sprite.LoadConfig(dir, []string{"ruri", "hisui"})

	// Existing entry must not be overwritten.
	if cfg.Agents["ruri"].Sheet != "ruri.png" {
		t.Errorf("expected ruri sheet to be preserved, got %q", cfg.Agents["ruri"].Sheet)
	}
	// New key must be added.
	if _, ok := cfg.Agents["hisui"]; !ok {
		t.Error("expected hisui to be auto-registered")
	}
}

func TestLoadConfig_AgentKeys_AutoRegistered_InvalidJSON(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("!!!"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	keys := []string{"kohaku"}
	cfg := sprite.LoadConfig(dir, keys)

	if _, ok := cfg.Agents["kohaku"]; !ok {
		t.Error("expected kohaku to be auto-registered even after invalid JSON")
	}
}

// --- InitConfig ---

func TestInitConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	if err := sprite.InitConfig(dir); err != nil {
		t.Fatalf("InitConfig returned error: %v", err)
	}

	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config.json not created: %v", err)
	}

	var cfg sprite.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}
	def := sprite.DefaultConfig()
	if cfg.CellWidth != def.CellWidth {
		t.Errorf("expected CellWidth %d in created file, got %d", def.CellWidth, cfg.CellWidth)
	}
}

func TestInitConfig_DoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := []byte(`{"cellWidth":99,"cellHeight":99,"states":[],"agents":{}}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := sprite.InitConfig(dir); err != nil {
		t.Fatalf("InitConfig returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var cfg sprite.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.CellWidth != 99 {
		t.Errorf("InitConfig must not overwrite existing file; expected CellWidth 99, got %d", cfg.CellWidth)
	}
}

// --- Tracker ---

func TestNewTracker_NonNil(t *testing.T) {
	tr := sprite.NewTracker()
	if tr == nil {
		t.Fatal("NewTracker returned nil")
	}
}

func TestTracker_SetGet(t *testing.T) {
	tr := sprite.NewTracker()
	tr.Set("ruri", sprite.Work)

	got := tr.Get("ruri")
	if got != sprite.Work {
		t.Errorf("expected %q, got %q", sprite.Work, got)
	}
}

func TestTracker_Get_UnknownAgent_ReturnsIdle(t *testing.T) {
	tr := sprite.NewTracker()
	got := tr.Get("nonexistent")
	if got != sprite.Idle {
		t.Errorf("expected Idle for unknown agent, got %q", got)
	}
}

func TestTracker_Set_Overwrite(t *testing.T) {
	tr := sprite.NewTracker()
	tr.Set("ruri", sprite.Idle)
	tr.Set("ruri", sprite.Think)

	got := tr.Get("ruri")
	if got != sprite.Think {
		t.Errorf("expected %q after overwrite, got %q", sprite.Think, got)
	}
}

func TestTracker_Snapshot_ReturnsCopy(t *testing.T) {
	tr := sprite.NewTracker()
	tr.Set("ruri", sprite.Work)
	tr.Set("hisui", sprite.Review)

	snap := tr.Snapshot()

	if snap["ruri"] != sprite.Work {
		t.Errorf("expected ruri=%q in snapshot, got %q", sprite.Work, snap["ruri"])
	}
	if snap["hisui"] != sprite.Review {
		t.Errorf("expected hisui=%q in snapshot, got %q", sprite.Review, snap["hisui"])
	}

	// Mutating the snapshot must not affect the tracker.
	snap["ruri"] = sprite.Error
	if tr.Get("ruri") != sprite.Work {
		t.Error("mutating snapshot must not affect tracker state")
	}
}

func TestTracker_Snapshot_Empty(t *testing.T) {
	tr := sprite.NewTracker()
	snap := tr.Snapshot()
	if len(snap) != 0 {
		t.Errorf("expected empty snapshot, got %d entries", len(snap))
	}
}

func TestTracker_Concurrent_SetGet(t *testing.T) {
	tr := sprite.NewTracker()
	agents := []string{"ruri", "hisui", "kokuyou", "kohaku"}
	states := []string{sprite.Idle, sprite.Work, sprite.Think, sprite.Talk, sprite.Review}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			agent := agents[i%len(agents)]
			state := states[i%len(states)]
			tr.Set(agent, state)
			_ = tr.Get(agent)
			_ = tr.Snapshot()
		}()
	}

	wg.Wait()
	// No race condition panic = pass. Also verify all agents return a valid (non-empty) state.
	for _, a := range agents {
		if s := tr.Get(a); s == "" {
			t.Errorf("agent %q returned empty state after concurrent writes", a)
		}
	}
}
