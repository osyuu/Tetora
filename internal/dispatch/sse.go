// Package dispatch provides shared types for task dispatch, routing, and SSE streaming.
// These types are used across multiple domain packages (discord, telegram, workflow, etc.)
// and must live in internal/ to break the root-only dependency.
package dispatch

import (
	"sync"
	"sync/atomic"
	"time"
)

// SSEEvent represents a Server-Sent Event for task/session streaming.
type SSEEvent struct {
	Type          string `json:"type"`                   // "started", "progress", "output_chunk", "completed", "error", "heartbeat"
	TaskID        string `json:"taskId,omitempty"`        // which task produced this event
	SessionID     string `json:"sessionId,omitempty"`     // which session this belongs to
	WorkflowRunID string `json:"workflowRunId,omitempty"` // workflow run ID for routing
	Data          any    `json:"data,omitempty"`          // event-specific payload
	Timestamp     string `json:"timestamp"`               // RFC3339
}

// SSE event type constants.
const (
	SSEStarted           = "started"
	SSEProgress          = "progress"
	SSEOutputChunk       = "output_chunk"
	SSECompleted         = "completed"
	SSEError             = "error"
	SSEHeartbeat         = "heartbeat"
	SSEQueued            = "task_queued"
	SSETaskReceived      = "task_received"
	SSETaskRouting       = "task_routing"
	SSEDiscordProcessing = "discord_processing"
	SSEDiscordReplying   = "discord_replying"
	SSEDashboardKey      = "__dashboard__"
	SSEToolCall          = "tool_call"
	SSEToolResult        = "tool_result"
	SSESessionMessage    = "session_message"
	SSEAgentState        = "agent_state"
	SSEHeartbeatAlert    = "heartbeat_alert"
	SSETaskStalled       = "task_stalled"
	SSETaskRecovered     = "task_recovered"
	SSEWorkerUpdate      = "worker_update"
	SSEHookEvent         = "hook_event"
	SSEPlanReview        = "plan_review"
)

// SSEBrokerPublisher is the interface for publishing SSE events.
// Broker satisfies this; domain packages use this interface.
type SSEBrokerPublisher interface {
	Publish(key string, event SSEEvent)
	PublishMulti(keys []string, event SSEEvent)
	HasSubscribers(key string) bool
}

// Compile-time check: Broker implements SSEBrokerPublisher.
var _ SSEBrokerPublisher = (*Broker)(nil)

// Broker is an in-memory pub/sub hub for SSE events.
// Clients subscribe by key (task ID or session ID) and receive events on a channel.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan SSEEvent]struct{}
	dropCount   atomic.Int64
}

func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string]map[chan SSEEvent]struct{}),
	}
}

// Subscribe registers a new listener for the given key.
// Returns the event channel and an unsubscribe function.
// The channel is buffered to avoid blocking publishers.
func (b *Broker) Subscribe(key string) (chan SSEEvent, func()) {
	ch := make(chan SSEEvent, 64)
	b.mu.Lock()
	if b.subscribers[key] == nil {
		b.subscribers[key] = make(map[chan SSEEvent]struct{})
	}
	b.subscribers[key][ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[key]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(b.subscribers, key)
			}
		}
		for len(ch) > 0 {
			<-ch
		}
	}
	return ch, unsubscribe
}

// Publish sends an event to all subscribers of the given key.
// Non-blocking: if a subscriber's channel is full, the event is dropped.
func (b *Broker) Publish(key string, event SSEEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	b.mu.RLock()
	channels := make([]chan SSEEvent, 0, len(b.subscribers[key]))
	for ch := range b.subscribers[key] {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- event:
		default:
			b.dropCount.Add(1)
		}
	}
}

// PublishMulti sends an event to subscribers of all given keys.
func (b *Broker) PublishMulti(keys []string, event SSEEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	b.mu.RLock()
	seen := make(map[chan SSEEvent]struct{})
	for _, key := range keys {
		for ch := range b.subscribers[key] {
			seen[ch] = struct{}{}
		}
	}
	channels := make([]chan SSEEvent, 0, len(seen))
	for ch := range seen {
		channels = append(channels, ch)
	}
	b.mu.RUnlock()

	for _, ch := range channels {
		select {
		case ch <- event:
		default:
			b.dropCount.Add(1)
		}
	}
}

// DroppedEvents returns the total number of dropped events and resets the counter.
func (b *Broker) DroppedEvents() int64 {
	return b.dropCount.Swap(0)
}

// TotalDroppedEvents returns the total number of dropped events without resetting.
func (b *Broker) TotalDroppedEvents() int64 {
	return b.dropCount.Load()
}

// ClientCount returns the total number of active subscribers across all keys.
func (b *Broker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, subs := range b.subscribers {
		n += len(subs)
	}
	return n
}

func (b *Broker) HasSubscribers(key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers[key]) > 0
}
