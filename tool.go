package main

// tool.go wires internal/tools to the root package via type aliases.
// All tool types and registry logic live in internal/tools/registry.go.

import (
	"context"
	"encoding/json"

	"tetora/internal/provider"
	"tetora/internal/tools"
)

// --- Type Aliases (canonical definitions in internal/tools) ---

type ToolDef = tools.ToolDef
type ToolCall = provider.ToolCall
type ToolHandler = tools.Handler
type ToolResult = tools.Result
type ToolRegistry = tools.Registry

// --- Forwarding Functions ---

func NewToolRegistry(cfg *Config) *ToolRegistry {
	r := tools.NewRegistry()
	registerBuiltins(r, cfg)
	return r
}

// newEmptyRegistry creates an empty registry (no builtins). Used in tests.
func newEmptyRegistry() *ToolRegistry { return tools.NewRegistry() }

var ToolsForProfile = tools.ForProfile
var ToolsForComplexity = tools.ForComplexity

// --- Built-in Tools ---

func registerBuiltins(r *ToolRegistry, cfg *Config) {
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}
	tools.RegisterCoreTools(r, cfg, enabled, buildCoreDeps())
	tools.RegisterMemoryTools(r, cfg, enabled, buildMemoryDeps())
	tools.RegisterLifeTools(r, cfg, enabled, buildLifeDeps())
	tools.RegisterIntegrationTools(r, cfg, enabled, buildIntegrationDeps(cfg))
	tools.RegisterDailyTools(r, cfg, enabled, buildDailyDeps(cfg))
	registerAdminTools(r, cfg, enabled)
	tools.RegisterTaskboardTools(r, cfg, enabled, buildTaskboardDeps(cfg))
	tools.RegisterImageGenTools(r, cfg, enabled, buildImageGenDeps())
}

// registerAdminTools registers admin/ops tools (backup, export, health,
// skills, sentori, create_skill).
func registerAdminTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	if enabled("create_skill") {
		r.Register(&ToolDef{
			Name:        "create_skill",
			Description: "Create a new reusable skill (shell script or Python script) that can be used in future tasks. The skill will need approval before it can execute unless autoApprove is enabled.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name (alphanumeric and hyphens only, max 64 chars)"},
					"description": {"type": "string", "description": "What the skill does"},
					"script": {"type": "string", "description": "The script content (bash or python)"},
					"language": {"type": "string", "enum": ["bash", "python"], "description": "Script language (default: bash)"},
					"matcher": {"type": "object", "properties": {"agents": {"type": "array", "items": {"type": "string"}}, "keywords": {"type": "array", "items": {"type": "string"}}}, "description": "Conditions for auto-injecting this skill"}
				},
				"required": ["name", "description", "script"]
			}`),
			Handler:     createSkillToolHandler,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("backup_now") {
		r.Register(&ToolDef{
			Name:        "backup_now",
			Description: "Trigger an immediate backup of the database",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler:     toolBackupNow,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("export_data") {
		r.Register(&ToolDef{
			Name:        "export_data",
			Description: "Export user data as a ZIP archive (GDPR compliance)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID to filter export (optional)"}
				}
			}`),
			Handler:     toolExportData,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("system_health") {
		r.Register(&ToolDef{
			Name:        "system_health",
			Description: "Get the overall system health status including database, channels, and integrations",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolSystemHealth,
			Builtin: true,
		})
	}

	if enabled("sentori_scan") {
		r.Register(&ToolDef{
			Name:        "sentori_scan",
			Description: "Security scan a skill script for dangerous patterns (exec, path access, exfiltration, env reads, listeners)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Skill name to scan (from store)"},
					"content": {"type": "string", "description": "Raw script content to scan (alternative to name)"}
				}
			}`),
			Handler: toolSentoriScan,
			Builtin: true,
		})
	}
	if enabled("skill_install") {
		r.Register(&ToolDef{
			Name:        "skill_install",
			Description: "Download and install a skill from a URL. Runs Sentori security scan before installation.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to download skill from"},
					"auto_approve": {"type": "boolean", "description": "Auto-approve safe skills (default false)"}
				},
				"required": ["url"]
			}`),
			Handler:     toolSkillInstall,
			Builtin:     true,
			RequireAuth: true,
		})
	}
	if enabled("skill_search") {
		r.Register(&ToolDef{
			Name:        "skill_search",
			Description: "Search the skill registry for installable skills by keyword",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"}
				},
				"required": ["query"]
			}`),
			Handler: toolSkillSearch,
			Builtin: true,
		})
	}
}

// --- Memory Tool Compatibility Wrappers (merged from tool_memory.go) ---
// Registration moved to internal/tools/memory.go.
// Wrappers below are used by tests that call handlers directly.

var memoryDepsForTest = tools.MemoryDeps{
	GetMemory: getMemory,
	SetMemory: func(cfg *Config, role, key, value string) error {
		return setMemory(cfg, role, key, value)
	},
	DeleteMemory: deleteMemory,
	SearchMemory: func(cfg *Config, role, query string) ([]tools.MemoryEntry, error) {
		entries, err := searchMemoryFS(cfg, role, query)
		if err != nil {
			return nil, err
		}
		result := make([]tools.MemoryEntry, len(entries))
		for i, e := range entries {
			result[i] = tools.MemoryEntry{Key: e.Key, Value: e.Value}
		}
		return result, nil
	},
}

func toolMemorySearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeMemorySearchHandler(memoryDepsForTest)
	return h(ctx, cfg, input)
}

func toolMemoryGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeMemoryGetHandler(memoryDepsForTest)
	return h(ctx, cfg, input)
}

func toolKnowledgeSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	h := tools.MakeKnowledgeSearchHandler()
	return h(ctx, cfg, input)
}

// --- ImageGen Compatibility Aliases (merged from tool_imagegen.go) ---
// Handler implementations are in internal/tools/imagegen.go.

// Type alias for backwards compat (used by App struct, tests).
type imageGenLimiter = tools.ImageGenLimiter

// globalImageGenLimiter is the default limiter instance.
var globalImageGenLimiter = &tools.ImageGenLimiter{}

// estimateImageCost forwards to tools.EstimateImageCost for test compat.
var estimateImageCost = tools.EstimateImageCost

// imageGenBaseURL forwards to tools.ImageGenBaseURL for test overrides.
var imageGenBaseURL = tools.ImageGenBaseURL

// toolImageGenerate wraps internal/tools image handler for test compat.
func toolImageGenerate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	// Set the shared base URL before calling.
	tools.ImageGenBaseURL = imageGenBaseURL
	deps := tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			return globalImageGenLimiter
		},
	}
	handler := tools.MakeImageGenerateHandler(deps)
	return handler(ctx, cfg, input)
}

// toolImageGenerateStatus wraps internal/tools status handler for test compat.
func toolImageGenerateStatus(ctx context.Context, cfg *Config, _ json.RawMessage) (string, error) {
	tools.ImageGenBaseURL = imageGenBaseURL
	deps := tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			return globalImageGenLimiter
		},
	}
	handler := tools.MakeImageGenerateStatusHandler(deps)
	return handler(ctx, cfg, nil)
}
