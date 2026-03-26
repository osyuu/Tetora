// Package terminal implements the TerminalProvider: executes tasks in persistent tmux sessions.
package terminal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tetora/internal/provider"
)

// ScreenState represents the detected state of a tmux worker's screen.
type ScreenState int

const (
	StateUnknown  ScreenState = iota
	StateStarting             // session just created, waiting for CLI tool to load
	StateWorking              // CLI tool is actively processing
	StateWaiting              // CLI tool is idle at input prompt
	StateApproval             // CLI tool is asking for permission
	StateQuestion             // CLI tool is showing AskUserQuestion choices
	StateDone                 // session exited or returned to shell prompt
)

// CLIProfile abstracts tool-specific behavior for running CLI tools inside tmux sessions.
type CLIProfile interface {
	// Name returns the profile identifier (e.g. "claude", "codex").
	Name() string
	// BuildCommand constructs the full CLI command string for the given request.
	BuildCommand(binaryPath string, req provider.Request) string
	// DetectState analyzes tmux capture output to determine the screen state.
	DetectState(capture string) ScreenState
	// ApproveKeys returns the tmux key sequence to approve a permission prompt.
	ApproveKeys() []string
	// RejectKeys returns the tmux key sequence to reject a permission prompt.
	RejectKeys() []string
}

// TmuxOps abstracts tmux operations so the provider can be tested and the root
// package functions can be injected at construction time.
type TmuxOps interface {
	Create(name string, cols, rows int, command, workdir string) error
	Capture(name string) (string, error)
	CaptureHistory(name string) (string, error)
	SendKeys(name string, keys ...string) error
	SendText(name string, text string) error
	LoadAndPaste(name, text string) error
	Kill(name string) error
	HasSession(name string) bool
}

// WorkerState tracks an active worker session for monitoring.
type WorkerState struct {
	TmuxName    string
	TaskID      string
	Agent       string
	Prompt      string
	Workdir     string
	State       ScreenState
	CreatedAt   time.Time
	LastCapture string
	LastChanged time.Time
}

// WorkerRegistry tracks active workers. It is injected so the root supervisor
// can be notified without creating a circular import.
type WorkerRegistry interface {
	Register(name string, w *WorkerState)
	Unregister(name string)
	UpdateState(name string, state ScreenState, capture string)
}

// DefaultWorkdirFn returns the configured default workdir when req.Workdir is empty.
type DefaultWorkdirFn func() string

// Provider executes tasks in persistent tmux sessions.
type Provider struct {
	binaryPath     string
	profile        CLIProfile
	tmux           TmuxOps
	workers        WorkerRegistry
	defaultWorkdir DefaultWorkdirFn
}

// New creates a new TerminalProvider.
func New(binaryPath string, profile CLIProfile, tmux TmuxOps, workers WorkerRegistry, defaultWorkdir DefaultWorkdirFn) *Provider {
	return &Provider{
		binaryPath:     binaryPath,
		profile:        profile,
		tmux:           tmux,
		workers:        workers,
		defaultWorkdir: defaultWorkdir,
	}
}

func (p *Provider) Name() string { return "terminal-" + p.profile.Name() }

func (p *Provider) Execute(ctx context.Context, req provider.Request) (*provider.Result, error) {
	sessionName := fmt.Sprintf("tetora-worker-%s-%d", p.profile.Name(), time.Now().UnixNano()%1000000)

	command := p.profile.BuildCommand(p.binaryPath, req)

	cols, rows := 120, 40

	workdir := req.Workdir
	if workdir == "" && p.defaultWorkdir != nil {
		workdir = p.defaultWorkdir()
	}
	if err := p.tmux.Create(sessionName, cols, rows, command, workdir); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}

	promptPreview := req.Prompt
	if len(promptPreview) > 200 {
		promptPreview = promptPreview[:200]
	}
	worker := &WorkerState{
		TmuxName:    sessionName,
		TaskID:      req.SessionID,
		Agent:       req.AgentName,
		Prompt:      promptPreview,
		Workdir:     workdir,
		State:       StateStarting,
		CreatedAt:   time.Now(),
		LastChanged: time.Now(),
	}
	p.workers.Register(sessionName, worker)
	defer p.workers.Unregister(sessionName)

	if err := p.waitForReady(ctx, sessionName, 30*time.Second); err != nil {
		p.tmux.Kill(sessionName)
		return nil, fmt.Errorf("tool not ready: %w", err)
	}

	if req.Prompt != "" {
		if len(req.Prompt) > 1000 {
			p.tmux.LoadAndPaste(sessionName, req.Prompt)
		} else {
			p.tmux.SendText(sessionName, req.Prompt)
		}
		p.tmux.SendKeys(sessionName, "Enter")
	}

	start := time.Now()
	result, err := p.pollUntilDone(ctx, sessionName, worker, req)
	elapsed := time.Since(start)

	if err != nil {
		p.tmux.Kill(sessionName)
		return &provider.Result{
			IsError:    true,
			Error:      err.Error(),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	history, _ := p.tmux.CaptureHistory(sessionName)
	p.tmux.Kill(sessionName)

	result.DurationMs = elapsed.Milliseconds()
	if history != "" {
		result.Output = extractResultFromHistory(history)
	}

	return result, nil
}

// waitForReady polls until the CLI tool shows its input prompt.
func (p *Provider) waitForReady(ctx context.Context, sessionName string, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("CLI tool did not become ready within %v", timeout)
		case <-ticker.C:
			capture, err := p.tmux.Capture(sessionName)
			if err != nil {
				continue
			}
			state := p.profile.DetectState(capture)
			if state == StateWaiting || state == StateApproval {
				return nil
			}
		}
	}
}

