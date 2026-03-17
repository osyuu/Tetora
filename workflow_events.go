package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

// =============================================================================
// Section: External Step Callbacks (from workflow_external.go)
// =============================================================================

// --- Section A: Types ---

// CallbackManager manages in-memory channels for pending external step callbacks.
type CallbackManager struct {
	mu       sync.RWMutex
	channels map[string]*callbackEntry
	dbPath   string
}

type callbackEntry struct {
	ch   chan CallbackResult
	mode string // "single" or "streaming"
	seq  int    // next sequence number for streaming persistence
}

// CallbackResult holds one callback delivery.
type CallbackResult struct {
	Status      int    `json:"status"`
	Body        string `json:"body"`
	ContentType string `json:"contentType"`
	RecvAt      string `json:"recvAt"`
}

// CallbackRecord is the DB representation (for recovery).
type CallbackRecord struct {
	Key        string
	RunID      string
	StepID     string
	Mode       string
	AuthMode   string
	URL        string
	Body       string
	Status     string
	TimeoutAt  string
	PostSent   bool
	Seq        int
	ResultBody string // populated when status=delivered (the callback response body)
}

// Package-level singleton (matches existing patterns like globalUserProfileService).
var callbackMgr *CallbackManager

// runCancellers maps runID -> context.CancelFunc for the cancel API.
var runCancellers sync.Map

// --- Section B: CallbackManager methods ---

func NewCallbackManager(dbPath string) *CallbackManager {
	return &CallbackManager{
		channels: make(map[string]*callbackEntry),
		dbPath:   dbPath,
	}
}

// Register creates a channel for the given callback key.
// Returns nil if key already exists or capacity exceeded.
func (cm *CallbackManager) Register(key string, ctx context.Context, mode string) chan CallbackResult {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Collision check.
	if _, exists := cm.channels[key]; exists {
		return nil
	}

	// Capacity guard.
	if len(cm.channels) >= 1000 {
		log.Warn("callback manager at capacity", "count", len(cm.channels))
		return nil
	}

	bufSize := 1
	if mode == "streaming" {
		bufSize = 256
	}

	ch := make(chan CallbackResult, bufSize)
	cm.channels[key] = &callbackEntry{ch: ch, mode: mode}

	// Context cleanup goroutine: remove channel when context is cancelled.
	go func() {
		<-ctx.Done()
		cm.Unregister(key)
	}()

	return ch
}

// DeliverResult indicates the outcome of a Deliver call.
type DeliverResult int

const (
	DeliverOK       DeliverResult = iota // Successfully sent to channel
	DeliverNoEntry                       // No channel registered for key
	DeliverDup                           // Single mode: already has data (idempotent reject)
	DeliverFull                          // Streaming: channel buffer full
)

// DeliverWithSeq holds the result of a Deliver call along with the allocated sequence number.
type DeliverWithSeq struct {
	Result DeliverResult
	Seq    int    // sequence number for streaming persistence (-1 if not applicable)
	Mode   string // callback mode captured under lock (avoids TOCTOU with GetMode)
}

// Deliver sends a callback result to the registered channel.
// Uses named return + recover to guard against send-on-closed-channel panic
// if concurrent Unregister closes the channel between RUnlock and send.
func (cm *CallbackManager) Deliver(key string, result CallbackResult) (dr DeliverResult) {
	out := cm.DeliverAndSeq(key, result)
	return out.Result
}

// DeliverAndSeq atomically delivers a result AND allocates a sequence number for streaming.
// This prevents the race where Unregister happens between Deliver and NextSeq.
func (cm *CallbackManager) DeliverAndSeq(key string, result CallbackResult) (out DeliverWithSeq) {
	out.Seq = -1

	cm.mu.Lock()
	entry, exists := cm.channels[key]
	if !exists {
		cm.mu.Unlock()
		out.Result = DeliverNoEntry
		return
	}
	// Capture mode and allocate seq under lock (avoids TOCTOU with GetMode).
	out.Mode = entry.mode
	isStreaming := entry.mode == "streaming"
	if isStreaming {
		out.Seq = entry.seq
		entry.seq++
	}
	cm.mu.Unlock()

	// Guard: if Unregister closes the channel concurrently, recover gracefully.
	defer func() {
		if r := recover(); r != nil {
			out.Result = DeliverNoEntry
		}
	}()

	// For single mode, check idempotency (don't send if channel already has data).
	if !isStreaming && len(entry.ch) > 0 {
		out.Result = DeliverDup
		return
	}

	select {
	case entry.ch <- result:
		out.Result = DeliverOK
	default:
		// Channel full (streaming overflow).
		out.Result = DeliverFull
	}
	return
}

// Unregister removes and closes the channel for the given key.
// Safe to call multiple times.
func (cm *CallbackManager) Unregister(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	entry, exists := cm.channels[key]
	if !exists {
		return
	}
	close(entry.ch)
	delete(cm.channels, key)
}

// HasChannel checks if a channel is registered for the key.
func (cm *CallbackManager) HasChannel(key string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	_, exists := cm.channels[key]
	return exists
}

// GetMode returns the callback mode for the key.
func (cm *CallbackManager) GetMode(key string) string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	entry, exists := cm.channels[key]
	if !exists {
		return ""
	}
	return entry.mode
}

// SetSeq sets the sequence counter for a streaming callback key.
// Used after ReplayAccumulated to avoid seq collisions with existing DB records.
func (cm *CallbackManager) SetSeq(key string, seq int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if entry, ok := cm.channels[key]; ok {
		entry.seq = seq
	}
}

// ReplayAccumulated sends previously accumulated streaming callbacks into the channel.
// Used after daemon restart to replay partial results.
func (cm *CallbackManager) ReplayAccumulated(key string, results []CallbackResult) {
	cm.mu.RLock()
	entry, exists := cm.channels[key]
	cm.mu.RUnlock()

	if !exists || entry.mode != "streaming" {
		return
	}
	for _, r := range results {
		select {
		case entry.ch <- r:
		default:
			log.Warn("replay: buffer full, skipping", "key", key)
		}
	}
}

// --- Section C: extractJSONPath + applyResponseMapping ---

