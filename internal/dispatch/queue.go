package dispatch

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tetora/internal/db"
)

// --- Queue Item ---

// QueueItem represents a task buffered in the offline queue.
type QueueItem struct {
	ID         int    `json:"id"`
	TaskJSON   string `json:"taskJson"`
	AgentName  string `json:"agent"`
	Source     string `json:"source"`
	Priority   int    `json:"priority"`   // higher = processed sooner (0 = normal)
	Status     string `json:"status"`     // pending, processing, completed, expired, failed
	RetryCount int    `json:"retryCount"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	Error      string `json:"error,omitempty"`
}

// MaxQueueRetries is the maximum number of retry attempts for a queued task.
const MaxQueueRetries = 3

// --- DB Init ---

// InitQueueDB creates the offline_queue table if it does not exist.
func InitQueueDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS offline_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_json TEXT NOT NULL,
    agent TEXT DEFAULT '',
    source TEXT DEFAULT '',
    priority INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'pending',
    retry_count INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    error TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_queue_status ON offline_queue(status);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("init offline_queue: %s: %w", string(out), err)
	}
	return nil
}

// --- Enqueue ---

// EnqueueTask adds a task to the offline queue for later retry.
// The task is serialized as JSON; agentName and priority are stored alongside it.
func EnqueueTask(dbPath string, taskJSON string, source string, agentName string, priority int) error {
	if dbPath == "" {
		return fmt.Errorf("no db path")
	}

	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO offline_queue (task_json, agent, source, priority, status, retry_count, created_at, updated_at)
		 VALUES ('%s','%s','%s',%d,'pending',0,'%s','%s')`,
		db.Escape(taskJSON),
		db.Escape(agentName),
		db.Escape(source),
		priority,
		db.Escape(now),
		db.Escape(now),
	)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("enqueue: %s: %w", string(out), err)
	}
	return nil
}

// --- Dequeue ---

// DequeueNext retrieves the oldest pending item (respecting priority) and marks it "processing".
// Returns nil if no pending items.
func DequeueNext(dbPath string) *QueueItem {
	if dbPath == "" {
		return nil
	}

	// Select highest-priority, oldest pending item.
	selectSQL := `SELECT id, task_json, agent, source, priority, status, retry_count, created_at, updated_at, error
		FROM offline_queue
		WHERE status = 'pending'
		ORDER BY priority DESC, id ASC
		LIMIT 1`

	rows, err := db.Query(dbPath, selectSQL)
	if err != nil || len(rows) == 0 {
		return nil
	}

	item := queueItemFromRow(rows[0])

	// Mark as processing.
	now := time.Now().Format(time.RFC3339)
	updateSQL := fmt.Sprintf(
		`UPDATE offline_queue SET status = 'processing', updated_at = '%s' WHERE id = %d AND status = 'pending'`,
		db.Escape(now), item.ID)
	cmd := exec.Command("sqlite3", dbPath, updateSQL)
	if _, err := cmd.CombinedOutput(); err != nil {
		return nil
	}

	item.Status = "processing"
	return &item
}

// --- Query ---

// QueryQueue returns queue items, optionally filtered by status (empty string = all).
func QueryQueue(dbPath, status string) []QueueItem {
	if dbPath == "" {
		return nil
	}

	where := ""
	if status != "" {
		where = fmt.Sprintf("WHERE status = '%s'", db.Escape(status))
	}

	sql := fmt.Sprintf(
		`SELECT id, task_json, agent, source, priority, status, retry_count, created_at, updated_at, error
		 FROM offline_queue %s
		 ORDER BY priority DESC, id ASC`, where)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}

	var items []QueueItem
	for _, row := range rows {
		items = append(items, queueItemFromRow(row))
	}
	return items
}

// QueryQueueItem returns a single queue item by ID.
func QueryQueueItem(dbPath string, id int) *QueueItem {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT id, task_json, agent, source, priority, status, retry_count, created_at, updated_at, error
		 FROM offline_queue WHERE id = %d`, id)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	item := queueItemFromRow(rows[0])
	return &item
}

// --- Update ---

// UpdateQueueStatus updates the status and error message of a queue item.
func UpdateQueueStatus(dbPath string, id int, status, errMsg string) {
	if dbPath == "" {
		return
	}
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE offline_queue SET status = '%s', error = '%s', updated_at = '%s' WHERE id = %d`,
		db.Escape(status), db.Escape(errMsg), db.Escape(now), id)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput() //nolint:errcheck
}

