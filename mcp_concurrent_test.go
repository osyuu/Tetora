package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Mock MCP server helper process ---
//
// When this test binary is invoked with GO_TEST_HELPER_PROCESS=1 it acts as a
// minimal stdio MCP server instead of running tests. This lets TestMCPServerStart
// exercise start/initialize/discoverTools/callTool/stop without spawning an
// external dependency.

func init() {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		return
	}
	// Run as mock MCP server and exit.
	runMockMCPServer()
	os.Exit(0)
}

// runMockMCPServer is a minimal MCP server that:
//   1. Responds to "initialize" with a valid initializeResult.
//   2. Responds to "tools/list" with one mock tool.
//   3. Responds to "tools/call" with a text content result.
//   4. Exits cleanly on notifications/close or EOF.
func runMockMCPServer() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	writeResp := func(id int, result interface{}) {
		resultJSON, err := json.Marshal(result)
		if err != nil {
			log.Fatalf("runMockMCPServer: marshal result: %v", err)
		}
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  json.RawMessage(resultJSON),
		}
		data, err := json.Marshal(resp)
		if err != nil {
			log.Fatalf("runMockMCPServer: marshal response: %v", err)
		}
		writer.Write(append(data, '\n'))
		writer.Flush()
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		switch req.Method {
		case "initialize":
			writeResp(req.ID, initializeResult{
				ProtocolVersion: mcpProtocolVersion,
				Capabilities:    map[string]interface{}{},
				ServerInfo: struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				}{Name: "mock-server", Version: "1.0"},
			})

		case "notifications/initialized":
			// No response for notifications.

		case "tools/list":
			writeResp(req.ID, toolsListResult{
				Tools: []struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					InputSchema json.RawMessage `json:"inputSchema"`
				}{
					{
						Name:        "mock_tool",
						Description: "A mock tool for testing",
						InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
					},
				},
			})

		case "tools/call":
			writeResp(req.ID, toolsCallResult{
				Content: []struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}{
					{Type: "text", Text: "mock result"},
				},
			})

		case "notifications/close":
			return
		}
	}
}

// --- Test infrastructure ---

// setupMockMCPServer creates an MCPServer backed by in-memory pipes.
// Returns the server, a reader for the outbound request pipe (what the server writes),
// and a writer for the inbound response pipe (what the server reads).
func setupMockMCPServer(t *testing.T) (*MCPServer, *bufio.Reader, io.WriteCloser) {
	t.Helper()

	// Server writes requests → stdinR (test reads these)
	stdinR, stdinW := io.Pipe()
	// Test writes responses → stdoutR (server reads these via runReader)
	stdoutR, stdoutW := io.Pipe()

	srv := &MCPServer{
		Name:       "mock",
		Stdin:      stdinW,
		Stdout:     bufio.NewReader(stdoutR),
		status:     "running",
		pending:    make(map[int]chan *jsonRPCResponse),
		readerDone: make(chan struct{}),
	}
	srv.ctx, srv.cancel = context.WithCancel(context.Background())

	t.Cleanup(func() {
		srv.cancel()
		stdinW.Close()
		stdoutW.Close()
		stdinR.Close()
		stdoutR.Close()
	})

	return srv, bufio.NewReader(stdinR), stdoutW
}

// sendMockResponse writes a JSON-RPC success response to w.
func sendMockResponse(t *testing.T, w io.Writer, id int, result interface{}) {
	t.Helper()
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("sendMockResponse: marshal result: %v", err)
	}
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  json.RawMessage(resultJSON),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("sendMockResponse: marshal response: %v", err)
	}
	w.Write(append(data, '\n'))
}

// sendMockError writes a JSON-RPC error response to w.
func sendMockError(t *testing.T, w io.Writer, id, code int, message string) {
	t.Helper()
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("sendMockError: marshal response: %v", err)
	}
	w.Write(append(data, '\n'))
}

// collectRequests reads exactly n JSON-RPC requests from r.
func collectRequests(t *testing.T, r *bufio.Reader, n int) []jsonRPCRequest {
	t.Helper()
	reqs := make([]jsonRPCRequest, 0, n)
	for i := 0; i < n; i++ {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("collectRequests[%d]: read error: %v", i, err)
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			t.Fatalf("collectRequests[%d]: unmarshal error: %v", i, err)
		}
		reqs = append(reqs, req)
	}
	return reqs
}