// extractJSONPath extracts a value from a JSON string using dot-notation path.
// Supports nested objects, array indices (e.g. "items.0.name"), and type conversion.
func extractJSONPath(jsonStr, path string) string {
	if jsonStr == "" || path == "" {
		return ""
	}

	var root any
	if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
		return ""
	}

	parts := strings.Split(path, ".")
	current := root

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return ""
			}
			current = val
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(v) {
				return ""
			}
			current = v[idx]
		default:
			return ""
		}
	}

	// Convert to string.
	switch v := current.(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		// For objects/arrays, marshal back to JSON.
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// extractXMLText provides basic extraction from XML callback bodies.
// For XML callbacks without ResponseMapping, returns the raw body.
// For XML with a simple tag path like "response.status", extracts inner text
// using the last segment as the tag name.
func extractXMLText(xmlStr, tagName string) string {
	if tagName == "" {
		return xmlStr
	}
	// Simple tag extraction: find <tagName>...</tagName>
	openTag := "<" + tagName + ">"
	closeTag := "</" + tagName + ">"
	start := strings.Index(xmlStr, openTag)
	if start < 0 {
		return ""
	}
	start += len(openTag)
	end := strings.Index(xmlStr[start:], closeTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(xmlStr[start : start+end])
}

// applyResponseMapping extracts data from callback body using ResponseMapping.
// Returns the extracted data path content, or the full body if no mapping.
// Tries JSON extraction first; falls back to XML tag extraction.
func applyResponseMapping(body string, mapping *ResponseMapping) string {
	if body == "" {
		return ""
	}
	if mapping == nil || mapping.DataPath == "" {
		return body
	}
	// Try JSON extraction first.
	extracted := extractJSONPath(body, mapping.DataPath)
	if extracted != "" {
		return extracted
	}
	// Fallback: try XML tag extraction (last segment of dot path as tag name).
	parts := strings.Split(mapping.DataPath, ".")
	tagName := parts[len(parts)-1]
	xmlExtracted := extractXMLText(body, tagName)
	if xmlExtracted != "" {
		return xmlExtracted
	}
	return body // fallback to full body
}

// --- Section D: Template helpers ---

// resolveTemplateWithFields resolves {{...}} templates and also handles
// {{steps.id.output.field}} by extracting JSON fields from step outputs.
func (e *workflowExecutor) resolveTemplateWithFields(tmpl string) string {
	// Re-process the original template for step output field access.
	result := templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])
		parts := strings.SplitN(expr, ".", 4)

		// Handle {{steps.id.output.fieldPath}}
		if len(parts) >= 4 && parts[0] == "steps" && parts[2] == "output" {
			stepID := parts[1]
			fieldPath := strings.Join(parts[3:], ".")
			stepResult, ok := e.wCtx.Steps[stepID]
			if !ok {
				return ""
			}
			return resolveStepOutputField(stepResult.Output, fieldPath)
		}

		// Fallback to standard resolution.
		return resolveExpr(expr, e.wCtx)
	})

	return result
}

// resolveTemplateMapWithFields resolves all values in a map using resolveTemplateWithFields.
func (e *workflowExecutor) resolveTemplateMapWithFields(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = e.resolveTemplateWithFields(v)
	}
	return result
}

// resolveTemplateXMLEscaped resolves templates and XML-escapes the result.
// Note: if the resolved value already contains XML entities (e.g. &amp;),
// they will be double-escaped. Use resolveTemplateWithFields for pre-escaped content.
func (e *workflowExecutor) resolveTemplateXMLEscaped(tmpl string) string {
	result := e.resolveTemplateWithFields(tmpl)
	// XML entity escaping.
	result = strings.ReplaceAll(result, "&", "&amp;")
	result = strings.ReplaceAll(result, "<", "&lt;")
	result = strings.ReplaceAll(result, ">", "&gt;")
	result = strings.ReplaceAll(result, "\"", "&quot;")
	result = strings.ReplaceAll(result, "'", "&apos;")
	return result
}

// resolveStepOutputField extracts a field from step output using JSON path.
func resolveStepOutputField(output, fieldPath string) string {
	return extractJSONPath(output, fieldPath)
}

// --- Section E: HTTP helpers ---

