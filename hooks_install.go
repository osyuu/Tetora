package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// --- Claude Code Hooks Installer ---
// Manages Tetora hook entries in ~/.claude/settings.json.
// See: https://docs.anthropic.com/en/docs/claude-code/hooks

// claudeSettings represents the structure of ~/.claude/settings.json.
type claudeSettings struct {
	raw map[string]json.RawMessage
}

func loadClaudeSettings() (*claudeSettings, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("get home dir: %w", err)
	}

	path := filepath.Join(homeDir, ".claude", "settings.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create empty settings.
			return &claudeSettings{raw: make(map[string]json.RawMessage)}, path, nil
		}
		return nil, "", fmt.Errorf("read settings: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("parse settings: %w", err)
	}

	return &claudeSettings{raw: raw}, path, nil
}

func (s *claudeSettings) save(path string) error {
	data, err := json.MarshalIndent(s.raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// hookCommand returns the curl command that Claude Code hooks will execute.
func hookCommand(listenAddr string) string {
	if listenAddr == "" {
		listenAddr = ":8991"
	}
	// Normalize address.
	addr := listenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "localhost" + addr
	}

	return fmt.Sprintf(
		`curl -sf -X POST http://%s/api/hooks/event -H 'Content-Type:application/json' -d @/dev/stdin 2>/dev/null || true`,
		addr,
	)
}

// tetoraHookMatcher checks if a hook command is a Tetora hook.
const tetoraHookMarker = "/api/hooks/event"

func isTetoraHook(cmd string) bool {
	return strings.Contains(cmd, tetoraHookMarker)
}

// hookEntry represents a single hook in the Claude Code settings.
type hookEntry struct {
	Matcher string `json:"matcher,omitempty"` // tool name pattern (for PreToolUse/PostToolUse)
	Command string `json:"command"`
}

// hooksConfig represents the hooks section of Claude Code settings.
type hooksConfig struct {
	PreToolUse  []hookEntry `json:"PreToolUse,omitempty"`
	PostToolUse []hookEntry `json:"PostToolUse,omitempty"`
	Stop        []hookEntry `json:"Stop,omitempty"`
	Notification []hookEntry `json:"Notification,omitempty"`
}

// installHooks adds Tetora hook entries to Claude Code settings.
func installHooks(listenAddr string) error {
	settings, path, err := loadClaudeSettings()
	if err != nil {
		return err
	}

	cmd := hookCommand(listenAddr)

	// Parse existing hooks section.
	var hooks hooksConfig
	if raw, ok := settings.raw["hooks"]; ok {
		json.Unmarshal(raw, &hooks)
	}

	// Add Tetora hooks (preserving existing non-Tetora hooks).
	hooks.PostToolUse = addTetoraHook(hooks.PostToolUse, cmd, "")
	hooks.Stop = addTetoraHook(hooks.Stop, cmd, "")
	hooks.Notification = addTetoraHook(hooks.Notification, cmd, "")

	// Serialize hooks back.
	hooksData, err := json.Marshal(hooks)
	if err != nil {
		return fmt.Errorf("marshal hooks: %w", err)
	}
	settings.raw["hooks"] = hooksData

	if err := settings.save(path); err != nil {
		return err
	}

	fmt.Printf("Hooks installed in %s\n", path)
	fmt.Printf("Hook command: %s\n", cmd)
	return nil
}

// addTetoraHook adds a Tetora hook to the list, replacing any existing Tetora hook.
func addTetoraHook(hooks []hookEntry, cmd, matcher string) []hookEntry {
	// Remove existing Tetora hooks.
	filtered := make([]hookEntry, 0, len(hooks))
	for _, h := range hooks {
		if !isTetoraHook(h.Command) {
			filtered = append(filtered, h)
		}
	}

	// Add new Tetora hook.
	entry := hookEntry{Command: cmd}
	if matcher != "" {
		entry.Matcher = matcher
	}
	return append(filtered, entry)
}

// removeHooks removes all Tetora hook entries from Claude Code settings.
func removeHooks() error {
	settings, path, err := loadClaudeSettings()
	if err != nil {
		return err
	}

	raw, ok := settings.raw["hooks"]
	if !ok {
		fmt.Println("No hooks configured.")
		return nil
	}

	var hooks hooksConfig
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return fmt.Errorf("parse hooks: %w", err)
	}

	removed := 0
	hooks.PreToolUse, removed = removeTetoraHooks(hooks.PreToolUse)
	hooks.PostToolUse, removed = removeTetoraHooksCount(hooks.PostToolUse, removed)
	hooks.Stop, removed = removeTetoraHooksCount(hooks.Stop, removed)
	hooks.Notification, removed = removeTetoraHooksCount(hooks.Notification, removed)

	if removed == 0 {
		fmt.Println("No Tetora hooks found.")
		return nil
	}

	hooksData, _ := json.Marshal(hooks)
	settings.raw["hooks"] = hooksData

	if err := settings.save(path); err != nil {
		return err
	}

	fmt.Printf("Removed %d Tetora hook(s) from %s\n", removed, path)
	return nil
}

func removeTetoraHooks(hooks []hookEntry) ([]hookEntry, int) {
	filtered := make([]hookEntry, 0, len(hooks))
	removed := 0
	for _, h := range hooks {
		if isTetoraHook(h.Command) {
			removed++
		} else {
			filtered = append(filtered, h)
		}
	}
	return filtered, removed
}

func removeTetoraHooksCount(hooks []hookEntry, prevRemoved int) ([]hookEntry, int) {
	result, r := removeTetoraHooks(hooks)
	return result, prevRemoved + r
}

// showHooksStatus displays the current state of Tetora hooks.
func showHooksStatus() error {
	settings, path, err := loadClaudeSettings()
	if err != nil {
		return err
	}

	fmt.Printf("Settings file: %s\n\n", path)

	raw, ok := settings.raw["hooks"]
	if !ok {
		fmt.Println("No hooks configured.")
		return nil
	}

	var hooks hooksConfig
	if err := json.Unmarshal(raw, &hooks); err != nil {
		return fmt.Errorf("parse hooks: %w", err)
	}

	found := false
	checkHookType := func(name string, entries []hookEntry) {
		for _, h := range entries {
			if isTetoraHook(h.Command) {
				fmt.Printf("  %s: %s\n", name, h.Command)
				if h.Matcher != "" {
					fmt.Printf("    matcher: %s\n", h.Matcher)
				}
				found = true
			}
		}
	}

	fmt.Println("Tetora hooks:")
	checkHookType("PreToolUse", hooks.PreToolUse)
	checkHookType("PostToolUse", hooks.PostToolUse)
	checkHookType("Stop", hooks.Stop)
	checkHookType("Notification", hooks.Notification)

	if !found {
		fmt.Println("  (none installed)")
	}

	return nil
}