// --- Scenario 1: Concurrent tool calls ---

// TestConcurrentToolCalls fires N sendRequest calls in parallel and verifies
// each receives a valid response — exercising the demux routing under load.
func TestConcurrentToolCalls(t *testing.T) {
	const N = 10
	srv, reqReader, respWriter := setupMockMCPServer(t)
	go srv.runReader()

	// Responder: spawn a goroutine per request to reply immediately, exercising
	// real concurrent demux routing rather than a sequential batch-then-respond pattern.
	done := make(chan struct{})
	go func() {
		defer close(done)
		var respWg sync.WaitGroup
		for i := 0; i < N; i++ {
			line, err := reqReader.ReadBytes('\n')
			if err != nil {
				t.Errorf("responder read[%d]: %v", i, err)
				return
			}
			var req jsonRPCRequest
			if err := json.Unmarshal(line, &req); err != nil {
				t.Errorf("responder unmarshal[%d]: %v", i, err)
				return
			}
			respWg.Add(1)
			go func(r jsonRPCRequest) {
				defer respWg.Done()
				sendMockResponse(t, respWriter, r.ID, map[string]int{"echo": r.ID})
			}(req)
		}
		respWg.Wait()
	}()

	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := srv.sendRequest(context.Background(), "tools/call",
				map[string]int{"idx": idx})
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	<-done

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
}

// --- Scenario 2: Process startup/shutdown race ---

// TestStartupShutdownRace covers two sub-cases:
//  (a) callTool on a non-running server returns an error immediately
//  (b) concurrent Stop() calls do not panic (stopOnce guard)
func TestStartupShutdownRace(t *testing.T) {
	t.Run("callToolOnStoppedServer", func(t *testing.T) {
		srv, _, _ := setupMockMCPServer(t)
		srv.mu.Lock()
		srv.status = "stopped"
		srv.mu.Unlock()

		_, err := srv.callTool(context.Background(), "any_tool", json.RawMessage(`{}`))
		if err == nil {
			t.Fatal("expected error when calling tool on stopped server, got nil")
		}
	})

	t.Run("concurrentStopCalls", func(t *testing.T) {
		cfg := &Config{
			MCPServers: map[string]MCPServerConfig{},
		}
		toolReg := NewToolRegistry(cfg)
		host := newMCPHost(cfg, toolReg)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := host.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}

		// Five concurrent Stop() calls must not panic.
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				host.Stop()
			}()
		}
		wg.Wait()
	})
}

// --- Scenario 3: Out-of-order response matching ---

// TestOutOfOrderResponseMatching sends N requests then has the responder
// reply in reverse order. Each goroutine must still receive the correct response.
func TestOutOfOrderResponseMatching(t *testing.T) {
	const N = 6
	srv, reqReader, respWriter := setupMockMCPServer(t)
	go srv.runReader()

	// Responder: buffer all requests, reply in reverse order.
	done := make(chan struct{})
	go func() {
		defer close(done)
		reqs := collectRequests(t, reqReader, N)
		for i := len(reqs) - 1; i >= 0; i-- {
			sendMockResponse(t, respWriter, reqs[i].ID, map[string]int{"reqID": reqs[i].ID})
		}
	}()

	type callResult struct {
		resp *jsonRPCResponse
		err  error
	}
	results := make([]callResult, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := srv.sendRequest(context.Background(), "ping", nil)
			results[idx] = callResult{resp, err}
		}(i)
	}
	wg.Wait()
	<-done

	for i, r := range results {
		if r.err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, r.err)
		}
		if r.resp == nil {
			t.Errorf("goroutine %d: got nil response", i)
		}
	}
}

// --- Scenario 4: Error propagation from tool handler ---

// TestErrorPropagationFromToolHandler verifies that a JSON-RPC error response
// propagates as a Go error from callTool.
func TestErrorPropagationFromToolHandler(t *testing.T) {
	srv, reqReader, respWriter := setupMockMCPServer(t)
	go srv.runReader()

	go func() {
		line, err := reqReader.ReadBytes('\n')
		if err != nil {
			t.Errorf("TestErrorPropagationFromToolHandler: read request: %v", err)
			return
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			t.Errorf("TestErrorPropagationFromToolHandler: unmarshal request: %v", err)
			return
		}
		sendMockError(t, respWriter, req.ID, -32000, "tool handler failed: permission denied")
	}()

	_, err := srv.callTool(context.Background(), "restricted_tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from callTool, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "tools/call error") {
		t.Errorf("expected error to contain 'tools/call error', got: %v", errMsg)
	}
	if !strings.Contains(errMsg, "tool handler failed") {
		t.Errorf("expected error to contain 'tool handler failed', got: %v", errMsg)
	}
	if !strings.Contains(errMsg, "permission denied") {
		t.Errorf("expected error to contain 'permission denied', got: %v", errMsg)
	}
}

