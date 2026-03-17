package provider

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TerminalProvider executes tasks in persistent tmux sessions.
type TerminalProvider struct {
	BinaryPath     string
	DefaultWorkdir string
	Profile        TmuxProfile
	Tmux           TmuxOps
	Workers        WorkerTracker
}

func (p *TerminalProvider) Name() string { return "terminal-" + p.Profile.Name() }

func (p *TerminalProvider) Execute(ctx context.Context, req Request) (*Result, error) {
	sessionName := fmt.Sprintf("tetora-worker-%s-%d", p.Profile.Name(), time.Now().UnixNano()%1000000)

	command := p.Profile.BuildCommand(p.BinaryPath, req)

	cols, rows := 120, 40

	workdir := req.Workdir
	if workdir == "" {
		workdir = p.DefaultWorkdir
	}
	if err := p.Tmux.Create(sessionName, cols, rows, command, workdir); err != nil {
		return nil, fmt.Errorf("create tmux session: %w", err)
	}

	promptPreview := req.Prompt
	if len(promptPreview) > 200 {
		promptPreview = promptPreview[:200]
	}
	p.Workers.Register(sessionName, WorkerInfo{
		TmuxName:    sessionName,
		TaskID:      req.SessionID,
		Agent:       req.AgentName,
		Prompt:      promptPreview,
		Workdir:     workdir,
		State:       ScreenStarting,
		CreatedAt:   time.Now(),
		LastChanged: time.Now(),
	})
	defer p.Workers.Unregister(sessionName)

	if err := p.waitForReady(ctx, sessionName, 30*time.Second); err != nil {
		p.Tmux.Kill(sessionName)
		return nil, fmt.Errorf("tool not ready: %w", err)
	}

	if req.Prompt != "" {
		if len(req.Prompt) > 1000 {
			p.Tmux.LoadAndPaste(sessionName, req.Prompt)
		} else {
			p.Tmux.SendText(sessionName, req.Prompt)
		}
		p.Tmux.SendKeys(sessionName, "Enter")
	}

	start := time.Now()
	result, err := p.pollUntilDone(ctx, sessionName, req)
	elapsed := time.Since(start)

	if err != nil {
		p.Tmux.Kill(sessionName)
		return &Result{
			IsError:    true,
			Error:      err.Error(),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	history, _ := p.Tmux.CaptureHistory(sessionName)

	p.Tmux.Kill(sessionName)

	result.DurationMs = elapsed.Milliseconds()
	if history != "" {
		result.Output = ExtractResultFromHistory(history)
	}

	return result, nil
}

func (p *TerminalProvider) waitForReady(ctx context.Context, sessionName string, timeout time.Duration) error {
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
			capture, err := p.Tmux.Capture(sessionName)
			if err != nil {
				continue
			}
			state := p.Profile.DetectState(capture)
			if state == ScreenWaiting || state == ScreenApproval {
				return nil
			}
		}
	}
}

func (p *TerminalProvider) pollUntilDone(ctx context.Context, sessionName string, req Request) (*Result, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastCapture := ""
	stableCount := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if !p.Tmux.HasSession(sessionName) {
				return &Result{
					Output: lastCapture,
				}, nil
			}

			capture, err := p.Tmux.Capture(sessionName)
			if err != nil {
				continue
			}

			state := p.Profile.DetectState(capture)

			// Update worker state via tracker.
			p.Workers.UpdateWorker(sessionName, state, capture, capture != lastCapture)

			// Emit SSE events if callback available.
			if req.OnEvent != nil && capture != lastCapture {
				req.OnEvent(Event{
					Type:      EventOutputChunk,
					TaskID:    req.SessionID,
					SessionID: req.SessionID,
					Data: map[string]any{
						"chunk":     capture,
						"chunkType": "terminal_capture",
					},
					Timestamp: time.Now().Format(time.RFC3339),
				})
			}

			switch state {
			case ScreenDone:
				return &Result{
					Output: capture,
				}, nil

			case ScreenApproval:
				if req.PermissionMode == "bypassPermissions" || req.PermissionMode == "acceptEdits" {
					p.Tmux.SendKeys(sessionName, p.Profile.ApproveKeys()...)
				}

			case ScreenWaiting:
				if !req.PersistSession {
					stableCount++
					if stableCount >= 3 {
						return &Result{
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

// ExtractResultFromHistory extracts the meaningful output from tmux scrollback history.
func ExtractResultFromHistory(history string) string {
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
