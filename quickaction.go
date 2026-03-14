package main

import "tetora/internal/quickaction"

type QuickAction = quickaction.Action
type QuickActionParam = quickaction.Param
type QuickActionEngine = quickaction.Engine

func newQuickActionEngine(cfg *Config) *QuickActionEngine {
	defaultAgent := ""
	if cfg.SmartDispatch.DefaultAgent != "" {
		defaultAgent = cfg.SmartDispatch.DefaultAgent
	}
	return quickaction.NewEngine(cfg.QuickActions, defaultAgent)
}
