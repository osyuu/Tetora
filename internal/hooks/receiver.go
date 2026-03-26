package hooks

// receiver.go — hookReceiver: processes Claude Code hook events, tracks workers,
// caches plans, and routes SSE notifications. Lives in internal/hooks so root
// can hold only the HTTP handlers that require root-only types (DiscordBot, Server).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/dispatch"
	"tetora/internal/log"
)

// --- Event Types ---

// HookEvent represents a Claude Code hook event payload (flat format).
// See: https://code.claude.com/docs/en/hooks
type HookEvent struct {
	// New flat format (Claude Code 2025+).
	HookEventName  string          `json:"hook_event_name"`                  // "PreToolUse", "PostToolUse", "Stop", "Notification"
	SessionID      string          `json:"session_id"`                       // session UUID
	Cwd            string          `json:"cwd,omitempty"`                    // working directory
	ToolName       string          `json:"tool_name,omitempty"`              // tool name (PreToolUse/PostToolUse)
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`             // tool input
	ToolResponse   json.RawMessage `json:"tool_response,omitempty"`          // tool output (PostToolUse)
	ToolUseID      string          `json:"tool_use_id,omitempty"`            // tool use ID
	StopHookActive bool            `json:"stop_hook_active,omitempty"`       // Stop event
	LastAssistant  string          `json:"last_assistant_message,omitempty"` // Stop event

	// Legacy nested format (backward compat).
	Type    string        `json:"type"`                // old: "PreToolUse", etc.
	Tool    *HookToolInfo `json:"tool,omitempty"`      // old: nested tool info
	Session *HookSession  `json:"session,omitempty"`   // old: nested session
	Stop    *HookStopInfo `json:"stop_info,omitempty"` // old: nested stop info

	Timestamp string          `json:"timestamp,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// ResolvedType returns the event type, supporting both new and legacy format.
func (e *HookEvent) ResolvedType() string {
	if e.HookEventName != "" {
		return e.HookEventName
	}
	return e.Type
}

// ResolvedSessionID returns the session ID from either format.
func (e *HookEvent) ResolvedSessionID() string {
	if e.SessionID != "" {
		return e.SessionID
	}
	if e.Session != nil {
		return e.Session.ID
	}
	return ""
}

// ResolvedToolName returns the tool name from either format.
func (e *HookEvent) ResolvedToolName() string {
	if e.ToolName != "" {
		return e.ToolName
	}
	if e.Tool != nil {
		return e.Tool.Name
	}
	return ""
}

// ResolvedCwd returns the working directory from either format.
func (e *HookEvent) ResolvedCwd() string {
	if e.Cwd != "" {
		return e.Cwd
	}
	if e.Session != nil {
		return e.Session.Cwd
	}
	return ""
}

// HookToolInfo contains tool-related details (legacy format).
type HookToolInfo struct {
	Name  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input,omitempty"`
}

// HookSession identifies the Claude Code session (legacy format).
type HookSession struct {
	ID  string `json:"session_id"`
	Cwd string `json:"cwd,omitempty"`
}

// HookStopInfo contains details about why Claude Code stopped (legacy format).
type HookStopInfo struct {
	Reason string `json:"reason,omitempty"`
}

// PlanGateDecision represents the result of a plan gate review.
type PlanGateDecision struct {
	Approved bool
	Reason   string
}

// WorkerEvent is a single safe event entry (no sensitive data).
type WorkerEvent struct {
	Timestamp string `json:"timestamp"`
	EventType string `json:"eventType"` // "PreToolUse", "PostToolUse", "Stop"
	ToolName  string `json:"toolName,omitempty"`
}

const WorkerEventsMax = 50

// WorkerOrigin tracks where a worker came from (cron, dispatch, ask, etc.).
type WorkerOrigin struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
	Source   string `json:"source"`  // "cron", "dispatch", "ask", "route:eng", etc.
	Agent    string `json:"agent"`
	JobID    string `json:"jobId"` // cron job ID (empty = non-cron)
}

// WorkerInfo tracks a Claude Code session detected via hooks.
type WorkerInfo struct {
	SessionID string
	State     string // "working", "idle", "done"
	LastTool  string
	Cwd       string
	FirstSeen time.Time
	LastSeen  time.Time
	ToolCount int
	Events    []WorkerEvent // ring buffer, max WorkerEventsMax
	Origin    *WorkerOrigin // nil = manual session

	// Claude Code usage data (updated via statusline bridge).
	CostUSD      float64 `json:"-"`
	InputTokens  int     `json:"-"`
	OutputTokens int     `json:"-"`
	ContextPct   int     `json:"-"`
	Model        string  `json:"-"`
}

