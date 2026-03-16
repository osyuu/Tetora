// Package messaging defines shared interfaces for messaging platform integrations.
package messaging

import (
	"context"
	"net/http"
)

// BotRuntime abstracts root package dependencies that messaging bots need.
// Internal messaging packages depend on this interface rather than importing root.
type BotRuntime interface {
	// Submit dispatches a task for execution and waits for result.
	Submit(ctx context.Context, req TaskRequest) (TaskResult, error)
	// Route determines which agent should handle a prompt.
	Route(ctx context.Context, prompt, source string) (agent string, err error)
	// GetOrCreateSession returns existing session or creates new one.
	GetOrCreateSession(platform, key, agent, title string) (sessionID string, err error)
	// BuildSessionContext returns recent messages formatted as context.
	BuildSessionContext(sessionID string, limit int) string
	// AddSessionMessage records a message in the session.
	AddSessionMessage(sessionID, role, content string)
	// UpdateSessionStats updates cost/token stats for a session.
	UpdateSessionStats(sessionID string, cost, tokensIn, tokensOut, msgCount float64)
	// RecordHistory saves task execution history.
	RecordHistory(taskID, name, source, agent, outputFile string, task, result interface{})
	// PublishEvent publishes an SSE event to connected clients.
	PublishEvent(eventType string, data map[string]interface{})
	// IsActive returns whether the system is currently busy.
	IsActive() bool
	// ExpandPrompt expands system prompt variables for an agent.
	ExpandPrompt(prompt, agent string) string
	// LoadAgentPrompt loads the system prompt for a named agent.
	LoadAgentPrompt(agent string) (string, error)
	// FillTaskDefaults fills in default task field values.
	FillTaskDefaults(agent *string, name *string, source string) (taskID string)
	// HistoryDB returns the path to the history database.
	HistoryDB() string
	// WorkspaceDir returns the workspace directory.
	WorkspaceDir() string
	// SaveUpload saves an uploaded file, returns path.
	SaveUpload(filename string, data []byte) (string, error)
	// Truncate truncates string to maxLen.
	Truncate(s string, maxLen int) string
	// NewTraceID generates a trace ID for a source.
	NewTraceID(source string) string
	// WithTraceID adds trace ID to context.
	WithTraceID(ctx context.Context, traceID string) context.Context
	// LogInfo logs at INFO level.
	LogInfo(msg string, args ...interface{})
	// LogWarn logs at WARN level.
	LogWarn(msg string, args ...interface{})
	// LogError logs at ERROR level with error.
	LogError(msg string, err error, args ...interface{})
	// LogInfoCtx logs at INFO level with context.
	LogInfoCtx(ctx context.Context, msg string, args ...interface{})
	// LogErrorCtx logs at ERROR level with context and error.
	LogErrorCtx(ctx context.Context, msg string, err error, args ...interface{})
	// LogDebugCtx logs at DEBUG level with context.
	LogDebugCtx(ctx context.Context, msg string, args ...interface{})
	// ClientIP extracts the client IP from a request.
	ClientIP(r *http.Request) string
	// AuditLog records an audit event.
	AuditLog(action, source, target, ip string)
	// QueryCostStats returns cost statistics.
	QueryCostStats() (today, week, month float64)
	// UpdateAgentModel changes the model for an agent.
	UpdateAgentModel(agent, model string) error
	// MaybeCompactSession compacts a session if needed.
	MaybeCompactSession(sessionID string, msgCount int, tokenCount float64)
	// UpdateSessionTitle updates the title of a session.
	UpdateSessionTitle(sessionID, title string)
	// SessionContextLimit returns the configured context message limit.
	SessionContextLimit() int
	// AgentConfig returns the model and permission mode for a named agent.
	AgentConfig(agent string) (model, permMode string, found bool)
}
