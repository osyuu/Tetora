// Package messaging defines shared interfaces for messaging platform integrations.
package messaging

import "context"

// TaskRequest represents a dispatch request from a messaging platform.
type TaskRequest struct {
	AgentRole      string
	Content        string
	SessionID      string            // session binding
	SystemPrompt   string            // agent SOUL prompt
	Model          string            // model override
	PermissionMode string            // agent permission mode
	Meta           map[string]string
}

// TaskResult represents the result of a dispatched task.
type TaskResult struct {
	Output     string
	Error      string
	Status     string  // "success", "error", etc.
	CostUSD    float64
	TokensIn   float64
	TokensOut  float64
	Model      string
	OutputFile string
	TaskID     string
	DurationMs int64
}

// Dispatcher abstracts the task dispatch mechanism from messaging integrations.
type Dispatcher interface {
	Submit(ctx context.Context, req TaskRequest) (TaskResult, error)
}
