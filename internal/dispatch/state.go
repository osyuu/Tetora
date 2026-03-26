package dispatch

import "context"

// StateTracker provides an abstract view of the dispatch state.
// Root dispatchState satisfies this interface; consumer packages use it
// to check status, register tasks, and publish SSE events without
// depending on root-only concrete types.
type StateTracker interface {
	// IsActive returns true when a batch dispatch is in progress.
	IsActive() bool
	// IsDraining returns true during graceful shutdown.
	IsDraining() bool
	// SetDraining enables or disables drain mode.
	SetDraining(bool)
	// Cancel cancels the dispatch context (graceful stop).
	Cancel()
	// PublishSSE publishes an SSE event to task, session, and dashboard channels.
	PublishSSE(event SSEEvent)
	// SSEBroker returns the underlying SSE broker for direct subscriptions.
	SSEBroker() SSEBrokerPublisher
	// StatusJSON returns the current dispatch status as JSON bytes.
	StatusJSON() []byte
	// RegisterRunning records a task as actively running.
	RegisterRunning(taskID string, task Task, cancel context.CancelFunc)
	// CompleteRunning removes a task from the running set.
	CompleteRunning(taskID string)
	// CancelTask cancels a specific running task by ID. Returns true if found.
	CancelTask(taskID string) bool
	// RunningCount returns the number of currently running tasks.
	RunningCount() int
	// FinishedResults returns completed task results.
	FinishedResults() []TaskResult
}
