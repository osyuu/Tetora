package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tetora/internal/circuit"
	dtypes "tetora/internal/dispatch"
	"tetora/internal/log"
)

// QueueItem is a type alias so root code uses dtypes.QueueItem transparently.
type QueueItem = dtypes.QueueItem

// maxQueueRetries is the maximum number of retry attempts for a queued task.
const maxQueueRetries = dtypes.MaxQueueRetries

// --- DB Init ---

func initQueueDB(dbPath string) error {
	return dtypes.InitQueueDB(dbPath)
}

// --- Enqueue ---

// enqueueTask marshals task and adds it to the offline queue.
func enqueueTask(dbPath string, task Task, agentName string, priority int) error {
	taskBytes, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	return dtypes.EnqueueTask(dbPath, string(taskBytes), task.Source, agentName, priority)
}

// --- Dequeue ---

func dequeueNext(dbPath string) *QueueItem {
	return dtypes.DequeueNext(dbPath)
}

// --- Query ---

func queryQueue(dbPath, status string) []QueueItem {
	return dtypes.QueryQueue(dbPath, status)
}

func queryQueueItem(dbPath string, id int) *QueueItem {
	return dtypes.QueryQueueItem(dbPath, id)
}

// --- Update ---

func updateQueueStatus(dbPath string, id int, status, errMsg string) {
	dtypes.UpdateQueueStatus(dbPath, id, status, errMsg)
}

func incrementQueueRetry(dbPath string, id int, status, errMsg string) {
	dtypes.IncrementQueueRetry(dbPath, id, status, errMsg)
}

// --- Delete ---

func deleteQueueItem(dbPath string, id int) error {
	return dtypes.DeleteQueueItem(dbPath, id)
}

// --- Cleanup ---

func cleanupExpiredQueue(dbPath string, ttl time.Duration) int {
	return dtypes.CleanupExpiredQueue(dbPath, ttl)
}

func cleanupOldQueueItems(dbPath string, days int) {
	dtypes.CleanupOldQueueItems(dbPath, days)
}

func countPendingQueue(dbPath string) int {
	return dtypes.CountPendingQueue(dbPath)
}

// --- Max Items Check ---

func isQueueFull(dbPath string, maxItems int) bool {
	return dtypes.IsQueueFull(dbPath, maxItems)
}

// --- Provider Availability ---

func isAllProvidersUnavailable(errMsg string) bool {
	return dtypes.IsAllProvidersUnavailable(errMsg)
}

// --- Queue Drainer ---

// queueDrainer processes offline queue items when providers recover.
// Kept in root because it depends on dispatchState, runSingleTask,
// recordHistory, and recordSessionActivity — all root-only constructs.
type queueDrainer struct {
	cfg      *Config
	sem      chan struct{}
	childSem chan struct{}
	state    *dispatchState
	notifyFn func(string)
	ttl      time.Duration
}

// anyProviderAvailable checks if at least one provider's circuit allows requests.
func (d *queueDrainer) anyProviderAvailable() bool {
	if d.cfg.Runtime.CircuitRegistry == nil {
		return true // no circuit breaker = always available
	}
	for name := range d.cfg.Providers {
		cb := d.cfg.Runtime.CircuitRegistry.(*circuit.Registry).Get(name)
		if cb.State() != circuit.Open {
			return true
		}
	}
	return false
}

// run starts the queue drainer loop.
func (d *queueDrainer) run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	log.Info("queue drainer started", "ttl", d.ttl.String())

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *queueDrainer) tick(ctx context.Context) {
	dbPath := d.cfg.HistoryDB
	if dbPath == "" {
		return
	}

	// 1. Expire old items.
	expired := cleanupExpiredQueue(dbPath, d.ttl)
	if expired > 0 {
		log.Warn("queue items expired", "count", expired)
		if d.notifyFn != nil {
			d.notifyFn(fmt.Sprintf("Offline queue: %d item(s) expired (TTL %s)", expired, d.ttl.String()))
		}
	}

	// 2. Check if any provider is available.
	if !d.anyProviderAvailable() {
		return
	}

	// 3. Drain pending items one at a time.
	for {
		if ctx.Err() != nil {
			return
		}

		item := dequeueNext(dbPath)
		if item == nil {
			return // queue empty
		}

		d.processItem(ctx, item)
	}
}

func (d *queueDrainer) processItem(ctx context.Context, item *QueueItem) {
	// Deserialize task.
	var task Task
	if err := json.Unmarshal([]byte(item.TaskJSON), &task); err != nil {
		log.Error("queue: bad task JSON", "id", item.ID, "error", err)
		updateQueueStatus(d.cfg.HistoryDB, item.ID, "failed", "invalid task JSON: "+err.Error())
		return
	}

	// Generate new IDs for the retry.
	task.ID = newUUID()
	task.SessionID = newUUID()
	task.Source = "queue:" + task.Source

	log.InfoCtx(ctx, "queue: retrying task", "queueId", item.ID, "taskId", task.ID[:8], "name", task.Name, "retry", item.RetryCount+1)

	// Run the task.
	result := runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, item.AgentName)

	if result.Status == "success" {
		updateQueueStatus(d.cfg.HistoryDB, item.ID, "completed", "")
		log.InfoCtx(ctx, "queue: task succeeded", "queueId", item.ID, "taskId", task.ID[:8])

		// Record to history.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
		recordSessionActivity(d.cfg.HistoryDB, task, result, item.AgentName)

		if d.notifyFn != nil {
			d.notifyFn(fmt.Sprintf("Offline queue: task %q completed successfully (retry #%d)", task.Name, item.RetryCount+1))
		}
	} else if isAllProvidersUnavailable(result.Error) {
		// Still unavailable — put back in queue.
		if item.RetryCount+1 >= maxQueueRetries {
			incrementQueueRetry(d.cfg.HistoryDB, item.ID, "failed", result.Error)
			log.WarnCtx(ctx, "queue: task failed after max retries", "queueId", item.ID, "retries", maxQueueRetries)
			if d.notifyFn != nil {
				d.notifyFn(fmt.Sprintf("Offline queue: task %q failed after %d retries: %s",
					task.Name, maxQueueRetries, truncate(result.Error, 200)))
			}
		} else {
			incrementQueueRetry(d.cfg.HistoryDB, item.ID, "pending", result.Error)
			log.InfoCtx(ctx, "queue: task still unavailable, re-queued", "queueId", item.ID, "retry", item.RetryCount+1)
		}
	} else {
		// Non-provider error — mark as failed.
		incrementQueueRetry(d.cfg.HistoryDB, item.ID, "failed", result.Error)
		log.WarnCtx(ctx, "queue: task failed with non-provider error", "queueId", item.ID, "error", result.Error)

		// Record to history even on failure.
		start := time.Now().Add(-time.Duration(result.DurationMs) * time.Millisecond)
		recordHistory(d.cfg.HistoryDB, task.ID, task.Name, task.Source, item.AgentName, task, result,
			start.Format(time.RFC3339), time.Now().Format(time.RFC3339), result.OutputFile)
	}
}
