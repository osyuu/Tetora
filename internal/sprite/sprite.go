package sprite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// --- Sprite State Constants ---
// Universal agent sprite states — not tied to any specific team.

const (
	Idle      = "idle"
	Work      = "work"
	Think     = "think"
	Talk      = "talk"
	Review    = "review"
	Celebrate = "celebrate"
	Error     = "error"

	WalkDown  = "walk_down"
	WalkUp    = "walk_up"
	WalkLeft  = "walk_left"
	WalkRight = "walk_right"
)

// --- Sprite Config (user-customizable) ---

// Config describes spritesheet layout and per-agent sheet assignments.
// Loaded from ~/.tetora/media/sprites/config.json.
type Config struct {
	CellWidth  int                  `json:"cellWidth"`
	CellHeight int                  `json:"cellHeight"`
	Background string               `json:"background,omitempty"` // optional background PNG filename
	States     []StateDef           `json:"states"`
	Agents     map[string]AgentDef  `json:"agents"`
}

// StateDef maps a state name to a spritesheet row.
type StateDef struct {
	Name   string `json:"name"`
	Row    int    `json:"row"`
	Frames int    `json:"frames"`
}

// AgentDef holds per-agent sprite configuration.
// Two modes:
//   - Single sheet: set "sheet" — one PNG with all states as rows (uses States row mapping).
//   - Multi sheet:  set "sheets" — one PNG per state, each is a single horizontal strip.
//
// If both are set, "sheets" entries take priority for matched states; "sheet" is used as fallback.
type AgentDef struct {
	Sheet  string            `json:"sheet,omitempty"`  // single spritesheet PNG
	Sheets map[string]string `json:"sheets,omitempty"` // state name -> PNG filename
}

// DefaultConfig returns the built-in sprite config with all 11 states.
func DefaultConfig() Config {
	return Config{
		CellWidth:  32,
		CellHeight: 32,
		States: []StateDef{
			{Name: WalkDown, Row: 0, Frames: 4},
			{Name: WalkUp, Row: 1, Frames: 4},
			{Name: WalkLeft, Row: 2, Frames: 4},
			{Name: WalkRight, Row: 3, Frames: 4},
			{Name: Idle, Row: 4, Frames: 4},
			{Name: Work, Row: 5, Frames: 4},
			{Name: Think, Row: 6, Frames: 2},
			{Name: Talk, Row: 7, Frames: 4},
			{Name: Review, Row: 8, Frames: 2},
			{Name: Celebrate, Row: 9, Frames: 4},
			{Name: Error, Row: 10, Frames: 2},
		},
		Agents: map[string]AgentDef{},
	}
}

// LoadConfig reads config.json from the sprites directory.
// Returns default config if file doesn't exist or is unreadable.
// agentKeys are auto-registered into Agents if not already present,
// so the frontend always sees all known agents (with or without custom sheets).
func LoadConfig(spritesDir string, agentKeys []string) Config {
	def := DefaultConfig()
	data, err := os.ReadFile(filepath.Join(spritesDir, "config.json"))
	if err != nil {
		// No config file — start from defaults.
		cfg := def
		for _, k := range agentKeys {
			cfg.Agents[k] = AgentDef{}
		}
		return cfg
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = def
		for _, k := range agentKeys {
			cfg.Agents[k] = AgentDef{}
		}
		return cfg
	}
	// Fill zero values from defaults.
	if cfg.CellWidth == 0 {
		cfg.CellWidth = def.CellWidth
	}
	if cfg.CellHeight == 0 {
		cfg.CellHeight = def.CellHeight
	}
	if len(cfg.States) == 0 {
		cfg.States = def.States
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentDef{}
	}
	// Auto-register known agents that aren't in config yet.
	for _, k := range agentKeys {
		if _, exists := cfg.Agents[k]; !exists {
			cfg.Agents[k] = AgentDef{}
		}
	}
	return cfg
}

// InitConfig writes the default config.json if it doesn't exist.
func InitConfig(spritesDir string) error {
	path := filepath.Join(spritesDir, "config.json")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	cfg := DefaultConfig()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// --- Per-Agent Sprite State Tracker ---

// Tracker tracks the current sprite state for each agent.
type Tracker struct {
	mu    sync.RWMutex
	state map[string]string // agent name -> sprite state
}

// NewTracker returns a new Tracker.
func NewTracker() *Tracker {
	return &Tracker{state: make(map[string]string)}
}

// Set records the sprite state for the given agent.
func (t *Tracker) Set(agent, state string) {
	t.mu.Lock()
	t.state[agent] = state
	t.mu.Unlock()
}

// Get returns the current sprite state for the given agent, defaulting to Idle.
func (t *Tracker) Get(agent string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if s, ok := t.state[agent]; ok {
		return s
	}
	return Idle
}

// Snapshot returns a copy of the current state map.
func (t *Tracker) Snapshot() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m := make(map[string]string, len(t.state))
	for k, v := range t.state {
		m[k] = v
	}
	return m
}
