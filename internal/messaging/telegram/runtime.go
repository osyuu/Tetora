// Package telegram provides the Telegram bot integration.
package telegram

import (
	"context"

	"tetora/internal/messaging"
)

// DispatchTask represents a single task for multi-task dispatch.
type DispatchTask struct {
	Name   string
	Prompt string
	Model  string
	Agent  string
	MCP    string
	Source string
}

// DispatchTaskResult holds the result of a single dispatched task.
type DispatchTaskResult struct {
	ID         string
	Name       string
	Status     string
	Output     string
	Error      string
	CostUSD    float64
	DurationMs int64
}

// DispatchResult holds the result of a multi-task dispatch.
type DispatchResult struct {
	Tasks      []DispatchTaskResult
	TotalCost  float64
	DurationMs int64
}

// RouteResult holds the result of smart dispatch routing.
type RouteResult struct {
	Agent      string
	Method     string
	Confidence string
}

// SmartDispatchResult holds the full result of a routed task.
type SmartDispatchResult struct {
	Route    RouteResult
	Task     messaging.TaskResult
	ReviewOK *bool
	Review   string
}

// CostEstimateTask is a single task estimate.
type CostEstimateTask struct {
	Model            string
	Provider         string
	EstimatedCostUSD float64
	Breakdown        string
}

// CostEstimate holds the full cost estimate for a set of tasks.
type CostEstimate struct {
	Tasks              []CostEstimateTask
	TotalEstimatedCost float64
	ClassifyCost       float64
}

// TrustLevel represents the trust level of an agent.
type TrustLevel string

const (
	TrustObserve TrustLevel = "observe"
	TrustSuggest TrustLevel = "suggest"
	TrustAuto    TrustLevel = "auto"
)

// TrustStatus holds the trust state for a single agent.
type TrustStatus struct {
	Agent              string
	Level              TrustLevel
	ConsecutiveSuccess int
	PromoteReady       bool
	NextLevel          TrustLevel
}

// CronJobInfo holds display info for a cron job.
type CronJobInfo struct {
	ID       string
	Name     string
	Schedule string
	Enabled  bool
	Running  bool
	NextRun  interface{} // time.Time or zero
	Errors   int
	AvgCost  float64
}

// TaskStats holds dashboard task statistics.
type TaskStats struct {
	Todo    int
	Running int
	Review  int
	Done    int
	Failed  int
	Total   int
}

// StuckTask is a task that has been running for too long.
type StuckTask struct {
	Title     string
	CreatedAt string
}

// ApprovalRequest describes a pending approval request.
type ApprovalRequest struct {
	ID      string
	Tool    string
	Summary string
}

// ApprovalGate processes pre-execution approval requests.
type ApprovalGate interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error)
	AutoApprove(toolName string)
	IsAutoApproved(toolName string) bool
}