// --- Scenario 5: Context cancellation during server startup ---

// TestContextCancellationDuringRequest verifies that cancelling the caller context
// while sendRequest is blocked waiting for a response returns promptly without hanging.
func TestContextCancellationDuringRequest(t *testing.T) {
	srv, reqReader, _ := setupMockMCPServer(t)
	go srv.runReader()

	// requestReceived is closed once the server has received the outbound request,
	// at which point we know sendRequest is blocked in the select waiting for a response.
	// We intentionally never write a response, so ctx cancel will unblock it.
	requestReceived := make(chan struct{})
	go func() {
		_, err := reqReader.ReadBytes('\n')
		if err != nil {
			return
		}
		close(requestReceived)
		// Drain any further requests to avoid blocking the write side.
		for {
			_, err := reqReader.ReadBytes('\n')
			if err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel the context only after the request has been received by the server,
	// guaranteeing sendRequest is blocked in the select when cancellation fires.
	go func() {
		select {
		case <-requestReceived:
			cancel()
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for request to be received")
			cancel()
		}
	}()

	_, err := srv.sendRequest(ctx, "tools/call", nil)
	if err == nil {
		t.Fatal("expected error on context cancellation, got nil")
	}
}

// TestServerContextCancellationDuringRequest verifies that cancelling the server-level
// context also unblocks any pending sendRequest callers.
func TestServerContextCancellationDuringRequest(t *testing.T) {
	srv, reqReader, _ := setupMockMCPServer(t)
	go srv.runReader()

	// requestReceived is closed once the outbound request arrives,
	// guaranteeing sendRequest is blocked in the select when we cancel.
	requestReceived := make(chan struct{})
	go func() {
		_, err := reqReader.ReadBytes('\n')
		if err != nil {
			return
		}
		close(requestReceived)
		// Drain any further requests to avoid blocking the write side.
		for {
			_, err := reqReader.ReadBytes('\n')
			if err != nil {
				return
			}
		}
	}()

	// Cancel the server context only after the request has been received.
	go func() {
		select {
		case <-requestReceived:
			srv.cancel()
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for request to be received")
			srv.cancel()
		}
	}()

	_, err := srv.sendRequest(context.Background(), "tools/call", nil)
	if err == nil {
		t.Fatal("expected error after server context cancellation, got nil")
	}
}

// --- Scenario 6: Config concurrent CRUD ---

// TestConfigConcurrentCRUD exercises concurrent set and list operations on
// the MCP config map and verifies there are no data races (run with -race).
func TestConfigConcurrentCRUD(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "mcp"), 0o755)
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{}`), 0o644)

	cfg := &Config{
		BaseDir:    dir,
		MCPConfigs: make(map[string]json.RawMessage),
		MCPPaths:   make(map[string]string),
	}

	raw := json.RawMessage(`{"mcpServers":{"t":{"command":"echo","args":["ok"]}}}`)

	const writers = 5
	const readers = 5

	var wg sync.WaitGroup

	// Pre-populate so readers have something to list.
	for i := 0; i < writers; i++ {
		name := fmt.Sprintf("pre-%d", i)
		setMCPConfig(cfg, configPath, name, raw)
	}

	// Concurrent writers.
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("server-%d", i)
			setMCPConfig(cfg, configPath, name, raw)
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			listMCPConfigs(cfg)
		}()
	}

	wg.Wait()

	// Verify all written entries are readable.
	for i := 0; i < writers; i++ {
		name := fmt.Sprintf("server-%d", i)
		if _, err := getMCPConfig(cfg, name); err != nil {
			t.Errorf("getMCPConfig(%q) after concurrent write: %v", name, err)
		}
	}
}

// --- Integration: start / stop / callTool via helper process ---

// TestMCPServerStartStop exercises start(), initialize(), discoverTools(),
// callTool(), stop(), and monitorHealth() using the in-process helper server
// (invoked as a subprocess via os.Args[0]).
func TestMCPServerStartStop(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") == "1" {
		t.Skip("running as helper process")
	}

	cfg := &Config{}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host.ctx, host.cancel = context.WithCancel(ctx)

	// Build an MCPServer pointing at this test binary as mock MCP server.
	server := &MCPServer{
		Name:      "integration",
		Command:   os.Args[0],
		Args:      []string{"-test.run=^$"}, // match no tests; init() exits via GO_TEST_HELPER_PROCESS
		Env:       map[string]string{"GO_TEST_HELPER_PROCESS": "1"},
		status:    "starting",
		parentCtx: host.ctx,
		toolReg:   toolReg,
	}
	server.ctx, server.cancel = context.WithCancel(host.ctx)

	host.mu.Lock()
	host.servers["integration"] = server
	host.mu.Unlock()

	// start() performs the full initialize+discoverTools handshake.
	if err := server.start(server.ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Server should now be running with one tool registered.
	server.mu.Lock()
	status := server.status
	tools := len(server.Tools)
	server.mu.Unlock()

	if status != "running" {
		t.Errorf("expected status=running, got %q", status)
	}
	if tools != 1 {
		t.Errorf("expected 1 tool, got %d", tools)
	}

	// callTool should succeed.
	out, err := server.callTool(ctx, "mock_tool", json.RawMessage(`{}`))
	if err != nil {
		t.Errorf("callTool: %v", err)
	}
	if out != "mock result" {
		t.Errorf("callTool output = %q, want %q", out, "mock result")
	}

	// Stop gracefully.
	server.stop()

	server.mu.Lock()
	finalStatus := server.status
	server.mu.Unlock()
	if finalStatus != "stopped" {
		t.Errorf("expected status=stopped after stop(), got %q", finalStatus)
	}
}

// TestMCPHostGetServer verifies getServer returns the correct server by name.
func TestMCPHostGetServer(t *testing.T) {
	cfg := &Config{}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	srv := &MCPServer{Name: "target"}
	host.mu.Lock()
	host.servers["target"] = srv
	host.servers["other"] = &MCPServer{Name: "other"}
	host.mu.Unlock()

	if got := host.getServer("target"); got != srv {
		t.Error("getServer returned wrong server")
	}
	if got := host.getServer("nonexistent"); got != nil {
		t.Error("getServer should return nil for missing server")
	}
}

// TestMCPStderrWriter verifies the stderr writer forwards to the log without error.
func TestMCPStderrWriter(t *testing.T) {
	w := &mcpStderrWriter{serverName: "test-server"}
	n, err := w.Write([]byte("some stderr output\n"))
	if err != nil {
		t.Errorf("Write returned error: %v", err)
	}
	if n != len("some stderr output\n") {
		t.Errorf("Write returned n=%d, want %d", n, len("some stderr output\n"))
	}
}

// TestMCPHostStartWithRealServer verifies that MCPHost.Start() launches a
// configured server using the helper process and that it reaches "running" status.
func TestMCPHostStartWithRealServer(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") == "1" {
		t.Skip("running as helper process")
	}

	enabled := true
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"mock": {
				Command: os.Args[0],
				Args:    []string{"-test.run=^$"},
				Env:     map[string]string{"GO_TEST_HELPER_PROCESS": "1"},
				Enabled: &enabled,
			},
		},
	}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := host.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Stop()

	statuses := host.ServerStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 server, got %d", len(statuses))
	}
	if statuses[0].Status != "running" {
		t.Errorf("expected status=running, got %q", statuses[0].Status)
	}
	if len(statuses[0].Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(statuses[0].Tools))
	}
}

// TestMonitorHealthCrashMaxRestarts exercises the monitorHealth crash path where
// the max restart count is already reached, so no restart is attempted.
func TestMonitorHealthCrashMaxRestarts(t *testing.T) {
	srv, _, respWriter := setupMockMCPServer(t)

	// Pre-set restarts to max so we take the "max restarts exceeded" branch.
	srv.restarts = 3
	srv.parentCtx = context.Background()

	// Start the reader; it will exit when respWriter is closed (EOF).
	go srv.runReader()

	// Run monitorHealth asynchronously.
	mhDone := make(chan struct{})
	go func() {
		defer close(mhDone)
		srv.monitorHealth()
	}()

	// Close the response writer to trigger EOF → reader exits → readerDone closes.
	respWriter.Close()

	select {
	case <-mhDone:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorHealth did not return in time")
	}

	srv.mu.Lock()
	status := srv.status
	srv.mu.Unlock()

	if status != "error" {
		t.Errorf("expected status=error after crash (max restarts), got %q", status)
	}
}

// TestMakeToolHandlerClosure verifies that the ToolHandler closure returned by
// makeToolHandler correctly forwards to callTool.
func TestMakeToolHandlerClosure(t *testing.T) {
	srv, reqReader, respWriter := setupMockMCPServer(t)
	go srv.runReader()

	// Respond to any tool call.
	go func() {
		line, err := reqReader.ReadBytes('\n')
		if err != nil {
			return
		}
		var req jsonRPCRequest
		json.Unmarshal(line, &req)
		sendMockResponse(t, respWriter, req.ID, toolsCallResult{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text,omitempty"`
			}{{Type: "text", Text: "handler result"}},
		})
	}()

	// Create handler via makeToolHandler and invoke the closure directly.
	handler := srv.makeToolHandler("some_tool")
	out, err := handler(context.Background(), &Config{}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if out != "handler result" {
		t.Errorf("handler output = %q, want %q", out, "handler result")
	}
}

