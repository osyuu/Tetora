package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- MCP Server Bridge ---
// Implements a stdio JSON-RPC MCP server that proxies requests to Tetora's HTTP API.
// Usage: tetora mcp-server
// Claude Code connects to this as an MCP server via ~/.tetora/mcp/bridge.json.

// mcpBridgeTool defines an MCP tool that maps to a Tetora HTTP API endpoint.
type mcpBridgeTool struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	InputSchema json.RawMessage   `json:"inputSchema"`
	Method      string            `json:"-"` // HTTP method
	Path        string            `json:"-"` // HTTP path template (e.g. "/memory/{agent}/{key}")
	PathParams  []string          `json:"-"` // params extracted from URL path
}

// mcpBridgeServer implements the MCP server protocol over stdio.
type mcpBridgeServer struct {
	baseURL string
	token   string
	tools   []mcpBridgeTool
	mu      sync.Mutex
	nextID  int
}

func newMCPBridgeServer(listenAddr, token string) *mcpBridgeServer {
	scheme := "http"
	if !strings.HasPrefix(listenAddr, ":") && !strings.Contains(listenAddr, "://") {
		listenAddr = "localhost" + listenAddr
	} else if strings.HasPrefix(listenAddr, ":") {
		listenAddr = "localhost" + listenAddr
	}

	return &mcpBridgeServer{
		baseURL: scheme + "://" + listenAddr,
		token:   token,
		tools:   mcpBridgeTools(),
	}
}

// mcpBridgeTools returns the list of MCP tools exposed by the bridge.
func mcpBridgeTools() []mcpBridgeTool {
	return []mcpBridgeTool{
		{
			Name:        "tetora_taskboard_list",
			Description: "List kanban board tickets. Optional filters: project, assignee, priority.",
			Method:      "GET",
			Path:        "/api/tasks/board",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"project":  {"type": "string", "description": "Filter by project name"},
					"assignee": {"type": "string", "description": "Filter by assignee"},
					"priority": {"type": "string", "description": "Filter by priority (P0-P4)"}
				}
			}`),
		},
		{
			Name:        "tetora_taskboard_update",
			Description: "Update a task on the kanban board (status, assignee, priority, etc).",
			Method:      "PATCH",
			Path:        "/api/tasks/{id}",
			PathParams:  []string{"id"},
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id":       {"type": "string", "description": "Task ID"},
					"status":   {"type": "string", "description": "New status (todo/in_progress/review/done)"},
					"assignee": {"type": "string", "description": "New assignee"},
					"priority": {"type": "string", "description": "New priority (P0-P4)"},
					"title":    {"type": "string", "description": "New title"}
				},
				"required": ["id"]
			}`),
		},
		{
			Name:        "tetora_taskboard_comment",
			Description: "Add a comment to a kanban board task.",
			Method:      "POST",
			Path:        "/api/tasks/{id}/comments",
			PathParams:  []string{"id"},
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id":      {"type": "string", "description": "Task ID"},
					"comment": {"type": "string", "description": "Comment text"},
					"author":  {"type": "string", "description": "Comment author (agent name)"}
				},
				"required": ["id", "comment"]
			}`),
		},
		{
			Name:        "tetora_memory_get",
			Description: "Read a memory entry for an agent. Returns the stored value.",
			Method:      "GET",
			Path:        "/memory/{agent}/{key}",
			PathParams:  []string{"agent", "key"},
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent": {"type": "string", "description": "Agent/role name"},
					"key":   {"type": "string", "description": "Memory key"}
				},
				"required": ["agent", "key"]
			}`),
		},
		{
			Name:        "tetora_memory_set",
			Description: "Write a memory entry for an agent.",
			Method:      "POST",
			Path:        "/memory",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent": {"type": "string", "description": "Agent/role name"},
					"key":   {"type": "string", "description": "Memory key"},
					"value": {"type": "string", "description": "Value to store"}
				},
				"required": ["agent", "key", "value"]
			}`),
		},
		{
			Name:        "tetora_memory_search",
			Description: "List all memory entries, optionally filtered by role.",
			Method:      "GET",
			Path:        "/memory",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"role": {"type": "string", "description": "Filter by role/agent name"}
				}
			}`),
		},
		{
			Name:        "tetora_dispatch",
			Description: "Dispatch a task to another agent via Tetora. Creates a new Claude Code session.",
			Method:      "POST",
			Path:        "/dispatch",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prompt":  {"type": "string", "description": "Task prompt/instructions"},
					"agent":   {"type": "string", "description": "Target agent name"},
					"workdir": {"type": "string", "description": "Working directory for the task"},
					"model":   {"type": "string", "description": "Model to use (optional)"}
				},
				"required": ["prompt"]
			}`),
		},
		{
			Name:        "tetora_knowledge_search",
			Description: "Search the shared knowledge base for relevant information.",
			Method:      "GET",
			Path:        "/knowledge/search",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"q":     {"type": "string", "description": "Search query"},
					"limit": {"type": "integer", "description": "Max results (default 10)"}
				},
				"required": ["q"]
			}`),
		},
		{
			Name:        "tetora_notify",
			Description: "Send a notification to the user via Discord/Telegram.",
			Method:      "POST",
			Path:        "/api/hooks/notify",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "Notification message"},
					"level":   {"type": "string", "description": "Notification level: info, warn, error (default: info)"}
				},
				"required": ["message"]
			}`),
		},
		{
			Name:        "tetora_ask_user",
			Description: "Ask the user a question via Discord. Use when you need user input. The user will see buttons for options and can also type a custom answer. This blocks until the user responds (up to 6 minutes).",
			Method:      "POST",
			Path:        "/api/hooks/ask-user",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"question": {"type": "string", "description": "The question to ask the user"},
					"options":  {"type": "array", "items": {"type": "string"}, "description": "Optional quick-reply buttons (max 4)"}
				},
				"required": ["question"]
			}`),
		},
	}
}

