package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"

	internalprovider "tetora/internal/provider"
	"tetora/internal/provider/claude"
	"tetora/internal/provider/codex"
	"tetora/internal/provider/openai"
	"tetora/internal/provider/terminal"
)

// --- Provider Adapter ---

// internalProviderAdapter wraps an internal Provider (using provider.Request/Result)
// and adapts it to the root Provider interface (using ProviderRequest/ProviderResult).
type internalProviderAdapter struct {
	inner internalprovider.Provider
}

func (a *internalProviderAdapter) Name() string { return a.inner.Name() }

func (a *internalProviderAdapter) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	ireq := toInternalRequest(req)
	ires, err := a.inner.Execute(ctx, ireq)
	if err != nil {
		return nil, err
	}
	return fromInternalResult(ires), nil
}

// internalToolCapableAdapter additionally implements ToolCapableProvider.
type internalToolCapableAdapter struct {
	inner interface {
		internalprovider.Provider
		ExecuteWithTools(ctx context.Context, req internalprovider.Request) (*internalprovider.Result, error)
	}
}

func (a *internalToolCapableAdapter) Name() string { return a.inner.Name() }

func (a *internalToolCapableAdapter) Execute(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	ireq := toInternalRequest(req)
	ires, err := a.inner.Execute(ctx, ireq)
	if err != nil {
		return nil, err
	}
	return fromInternalResult(ires), nil
}

func (a *internalToolCapableAdapter) ExecuteWithTools(ctx context.Context, req ProviderRequest) (*ProviderResult, error) {
	ireq := toInternalRequest(req)
	ires, err := a.inner.ExecuteWithTools(ctx, ireq)
	if err != nil {
		return nil, err
	}
	return fromInternalResult(ires), nil
}

// --- Type Conversions ---