// CachedPlan stores plan file info detected from hook events.
type CachedPlan struct {
	SessionID string `json:"sessionId"`
	FilePath  string `json:"filePath"`
	Content   string `json:"content,omitempty"`
	CachedAt  time.Time
	// ReadyForReview: ExitPlanMode detected — plan is ready for review.
	ReadyForReview bool `json:"readyForReview"`
}

// --- Receiver ---

// Receiver processes incoming hook events and routes them to the system.
type Receiver struct {
	mu     sync.RWMutex
	broker *dispatch.Broker
	cfg    *config.Config

	// planCache stores recently seen plan file paths and content.
	planCache   map[string]*CachedPlan // sessionID → plan
	planCacheMu sync.RWMutex

	// planGates tracks pending plan gate long-poll channels.
	planGates   map[string]chan PlanGateDecision
	planGatesMu sync.Mutex

	// questionGates tracks pending ask-user long-poll channels.
	questionGates   map[string]chan string
	questionGatesMu sync.Mutex

	// hookWorkers tracks sessions detected via hooks.
	hookWorkers   map[string]*WorkerInfo
	hookWorkersMu sync.RWMutex

	// workerOrigins maps sessionID → origin (registered before CLI starts).
	workerOrigins   map[string]*WorkerOrigin
	workerOriginsMu sync.RWMutex

	// stats
	eventCount    int64
	lastEventTime time.Time
}

// NewReceiver creates a new hook event receiver.
func NewReceiver(broker *dispatch.Broker, cfg *config.Config) *Receiver {
	return &Receiver{
		broker:        broker,
		cfg:           cfg,
		planCache:     make(map[string]*CachedPlan),
		planGates:     make(map[string]chan PlanGateDecision),
		questionGates: make(map[string]chan string),
		hookWorkers:   make(map[string]*WorkerInfo),
		workerOrigins: make(map[string]*WorkerOrigin),
	}
}

// EventCount returns the total number of events processed.
func (hr *Receiver) EventCount() int64 {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return hr.eventCount
}

// Broker returns the SSE broker used by this receiver.
func (hr *Receiver) Broker() *dispatch.Broker {
	return hr.broker
}

// PlanGates returns the plan gates map with its mutex for root handlers.
func (hr *Receiver) RegisterPlanGate(gateID string, ch chan PlanGateDecision) {
	hr.planGatesMu.Lock()
	hr.planGates[gateID] = ch
	hr.planGatesMu.Unlock()
}

// UnregisterPlanGate removes a plan gate channel.
func (hr *Receiver) UnregisterPlanGate(gateID string) {
	hr.planGatesMu.Lock()
	delete(hr.planGates, gateID)
	hr.planGatesMu.Unlock()
}

// RegisterQuestionGate registers a question gate channel.
func (hr *Receiver) RegisterQuestionGate(qID string, ch chan string) {
	hr.questionGatesMu.Lock()
	hr.questionGates[qID] = ch
	hr.questionGatesMu.Unlock()
}

// UnregisterQuestionGate removes a question gate channel.
func (hr *Receiver) UnregisterQuestionGate(qID string) {
	hr.questionGatesMu.Lock()
	delete(hr.questionGates, qID)
	hr.questionGatesMu.Unlock()
}

// --- HTTP Handlers ---

