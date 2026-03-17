package main

import (
	"context"
	"encoding/json"
	"sync"

	"tetora/internal/classify"
	"tetora/internal/provider"
)

// --- Tool Types ---

// ToolDef defines a tool that can be called by agents.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Handler     ToolHandler     `json:"-"`
	Builtin     bool            `json:"-"`
	RequireAuth bool            `json:"requireAuth,omitempty"`
}

// ToolCall is an alias for provider.ToolCall.
type ToolCall = provider.ToolCall

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolHandler is a function that executes a tool.
type ToolHandler func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error)

// --- Tool Registry ---

// ToolRegistry manages available tools.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
}

// NewToolRegistry creates a new tool registry with built-in tools.
func NewToolRegistry(cfg *Config) *ToolRegistry {
	r := &ToolRegistry{
		tools: make(map[string]*ToolDef),
	}
	r.registerBuiltins(cfg)
	return r
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(tool *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = tool
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools.
func (r *ToolRegistry) List() []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ListFiltered returns tools whose Name is in the allowed map.
// If allowed is nil or empty, returns all tools (backward compat).
func (r *ToolRegistry) ListFiltered(allowed map[string]bool) []*ToolDef {
	if len(allowed) == 0 {
		return r.List()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ToolDef, 0, len(allowed))
	for _, t := range r.tools {
		if allowed[t.Name] {
			result = append(result, t)
		}
	}
	return result
}

// ListForProvider serializes tools for API calls (no Handler field).
func (r *ToolRegistry) ListForProvider() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]map[string]any, 0, len(r.tools))
	for _, t := range r.tools {
		var schema map[string]any
		if len(t.InputSchema) > 0 {
			json.Unmarshal(t.InputSchema, &schema)
		}
		result = append(result, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": schema,
		})
	}
	return result
}

// --- Tool Profiles ---

// toolProfileSets defines which tools are included in each profile.
var toolProfileSets = map[string][]string{
	"minimal": {
		"memory_get", "memory_search", "knowledge_search",
		"web_search", "agent_dispatch",
	},
	"standard": {
		"memory_get", "memory_search", "memory_store", "memory_recall",
		"memory_um_search", "memory_forget",
		"knowledge_search", "web_search", "web_fetch",
		"agent_dispatch", "lesson_record",
		"task_create", "task_list", "task_update",
		"file_read", "file_write",
		"taskboard_list", "taskboard_get", "taskboard_create",
		"taskboard_move", "taskboard_comment", "taskboard_decompose",
	},
	// "full" = all tools (no filtering)
}

// ToolsForProfile returns the allowed tool set for a given profile name.
// Returns nil for "full" or unknown profiles (which means all tools).
func ToolsForProfile(profile string) map[string]bool {
	tools, ok := toolProfileSets[profile]
	if !ok || profile == "full" {
		return nil // nil = all tools
	}
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t] = true
	}
	return allowed
}

// ToolsForComplexity returns the tool profile name appropriate for the given request complexity.
func ToolsForComplexity(c classify.Complexity) string {
	switch c {
	case classify.Simple:
		return "none"
	case classify.Standard:
		return "standard"
	default:
		return "full"
	}
}

// --- Built-in Tools ---

func (r *ToolRegistry) registerBuiltins(cfg *Config) {
	enabled := func(name string) bool {
		if cfg.Tools.Builtin == nil {
			return true
		}
		e, ok := cfg.Tools.Builtin[name]
		return !ok || e
	}
	registerCoreTools(r, cfg, enabled)
	registerMemoryTools(r, cfg, enabled)
	registerLifeTools(r, cfg, enabled)
	registerIntegrationTools(r, cfg, enabled)
	registerDailyTools(r, cfg, enabled)
	registerAdminTools(r, cfg, enabled)
	registerTaskboardTools(r, cfg, enabled)
}

// registerAdminTools registers admin/ops tools (backup, export, health,
// skills, sentori, create_skill).
// Note: most handler functions are defined in their own files (ops.go, skill_install.go, etc.).
func registerAdminTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	// --- P18.4: Self-Improving Skills ---
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

	// --- P23.7: Reliability & Operations Tools ---
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

	// --- P27.1: Skill Install + Sentori Scanner ---
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