// TestRestartServerNotFound verifies RestartServer returns an error for unknown server.
func TestRestartServerNotFound(t *testing.T) {
	cfg := &Config{}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	err := host.RestartServer("does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown server, got nil")
	}
}

// TestSendRequestStdinClosed verifies sendRequest returns an error when Stdin is nil.
func TestSendRequestStdinClosed(t *testing.T) {
	srv, _, _ := setupMockMCPServer(t)
	go srv.runReader()

	// Close stdin to simulate a process that has exited.
	srv.mu.Lock()
	srv.Stdin = nil
	srv.mu.Unlock()

	_, err := srv.sendRequest(context.Background(), "tools/call", nil)
	if err == nil {
		t.Fatal("expected error when Stdin is nil, got nil")
	}
	if !strings.Contains(err.Error(), "stdin closed") {
		t.Errorf("expected 'stdin closed' error, got: %v", err)
	}
}

// TestMCPHostStartServerFailure verifies that when a server fails to start,
// its status is set to "error" while Start() still returns nil (non-fatal).
func TestMCPHostStartServerFailure(t *testing.T) {
	cfg := &Config{
		MCPServers: map[string]MCPServerConfig{
			"bad-server": {
				Command: "this-command-does-not-exist-xyz-999",
				Args:    []string{},
			},
		},
	}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start should return nil even if the server fails to start.
	if err := host.Start(ctx); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	defer host.Stop()

	statuses := host.ServerStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 server status, got %d", len(statuses))
	}
	if statuses[0].Status != "error" {
		t.Errorf("expected status=error for failed server, got %q", statuses[0].Status)
	}
}

