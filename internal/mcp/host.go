package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
	"tetora/internal/tools"
)

// ServerStatus provides status information for API endpoints.
type ServerStatus struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Tools     []string `json:"tools"`
	Restarts  int      `json:"restarts"`
	LastError string   `json:"lastError,omitempty"`
}

// Host manages the lifecycle of MCP server processes.
type Host struct {
	Servers  map[string]*Server
	Mu       sync.RWMutex
	Cfg      *config.Config
	ToolReg  *tools.Registry
	Ctx      context.Context
	Cancel   context.CancelFunc
	stopOnce sync.Once
}

// Server represents a single MCP server process.
type Server struct {
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	Cmd       *exec.Cmd
	Stdin     io.WriteCloser
	Stdout    *bufio.Reader
	Tools     []tools.ToolDef
	Mu        sync.Mutex
	nextID    uint64
	Status    string // "starting", "running", "stopped", "error"
	LastError string
	Restarts  int
	Ctx       context.Context
	Cancel    context.CancelFunc
	ParentCtx context.Context
	ToolReg   *tools.Registry

	Pending    map[int]chan *JSONRPCResponse
	PendingMu  sync.Mutex
	ReaderDone chan struct{}
	waitOnce   sync.Once
}

// NewHost creates a new MCP host.
func NewHost(cfg *config.Config, toolReg *tools.Registry) *Host {
	return &Host{
		Servers: make(map[string]*Server),
		Cfg:     cfg,
		ToolReg: toolReg,
	}
}

// Start initializes and starts all configured MCP servers.
func (h *Host) Start(ctx context.Context) error {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	h.Ctx, h.Cancel = context.WithCancel(ctx)

	go func() {
		<-h.Ctx.Done()
		h.Stop()
	}()

	var wg sync.WaitGroup
	for name, serverCfg := range h.Cfg.MCPServers {
		if serverCfg.Enabled != nil && !*serverCfg.Enabled {
			log.Info("MCP server %s disabled, skipping", name)
			continue
		}

		server := &Server{
			Name:      name,
			Command:   serverCfg.Command,
			Args:      serverCfg.Args,
			Env:       serverCfg.Env,
			Status:    "starting",
			ParentCtx: h.Ctx,
			ToolReg:   h.ToolReg,
		}
		server.Ctx, server.Cancel = context.WithCancel(h.Ctx)

		h.Servers[name] = server

		wg.Add(1)
		go func(s *Server) {
			defer wg.Done()
			if err := s.Start(s.Ctx); err != nil {
				s.Mu.Lock()
				s.Status = "error"
				s.LastError = err.Error()
				s.Mu.Unlock()
				log.Error("MCP server %s failed to start: %v", s.Name, err)
				return
			}
			go s.MonitorHealth()
		}(server)
	}

	wg.Wait()
	return nil
}

// Stop shuts down all MCP servers. Safe to call multiple times.
func (h *Host) Stop() {
	h.stopOnce.Do(func() {
		if h.Cancel != nil {
			h.Cancel()
		}

		h.Mu.Lock()
		defer h.Mu.Unlock()

		for _, server := range h.Servers {
			server.Stop()
		}
	})
}

// RestartServer restarts a specific MCP server.
func (h *Host) RestartServer(name string) error {
	h.Mu.Lock()
	server, ok := h.Servers[name]
	h.Mu.Unlock()

	if !ok {
		return fmt.Errorf("server %s not found", name)
	}

	server.Stop()

	server.Mu.Lock()
	server.Status = "starting"
	server.Restarts++
	server.ParentCtx = h.Ctx
	server.Ctx, server.Cancel = context.WithCancel(h.Ctx)
	server.Mu.Unlock()

	go func() {
		if err := server.Start(server.Ctx); err != nil {
			server.Mu.Lock()
			server.Status = "error"
			server.LastError = err.Error()
			server.Mu.Unlock()
			log.Error("MCP server %s restart failed: %v", server.Name, err)
			return
		}
		go server.MonitorHealth()
	}()

	return nil
}

// ServerStatus returns the status of all MCP servers.
func (h *Host) ServerStatus() []ServerStatus {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	result := make([]ServerStatus, 0, len(h.Servers))
	for _, server := range h.Servers {
		server.Mu.Lock()
		toolNames := make([]string, len(server.Tools))
		for i, t := range server.Tools {
			toolNames[i] = t.Name
		}
		result = append(result, ServerStatus{
			Name:      server.Name,
			Status:    server.Status,
			Tools:     toolNames,
			Restarts:  server.Restarts,
			LastError: server.LastError,
		})
		server.Mu.Unlock()
	}

	return result
}