// TelegramRuntime extends BotRuntime with Telegram-specific capabilities
// that require deep access to root package internals.
type TelegramRuntime interface {
	messaging.BotRuntime

	// Dispatch runs a set of tasks in parallel and returns aggregated results.
	Dispatch(ctx context.Context, tasks []DispatchTask) *DispatchResult

	// DispatchStatus returns the current dispatch status string.
	DispatchStatus() string

	// DispatchActive returns true if there is currently an active dispatch.
	DispatchActive() bool

	// CancelDispatch cancels any active dispatch.
	CancelDispatch()

	// RouteAndRun routes a prompt to an agent and runs the task.
	// Returns the full SmartDispatchResult.
	RouteAndRun(ctx context.Context, prompt, source, sessionID, sessionCtx string) *SmartDispatchResult

	// RunAsk runs a single task with the default agent.
	RunAsk(ctx context.Context, prompt, sessionID, sessionCtx string) messaging.TaskResult

	// EstimateCost estimates the cost of executing the given prompt.
	EstimateCost(prompt string) *CostEstimate

	// EstimateThreshold returns the cost confirmation threshold.
	EstimateThreshold() float64

	// GetTrustLevel returns the trust level for an agent.
	GetTrustLevel(agent string) TrustLevel

	// GetAllTrustStatuses returns trust statuses for all agents.
	GetAllTrustStatuses() []TrustStatus

	// ReviewOutput runs coordinator review on task output.
	ReviewOutput(ctx context.Context, prompt, output, agent string) (bool, string)

	// SetMemory stores a value in agent memory.
	SetMemory(agent, key, value string)

	// SearchMemory searches memory files for a keyword.
	SearchMemory(keyword string) string

	// GetCostStats returns cost statistics (today, week, month).
	GetCostStats() (today, week, month float64)

	// GetCostByJob returns per-job cost map for the last 30 days.
	GetCostByJob() map[string]float64

	// GetTaskStats returns dashboard task board statistics.
	GetTaskStats() (*TaskStats, error)

	// GetStuckTasks returns tasks that have been running over threshold minutes.
	GetStuckTasks(thresholdMin int) []StuckTask

	// CronListJobs returns the list of cron jobs.
	CronListJobs() []CronJobInfo

	// CronToggleJob enables or disables a cron job.
	CronToggleJob(id string, enabled bool) error

	// CronRunJob triggers a cron job by ID immediately.
	CronRunJob(ctx context.Context, id string) error

	// CronApproveJob approves a pending cron job.
	CronApproveJob(id string) error

	// CronRejectJob rejects a pending cron job.
	CronRejectJob(id string) error

	// CronAvailable returns true if the cron engine is initialized.
	CronAvailable() bool

	// MaxConcurrent returns the configured max concurrent tasks.
	MaxConcurrent() int

	// SmartDispatchEnabled returns true if smart dispatch is enabled.
	SmartDispatchEnabled() bool

	// SmartDispatchReview returns true if coordinator review is enabled.
	SmartDispatchReview() bool

	// StreamToChannels returns true if task streaming to channels is enabled.
	StreamToChannels() bool

	// DefaultWorkdir returns the configured default work directory.
	DefaultWorkdir() string

	// ApprovalGatesEnabled returns true if approval gates are enabled.
	ApprovalGatesEnabled() bool

	// ApprovalGateAutoApproveTools returns the list of auto-approved tools.
	ApprovalGateAutoApproveTools() []string

	// SubscribeTaskEvents subscribes to SSE events for a specific task.
	// Returns a channel of events and an unsubscribe function.
	SubscribeTaskEvents(taskID string) (<-chan SSEEvent, func())

	// SSEBrokerAvailable returns true if the SSE broker is available.
	SSEBrokerAvailable() bool

	// GetOrCreateChannelSession finds or creates a session with full session info.
	GetOrCreateChannelSession(platform, key, agent, title string) (*ChannelSession, error)

	// ArchiveChannelSession archives an existing channel session.
	ArchiveChannelSession(key string) error

	// ChannelSessionKey builds the canonical key for a channel session.
	ChannelSessionKey(platform, agent string) string

	// WrapWithContext wraps a prompt with session context.
	WrapWithContext(sessionCtx, prompt string) string

	// ProviderHasNativeSession returns true if the provider manages its own session.
	ProviderHasNativeSession(agent string) bool

	// SaveFileUpload downloads a Telegram file by ID and saves it to uploads dir.
	// Returns (filename, content, error).
	SaveFileUpload(telegramToken, fileID, hint string) (filename string, data []byte, err error)

	// SaveUploadedFile saves raw bytes to the uploads directory.
	SaveUploadedFile(filename string, data []byte, source string) (path string, err error)

	// FormatResultCostFooter returns a cost footer string for a task result, or "".
	FormatResultCostFooter(result *messaging.TaskResult) string

	// Agents returns agent name → model mapping for display.
	AgentModels() map[string]string

	// UpdateAgentModelByName updates the model for a named agent, returns old model.
	UpdateAgentModelByName(agent, model string) (old string, err error)

	// DefaultSmartDispatchAgent returns the configured default smart dispatch agent.
	DefaultSmartDispatchAgent() string

	// RecordAndCompact records session messages + compacts if needed.
	RecordAndCompact(sessID string, msgCount int, tokensIn float64, userMsg, assistantMsg string, result *messaging.TaskResult)

	// NewUUID generates a new random UUID string.
	NewUUID() string

	// RetryTask re-runs a previously failed task by ID.
	RetryTask(ctx context.Context, taskID string) (*RetryResult, error)

	// RerouteTask re-dispatches a previously failed task via smart dispatch.
	RerouteTask(ctx context.Context, taskID string) (*SmartDispatchResult, error)
}

// RetryResult holds the result of a retried task.
type RetryResult struct {
	TaskID     string
	Name       string
	Status     string
	Output     string
	Error      string
	CostUSD    float64
	DurationMs int64
}

// SSEEvent represents a single SSE event from the broker.
type SSEEvent struct {
	Type string
	Data interface{}
}

// ChannelSession holds session state for a messaging channel.
type ChannelSession struct {
	ID           string
	MessageCount int
	TotalTokensIn float64
}
