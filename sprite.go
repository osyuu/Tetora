package main

import (
	"strings"
	"tetora/internal/sprite"
)

// --- Sprite State Constants ---

const (
	SpriteIdle      = sprite.Idle
	SpriteWork      = sprite.Work
	SpriteThink     = sprite.Think
	SpriteTalk      = sprite.Talk
	SpriteReview    = sprite.Review
	SpriteCelebrate = sprite.Celebrate
	SpriteError     = sprite.Error

	SpriteWalkDown  = sprite.WalkDown
	SpriteWalkUp    = sprite.WalkUp
	SpriteWalkLeft  = sprite.WalkLeft
	SpriteWalkRight = sprite.WalkRight
)

// --- Type Aliases ---

type SpriteConfig = sprite.Config
type SpriteStateDef = sprite.StateDef
type AgentSpriteDef = sprite.AgentDef
type agentSpriteTracker = sprite.Tracker

// --- Wrapper Functions ---

func defaultSpriteConfig() SpriteConfig                              { return sprite.DefaultConfig() }
func loadSpriteConfig(dir string, keys []string) SpriteConfig        { return sprite.LoadConfig(dir, keys) }
func initSpriteConfig(dir string) error                              { return sprite.InitConfig(dir) }
func newAgentSpriteTracker() *agentSpriteTracker                     { return sprite.NewTracker() }

// --- State Resolution ---

// isChatSource returns true if the task source indicates a chat conversation.
// Uses chatSources map from classify.go.
func isChatSource(source string) bool {
	s := strings.ToLower(source)
	// Source may include channel suffix like "discord:12345".
	if i := strings.IndexByte(s, ':'); i > 0 {
		s = s[:i]
	}
	return chatSources[s]
}

// resolveAgentSprite determines the sprite state from dispatch/task context.
// Priority: error > celebrate > review > talk > think > work > idle.
func resolveAgentSprite(taskStatus, dispatchStatus, source string) string {
	switch taskStatus {
	case "failed", "error":
		return SpriteError
	case "done", "success":
		return SpriteCelebrate
	case "review":
		return SpriteReview
	}

	if isChatSource(source) && (taskStatus == "running" || taskStatus == "doing") {
		return SpriteTalk
	}

	switch dispatchStatus {
	case "dispatching":
		return SpriteThink
	}

	switch taskStatus {
	case "running", "doing", "processing":
		return SpriteWork
	}

	return SpriteIdle
}