// GetServer retrieves a server by name. Returns nil if not found.
func (h *Host) GetServer(name string) *Server {
	h.Mu.RLock()
	defer h.Mu.RUnlock()
	return h.Servers[name]
}

// Start spawns the MCP server process and initializes the connection.
func (s *Server) Start(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.Command, s.Args...)

	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	s.Stdin = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	s.Stdout = bufio.NewReader(stdout)

	cmd.Stderr = &stderrWriter{serverName: s.Name}

	s.Cmd = cmd
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	log.Info("MCP server %s started (PID %d)", s.Name, cmd.Process.Pid)

	s.Pending = make(map[int]chan *JSONRPCResponse)
	s.ReaderDone = make(chan struct{})
	s.waitOnce = sync.Once{}
	go s.RunReader()

	if err := s.initialize(); err != nil {
		s.Stdin.Close()
		s.Cmd.Process.Kill()
		<-s.ReaderDone
		return fmt.Errorf("initialize: %w", err)
	}

	discovered, err := s.discoverTools()
	if err != nil {
		s.Stdin.Close()
		s.Cmd.Process.Kill()
		<-s.ReaderDone
		return fmt.Errorf("discover tools: %w", err)
	}

	s.Mu.Lock()
	s.Tools = discovered
	s.Status = "running"
	s.Mu.Unlock()

	log.Info("MCP server %s running with %d tools", s.Name, len(discovered))

	return nil
}

// Stop gracefully stops the MCP server.
func (s *Server) Stop() {
	s.Mu.Lock()
	if s.Status == "stopped" {
		s.Mu.Unlock()
		return
	}

	s.Status = "stopped"

	if s.Cancel != nil {
		s.Cancel()
	}

	if s.Stdin != nil {
		closeReq := JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "notifications/close",
		}
		data, _ := json.Marshal(closeReq)
		s.Stdin.Write(append(data, '\n'))
		s.Stdin.Close()
	}

	if s.Cmd != nil && s.Cmd.Process != nil {
		s.Cmd.Process.Kill()
	}
	s.Mu.Unlock()

	if s.ReaderDone != nil {
		<-s.ReaderDone
	}

	s.waitOnce.Do(func() {
		if s.Cmd != nil {
			s.Cmd.Wait()
		}
	})

	log.Info("MCP server %s stopped", s.Name)
}

// initialize performs the MCP initialization handshake.
func (s *Server) initialize() error {
	params := initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities: map[string]interface{}{
			"roots": map[string]interface{}{
				"listChanged": true,
			},
		},
	}
	params.ClientInfo.Name = "tetora"
	params.ClientInfo.Version = "2.0"

	resp, err := s.SendRequest(s.Ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	var result initializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse initialize result: %w", err)
	}

	log.Debug("MCP server %s initialized: %s %s", s.Name, result.ServerInfo.Name, result.ServerInfo.Version)

	initNotif := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	data, _ := json.Marshal(initNotif)
	if _, err := s.Stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}

	return nil
}

// discoverTools discovers available tools from the server and registers them.
func (s *Server) discoverTools() ([]tools.ToolDef, error) {
	resp, err := s.SendRequest(s.Ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list request: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	var result toolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}

	discovered := make([]tools.ToolDef, 0, len(result.Tools))
	for _, t := range result.Tools {
		toolName := fmt.Sprintf("mcp:%s:%s", s.Name, t.Name)
		toolDef := tools.ToolDef{
			Name:        toolName,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Handler:     s.MakeToolHandler(t.Name),
			Builtin:     false,
		}
		discovered = append(discovered, toolDef)

		if s.ToolReg != nil {
			s.ToolReg.Register(&toolDef)
			log.Debug("registered MCP tool: %s", toolName)
		}
	}

	return discovered, nil
}

// MakeToolHandler creates a tool handler that forwards to the MCP server.
func (s *Server) MakeToolHandler(toolName string) tools.Handler {
	return func(ctx context.Context, cfg *config.Config, input json.RawMessage) (string, error) {
		return s.CallTool(ctx, toolName, input)
	}
}