// HandleEvent receives a hook event from Claude Code.
// POST /api/hooks/event
func (hr *Receiver) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event HookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	event.Raw = body

	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Extract session ID from various locations in the payload.
	sessionID := event.ResolvedSessionID()
	if sessionID == "" {
		sessionID = hr.ExtractSessionID(&event, body)
	}
	eventType := event.ResolvedType()

	// Update stats.
	hr.mu.Lock()
	hr.eventCount++
	hr.lastEventTime = time.Now()
	hr.mu.Unlock()

	// Route by event type.
	switch eventType {
	case "PreToolUse":
		hr.handlePreToolUse(&event, sessionID)
	case "PostToolUse":
		hr.handlePostToolUse(&event, sessionID)
	case "Stop":
		hr.handleStop(&event, sessionID)
	case "Notification":
		hr.handleNotification(&event, sessionID)
	default:
		log.Debug("hooks: unknown event type", "type", eventType)
	}

	// Publish raw event to dashboard SSE.
	if hr.broker != nil {
		hr.broker.Publish(dispatch.SSEDashboardKey, dispatch.SSEEvent{
			Type:      dispatch.SSEHookEvent,
			SessionID: sessionID,
			Data: map[string]any{
				"hookType":  eventType,
				"toolName":  event.ResolvedToolName(),
				"sessionId": sessionID,
				"timestamp": event.Timestamp,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// HandleStatus returns hook receiver status.
// GET /api/hooks/status
func (hr *Receiver) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	hr.mu.RLock()
	count := hr.eventCount
	lastEvent := hr.lastEventTime
	hr.mu.RUnlock()

	hr.planCacheMu.RLock()
	plans := make([]map[string]any, 0, len(hr.planCache))
	for sid, p := range hr.planCache {
		plans = append(plans, map[string]any{
			"sessionId":      sid,
			"filePath":       p.FilePath,
			"readyForReview": p.ReadyForReview,
			"cachedAt":       p.CachedAt.Format(time.RFC3339),
		})
	}
	hr.planCacheMu.RUnlock()

	resp := map[string]any{
		"eventCount":    count,
		"lastEventTime": lastEvent.Format(time.RFC3339),
		"planCache":     plans,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleUsageUpdate receives usage data from the Claude Code statusline script.
// POST /api/hooks/usage
func (hr *Receiver) HandleUsageUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionID    string  `json:"sessionId"`
		CostUSD      float64 `json:"costUsd"`
		InputTokens  int     `json:"inputTokens"`
		OutputTokens int     `json:"outputTokens"`
		ContextPct   int     `json:"contextPct"`
		Model        string  `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.SessionID == "" {
		http.Error(w, `{"error":"sessionId required"}`, http.StatusBadRequest)
		return
	}

	hr.hookWorkersMu.Lock()
	// Find worker by prefix match (statusline may send full or short session ID).
	var target *WorkerInfo
	for sid, wk := range hr.hookWorkers {
		if strings.HasPrefix(sid, body.SessionID) || strings.HasPrefix(body.SessionID, sid) {
			target = wk
			break
		}
	}
	if target == nil {
		// Create a minimal worker entry so usage data is visible even before first hook event.
		target = &WorkerInfo{
			SessionID: body.SessionID,
			State:     "working",
			FirstSeen: time.Now(),
			LastSeen:  time.Now(),
		}
		hr.hookWorkers[body.SessionID] = target
	}
	target.CostUSD = body.CostUSD
	target.InputTokens = body.InputTokens
	target.OutputTokens = body.OutputTokens
	target.ContextPct = body.ContextPct
	if body.Model != "" {
		target.Model = body.Model
	}
	target.LastSeen = time.Now()
	hr.hookWorkersMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// --- Event Handlers (internal) ---

func (hr *Receiver) handlePreToolUse(event *HookEvent, sessionID string) {
	toolName := event.ResolvedToolName()
	log.Debug("hooks: PreToolUse", "tool", toolName, "session", sessionID)

	hr.trackHookWorker(event, sessionID, "working", toolName, "PreToolUse")
}

func (hr *Receiver) handlePostToolUse(event *HookEvent, sessionID string) {
	toolName := event.ResolvedToolName()
	log.Debug("hooks: PostToolUse", "tool", toolName, "session", sessionID)

	hr.trackHookWorker(event, sessionID, "working", toolName, "PostToolUse")

	// Check for plan-related tool calls.
	switch toolName {
	case "Write", "Edit":
		// Check if writing to a plan file.
		hr.checkPlanFileWrite(event, sessionID)
	case "ExitPlanMode":
		// Plan review triggered — cache and publish.
		hr.HandlePlanReviewTrigger(sessionID)
	}
}

func (hr *Receiver) handleStop(event *HookEvent, sessionID string) {
	reason := ""
	if event.Stop != nil {
		reason = event.Stop.Reason
	}
	log.Info("hooks: Stop", "reason", reason, "session", sessionID)

	hr.trackHookWorker(event, sessionID, "done", "", "Stop")

	// Publish stop event.
	if hr.broker != nil {
		hr.broker.Publish(dispatch.SSEDashboardKey, dispatch.SSEEvent{
			Type:      dispatch.SSECompleted,
			SessionID: sessionID,
			Data: map[string]any{
				"reason":    reason,
				"sessionId": sessionID,
			},
		})
	}
}

func (hr *Receiver) handleNotification(event *HookEvent, sessionID string) {
	log.Info("hooks: Notification", "session", sessionID)

	if hr.broker != nil {
		hr.broker.Publish(dispatch.SSEDashboardKey, dispatch.SSEEvent{
			Type:      dispatch.SSEHookEvent,
			SessionID: sessionID,
			Data: map[string]any{
				"hookType":  "Notification",
				"sessionId": sessionID,
			},
		})
	}
}

// --- Plan File Detection ---

// checkPlanFileWrite checks if a Write/Edit tool call is targeting a plan file.
func (hr *Receiver) checkPlanFileWrite(event *HookEvent, sessionID string) {
	// Get tool input from either format.
	toolInput := event.ToolInput
	if len(toolInput) == 0 && event.Tool != nil {
		toolInput = event.Tool.Input
	}
	if len(toolInput) == 0 {
		return
	}

	// Parse tool input to find file_path.
	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(toolInput, &input); err != nil || input.FilePath == "" {
		return
	}

	// Check if writing to a plan file location.
	homeDir, _ := os.UserHomeDir()
	planDir := filepath.Join(homeDir, ".claude", "plans")
	if !strings.HasPrefix(input.FilePath, planDir) {
		return
	}

	log.Info("hooks: plan file write detected", "path", input.FilePath, "session", sessionID)

	// Read the plan file content.
	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		log.Warn("hooks: failed to read plan file", "path", input.FilePath, "error", err)
		content = nil
	}

	hr.planCacheMu.Lock()
	hr.planCache[sessionID] = &CachedPlan{
		SessionID: sessionID,
		FilePath:  input.FilePath,
		Content:   string(content),
		CachedAt:  time.Now(),
	}
	hr.planCacheMu.Unlock()
}

// HandlePlanReviewTrigger is called when ExitPlanMode is detected.
func (hr *Receiver) HandlePlanReviewTrigger(sessionID string) {
	log.Info("hooks: plan review triggered (ExitPlanMode)", "session", sessionID)

	hr.planCacheMu.Lock()
	plan, ok := hr.planCache[sessionID]
	if ok {
		plan.ReadyForReview = true
	} else {
		// ExitPlanMode without a Write — try to find the plan file.
		plan = &CachedPlan{
			SessionID:      sessionID,
			ReadyForReview: true,
			CachedAt:       time.Now(),
		}
		hr.planCache[sessionID] = plan
	}
	hr.planCacheMu.Unlock()

	// Publish plan review event to dashboard.
	if hr.broker != nil {
		data := map[string]any{
			"sessionId":      sessionID,
			"readyForReview": true,
		}
		if plan != nil {
			data["filePath"] = plan.FilePath
			if len(plan.Content) > 0 {
				// Truncate for SSE (full content via API).
				preview := plan.Content
				if len(preview) > 2000 {
					preview = preview[:2000] + "\n... (truncated)"
				}
				data["preview"] = preview
			}
		}
		hr.broker.Publish(dispatch.SSEDashboardKey, dispatch.SSEEvent{
			Type:      dispatch.SSEPlanReview,
			SessionID: sessionID,
			Data:      data,
		})
	}
}

// GetCachedPlan returns the cached plan for a session, if any.
func (hr *Receiver) GetCachedPlan(sessionID string) *CachedPlan {
	hr.planCacheMu.RLock()
	defer hr.planCacheMu.RUnlock()
	return hr.planCache[sessionID]
}

// ClearPlanCache removes a session's cached plan after review is complete.
func (hr *Receiver) ClearPlanCache(sessionID string) {
	hr.planCacheMu.Lock()
	delete(hr.planCache, sessionID)
	hr.planCacheMu.Unlock()
}

// --- Helpers ---

// ExtractSessionID tries to extract the session ID from various locations in the event.
func (hr *Receiver) ExtractSessionID(event *HookEvent, body []byte) string {
	// Try session field first.
	if event.Session != nil && event.Session.ID != "" {
		return event.Session.ID
	}

	// Try to extract from raw JSON (Claude Code may place it at different levels).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err == nil {
		for _, key := range []string{"session_id", "sessionId"} {
			if v, ok := raw[key]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil && s != "" {
					return s
				}
			}
		}
	}

	return ""
}

// CleanupStalePlans removes plan cache entries older than 1 hour.
func (hr *Receiver) CleanupStalePlans() {
	hr.planCacheMu.Lock()
	defer hr.planCacheMu.Unlock()
	cutoff := time.Now().Add(-1 * time.Hour)
	for sid, p := range hr.planCache {
		if p.CachedAt.Before(cutoff) {
			delete(hr.planCache, sid)
		}
	}
}

// --- Worker Tracking ---

// RegisterOrigin registers a worker origin before the CLI session starts.
func (hr *Receiver) RegisterOrigin(sessionID string, o *WorkerOrigin) {
	hr.workerOriginsMu.Lock()
	hr.workerOrigins[sessionID] = o
	hr.workerOriginsMu.Unlock()
}

// RegisterOriginIfAbsent registers origin only if not already registered (e.g. by cron layer).
func (hr *Receiver) RegisterOriginIfAbsent(sessionID string, o *WorkerOrigin) {
	hr.workerOriginsMu.Lock()
	if _, exists := hr.workerOrigins[sessionID]; !exists {
		hr.workerOrigins[sessionID] = o
	}
	hr.workerOriginsMu.Unlock()
}

// trackHookWorker creates or updates a hook worker entry.
func (hr *Receiver) trackHookWorker(event *HookEvent, sessionID string, state, toolName, eventType string) {
	if sessionID == "" {
		return
	}

	cwd := event.ResolvedCwd()

	hr.hookWorkersMu.Lock()
	defer hr.hookWorkersMu.Unlock()

	wk, ok := hr.hookWorkers[sessionID]
	if !ok {
		wk = &WorkerInfo{
			SessionID: sessionID,
			FirstSeen: time.Now(),
		}
		// Bring in origin from registry.
		hr.workerOriginsMu.RLock()
		wk.Origin = hr.workerOrigins[sessionID]
		hr.workerOriginsMu.RUnlock()
		hr.hookWorkers[sessionID] = wk
	}
	wk.State = state
	wk.LastSeen = time.Now()
	if toolName != "" {
		wk.LastTool = toolName
		wk.ToolCount++
	}
	if cwd != "" {
		wk.Cwd = cwd
	}

	// Record safe event (no sensitive data).
	if eventType != "" {
		entry := WorkerEvent{
			Timestamp: time.Now().Format(time.RFC3339),
			EventType: eventType,
			ToolName:  toolName,
		}
		wk.Events = append(wk.Events, entry)
		if len(wk.Events) > WorkerEventsMax {
			wk.Events = wk.Events[len(wk.Events)-WorkerEventsMax:]
		}
	}
}

// ListHookWorkers returns all hook-tracked workers.
func (hr *Receiver) ListHookWorkers() []*WorkerInfo {
	hr.hookWorkersMu.RLock()
	defer hr.hookWorkersMu.RUnlock()

	out := make([]*WorkerInfo, 0, len(hr.hookWorkers))
	for _, wk := range hr.hookWorkers {
		out = append(out, wk)
	}
	return out
}

// FindHookWorkerByPrefix returns a worker matching the session ID prefix, plus a snapshot of its events.
func (hr *Receiver) FindHookWorkerByPrefix(prefix string) (*WorkerInfo, []WorkerEvent) {
	hr.hookWorkersMu.RLock()
	defer hr.hookWorkersMu.RUnlock()

	for sid, wk := range hr.hookWorkers {
		if strings.HasPrefix(sid, prefix) {
			events := make([]WorkerEvent, len(wk.Events))
			copy(events, wk.Events)
			return wk, events
		}
	}
	return nil, nil
}

// HasActiveWorkers returns true if any hook worker is in "working" state.
func (hr *Receiver) HasActiveWorkers() bool {
	hr.hookWorkersMu.RLock()
	defer hr.hookWorkersMu.RUnlock()
	for _, wk := range hr.hookWorkers {
		if wk.State == "working" {
			return true
		}
	}
	return false
}

// CleanupStaleHookWorkers removes hook workers not seen in 10 minutes.
func (hr *Receiver) CleanupStaleHookWorkers() {
	hr.hookWorkersMu.Lock()
	defer hr.hookWorkersMu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for sid, wk := range hr.hookWorkers {
		if wk.LastSeen.Before(cutoff) {
			delete(hr.hookWorkers, sid)
		}
	}
	// Also clean up stale origins (same 10-minute window).
	hr.workerOriginsMu.Lock()
	for sid := range hr.workerOrigins {
		// Keep origin if worker still exists.
		if _, exists := hr.hookWorkers[sid]; !exists {
			delete(hr.workerOrigins, sid)
		}
	}
	hr.workerOriginsMu.Unlock()
}

// --- Auth bypass ---

// IsHooksPath returns true if the request path is a hooks endpoint.
// These are called from Claude Code hook scripts running locally,
// so they should bypass API token auth (they use curl from a shell script).
func IsHooksPath(path string) bool {
	return strings.HasPrefix(path, "/api/hooks/")
}

// --- Debug ---

func (hr *Receiver) String() string {
	hr.mu.RLock()
	count := hr.eventCount
	last := hr.lastEventTime
	hr.mu.RUnlock()
	return fmt.Sprintf("hookReceiver{events=%d, last=%s}", count, last.Format(time.RFC3339))
}