// httpPostWithRetry sends an HTTP POST with exponential backoff retry.
// Respects context cancellation for both requests and retry delays.
func httpPostWithRetry(ctx context.Context, url, contentType string, headers map[string]string, body string, maxRetry int) (*http.Response, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	var lastErr error
	for attempt := 0; attempt <= maxRetry; attempt++ {
		if attempt > 0 && attempt-1 < len(delays) {
			// Context-aware sleep between retries.
			select {
			case <-time.After(delays[attempt-1]):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", contentType)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		// Success on 2xx, retry on 5xx.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 && attempt < maxRetry {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
			continue
		}

		// Non-retryable error (#13: return nil instead of consumed resp).
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return nil, fmt.Errorf("all %d retries failed: %w", maxRetry+1, lastErr)
}

// --- Section F: Execution ---

// runExternalStep executes an external step: POST to URL, wait for callback.
func (e *workflowExecutor) runExternalStep(ctx context.Context, step *WorkflowStep, result *StepRunResult) {
	if callbackMgr == nil {
		result.Status = "error"
		result.Error = "callback manager not initialized"
		return
	}

	// Resolve templates in all fields.
	url := e.resolveTemplateWithFields(step.ExternalURL)
	headers := e.resolveTemplateMapWithFields(step.ExternalHeaders)

	// Resolve auth mode early — needed before callbackKey for header injection.
	authMode := step.CallbackAuth
	if authMode == "" {
		authMode = "bearer"
	}

	callbackKey := e.resolveTemplateWithFields(step.CallbackKey)
	if callbackKey == "" {
		// Check for recovery-injected key (from recoverPendingWorkflows).
		if recoveredKey, ok := e.wCtx.Input["__cb_key_"+step.ID]; ok && recoveredKey != "" {
			callbackKey = recoveredKey
		} else {
			callbackKey = fmt.Sprintf("%s-%s-%s", e.run.ID, step.ID, newUUID()[:8])
		}
	}

	// For signature auth, include callback secret in outgoing headers so the
	// external service knows the HMAC secret for signing its callback.
	if authMode == "signature" {
		if url != "" && !strings.HasPrefix(url, "https://") {
			log.Warn("HMAC callback secret sent over non-HTTPS connection", "step", step.ID, "url", url)
		}
		cbSecret := callbackSignatureSecret(e.cfg.APIToken, callbackKey)
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["X-Callback-Secret"] = cbSecret
	}

	// Build request body.
	contentType := step.ExternalContentType
	if contentType == "" {
		contentType = "application/json"
	}
	var bodyStr string
	if step.ExternalRawBody != "" {
		bodyStr = e.resolveTemplateWithFields(step.ExternalRawBody)
	} else if step.ExternalBody != nil {
		resolvedBody := e.resolveTemplateMapWithFields(step.ExternalBody)
		if contentType == "application/x-www-form-urlencoded" {
			// Form-encode the body.
			vals := neturl.Values{}
			for k, v := range resolvedBody {
				vals.Set(k, v)
			}
			bodyStr = vals.Encode()
		} else {
			bodyBytes, _ := json.Marshal(resolvedBody)
			bodyStr = string(bodyBytes)
		}
	}

	// Callback mode and timeout.
	mode := step.CallbackMode
	if mode == "" {
		mode = "single"
	}
	timeout := 1 * time.Hour // default
	if step.CallbackTimeout != "" {
		if d, err := parseDurationWithDays(step.CallbackTimeout); err == nil {
			timeout = d
		}
	}

	// Check DB state for resume/retry.
	isResume := false
	existingRecord := queryPendingCallbackByKey(callbackMgr.dbPath, callbackKey)
	if existingRecord != nil {
		switch existingRecord.Status {
		case "delivered":
			// Already completed — skip re-execution. Use result_body (the callback response), not body (the request).
			result.Status = "success"
			output := existingRecord.ResultBody
			if output == "" {
				output = existingRecord.Body // fallback for legacy records
			}
			result.Output = output
			log.Info("external step already delivered, skipping", "step", step.ID, "key", callbackKey)
			return
		case "completed", "timeout":
			// Previous attempt finished (timeout/error) — reset for retry.
			resetCallbackRecord(callbackMgr.dbPath, callbackKey)
			log.Info("external step retrying (reset old record)", "step", step.ID, "key", callbackKey, "oldStatus", existingRecord.Status)
		default:
			// "waiting" — check if POST was already sent (resume).
			if existingRecord.PostSent {
				isResume = true
				log.Info("external step resuming (POST already sent)", "step", step.ID, "key", callbackKey)
			}
		}
	}

	// If this is a recovered key, update the DB record to reference the new run ID.
	if _, ok := e.wCtx.Input["__cb_key_"+step.ID]; ok {
		updateCallbackRunID(callbackMgr.dbPath, callbackKey, e.run.ID)
	}

	// Register channel BEFORE POST to prevent race condition.
	ch := callbackMgr.Register(callbackKey, ctx, mode)
	if ch == nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("failed to register callback channel (key collision or at capacity): %s", callbackKey)
		return
	}
	defer callbackMgr.Unregister(callbackKey)

	// Calculate timeout time.
	timeoutAt := time.Now().Add(timeout)

	// Write DB record. Store timeout_at in UTC to match SQLite datetime('now') format.
	if !isResume {
		recordPendingCallback(callbackMgr.dbPath, callbackKey, e.run.ID, step.ID,
			mode, authMode, url, bodyStr, timeoutAt.UTC().Format("2006-01-02 15:04:05"))
	}

	// Replay accumulated streaming callbacks on resume.
	if isResume && mode == "streaming" {
		accumulated := queryStreamingCallbacks(callbackMgr.dbPath, callbackKey)
		if len(accumulated) > 0 {
			callbackMgr.ReplayAccumulated(callbackKey, accumulated)
			// Advance seq counter past existing DB records to prevent collisions.
			callbackMgr.SetSeq(callbackKey, len(accumulated))
			log.Info("replayed accumulated streaming callbacks", "step", step.ID, "key", callbackKey, "count", len(accumulated))
		}
	}

	// HTTP POST (skip if resuming — POST was already sent).
	// Order: mark post_sent FIRST, then POST. If crash after POST but before DB,
	// resume will skip re-sending (prevents duplicate charges/operations).
	if !isResume && url != "" {
		// Mark intent to send BEFORE actually sending (先 DB 再 POST).
		markPostSent(callbackMgr.dbPath, callbackKey)

		retryMax := step.RetryMax
		if retryMax <= 0 {
			retryMax = 2 // default 2 retries for the outgoing POST
		}
		resp, err := httpPostWithRetry(ctx, url, contentType, headers, bodyStr, retryMax)
		if err != nil {
			result.Status = "error"
			result.Error = fmt.Sprintf("external POST failed: %v", err)
			// POST failed — reset so next retry will re-attempt.
			resetCallbackRecord(callbackMgr.dbPath, callbackKey)
			return
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	} else if !isResume {
		// No URL — callback-only mode (e.g. manual approval).
		markPostSent(callbackMgr.dbPath, callbackKey)
	}

	// Publish waiting event.
	e.publishEvent("step_waiting", map[string]any{
		"runId":       e.run.ID,
		"stepId":      step.ID,
		"callbackKey": callbackKey,
		"timeout":     timeout.String(),
	})

	log.Info("external step waiting for callback", "step", step.ID, "key", callbackKey, "timeout", timeout.String())

	// Wait for callback(s).
	if mode == "streaming" {
		lastResult, accumulated := waitStreamingCallback(ctx, ch, callbackKey, step, timeout)

		if lastResult == nil {
			// No results received at all.
			handleCallbackTimeout(step, result, timeout, ctx)
			return
		}

		// Build output based on accumulate setting.
		var output string
		if step.CallbackAccumulate && len(accumulated) > 0 {
			// Merge all callback bodies into a JSON array (#9: ensure valid JSON).
			var parts []string
			for _, a := range accumulated {
				mapped := applyResponseMapping(a.Body, step.CallbackResponseMap)
				if !json.Valid([]byte(mapped)) {
					// Wrap non-JSON as a JSON string to ensure valid array.
					b, _ := json.Marshal(mapped)
					mapped = string(b)
				}
				parts = append(parts, mapped)
			}
			output = "[" + strings.Join(parts, ",") + "]"
		} else {
			output = applyResponseMapping(lastResult.Body, step.CallbackResponseMap)
		}

		// Streaming results already persisted to DB by the HTTP handler (fix #6).

		// Check if done or timed out.
		isDone := false
		if step.CallbackResponseMap != nil && step.CallbackResponseMap.DonePath != "" {
			doneVal := extractJSONPath(lastResult.Body, step.CallbackResponseMap.DonePath)
			isDone = doneVal == step.CallbackResponseMap.DoneValue
		}

		if isDone {
			result.Status = "success"
			result.Output = output
		} else if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Error = "workflow cancelled while waiting for callback"
			result.Output = output
		} else {
			// Timeout with partial results.
			onTimeout := step.OnTimeout
			if onTimeout == "" {
				onTimeout = "stop"
			}
			if onTimeout == "skip" {
				result.Status = "skipped"
				result.Error = "streaming timeout (partial)"
				result.Output = output
			} else {
				result.Status = "timeout"
				result.Error = "streaming timeout (partial)"
				result.Output = output
			}
		}
		clearPendingCallback(callbackMgr.dbPath, callbackKey)
		log.Info("external step completed (streaming)", "step", step.ID, "key", callbackKey, "callbacks", len(accumulated))
	} else {
		// Single mode.
		cbResult := waitSingleCallback(ctx, ch, callbackKey, step, timeout)
		if cbResult == nil {
			handleCallbackTimeout(step, result, timeout, ctx)
			return
		}

		markCallbackDelivered(callbackMgr.dbPath, callbackKey, 0, *cbResult)

		output := cbResult.Body
		if step.CallbackResponseMap != nil {
			output = applyResponseMapping(output, step.CallbackResponseMap)
		}

		result.Status = "success"
		result.Output = output
		clearPendingCallback(callbackMgr.dbPath, callbackKey)
		log.Info("external step completed", "step", step.ID, "key", callbackKey)
	}
}

// handleCallbackTimeout sets the result for a callback timeout/cancellation.
func handleCallbackTimeout(step *WorkflowStep, result *StepRunResult, timeout time.Duration, ctx context.Context) {
	onTimeout := step.OnTimeout
	if onTimeout == "" {
		onTimeout = "stop"
	}
	if ctx.Err() != nil {
		result.Status = "cancelled"
		result.Error = "workflow cancelled while waiting for callback"
	} else if onTimeout == "skip" {
		result.Status = "skipped"
		result.Output = fmt.Sprintf("callback timeout after %s (skipped)", timeout.String())
	} else {
		result.Status = "timeout"
		result.Error = fmt.Sprintf("callback timeout after %s", timeout.String())
	}
}

// waitSingleCallback waits for a single callback result or timeout.
func waitSingleCallback(ctx context.Context, ch chan CallbackResult, key string, step *WorkflowStep, timeout time.Duration) *CallbackResult {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result, ok := <-ch:
		if !ok {
			return nil // channel closed
		}
		return &result
	case <-timer.C:
		return nil // timeout
	case <-ctx.Done():
		return nil // cancelled
	}
}