// Run starts the MCP bridge server, reading JSON-RPC from stdin and writing to stdout.
func (s *mcpBridgeServer) Run() error {
	reader := bufio.NewReader(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}

		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      0,
				Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
			}
			if err := encoder.Encode(resp); err != nil {
				fmt.Fprintf(os.Stderr, "mcp: encode response: %v\n", err)
			}
			continue
		}

		// JSON-RPC 2.0: notifications must not receive a response.
		if req.Method == "initialized" || strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		resp := s.handleRequest(&req)
		if err := encoder.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: encode response: %v\n", err)
		}
	}
}

func (s *mcpBridgeServer) handleRequest(req *jsonRPCRequest) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "ping":
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *mcpBridgeServer) handleInitialize(req *jsonRPCRequest) jsonRPCResponse {
	result := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "tetora",
			"version": tetoraVersion,
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, -32603, "internal: marshal failed")
	}
	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func (s *mcpBridgeServer) handleToolsList(req *jsonRPCRequest) jsonRPCResponse {
	type toolDef struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}

	tools := make([]toolDef, len(s.tools))
	for i, t := range s.tools {
		tools[i] = toolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}

	result := map[string]any{"tools": tools}
	data, err := json.Marshal(result)
	if err != nil {
		return s.errorResponse(req.ID, -32603, "internal: marshal failed")
	}
	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: data}
}