// pollUntilDone polls the tmux session until the task is done or context is cancelled.
func (p *Provider) pollUntilDone(ctx context.Context, sessionName string, worker *WorkerState, req provider.Request) (*provider.Result, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastCapture := ""
	stableCount := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if !p.tmux.HasSession(sessionName) {
				return &provider.Result{
					Output: lastCapture,
				}, nil
			}

			capture, err := p.tmux.Capture(sessionName)
			if err != nil {
				continue
			}

			state := p.profile.DetectState(capture)

			p.workers.UpdateState(sessionName, state, capture)

			if req.EventCh != nil && capture != lastCapture {
				req.EventCh <- provider.Event{
					Type:      provider.EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"chunk":     capture,
						"chunkType": "terminal_capture",
					},
					Timestamp: time.Now().Format(time.RFC3339),
				}
			}

			switch state {
			case StateDone:
				return &provider.Result{
					Output: capture,
				}, nil

			case StateApproval:
				if req.PermissionMode == "bypassPermissions" || req.PermissionMode == "acceptEdits" {
					p.tmux.SendKeys(sessionName, p.profile.ApproveKeys()...)
				}

			case StateWaiting:
				if !req.PersistSession {
					stableCount++
					if stableCount >= 3 {
						return &provider.Result{
							Output: capture,
						}, nil
					}
				}

			default:
				stableCount = 0
			}

			if capture != lastCapture {
				lastCapture = capture
			}
		}
	}
}

// extractResultFromHistory extracts the meaningful output from tmux scrollback history.
func extractResultFromHistory(history string) string {
	lines := strings.Split(history, "\n")

	var resultLines []string
	inOutput := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inOutput && trimmed == "" {
			continue
		}
		if !inOutput && strings.HasPrefix(trimmed, "❯") {
			inOutput = true
			continue
		}
		if inOutput {
			if strings.HasPrefix(trimmed, "❯") {
				break
			}
			resultLines = append(resultLines, line)
		}
	}

	result := strings.Join(resultLines, "\n")
	return strings.TrimSpace(result)
}

// --- No-op WorkerRegistry for cases where monitoring is not needed ---

// NoopRegistry is a WorkerRegistry that does nothing. Useful for testing.
type NoopRegistry struct{}

func (NoopRegistry) Register(name string, w *WorkerState)                    {}
func (NoopRegistry) Unregister(name string)                                  {}
func (NoopRegistry) UpdateState(name string, state ScreenState, capture string) {}

// --- SimpleRegistry is a basic in-memory WorkerRegistry ---

// SimpleRegistry tracks workers in memory with optional change notification.
type SimpleRegistry struct {
	mu      sync.RWMutex
	workers map[string]*WorkerState
	onChange func(name string, w *WorkerState)
}

// NewSimpleRegistry creates a new SimpleRegistry.
// onChange is called (if non-nil) whenever a worker is registered, unregistered, or updated.
func NewSimpleRegistry(onChange func(name string, w *WorkerState)) *SimpleRegistry {
	return &SimpleRegistry{
		workers:  make(map[string]*WorkerState),
		onChange: onChange,
	}
}

func (r *SimpleRegistry) Register(name string, w *WorkerState) {
	r.mu.Lock()
	r.workers[name] = w
	r.mu.Unlock()
	if r.onChange != nil {
		r.onChange(name, w)
	}
}

func (r *SimpleRegistry) Unregister(name string) {
	r.mu.Lock()
	delete(r.workers, name)
	r.mu.Unlock()
	if r.onChange != nil {
		r.onChange(name, nil)
	}
}

func (r *SimpleRegistry) UpdateState(name string, state ScreenState, capture string) {
	r.mu.Lock()
	w := r.workers[name]
	if w != nil {
		w.State = state
		if capture != w.LastCapture {
			w.LastCapture = capture
			w.LastChanged = time.Now()
		}
	}
	r.mu.Unlock()
	if r.onChange != nil && w != nil {
		r.onChange(name, w)
	}
}

// List returns a snapshot of all active workers.
func (r *SimpleRegistry) List() []*WorkerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*WorkerState, 0, len(r.workers))
	for _, w := range r.workers {
		result = append(result, w)
	}
	return result
}

// Get returns a worker by tmux session name.
func (r *SimpleRegistry) Get(name string) *WorkerState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workers[name]
}
