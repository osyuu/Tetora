package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- MCP Config Types ---

// MCPConfigInfo represents summary info about an MCP server config.
type MCPConfigInfo struct {
	Name    string          `json:"name"`
	Command string          `json:"command,omitempty"`
	Args    string          `json:"args,omitempty"`
	Config  json.RawMessage `json:"config"`
}

// --- CRUD ---

func listMCPConfigs(cfg *Config) []MCPConfigInfo {
	cfg.MCPMu.RLock()
	defer cfg.MCPMu.RUnlock()

	if len(cfg.MCPConfigs) == 0 {
		return nil
	}

	var configs []MCPConfigInfo
	for name, raw := range cfg.MCPConfigs {
		cmd, args := extractMCPSummary(raw)
		configs = append(configs, MCPConfigInfo{
			Name:    name,
			Command: cmd,
			Args:    args,
			Config:  raw,
		})
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})
	return configs
}

func getMCPConfig(cfg *Config, name string) (json.RawMessage, error) {
	cfg.MCPMu.RLock()
	defer cfg.MCPMu.RUnlock()

	raw, ok := cfg.MCPConfigs[name]
	if !ok {
		return nil, fmt.Errorf("MCP config %q not found", name)
	}
	return raw, nil
}

func setMCPConfig(cfg *Config, configPath, name string, config json.RawMessage) error {
	if name == "" {
		return fmt.Errorf("MCP name is required")
	}
	// Validate name.
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in MCP name (use a-z, 0-9, -, _)", string(r))
		}
	}
	// Validate JSON.
	if !json.Valid(config) {
		return fmt.Errorf("invalid JSON config")
	}

	if err := updateConfigMCPs(configPath, name, config); err != nil {
		return err
	}

	// Write MCP file for immediate use (outside lock to avoid holding lock during I/O).
	mcpDir := filepath.Join(cfg.BaseDir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		return fmt.Errorf("create mcp dir: %w", err)
	}
	path := filepath.Join(mcpDir, name+".json")
	if err := os.WriteFile(path, config, 0o644); err != nil {
		return fmt.Errorf("write mcp file %q: %w", path, err)
	}

	// Update in-memory under lock.
	cfg.MCPMu.Lock()
	if cfg.MCPConfigs == nil {
		cfg.MCPConfigs = make(map[string]json.RawMessage)
	}
	cfg.MCPConfigs[name] = config
	if cfg.MCPPaths == nil {
		cfg.MCPPaths = make(map[string]string)
	}
	cfg.MCPPaths[name] = path
	cfg.MCPMu.Unlock()

	return nil
}

func deleteMCPConfig(cfg *Config, configPath, name string) error {
	cfg.MCPMu.RLock()
	_, ok := cfg.MCPConfigs[name]
	cfg.MCPMu.RUnlock()
	if !ok {
		return fmt.Errorf("MCP config %q not found", name)
	}

	if err := updateConfigMCPs(configPath, name, nil); err != nil {
		return err
	}

	// Determine file path and update in-memory under lock.
	cfg.MCPMu.Lock()
	var filePath string
	if p, ok := cfg.MCPPaths[name]; ok {
		filePath = p
		delete(cfg.MCPPaths, name)
	} else {
		filePath = filepath.Join(cfg.BaseDir, "mcp", name+".json")
	}
	delete(cfg.MCPConfigs, name)
	cfg.MCPMu.Unlock()

	// Remove MCP file outside lock.
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove mcp file %q: %w", filePath, err)
	}

	return nil
}

// --- Test ---

func testMCPConfig(raw json.RawMessage) (bool, string) {
	cmd, args := extractMCPSummary(raw)
	if cmd == "" {
		return false, "could not extract command from config"
	}

	// Check if command exists.
	cmdPath, err := exec.LookPath(cmd)
	if err != nil {
		return false, fmt.Sprintf("command %q not found in PATH", cmd)
	}

	// Try a brief start to see if it doesn't immediately crash.
	var cmdArgs []string
	if args != "" {
		cmdArgs = strings.Fields(args)
	}
	proc := exec.Command(cmdPath, cmdArgs...)
	proc.Env = os.Environ()

	// Start and immediately kill after a short timeout.
	if err := proc.Start(); err != nil {
		return false, fmt.Sprintf("failed to start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	select {
	case err := <-done:
		// Process exited on its own — check if it crashed immediately.
		if err != nil {
			return false, fmt.Sprintf("process exited: %v", err)
		}
		return true, fmt.Sprintf("OK: %s (%s)", cmd, cmdPath)
	case <-time.After(2 * time.Second):
		// Process is still running — that's good for a server.
		proc.Process.Kill()
		return true, fmt.Sprintf("OK: %s started successfully (%s)", cmd, cmdPath)
	}
}

// --- Helper ---

// extractMCPSummary parses the mcpServers wrapper to extract command and args.
func extractMCPSummary(raw json.RawMessage) (command, args string) {
	// Try mcpServers wrapper format.
	var wrapper struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.MCPServers) > 0 {
		for _, srv := range wrapper.MCPServers {
			return srv.Command, strings.Join(srv.Args, " ")
		}
	}

	// Try flat format.
	var flat struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if json.Unmarshal(raw, &flat) == nil && flat.Command != "" {
		return flat.Command, strings.Join(flat.Args, " ")
	}

	return "", ""
}