func (s *mcpBridgeServer) handleToolsCall(req *jsonRPCRequest) jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	paramData, err := json.Marshal(req.Params)
	if err != nil {
		return s.errorResponse(req.ID, -32602, "invalid params")
	}
	if err := json.Unmarshal(paramData, &params); err != nil {
		return s.errorResponse(req.ID, -32602, "invalid params: "+err.Error())
	}

	// Find the tool.
	var tool *mcpBridgeTool
	for i := range s.tools {
		if s.tools[i].Name == params.Name {
			tool = &s.tools[i]
			break
		}
	}
	if tool == nil {
		return s.errorResponse(req.ID, -32602, "unknown tool: "+params.Name)
	}

	// Parse arguments.
	var args map[string]any
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return s.errorResponse(req.ID, -32602, "invalid arguments: "+err.Error())
		}
	}
	if args == nil {
		args = make(map[string]any)
	}

	// Build HTTP request path (substitute path params).
	path := tool.Path
	for _, p := range tool.PathParams {
		val, ok := args[p]
		if !ok {
			return s.errorResponse(req.ID, -32602, "missing required param: "+p)
		}
		valStr := fmt.Sprint(val)
		if strings.Contains(valStr, "/") {
			return s.errorResponse(req.ID, -32602, fmt.Sprintf("param %q must not contain '/'", p))
		}
		path = strings.Replace(path, "{"+p+"}", url.PathEscape(valStr), 1)
		delete(args, p) // Remove from body/query
	}

	// Execute HTTP request.
	result, err := s.doHTTP(tool.Method, path, args)
	if err != nil {
		return s.errorResponse(req.ID, -32603, err.Error())
	}

	// Format as MCP tool result.
	content := []map[string]any{
		{
			"type": "text",
			"text": string(result),
		},
	}
	respData, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return s.errorResponse(req.ID, -32603, "internal: marshal failed")
	}
	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: respData}
}

// doHTTP executes an HTTP request against the Tetora API.
func (s *mcpBridgeServer) doHTTP(method, path string, args map[string]any) ([]byte, error) {
	reqURL := s.baseURL + path

	var body io.Reader
	if method == "GET" {
		// Add args as query parameters.
		if len(args) > 0 {
			q := url.Values{}
			for k, v := range args {
				q.Set(k, fmt.Sprint(v))
			}
			reqURL += "?" + q.Encode()
		}
	} else {
		// POST/PATCH/PUT — send as JSON body.
		data, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		body = strings.NewReader(string(data))
	}

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	req.Header.Set("X-Tetora-Source", "mcp-bridge")

	// Long-poll endpoints need extended timeout.
	timeout := 30 * time.Second
	if strings.Contains(path, "/api/hooks/ask-user") || strings.Contains(path, "/api/hooks/plan-gate") {
		timeout = 7 * time.Minute
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (s *mcpBridgeServer) errorResponse(id int, code int, msg string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg},
	}
}

// --- MCP Bridge Config File Generation ---

// generateMCPBridgeConfig creates the ~/.tetora/mcp/bridge.json config file
// that Claude Code uses to connect to the Tetora MCP server.
func generateMCPBridgeConfig(cfg *Config) error {
	baseDir := cfg.BaseDir
	if baseDir == "" {
		homeDir, _ := os.UserHomeDir()
		baseDir = filepath.Join(homeDir, ".tetora")
	}

	mcpDir := filepath.Join(baseDir, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		return fmt.Errorf("create mcp dir: %w", err)
	}

	// Find the tetora binary path.
	tetoraPath, err := os.Executable()
	if err != nil {
		tetoraPath = "tetora" // fallback
	}

	bridgeConfig := map[string]any{
		"mcpServers": map[string]any{
			"tetora": map[string]any{
				"command": tetoraPath,
				"args":    []string{"mcp-server"},
			},
		},
	}

	data, err := json.MarshalIndent(bridgeConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	configPath := filepath.Join(mcpDir, "bridge.json")
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// cmdMCPServer is the entry point for `tetora mcp-server`.
func cmdMCPServer() {
	cfg := loadConfig("")

	// Generate bridge config on first run.
	if err := generateMCPBridgeConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to generate bridge config: %v\n", err)
	}

	server := newMCPBridgeServer(cfg.ListenAddr, cfg.APIToken)
	if err := server.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-server error: %v\n", err)
		os.Exit(1)
	}
}