// IncrementQueueRetry increments retry_count and updates the status.
func IncrementQueueRetry(dbPath string, id int, status, errMsg string) {
	if dbPath == "" {
		return
	}
	now := time.Now().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`UPDATE offline_queue SET status = '%s', error = '%s', retry_count = retry_count + 1, updated_at = '%s' WHERE id = %d`,
		db.Escape(status), db.Escape(errMsg), db.Escape(now), id)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput() //nolint:errcheck
}

// --- Delete ---

// DeleteQueueItem removes a queue item by ID.
func DeleteQueueItem(dbPath string, id int) error {
	if dbPath == "" {
		return fmt.Errorf("no db path")
	}
	sql := fmt.Sprintf(`DELETE FROM offline_queue WHERE id = %d`, id)
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete queue item: %s: %w", string(out), err)
	}
	return nil
}

// --- Cleanup ---

// CleanupExpiredQueue marks pending items older than ttl as "expired".
// Returns the number of expired items.
func CleanupExpiredQueue(dbPath string, ttl time.Duration) int {
	if dbPath == "" {
		return 0
	}

	cutoff := time.Now().Add(-ttl).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)

	// Count before updating.
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM offline_queue WHERE status = 'pending' AND created_at < '%s'`,
		db.Escape(cutoff))
	rows, err := db.Query(dbPath, countSQL)
	if err != nil || len(rows) == 0 {
		return 0
	}
	count := queueJSONInt(rows[0]["cnt"])
	if count == 0 {
		return 0
	}

	// Update expired items.
	updateSQL := fmt.Sprintf(
		`UPDATE offline_queue SET status = 'expired', error = 'TTL exceeded', updated_at = '%s'
		 WHERE status = 'pending' AND created_at < '%s'`,
		db.Escape(now), db.Escape(cutoff))
	cmd := exec.Command("sqlite3", dbPath, updateSQL)
	cmd.CombinedOutput() //nolint:errcheck

	return count
}

// CleanupOldQueueItems removes completed/expired/failed items older than n days.
func CleanupOldQueueItems(dbPath string, days int) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`DELETE FROM offline_queue WHERE status IN ('completed','expired','failed') AND datetime(updated_at) < datetime('now','-%d days')`,
		days)
	cmd := exec.Command("sqlite3", dbPath, sql)
	cmd.CombinedOutput() //nolint:errcheck
}

// CountPendingQueue returns the number of pending/processing items in the queue.
func CountPendingQueue(dbPath string) int {
	if dbPath == "" {
		return 0
	}
	rows, err := db.Query(dbPath, `SELECT COUNT(*) as cnt FROM offline_queue WHERE status IN ('pending','processing')`)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return queueJSONInt(rows[0]["cnt"])
}

// IsQueueFull reports whether the queue has reached its max capacity.
func IsQueueFull(dbPath string, maxItems int) bool {
	return CountPendingQueue(dbPath) >= maxItems
}

// --- Provider Availability ---

// IsAllProvidersUnavailable reports whether an error message indicates total provider failure.
func IsAllProvidersUnavailable(errMsg string) bool {
	return strings.Contains(strings.ToLower(errMsg), "all providers unavailable")
}

// --- Marshal helpers used by callers ---

// MarshalTask serializes a value to JSON for storage in task_json.
func MarshalTask(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// --- Internal helpers ---

func queueItemFromRow(row map[string]any) QueueItem {
	return QueueItem{
		ID:         queueJSONInt(row["id"]),
		TaskJSON:   queueJSONStr(row["task_json"]),
		AgentName:  queueJSONStr(row["agent"]),
		Source:     queueJSONStr(row["source"]),
		Priority:   queueJSONInt(row["priority"]),
		Status:     queueJSONStr(row["status"]),
		RetryCount: queueJSONInt(row["retry_count"]),
		CreatedAt:  queueJSONStr(row["created_at"]),
		UpdatedAt:  queueJSONStr(row["updated_at"]),
		Error:      queueJSONStr(row["error"]),
	}
}

func queueJSONStr(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func queueJSONInt(v any) int {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		var n int
		fmt.Sscanf(t, "%d", &n)
		return n
	}
	return 0
}