// waitStreamingCallback waits for multiple callbacks until DonePath==DoneValue or timeout.
func waitStreamingCallback(ctx context.Context, ch chan CallbackResult, key string, step *WorkflowStep, timeout time.Duration) (*CallbackResult, []CallbackResult) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var accumulated []CallbackResult
	var lastResult *CallbackResult

	for {
		select {
		case result, ok := <-ch:
			if !ok {
				return lastResult, accumulated
			}
			accumulated = append(accumulated, result)
			lastResult = &result

			// Check if this is the final callback.
			if step.CallbackResponseMap != nil && step.CallbackResponseMap.DonePath != "" {
				doneVal := extractJSONPath(result.Body, step.CallbackResponseMap.DonePath)
				if doneVal == step.CallbackResponseMap.DoneValue {
					return lastResult, accumulated
				}
			}

		case <-timer.C:
			return lastResult, accumulated // partial results on timeout

		case <-ctx.Done():
			return lastResult, accumulated // cancelled
		}
	}
}

// --- Section G: DB helpers ---

const callbackTableSQL = `CREATE TABLE IF NOT EXISTS workflow_callbacks (
	key TEXT PRIMARY KEY,
	run_id TEXT NOT NULL,
	step_id TEXT NOT NULL,
	mode TEXT DEFAULT 'single',
	auth_mode TEXT DEFAULT 'bearer',
	url TEXT,
	body TEXT,
	status TEXT DEFAULT 'waiting',
	timeout_at TEXT,
	post_sent INTEGER DEFAULT 0,
	seq INTEGER DEFAULT 0,
	result_body TEXT,
	result_status INTEGER DEFAULT 0,
	result_content_type TEXT,
	delivered_at TEXT,
	created_at TEXT DEFAULT (datetime('now'))
)`

const callbackStreamTableSQL = `CREATE TABLE IF NOT EXISTS workflow_callback_stream (
	key TEXT NOT NULL,
	seq INTEGER NOT NULL,
	body TEXT,
	content_type TEXT,
	recv_at TEXT,
	PRIMARY KEY (key, seq)
)`

func initCallbackTable(dbPath string) {
	if dbPath == "" {
		return
	}
	if _, err := db.Query(dbPath, callbackTableSQL); err != nil {
		log.Warn("init workflow_callbacks table failed", "error", err)
	}
	if _, err := db.Query(dbPath, callbackStreamTableSQL); err != nil {
		log.Warn("init workflow_callback_stream table failed", "error", err)
	}
}

func recordPendingCallback(dbPath, key, runID, stepID, mode, authMode, url, body, timeoutAt string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_callbacks (key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, created_at)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s','waiting','%s',datetime('now'))`,
		db.Escape(key), db.Escape(runID), db.Escape(stepID),
		db.Escape(mode), db.Escape(authMode),
		db.Escape(url), db.Escape(body), db.Escape(timeoutAt),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("record pending callback failed", "error", err, "key", key)
	}
}

// queryPendingCallbackByKey returns a callback record by key (any status, seq=0).
func queryPendingCallbackByKey(dbPath, key string) *CallbackRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, post_sent, seq, result_body
		 FROM workflow_callbacks WHERE key='%s' LIMIT 1`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return parseCallbackRecord(rows[0])
}

// queryPendingCallback returns a callback record only if status='waiting'.
func queryPendingCallback(dbPath, key string) *CallbackRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, post_sent, seq, result_body
		 FROM workflow_callbacks WHERE key='%s' AND status='waiting' LIMIT 1`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil || len(rows) == 0 {
		return nil
	}
	return parseCallbackRecord(rows[0])
}

// queryPendingCallbacksByRun returns all pending callbacks for a workflow run.
func queryPendingCallbacksByRun(dbPath, runID string) []*CallbackRecord {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT key, run_id, step_id, mode, auth_mode, url, body, status, timeout_at, post_sent, seq, result_body
		 FROM workflow_callbacks WHERE run_id='%s' AND status='waiting'`,
		db.Escape(runID),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var records []*CallbackRecord
	for _, row := range rows {
		records = append(records, parseCallbackRecord(row))
	}
	return records
}

// sqlStr safely converts a DB value to string, returning "" for nil/NULL.
func sqlStr(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%v", v)
	if s == "<nil>" {
		return ""
	}
	return s
}

func parseCallbackRecord(row map[string]any) *CallbackRecord {
	rec := &CallbackRecord{
		Key:        sqlStr(row["key"]),
		RunID:      sqlStr(row["run_id"]),
		StepID:     sqlStr(row["step_id"]),
		Mode:       sqlStr(row["mode"]),
		AuthMode:   sqlStr(row["auth_mode"]),
		URL:        sqlStr(row["url"]),
		Body:       sqlStr(row["body"]),
		Status:     sqlStr(row["status"]),
		TimeoutAt:  sqlStr(row["timeout_at"]),
		ResultBody: sqlStr(row["result_body"]),
	}
	if ps, ok := row["post_sent"]; ok {
		rec.PostSent = fmt.Sprintf("%v", ps) == "1"
	}
	if sq, ok := row["seq"]; ok {
		if n, err := strconv.Atoi(fmt.Sprintf("%v", sq)); err == nil {
			rec.Seq = n
		}
	}
	return rec
}

func markPostSent(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET post_sent=1 WHERE key='%s'`,
		db.Escape(key),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("mark post sent failed", "error", err, "key", key)
	}
}