// toInternalRequest converts a root ProviderRequest to an internal provider.Request.
func toInternalRequest(req ProviderRequest) internalprovider.Request {
	var eventCh chan<- internalprovider.Event
	if req.EventCh != nil {
		ch := make(chan internalprovider.Event, cap(req.EventCh))
		eventCh = ch
		// Bridge: forward internal events to root SSEEvent channel.
		go bridgeEvents(ch, req.EventCh)
	}

	// Convert root Messages → internal Messages.
	var msgs []internalprovider.Message
	for _, m := range req.Messages {
		msgs = append(msgs, internalprovider.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	// Convert root Tools → internal Tools.
	var tools []internalprovider.ToolDef
	for _, t := range req.Tools {
		tools = append(tools, internalprovider.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	return internalprovider.Request{
		Prompt:         req.Prompt,
		SystemPrompt:   req.SystemPrompt,
		Model:          req.Model,
		Workdir:        req.Workdir,
		Timeout:        req.Timeout,
		Budget:         req.Budget,
		PermissionMode: req.PermissionMode,
		MCP:            req.MCP,
		MCPPath:        req.MCPPath,
		AddDirs:        req.AddDirs,
		SessionID:      req.SessionID,
		Resume:         req.Resume,
		PersistSession: req.PersistSession,
		AgentName:      req.AgentName,
		Docker:         req.Docker,
		Tools:          tools,
		EventCh:        eventCh,
		Messages:       msgs,
	}
}

// fromInternalResult converts an internal provider.Result to a root ProviderResult.
func fromInternalResult(r *internalprovider.Result) *ProviderResult {
	if r == nil {
		return &ProviderResult{}
	}

	// Convert internal ToolCalls → root ToolCalls.
	var toolCalls []ToolCall
	for _, tc := range r.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:    tc.ID,
			Name:  tc.Name,
			Input: tc.Input,
		})
	}

	return &ProviderResult{
		Output:     r.Output,
		CostUSD:    r.CostUSD,
		DurationMs: r.DurationMs,
		SessionID:  r.SessionID,
		IsError:    r.IsError,
		Error:      r.Error,
		Provider:   r.Provider,
		TokensIn:   r.TokensIn,
		TokensOut:  r.TokensOut,
		ProviderMs: r.ProviderMs,
		ToolCalls:  toolCalls,
		StopReason: r.StopReason,
	}
}

// bridgeEvents forwards internal provider.Events to the root SSEEvent channel.
func bridgeEvents(src <-chan internalprovider.Event, dst chan<- SSEEvent) {
	for ev := range src {
		// Map internal event Data (any) directly; types are compatible.
		sseEv := SSEEvent{
			Type:          ev.Type,
			TaskID:        ev.TaskID,
			SessionID:     ev.SessionID,
			WorkflowRunID: ev.WorkflowRunID,
			Data:          ev.Data,
			Timestamp:     ev.Timestamp,
		}
		select {
		case dst <- sseEv:
		default:
			// Drop if destination channel is full.
		}
	}
}

// --- Provider Initialization ---

// initProviders creates provider instances from config using internal packages.
func initProviders(cfg *Config) *providerRegistry {
	reg := newProviderRegistry()

	for name, pc := range cfg.Providers {
		switch pc.Type {
		case "claude-cli":
			path := pc.Path
			if path == "" {
				path = cfg.ClaudePath
			}
			if path == "" {
				path = "claude"
			}
			reg.register(name, newClaudeAdapter(path, cfg))

		case "openai-compatible":
			p := openai.New(name, pc.BaseURL, pc.APIKey, pc.Model)
			reg.register(name, &internalToolCapableAdapter{inner: p})

		case "claude-api":
			// Deprecated in v3: use "claude-code" instead.
			logWarn("provider type 'claude-api' is deprecated in v3, use 'claude-code' instead", "name", name)
			path := pc.Path
			if path == "" {
				path = "/usr/local/bin/claude"
			}
			reg.register(name, newClaudeAdapter(path, cfg))

		case "claude-code", "claude-tmux":
			if pc.Type == "claude-tmux" {
				logWarn("provider type 'claude-tmux' is deprecated in v3, use 'claude-code' instead", "name", name)
			}
			path := pc.Path
			if path == "" {
				path = "/usr/local/bin/claude"
			}
			reg.register(name, newClaudeAdapter(path, cfg))

		case "terminal-claude":
			path := pc.Path
			if path == "" {
				path = cfg.ClaudePath
			}
			if path == "" {
				path = "/usr/local/bin/claude"
			}
			reg.register(name, newTerminalAdapter(path, &claudeTmuxProfile{}, cfg))

		case "terminal-codex":
			path := pc.Path
			if path == "" {
				path = "codex"
			}
			reg.register(name, newTerminalAdapter(path, &codexTmuxProfile{}, cfg))

		case "codex-cli":
			path := pc.Path
			if path == "" {
				path = "codex"
			}
			p := codex.New(path)
			reg.register(name, &internalProviderAdapter{inner: p})
		}
	}

	// Ensure "claude" provider always exists (backward compat).
	if _, err := reg.get("claude"); err != nil {
		path := cfg.ClaudePath
		if path == "" {
			path = "claude"
		}
		reg.register("claude", newClaudeAdapter(path, cfg))
	}

	// Ensure "claude-code" provider always exists.
	if _, err := reg.get("claude-code"); err != nil {
		path := cfg.ClaudePath
		if path == "" {
			path = "/usr/local/bin/claude"
		}
		reg.register("claude-code", newClaudeAdapter(path, cfg))
	}

	// Auto-register "codex" if the binary is found on PATH.
	if _, err := reg.get("codex"); err != nil {
		if path, lookErr := exec.LookPath("codex"); lookErr == nil {
			p := codex.New(path)
			reg.register("codex", &internalProviderAdapter{inner: p})
		}
	}

	return reg
}

// newClaudeAdapter creates a claude provider adapter with docker support wired in.
func newClaudeAdapter(binaryPath string, cfg *Config) Provider {
	dockerEnabled := cfg.Docker.Enabled

	var dockerBuilder claude.DockerCmdBuilder
	if dockerEnabled {
		dockerBuilder = func(
			ctx context.Context,
			workdir string,
			claudePath string,
			args []string,
			addDirs []string,
			mcpPath string,
			envVars []string,
		) *exec.Cmd {
			return buildDockerCmd(ctx, cfg.Docker, workdir, claudePath, args, addDirs, mcpPath, envVars)
		}
	}

	var envFilter claude.EnvFilter
	if dockerEnabled {
		envFilter = func() []string {
			return dockerEnvFilter(cfg.Docker)
		}
	}

	p := claude.New(binaryPath, dockerEnabled, dockerBuilder, envFilter)
	return &internalProviderAdapter{inner: p}
}

// --- tmuxProfileAdapter bridges root tmuxCLIProfile to terminal.CLIProfile ---

// tmuxProfileAdapter adapts a root tmuxCLIProfile to the terminal.CLIProfile interface.
// It also handles the ProviderRequest → provider.Request translation needed for BuildCommand.
type tmuxProfileAdapter struct {
	inner tmuxCLIProfile
}

func (a *tmuxProfileAdapter) Name() string { return a.inner.Name() }

func (a *tmuxProfileAdapter) BuildCommand(binaryPath string, req internalprovider.Request) string {
	// Convert internal Request back to root ProviderRequest for the profile.
	rootReq := ProviderRequest{
		Model:          req.Model,
		PermissionMode: req.PermissionMode,
		SystemPrompt:   req.SystemPrompt,
		AddDirs:        req.AddDirs,
		MCPPath:        req.MCPPath,
		SessionID:      req.SessionID,
	}
	return a.inner.BuildCommand(binaryPath, rootReq)
}

func (a *tmuxProfileAdapter) DetectState(capture string) terminal.ScreenState {
	s := a.inner.DetectState(capture)
	return rootStateToTerminal(s)
}

func (a *tmuxProfileAdapter) ApproveKeys() []string { return a.inner.ApproveKeys() }
func (a *tmuxProfileAdapter) RejectKeys() []string  { return a.inner.RejectKeys() }

// rootStateToTerminal converts root tmuxScreenState to terminal.ScreenState.
func rootStateToTerminal(s tmuxScreenState) terminal.ScreenState {
	switch s {
	case tmuxStateStarting:
		return terminal.StateStarting
	case tmuxStateWorking:
		return terminal.StateWorking
	case tmuxStateWaiting:
		return terminal.StateWaiting
	case tmuxStateApproval:
		return terminal.StateApproval
	case tmuxStateQuestion:
		return terminal.StateQuestion
	case tmuxStateDone:
		return terminal.StateDone
	default:
		return terminal.StateUnknown
	}
}

// --- tmuxOpsAdapter bridges root tmux functions to terminal.TmuxOps ---

type tmuxOpsAdapter struct{}

func (tmuxOpsAdapter) Create(name string, cols, rows int, command, workdir string) error {
	return tmuxCreate(name, cols, rows, command, workdir)
}
func (tmuxOpsAdapter) Capture(name string) (string, error)        { return tmuxCapture(name) }
func (tmuxOpsAdapter) CaptureHistory(name string) (string, error) { return tmuxCaptureHistory(name) }
func (tmuxOpsAdapter) SendKeys(name string, keys ...string) error  { return tmuxSendKeys(name, keys...) }
func (tmuxOpsAdapter) SendText(name string, text string) error     { return tmuxSendText(name, text) }
func (tmuxOpsAdapter) LoadAndPaste(name, text string) error        { return tmuxLoadAndPaste(name, text) }
func (tmuxOpsAdapter) Kill(name string) error                      { return tmuxKill(name) }
func (tmuxOpsAdapter) HasSession(name string) bool                 { return tmuxHasSession(name) }

// --- workerRegistryAdapter bridges tmuxSupervisor to terminal.WorkerRegistry ---

type workerRegistryAdapter struct {
	sup *tmuxSupervisor
}

func (a *workerRegistryAdapter) Register(name string, w *terminal.WorkerState) {
	tw := &tmuxWorker{
		TmuxName:    w.TmuxName,
		TaskID:      w.TaskID,
		Agent:       w.Agent,
		Prompt:      w.Prompt,
		Workdir:     w.Workdir,
		State:       tmuxStateStarting,
		CreatedAt:   w.CreatedAt,
		LastChanged: w.LastChanged,
	}
	a.sup.register(name, tw)
}

func (a *workerRegistryAdapter) Unregister(name string) {
	a.sup.unregister(name)
}

func (a *workerRegistryAdapter) UpdateState(name string, state terminal.ScreenState, capture string) {
	a.sup.mu.Lock()
	defer a.sup.mu.Unlock()
	if w := a.sup.workers[name]; w != nil {
		w.State = terminalStateToRoot(state)
		if capture != w.LastCapture {
			w.LastCapture = capture
		}
	}
}

// terminalStateToRoot converts terminal.ScreenState to root tmuxScreenState.
func terminalStateToRoot(s terminal.ScreenState) tmuxScreenState {
	switch s {
	case terminal.StateStarting:
		return tmuxStateStarting
	case terminal.StateWorking:
		return tmuxStateWorking
	case terminal.StateWaiting:
		return tmuxStateWaiting
	case terminal.StateApproval:
		return tmuxStateApproval
	case terminal.StateQuestion:
		return tmuxStateQuestion
	case terminal.StateDone:
		return tmuxStateDone
	default:
		return tmuxStateUnknown
	}
}

// newTerminalAdapter creates a terminal provider adapter.
func newTerminalAdapter(binaryPath string, profile tmuxCLIProfile, cfg *Config) Provider {
	profileAdapter := &tmuxProfileAdapter{inner: profile}
	workerReg := &workerRegistryAdapter{sup: newTmuxSupervisor()}
	defaultWorkdir := func() string { return cfg.DefaultWorkdir }

	p := terminal.New(binaryPath, profileAdapter, tmuxOpsAdapter{}, workerReg, defaultWorkdir)
	return &internalProviderAdapter{inner: p}
}

// --- Helpers used by other root files that previously lived in provider.go ---

// providerHasNativeSession returns true if the provider maintains its own session state.
func providerHasNativeSession(providerName string) bool {
	return internalprovider.HasNativeSession(providerName)
}

// toInternalToolDefs converts root ToolDef slice to internal ToolDef slice.
// Used by dispatch_tools.go and similar files.
func toInternalToolDefs(tools []ToolDef) []internalprovider.ToolDef {
	result := make([]internalprovider.ToolDef, 0, len(tools))
	for _, t := range tools {
		result = append(result, internalprovider.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return result
}

// fromInternalToolCalls converts internal ToolCall slice to root ToolCall slice.
func fromInternalToolCalls(calls []internalprovider.ToolCall) []ToolCall {
	result := make([]ToolCall, 0, len(calls))
	for _, c := range calls {
		result = append(result, ToolCall{
			ID:    c.ID,
			Name:  c.Name,
			Input: c.Input,
		})
	}
	return result
}

// fromInternalMessages converts internal Message slice to root Message slice.
func fromInternalMessages(msgs []internalprovider.Message) []Message {
	result := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result
}

// toInternalMessages converts root Message slice to internal Message slice.
func toInternalMessages(msgs []Message) []internalprovider.Message {
	result := make([]internalprovider.Message, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, internalprovider.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return result
}

// toRootContentBlock converts internal ContentBlock to root ContentBlock.
func toRootContentBlock(b internalprovider.ContentBlock) ContentBlock {
	return ContentBlock{
		Type:      b.Type,
		Text:      b.Text,
		ID:        b.ID,
		Name:      b.Name,
		Input:     b.Input,
		ToolUseID: b.ToolUseID,
		Content:   b.Content,
		IsError:   b.IsError,
	}
}

// marshalContentBlocks marshals content blocks to JSON for Message.Content.
func marshalContentBlocks(blocks []ContentBlock) (json.RawMessage, error) {
	return json.Marshal(blocks)
}

// isTransientError checks whether an error message indicates a transient failure.
func isTransientError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	transient := []string{
		"timeout", "timed out", "deadline exceeded",
		"connection refused", "connection reset",
		"eof", "broken pipe",
		"http 5", "status 5",
		"temporarily unavailable", "service unavailable",
		"too many requests", "rate limit",
	}
	for _, t := range transient {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}