// TestMCPHostRestartServer verifies RestartServer re-initialises a server via
// the helper process.
func TestMCPHostRestartServer(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") == "1" {
		t.Skip("running as helper process")
	}

	cfg := &Config{}
	toolReg := NewToolRegistry(cfg)
	host := newMCPHost(cfg, toolReg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host.ctx, host.cancel = context.WithCancel(ctx)

	server := &MCPServer{
		Name:      "restart-test",
		Command:   os.Args[0],
		Args:      []string{"-test.run=^$"},
		Env:       map[string]string{"GO_TEST_HELPER_PROCESS": "1"},
		status:    "starting",
		parentCtx: host.ctx,
		toolReg:   toolReg,
	}
	server.ctx, server.cancel = context.WithCancel(host.ctx)

	host.mu.Lock()
	host.servers["restart-test"] = server
	host.mu.Unlock()

	if err := server.start(server.ctx); err != nil {
		t.Fatalf("initial start: %v", err)
	}

	// Stop the server manually, then restart via host.
	server.stop()

	if err := host.RestartServer("restart-test"); err != nil {
		t.Fatalf("RestartServer: %v", err)
	}

	// Wait briefly for restart to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		s := server.status
		server.mu.Unlock()
		if s == "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	server.mu.Lock()
	finalStatus := server.status
	server.mu.Unlock()

	if finalStatus != "running" {
		t.Errorf("expected status=running after restart, got %q", finalStatus)
	}

	host.Stop()
}