func markCallbackDelivered(dbPath, key string, seq int, result CallbackResult) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET status='delivered', seq=%d, result_body='%s', result_status=%d, result_content_type='%s', delivered_at=datetime('now')
		 WHERE key='%s'`,
		seq, db.Escape(result.Body), result.Status, db.Escape(result.ContentType),
		db.Escape(key),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("mark callback delivered failed", "error", err, "key", key)
	}
}

// updateCallbackRunID updates the run_id for a callback record (used during recovery).
func updateCallbackRunID(dbPath, key, newRunID string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET run_id='%s' WHERE key='%s'`,
		db.Escape(newRunID), db.Escape(key),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("update callback run_id failed", "error", err, "key", key)
	}
}

func resetCallbackRecord(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET status='waiting', post_sent=0, seq=0, result_body=NULL, delivered_at=NULL WHERE key='%s'`,
		db.Escape(key),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("reset callback record failed", "error", err, "key", key)
	}
}

func isCallbackDelivered(dbPath, key string, seq int) bool {
	if dbPath == "" {
		return false
	}
	sql := fmt.Sprintf(
		`SELECT 1 FROM workflow_callbacks WHERE key='%s' AND status='delivered' AND seq>=%d LIMIT 1`,
		db.Escape(key), seq,
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return false
	}
	return len(rows) > 0
}

func clearPendingCallback(dbPath, key string) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`UPDATE workflow_callbacks SET status='completed' WHERE key='%s'`,
		db.Escape(key),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("clear pending callback failed", "error", err, "key", key)
	}
}

// appendStreamingCallback records a streaming callback result to DB.
func appendStreamingCallback(dbPath, key string, seq int, result CallbackResult) {
	if dbPath == "" {
		return
	}
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO workflow_callback_stream (key, seq, body, content_type, recv_at)
		 VALUES ('%s', %d, '%s', '%s', '%s')`,
		db.Escape(key), seq, db.Escape(result.Body),
		db.Escape(result.ContentType), db.Escape(result.RecvAt),
	)
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("append streaming callback failed", "error", err, "key", key, "seq", seq)
	}
}

// queryStreamingCallbacks returns all streaming callback results for a key, ordered by seq.
func queryStreamingCallbacks(dbPath, key string) []CallbackResult {
	if dbPath == "" {
		return nil
	}
	sql := fmt.Sprintf(
		`SELECT body, content_type, recv_at FROM workflow_callback_stream WHERE key='%s' ORDER BY seq`,
		db.Escape(key),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	var results []CallbackResult
	for _, row := range rows {
		results = append(results, CallbackResult{
			Body:        sqlStr(row["body"]),
			ContentType: sqlStr(row["content_type"]),
			RecvAt:      sqlStr(row["recv_at"]),
		})
	}
	return results
}

// cleanupExpiredCallbacks marks timed-out callbacks and cleans old streaming records.
func cleanupExpiredCallbacks(dbPath string) {
	if dbPath == "" {
		return
	}
	// Mark expired waiting callbacks as timeout.
	sql := `UPDATE workflow_callbacks SET status='timeout'
		WHERE status='waiting' AND timeout_at != '' AND timeout_at < datetime('now')`
	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("cleanup expired callbacks failed", "error", err)
	}

	// Clean streaming records older than 7 days for completed callbacks.
	sql2 := `DELETE FROM workflow_callback_stream WHERE key IN (
		SELECT key FROM workflow_callbacks WHERE status IN ('completed','delivered','timeout')
		AND created_at < datetime('now', '-7 days')
	)`
	if _, err := db.Query(dbPath, sql2); err != nil {
		log.Warn("cleanup old streaming records failed", "error", err)
	}
}

// --- Section H: Recovery ---

