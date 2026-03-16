package main

import "tetora/internal/quickaction"

// QuickAction and QuickActionParam are aliased in config.go via internal/config.
type QuickActionEngine = quickaction.Engine

func newQuickActionEngine(cfg *Config) *QuickActionEngine {
	defaultAgent := ""
	if cfg.SmartDispatch.DefaultAgent != "" {
		defaultAgent = cfg.SmartDispatch.DefaultAgent
	}
	return quickaction.NewEngine(cfg.QuickActions, defaultAgent)
}
