package main

import (
	"embed"
	"net/http"
	"strings"

	"tetora/internal/classify"
	"tetora/internal/httpapi"
	"tetora/internal/sprite"
)

//go:embed dashboard.html
var dashboardHTML []byte

//go:embed office_bg.webp
var officeBgWebp []byte

//go:embed sprite_ruri.png sprite_hisui.png sprite_kokuyou.png sprite_kohaku.png sprite_default.png
var spriteFS embed.FS

//go:embed README.md README.*.md INSTALL.md CHANGELOG.md ROADMAP.md CONTRIBUTING.md docs/*.md
var docsFS embed.FS

var supportedDocsLangs = []string{"zh-TW", "ja", "ko", "id", "th", "fil", "es", "fr", "de"}

var docsList = []httpapi.DocsPageEntry{
	{Name: "README", File: "README.md", Description: "Project Overview"},
	{Name: "Configuration", File: "docs/configuration.md", Description: "Config Reference"},
	{Name: "Workflows", File: "docs/workflow.md", Description: "Workflow Engine"},
	{Name: "Taskboard", File: "docs/taskboard.md", Description: "Kanban & Auto-Dispatch"},
	{Name: "Hooks", File: "docs/hooks.md", Description: "Claude Code Hooks"},
	{Name: "MCP", File: "docs/mcp.md", Description: "Model Context Protocol"},
	{Name: "Discord Multitasking", File: "docs/discord-multitasking.md", Description: "Thread & Focus"},
	{Name: "Troubleshooting", File: "docs/troubleshooting.md", Description: "Common Issues"},
	{Name: "Changelog", File: "CHANGELOG.md", Description: "Release History"},
	{Name: "Roadmap", File: "ROADMAP.md", Description: "Future Plans"},
	{Name: "Contributing", File: "CONTRIBUTING.md", Description: "Contributor Guide"},
	{Name: "Installation", File: "INSTALL.md", Description: "Setup Guide"},
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(dashboardHTML)
}

func handleOfficeBg(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(officeBgWebp)
}

func handleSprite(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/dashboard/sprites/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	data, err := spriteFS.ReadFile("sprite_" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Detect content type from extension
	ct := "image/png"
	if strings.HasSuffix(name, ".webp") {
		ct = "image/webp"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// registerDocsRoutesVia delegates to httpapi.RegisterDocsRoutes with embedded FS.
func registerDocsRoutesVia(mux *http.ServeMux) {
	httpapi.RegisterDocsRoutes(mux, docsFS, docsList, supportedDocsLangs)
}

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
func isChatSource(source string) bool {
	s := strings.ToLower(source)
	if i := strings.IndexByte(s, ':'); i > 0 {
		s = s[:i]
	}
	return classify.ChatSources[s]
}

// resolveAgentSprite determines the sprite state from dispatch/task context.
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
