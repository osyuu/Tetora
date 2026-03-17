package tmux

import (
	"strings"
	"sync"
	"time"

	tlog "tetora/internal/log"
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

func (s ScreenState) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateWorking:
		return "working"
	case StateWaiting:
		return "waiting"
	case StateApproval:
		return "approval"
	case StateQuestion:
		return "question"
	case StateDone:
		return "done"
	default:
		return "unknown"
	}
}

// Worker represents a single tmux-based CLI tool worker session.
type Worker struct {
	TmuxName    string
	TaskID      string
	Agent       string
	Prompt      string // first 200 chars for display
	Workdir     string
	State       ScreenState
	CreatedAt   time.Time
	LastCapture string
	LastChanged time.Time
}

// EventPublisher publishes worker lifecycle events (implemented by sseBroker in root).
type EventPublisher interface {
	PublishWorkerEvent(action, name, state string)
}

// Supervisor tracks all active tmux workers.
type Supervisor struct {
	Mu      sync.RWMutex
	Workers map[string]*Worker // tmuxName → worker
	broker  EventPublisher     // optional
}

// NewSupervisor creates a new Supervisor.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		Workers: make(map[string]*Worker),
	}
}

// SetBroker sets the event publisher (call after construction).
func (s *Supervisor) SetBroker(b EventPublisher) {
	s.Mu.Lock()
	s.broker = b
	s.Mu.Unlock()
}

func (s *Supervisor) Register(name string, w *Worker) {
	s.Mu.Lock()
	s.Workers[name] = w
	broker := s.broker
	s.Mu.Unlock()
	if broker != nil {
		broker.PublishWorkerEvent("registered", name, w.State.String())
	}
}

func (s *Supervisor) Unregister(name string) {
	s.Mu.Lock()
	delete(s.Workers, name)
	broker := s.broker
	s.Mu.Unlock()
	if broker != nil {
		broker.PublishWorkerEvent("unregistered", name, "")
	}
}

func (s *Supervisor) ListWorkers() []*Worker {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	result := make([]*Worker, 0, len(s.Workers))
	for _, w := range s.Workers {
		result = append(result, w)
	}
	return result
}

func (s *Supervisor) GetWorker(name string) *Worker {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.Workers[name]
}

// UpdateWorkerState atomically updates a worker's state and capture.
func (s *Supervisor) UpdateWorkerState(name string, state ScreenState, capture, lastCapture string) {
	s.Mu.Lock()
	if w := s.Workers[name]; w != nil {
		w.State = state
		w.LastCapture = capture
		if capture != lastCapture {
			w.LastChanged = time.Now()
		}
	}
	s.Mu.Unlock()
}

// CheckSessionHealth inspects all tracked workers for health issues.
func (s *Supervisor) CheckSessionHealth() []string {
	s.Mu.RLock()
	defer s.Mu.RUnlock()

	var issues []string
	for name, w := range s.Workers {
		if !HasSession(name) {
			issues = append(issues, "zombie worker: "+name+" (tmux session gone)")
			continue
		}
		if w.State == StateWorking && time.Since(w.LastChanged) > 10*time.Minute {
			issues = append(issues, "stalled worker: "+name+" (no change in 10m)")
		}
	}
	return issues
}

// CleanupOrphanedSessions handles tetora-worker-* tmux sessions left from a previous daemon run.
func (s *Supervisor) CleanupOrphanedSessions(keepOne bool, profile CLIProfile) {
	sessions := ListSessions()
	cleaned := 0
	kept := false
	for _, name := range sessions {
		if !strings.HasPrefix(name, "tetora-worker-") && !strings.HasPrefix(name, "tetora-term-") {
			continue
		}
		if s.GetWorker(name) != nil {
			continue
		}

		if keepOne && !kept && profile != nil {
			if capture, err := Capture(name); err == nil {
				if profile.DetectState(capture) == StateWaiting {
					w := &Worker{
						TmuxName:    name,
						State:       StateWaiting,
						CreatedAt:   time.Now(),
						LastChanged: time.Now(),
					}
					s.Register(name, w)
					kept = true
					tlog.Info("keeping idle tmux session for reuse", "tmux", name)
					continue
				}
			}
		}

		Kill(name)
		cleaned++
	}
	if cleaned > 0 {
		tlog.Info("cleaned up orphaned tmux sessions", "count", cleaned)
	}
}

// IsShellPrompt checks if a line looks like a shell prompt.
func IsShellPrompt(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return strings.HasSuffix(trimmed, "$") || strings.HasSuffix(trimmed, "%") || strings.HasSuffix(trimmed, "#")
}