// CallTool calls a tool on the MCP server.
func (s *Server) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	s.Mu.Lock()
	if s.Status != "running" {
		s.Mu.Unlock()
		return "", fmt.Errorf("server not running: %s", s.Status)
	}
	s.Mu.Unlock()

	params := toolsCallParams{
		Name:      name,
		Arguments: args,
	}

	resp, err := s.SendRequest(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("tools/call request: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("tools/call error: %s", resp.Error.Message)
	}

	var result toolsCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse tools/call result: %w", err)
	}

	var output string
	for _, c := range result.Content {
		if c.Type == "text" {
			output += c.Text
		}
	}

	return output, nil
}

// SendRequest sends a JSON-RPC request and waits for the demuxed response.
func (s *Server) SendRequest(ctx context.Context, method string, params interface{}) (*JSONRPCResponse, error) {
	s.Mu.Lock()
	s.nextID++
	id := s.nextID
	s.Mu.Unlock()

	intID := int(id)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      intID,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan *JSONRPCResponse, 1)
	s.PendingMu.Lock()
	s.Pending[intID] = ch
	s.PendingMu.Unlock()

	s.Mu.Lock()
	if s.Stdin == nil {
		s.Mu.Unlock()
		s.PendingMu.Lock()
		delete(s.Pending, intID)
		s.PendingMu.Unlock()
		return nil, fmt.Errorf("stdin closed")
	}
	_, err = s.Stdin.Write(append(data, '\n'))
	s.Mu.Unlock()

	if err != nil {
		s.PendingMu.Lock()
		delete(s.Pending, intID)
		s.PendingMu.Unlock()
		return nil, fmt.Errorf("write request: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("reader closed before response received")
		}
		return resp, nil
	case <-ctx.Done():
		s.PendingMu.Lock()
		delete(s.Pending, intID)
		s.PendingMu.Unlock()
		return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
	case <-s.Ctx.Done():
		s.PendingMu.Lock()
		delete(s.Pending, intID)
		s.PendingMu.Unlock()
		return nil, fmt.Errorf("context cancelled: %w", s.Ctx.Err())
	}
}

// RunReader is the single goroutine that reads stdout and demuxes responses by ID.
func (s *Server) RunReader() {
	defer func() {
		s.PendingMu.Lock()
		for id, ch := range s.Pending {
			close(ch)
			delete(s.Pending, id)
		}
		s.PendingMu.Unlock()

		s.waitOnce.Do(func() {
			if s.Cmd != nil {
				s.Cmd.Wait()
			}
		})
		close(s.ReaderDone)
	}()

	for {
		line, err := s.Stdout.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				log.Debug("MCP server %s reader: %v", s.Name, err)
			}
			return
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Warn("MCP server %s: invalid JSON from stdout: %v", s.Name, err)
			continue
		}

		if resp.ID == 0 {
			log.Debug("MCP server %s: notification received", s.Name)
			continue
		}

		s.PendingMu.Lock()
		ch, ok := s.Pending[resp.ID]
		if ok {
			delete(s.Pending, resp.ID)
		}
		s.PendingMu.Unlock()

		if ok {
			ch <- &resp
		} else {
			log.Warn("MCP server %s: unexpected response ID %d", s.Name, resp.ID)
		}
	}
}

// MonitorHealth monitors the server process and restarts on failure.
func (s *Server) MonitorHealth() {
	if s.ReaderDone == nil {
		return
	}

	<-s.ReaderDone

	s.Mu.Lock()

	if s.Status == "stopped" {
		s.Mu.Unlock()
		return
	}

	s.Status = "error"
	s.LastError = "process exited unexpectedly"

	if s.Restarts < 3 && s.ParentCtx.Err() == nil {
		s.Restarts++
		restarts := s.Restarts
		log.Warn("MCP server %s crashed (restart %d/3), restarting...", s.Name, restarts)

		backoff := time.Duration(restarts) * 2 * time.Second

		s.Status = "starting"
		s.Ctx, s.Cancel = context.WithCancel(s.ParentCtx)
		s.Mu.Unlock()

		time.Sleep(backoff)

		if err := s.Start(s.Ctx); err != nil {
			s.Mu.Lock()
			s.Status = "error"
			s.LastError = err.Error()
			s.Mu.Unlock()
			log.Error("MCP server %s restart failed: %v", s.Name, err)
			return
		}
		s.MonitorHealth()
	} else {
		log.Error("MCP server %s crashed, max restarts exceeded", s.Name)
		s.Mu.Unlock()
	}
}

// stderrWriter forwards MCP server stderr to our logs.
type stderrWriter struct {
	serverName string
}

func (w *stderrWriter) Write(p []byte) (n int, err error) {
	log.Warn("MCP server %s stderr: %s", w.serverName, string(p))
	return len(p), nil
}
