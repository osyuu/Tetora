package dispatch

import "context"

// TaskExecutor runs a single task and returns the result.
// Root package wraps runSingleTask as a concrete implementation;
// consumer packages depend only on this interface.
type TaskExecutor interface {
	RunTask(ctx context.Context, task Task, agentName string) TaskResult
}

// TaskExecutorFunc is an adapter to allow the use of ordinary functions as TaskExecutor.
type TaskExecutorFunc func(ctx context.Context, task Task, agentName string) TaskResult

// RunTask calls f(ctx, task, agentName).
func (f TaskExecutorFunc) RunTask(ctx context.Context, task Task, agentName string) TaskResult {
	return f(ctx, task, agentName)
}