// hashWorkflow returns a SHA256 hash of the workflow steps JSON (for change detection).
func hashWorkflow(wf *Workflow) string {
	data, _ := json.Marshal(wf.Steps)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// recoverPendingWorkflows scans for workflows with pending external steps and resumes them.
func recoverPendingWorkflows(cfg *Config, state *dispatchState, sem, childSem chan struct{}) {
	if cfg.HistoryDB == "" || callbackMgr == nil {
		return
	}

	// Find all unique run IDs with waiting callbacks.
	sql := `SELECT DISTINCT run_id FROM workflow_callbacks WHERE status='waiting'`
	rows, err := db.Query(cfg.HistoryDB, sql)
	if err != nil || len(rows) == 0 {
		return
	}

	for _, row := range rows {
		runID := fmt.Sprintf("%v", row["run_id"])

		// Load the workflow run.
		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil || run == nil {
			log.Warn("recovery: cannot load workflow run", "runID", runID, "error", err)
			continue
		}

		// Load the workflow definition.
		wf, err := loadWorkflowByName(cfg, run.WorkflowName)
		if err != nil {
			log.Warn("recovery: cannot load workflow", "workflow", run.WorkflowName, "error", err)
			continue
		}

		log.Info("recovering pending workflow", "workflow", run.WorkflowName, "runID", runID[:8])

		// Collect pending callback keys for this run so the new execution can reuse them.
		pendingCallbacks := queryPendingCallbacksByRun(cfg.HistoryDB, runID)
		recoveryVars := make(map[string]string)
		for k, v := range run.Variables {
			recoveryVars[k] = v
		}
		for _, cb := range pendingCallbacks {
			recoveryVars["__cb_key_"+cb.StepID] = cb.Key
		}

		// Mark old run as superseded so it's not left orphaned.
		markRunSuperseded := func(oldRunID string) {
			sql := fmt.Sprintf(
				`UPDATE workflow_runs SET status='recovered', finished_at=datetime('now') WHERE id='%s' AND status IN ('running','waiting')`,
				db.Escape(oldRunID),
			)
			db.Query(cfg.HistoryDB, sql)
		}
		markRunSuperseded(runID)

		// Re-execute the workflow in background (it will detect pending callbacks and resume).
		go executeWorkflow(context.Background(), cfg, wf, recoveryVars, state, sem, childSem)
	}
}

// checkpointRun saves current workflow run state to DB.
func checkpointRun(e *workflowExecutor) {
	recordWorkflowRun(e.cfg.HistoryDB, e.run)
}

// hasWaitingExternalStep checks if any step result indicates a waiting external step.
func hasWaitingExternalStep(results map[string]*StepRunResult) bool {
	for _, r := range results {
		if r.Status == "waiting" {
			return true
		}
	}
	return false
}

// --- Validation helpers ---

var callbackKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func isValidCallbackKey(key string) bool {
	if len(key) == 0 || len(key) > 256 {
		return false
	}
	return callbackKeyRegex.MatchString(key)
}

// parseDurationWithDays extends time.ParseDuration to support "d" suffix for days.
func parseDurationWithDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(numStr)
		if err != nil {
			return 0, fmt.Errorf("invalid days: %s", s)
		}
		if days < 0 || days > 30 {
			return 0, fmt.Errorf("days out of range (0-30): %d", days)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// callbackSignatureSecret derives a per-callback HMAC secret.
// secret = hex(HMAC-SHA256(serverSecret, callbackKey))
func callbackSignatureSecret(serverSecret, callbackKey string) string {
	mac := hmac.New(sha256.New, []byte(serverSecret))
	mac.Write([]byte(callbackKey))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyCallbackSignature checks the X-Callback-Signature header.
func verifyCallbackSignature(body []byte, secret, signatureHex string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signatureHex))
}

// =============================================================================
// Section: Workflow Trigger Engine (from workflow_trigger.go)
// =============================================================================

// --- P18.3: Workflow Trigger Engine ---

// TriggerInfo provides status information about a configured trigger.
type TriggerInfo struct {
	Name         string `json:"name"`
	WorkflowName string `json:"workflowName"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	LastFired    string `json:"lastFired,omitempty"`
	NextCron     string `json:"nextCron,omitempty"`
	Cooldown     string `json:"cooldown,omitempty"`
	CooldownLeft string `json:"cooldownLeft,omitempty"`
}

// --- Trigger Engine ---

// WorkflowTriggerEngine manages workflow triggers: cron-based, event-based, and webhook-based.
type WorkflowTriggerEngine struct {
	cfg       *Config
	state     *dispatchState
	sem       chan struct{}
	childSem  chan struct{}
	broker    *sseBroker
	triggers  []WorkflowTriggerConfig
	cooldowns map[string]time.Time // trigger name -> cooldown expiry
	lastFired map[string]time.Time // trigger name -> last fire time
	mu        sync.RWMutex
	parentCtx context.Context    // parent context from Start(), preserved for ReloadTriggers
	ctx       context.Context    // engine-scoped context, cancelled on Stop
	cancel    context.CancelFunc
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func newWorkflowTriggerEngine(cfg *Config, state *dispatchState, sem, childSem chan struct{}, broker *sseBroker) *WorkflowTriggerEngine {
	e := &WorkflowTriggerEngine{
		cfg:       cfg,
		state:     state,
		sem:       sem,
		childSem:  childSem,
		broker:    broker,
		triggers:  cfg.WorkflowTriggers,
		cooldowns: make(map[string]time.Time),
		lastFired: make(map[string]time.Time),
		ctx:       context.Background(), // safe default; overridden by Start()
		stopCh:    make(chan struct{}),
	}
	return e
}

// Start launches the cron loop and event listener goroutines.
func (e *WorkflowTriggerEngine) Start(ctx context.Context) {
	e.parentCtx = ctx
	e.ctx, e.cancel = context.WithCancel(ctx)

	if len(e.triggers) == 0 {
		log.Info("workflow trigger engine: no triggers configured")
		return
	}

	hasCron := false
	hasEvent := false
	for _, t := range e.triggers {
		if t.Trigger.Type == "cron" {
			hasCron = true
		}
		if t.Trigger.Type == "event" {
			hasEvent = true
		}
	}

	if hasCron {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.cronLoop(ctx)
		}()
	}

	if hasEvent && e.broker != nil {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.eventLoop(ctx)
		}()
	}

	// Init trigger runs table.
	initTriggerRunsTable(e.cfg.HistoryDB)

	enabled := 0
	for _, t := range e.triggers {
		if t.IsEnabled() {
			enabled++
		}
	}
	log.Info("workflow trigger engine started", "total", len(e.triggers), "enabled", enabled, "cron", hasCron, "event", hasEvent)
}

// Stop gracefully shuts down the trigger engine.
func (e *WorkflowTriggerEngine) Stop() {
	close(e.stopCh)
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	log.Info("workflow trigger engine stopped")
}

// ReloadTriggers hot-swaps triggers: stops the current engine loops and restarts with new triggers.
func (e *WorkflowTriggerEngine) ReloadTriggers(triggers []WorkflowTriggerConfig) {
	// Stop current loops.
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()

	// Swap triggers.
	e.mu.Lock()
	e.triggers = triggers
	e.stopCh = make(chan struct{})
	e.mu.Unlock()

	// Restart with stored parent context (preserves shutdown signal).
	parentCtx := e.parentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if len(triggers) > 0 {
		e.Start(parentCtx)
	}
	log.Info("workflow triggers reloaded", "count", len(triggers))
}

// cronLoop checks cron triggers every 30 seconds.
func (e *WorkflowTriggerEngine) cronLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	cleanupCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.checkCronTriggers(ctx)

			// Clean up expired cooldown entries every ~5 minutes (10 ticks × 30s).
			cleanupCounter++
			if cleanupCounter >= 10 {
				cleanupCounter = 0
				e.cleanupExpiredCooldowns()
			}
		}
	}
}

// cleanupExpiredCooldowns removes expired entries from the cooldowns map to prevent memory leaks.
func (e *WorkflowTriggerEngine) cleanupExpiredCooldowns() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for k, v := range e.cooldowns {
		if now.After(v) {
			delete(e.cooldowns, k)
		}
	}
}

func (e *WorkflowTriggerEngine) checkCronTriggers(ctx context.Context) {
	now := time.Now()

	e.mu.RLock()
	triggers := e.triggers
	e.mu.RUnlock()

	for _, t := range triggers {
		if !t.IsEnabled() || t.Trigger.Type != "cron" || t.Trigger.Cron == "" {
			continue
		}

		expr, err := parseCronExpr(t.Trigger.Cron)
		if err != nil {
			log.Warn("workflow trigger bad cron", "trigger", t.Name, "cron", t.Trigger.Cron, "error", err)
			continue
		}

		// Resolve timezone.
		loc := time.Local
		if t.Trigger.TZ != "" {
			if l, err := time.LoadLocation(t.Trigger.TZ); err == nil {
				loc = l
			}
		}

		nowLocal := now.In(loc)
		if !expr.Matches(nowLocal) {
			continue
		}

		// Avoid double-firing in the same minute.
		e.mu.RLock()
		lastFired := e.lastFired[t.Name]
		e.mu.RUnlock()

		if !lastFired.IsZero() &&
			lastFired.In(loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute)) {
			continue
		}

		// Check cooldown.
		if !e.checkCooldown(t.Name) {
			log.Debug("workflow trigger cooldown active", "trigger", t.Name)
			continue
		}

		log.Info("workflow trigger cron firing", "trigger", t.Name, "workflow", t.WorkflowName)
		go e.executeTrigger(ctx, t, nil)
	}
}

// eventLoop subscribes to all SSE events and matches event triggers.
func (e *WorkflowTriggerEngine) eventLoop(ctx context.Context) {
	// Subscribe to a global event channel.
	ch, unsub := e.broker.Subscribe("_triggers")
	defer unsub()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			e.matchEventTriggers(ctx, event)
		}
	}
}

func (e *WorkflowTriggerEngine) matchEventTriggers(ctx context.Context, event SSEEvent) {
	e.mu.RLock()
	triggers := e.triggers
	e.mu.RUnlock()

	for _, t := range triggers {
		if !t.IsEnabled() || t.Trigger.Type != "event" || t.Trigger.Event == "" {
			continue
		}

		// Match event type (supports prefix matching with *)
		if !matchEventType(event.Type, t.Trigger.Event) {
			continue
		}

		if !e.checkCooldown(t.Name) {
			continue
		}

		// Build extra vars from event data.
		extraVars := map[string]string{
			"event_type": event.Type,
			"task_id":    event.TaskID,
			"session_id": event.SessionID,
		}
		if data, ok := event.Data.(map[string]any); ok {
			for k, v := range data {
				extraVars["event_"+k] = fmt.Sprintf("%v", v)
			}
		}

		log.Info("workflow trigger event firing", "trigger", t.Name, "eventType", event.Type)
		go e.executeTrigger(ctx, t, extraVars)
	}
}

// matchEventType checks if an event type matches a pattern.
// Supports exact match and wildcard prefix (e.g. "workflow_*").
func matchEventType(eventType, pattern string) bool {
	if pattern == eventType {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(eventType, prefix)
	}
	return false
}

// HandleWebhookTrigger fires a webhook trigger by name with the given payload.
func (e *WorkflowTriggerEngine) HandleWebhookTrigger(triggerName string, payload map[string]string) error {
	e.mu.Lock()
	var found *WorkflowTriggerConfig
	for i := range e.triggers {
		t := &e.triggers[i]
		if t.Name == triggerName && t.Trigger.Type == "webhook" {
			found = t
			break
		}
	}

	if found == nil {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q not found", triggerName)
	}
	if !found.IsEnabled() {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q is disabled", triggerName)
	}

	// Check cooldown under write lock to prevent TOCTOU race.
	expiry, ok := e.cooldowns[triggerName]
	if ok && !time.Now().After(expiry) {
		e.mu.Unlock()
		return fmt.Errorf("webhook trigger %q is in cooldown", triggerName)
	}

	// Set cooldown immediately before releasing lock.
	if found.Cooldown != "" {
		if d, err := time.ParseDuration(found.Cooldown); err == nil {
			e.cooldowns[triggerName] = time.Now().Add(d)
		} else {
			log.Warn("webhook trigger cooldown parse failed", "trigger", triggerName, "cooldown", found.Cooldown, "error", err)
		}
	}
	e.lastFired[triggerName] = time.Now()
	triggerCopy := *found
	e.mu.Unlock()

	log.Info("workflow trigger webhook firing", "trigger", triggerName, "workflow", triggerCopy.WorkflowName)
	go e.executeTrigger(e.ctx, triggerCopy, payload)
	return nil
}

// executeTrigger loads the workflow, merges variables, and executes it.
func (e *WorkflowTriggerEngine) executeTrigger(ctx context.Context, trigger WorkflowTriggerConfig, extraVars map[string]string) {
	startedAt := time.Now()

	// Update last fired and cooldown.
	e.mu.Lock()
	e.lastFired[trigger.Name] = startedAt
	if trigger.Cooldown != "" {
		if d, err := time.ParseDuration(trigger.Cooldown); err == nil {
			e.cooldowns[trigger.Name] = startedAt.Add(d)
		}
	}
	e.mu.Unlock()

	// Load workflow.
	wf, err := loadWorkflowByName(e.cfg, trigger.WorkflowName)
	if err != nil {
		errMsg := fmt.Sprintf("load workflow: %v", err)
		log.Error("workflow trigger exec failed", "trigger", trigger.Name, "error", errMsg)
		recordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, "", "error",
			startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)
		return
	}

	// Validate workflow.
	if errs := validateWorkflow(wf); len(errs) > 0 {
		errMsg := fmt.Sprintf("validation: %s", strings.Join(errs, "; "))
		log.Error("workflow trigger validation failed", "trigger", trigger.Name, "errors", errs)
		recordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, "", "error",
			startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)
		return
	}

	// Merge variables: workflow defaults < trigger config < extra vars.
	vars := make(map[string]string)
	for k, v := range wf.Variables {
		vars[k] = v
	}
	for k, v := range trigger.Variables {
		vars[k] = v
	}
	for k, v := range extraVars {
		vars[k] = v
	}

	// Add trigger metadata as variables.
	vars["_trigger_name"] = trigger.Name
	vars["_trigger_type"] = trigger.Trigger.Type
	vars["_trigger_time"] = startedAt.Format(time.RFC3339)

	// Execute workflow.
	run := executeWorkflow(ctx, e.cfg, wf, vars, e.state, e.sem, e.childSem)

	// Record trigger run.
	status := "success"
	errMsg := ""
	if run.Status != "success" {
		status = "error"
		errMsg = run.Error
	}
	recordTriggerRun(e.cfg.HistoryDB, trigger.Name, trigger.WorkflowName, run.ID, status,
		startedAt.Format(time.RFC3339), time.Now().Format(time.RFC3339), errMsg)

	// Publish trigger event.
	if e.broker != nil {
		e.broker.Publish("_triggers", SSEEvent{
			Type: "trigger_fired",
			Data: map[string]any{
				"trigger":     trigger.Name,
				"workflow":    trigger.WorkflowName,
				"runId":       run.ID,
				"status":      run.Status,
				"triggerType": trigger.Trigger.Type,
				"durationMs":  run.DurationMs,
			},
		})
	}
}

// checkCooldown returns true if the trigger is past its cooldown period.
func (e *WorkflowTriggerEngine) checkCooldown(triggerName string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	expiry, ok := e.cooldowns[triggerName]
	if !ok {
		return true // no cooldown set
	}
	return time.Now().After(expiry)
}

// ListTriggers returns status info for all configured triggers.
func (e *WorkflowTriggerEngine) ListTriggers() []TriggerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var infos []TriggerInfo
	now := time.Now()

	for _, t := range e.triggers {
		info := TriggerInfo{
			Name:         t.Name,
			WorkflowName: t.WorkflowName,
			Type:         t.Trigger.Type,
			Enabled:      t.IsEnabled(),
			Cooldown:     t.Cooldown,
		}

		// Last fired.
		if lf, ok := e.lastFired[t.Name]; ok {
			info.LastFired = lf.Format(time.RFC3339)
		}

		// Cooldown remaining.
		if expiry, ok := e.cooldowns[t.Name]; ok && now.Before(expiry) {
			info.CooldownLeft = expiry.Sub(now).Round(time.Second).String()
		}

		// Next cron run.
		if t.Trigger.Type == "cron" && t.Trigger.Cron != "" {
			expr, err := parseCronExpr(t.Trigger.Cron)
			if err == nil {
				loc := time.Local
				if t.Trigger.TZ != "" {
					if l, err := time.LoadLocation(t.Trigger.TZ); err == nil {
						loc = l
					}
				}
				next := nextRunAfter(expr, loc, now.In(loc))
				if !next.IsZero() {
					info.NextCron = next.Format(time.RFC3339)
				}
			}
		}

		infos = append(infos, info)
	}

	return infos
}

// FireTrigger manually fires a trigger by name.
func (e *WorkflowTriggerEngine) FireTrigger(name string) error {
	e.mu.RLock()
	var found *WorkflowTriggerConfig
	for i := range e.triggers {
		if e.triggers[i].Name == name {
			found = &e.triggers[i]
			break
		}
	}
	e.mu.RUnlock()

	if found == nil {
		return fmt.Errorf("trigger %q not found", name)
	}
	if !found.IsEnabled() {
		return fmt.Errorf("trigger %q is disabled", name)
	}

	log.Info("workflow trigger manual fire", "trigger", name, "workflow", found.WorkflowName)
	go e.executeTrigger(e.ctx, *found, map[string]string{
		"_manual": "true",
	})
	return nil
}

// --- Trigger Run Recording ---

const triggerRunsTableSQL = `CREATE TABLE IF NOT EXISTS workflow_trigger_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trigger_name TEXT NOT NULL,
	workflow_name TEXT NOT NULL,
	workflow_run_id TEXT DEFAULT '',
	status TEXT NOT NULL DEFAULT 'started',
	started_at TEXT NOT NULL,
	finished_at TEXT DEFAULT '',
	error TEXT DEFAULT ''
)`

func initTriggerRunsTable(dbPath string) {
	if dbPath == "" {
		return
	}
	// Migration: add workflow_run_id column if missing (for DBs created before this column existed).
	if err := db.Exec(dbPath, `ALTER TABLE workflow_trigger_runs ADD COLUMN workflow_run_id TEXT DEFAULT '';`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "no such table") {
			log.Warn("workflow_trigger_runs migration failed", "error", err)
		}
	}
	if _, err := db.Query(dbPath, triggerRunsTableSQL); err != nil {
		log.Warn("init workflow_trigger_runs table failed", "error", err)
	}
}

func recordTriggerRun(dbPath, triggerName, workflowName, runID, status, startedAt, finishedAt, errMsg string) {
	if dbPath == "" {
		return
	}

	sql := fmt.Sprintf(
		`INSERT INTO workflow_trigger_runs (trigger_name, workflow_name, workflow_run_id, status, started_at, finished_at, error)
		 VALUES ('%s','%s','%s','%s','%s','%s','%s')`,
		db.Escape(triggerName),
		db.Escape(workflowName),
		db.Escape(runID),
		db.Escape(status),
		db.Escape(startedAt),
		db.Escape(finishedAt),
		db.Escape(errMsg),
	)

	if _, err := db.Query(dbPath, sql); err != nil {
		log.Warn("record trigger run failed", "error", err)
	}
}

func queryTriggerRuns(dbPath, triggerName string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}

	where := ""
	if triggerName != "" {
		where = fmt.Sprintf("WHERE trigger_name='%s'", db.Escape(triggerName))
	}

	sql := fmt.Sprintf(
		`SELECT id, trigger_name, workflow_name, workflow_run_id, status, started_at, finished_at, error
		 FROM workflow_trigger_runs %s ORDER BY id DESC LIMIT %d`,
		where, limit,
	)

	rows, err := db.Query(dbPath, sql)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}

	return rows, nil
}

// validateTriggerConfig checks a trigger config for errors.
func validateTriggerConfig(t WorkflowTriggerConfig, existingNames map[string]bool) []string {
	var errs []string
	if t.Name == "" {
		errs = append(errs, "name is required")
	}
	if existingNames != nil && existingNames[t.Name] {
		errs = append(errs, fmt.Sprintf("name %q already exists", t.Name))
	}
	if t.WorkflowName == "" {
		errs = append(errs, "workflowName is required")
	}
	switch t.Trigger.Type {
	case "cron":
		if t.Trigger.Cron == "" {
			errs = append(errs, "cron expression required for cron trigger")
		} else if _, err := parseCronExpr(t.Trigger.Cron); err != nil {
			errs = append(errs, fmt.Sprintf("invalid cron expression: %v", err))
		}
	case "event":
		if t.Trigger.Event == "" {
			errs = append(errs, "event pattern required for event trigger")
		}
	case "webhook":
		if t.Trigger.Webhook == "" {
			errs = append(errs, "webhook ID required for webhook trigger")
		}
	case "":
		errs = append(errs, "trigger type is required (cron, event, webhook)")
	default:
		errs = append(errs, fmt.Sprintf("unknown trigger type: %s", t.Trigger.Type))
	}
	return errs
}

// --- Variable Expansion for Tool Inputs ---

// expandVars replaces {{key}} with values from the vars map.
// Same pattern as expandSkillVars but used for workflow step tool inputs.
func expandVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

// expandToolInput expands {{var}} in all tool input values.
func expandToolInput(input map[string]string, vars map[string]string) map[string]string {
	if len(input) == 0 {
		return input
	}
	result := make(map[string]string, len(input))
	for k, v := range input {
		result[k] = expandVars(v, vars)
	}
	return result
}

// toolInputToJSON converts a map[string]string to json.RawMessage.
func toolInputToJSON(input map[string]string) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}
	// Convert to map[string]any for JSON marshaling.
	m := make(map[string]any, len(input))
	for k, v := range input {
		m[k] = v
	}
	data, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
