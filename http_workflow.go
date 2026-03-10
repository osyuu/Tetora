package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) registerWorkflowRoutes(mux *http.ServeMux) {
	cfg := s.cfg
	state := s.state
	sem := s.sem
	childSem := s.childSem

	// --- Workflows ---
	mux.HandleFunc("/workflows", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			workflows, err := listWorkflows(cfg)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if workflows == nil {
				workflows = []*Workflow{}
			}
			json.NewEncoder(w).Encode(workflows)

		case http.MethodPost:
			var wf Workflow
			if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %v"}`, err), http.StatusBadRequest)
				return
			}
			errs := validateWorkflow(&wf)
			if len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			if err := saveWorkflow(cfg, &wf); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.create", "http",
				fmt.Sprintf("name=%s steps=%d", wf.Name, len(wf.Steps)), clientIP(r))
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "name": wf.Name})

		default:
			http.Error(w, `{"error":"GET or POST"}`, http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/workflows/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		name := strings.TrimPrefix(r.URL.Path, "/workflows/")
		if name == "" {
			http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
			return
		}

		// Strip sub-paths (e.g. /workflows/name/validate).
		parts := strings.SplitN(name, "/", 2)
		name = parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		switch {
		case action == "validate" && r.Method == http.MethodPost:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			errs := validateWorkflow(wf)
			valid := len(errs) == 0
			resp := map[string]any{"valid": valid, "name": wf.Name}
			if !valid {
				resp["errors"] = errs
			} else {
				resp["executionOrder"] = topologicalSort(wf.Steps)
			}
			json.NewEncoder(w).Encode(resp)

		case action == "" && r.Method == http.MethodGet:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(wf)

		case action == "" && r.Method == http.MethodDelete:
			if err := deleteWorkflow(cfg, name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "workflow.delete", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "name": name})

		case action == "run" && r.Method == http.MethodPost:
			wf, err := loadWorkflowByName(cfg, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
				return
			}
			if errs := validateWorkflow(wf); len(errs) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"errors": errs})
				return
			}
			var body struct {
				Variables map[string]string `json:"variables"`
			}
			json.NewDecoder(r.Body).Decode(&body)

			// Sanitize: strip internal-namespace variables to prevent injection.
			for k := range body.Variables {
				if strings.HasPrefix(k, "__") {
					delete(body.Variables, k)
				}
			}

			auditLog(cfg.HistoryDB, "workflow.run", "http",
				fmt.Sprintf("name=%s", name), clientIP(r))

			// Run asynchronously.
			wfTraceID := traceIDFromContext(r.Context())
			go executeWorkflow(withTraceID(context.Background(), wfTraceID), cfg, wf, body.Variables, state, sem, childSem)

			// Return immediately with run acknowledgment.
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":   "accepted",
				"workflow": name,
			})

		case action == "runs" && r.Method == http.MethodGet:
			runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if runs == nil {
				runs = []WorkflowRun{}
			}
			json.NewEncoder(w).Encode(runs)

		default:
			http.Error(w, `{"error":"GET, DELETE, or POST .../validate|run"}`, http.StatusMethodNotAllowed)
		}
	})

	// --- Workflow Runs ---
	mux.HandleFunc("/workflow-runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("workflow")
		runs, err := queryWorkflowRuns(cfg.HistoryDB, 20, name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if runs == nil {
			runs = []WorkflowRun{}
		}
		json.NewEncoder(w).Encode(runs)
	})

	mux.HandleFunc("/workflow-runs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/workflow-runs/")
		parts := strings.SplitN(path, "/", 2)
		runID := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		if runID == "" {
			http.Error(w, `{"error":"run ID required"}`, http.StatusBadRequest)
			return
		}

		// POST /workflow-runs/{id}/cancel
		if action == "cancel" && r.Method == http.MethodPost {
			if cancel, ok := runCancellers.Load(runID); ok {
				cancel.(context.CancelFunc)()
				runCancellers.Delete(runID)
			}
			// Also update DB.
			if _, err := queryDB(cfg.HistoryDB, fmt.Sprintf(
				`UPDATE workflow_runs SET status='cancelled', finished_at=datetime('now') WHERE id='%s' AND status IN ('running','waiting')`,
				escapeSQLite(runID),
			)); err != nil {
				logWarn("cancel workflow run failed", "runID", runID, "error", err)
			}
			auditLog(cfg.HistoryDB, "workflow.cancel", "http",
				fmt.Sprintf("runID=%s", runID), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{"status": "cancelled", "runId": runID})
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET or POST .../cancel"}`, http.StatusMethodNotAllowed)
			return
		}

		run, err := queryWorkflowRunByID(cfg.HistoryDB, runID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		// Enrich with handoffs and messages.
		handoffs, _ := queryHandoffs(cfg.HistoryDB, run.ID)
		messages, _ := queryAgentMessages(cfg.HistoryDB, run.ID, "", 100)
		if handoffs == nil {
			handoffs = []Handoff{}
		}
		if messages == nil {
			messages = []AgentMessage{}
		}
		result := map[string]any{
			"run":      run,
			"handoffs": handoffs,
			"messages": messages,
		}
		json.NewEncoder(w).Encode(result)
	})

	// --- P18.3: Workflow Triggers ---
	// Build trigger engine reference for HTTP handlers.
	var triggerEngine *WorkflowTriggerEngine
	if len(cfg.WorkflowTriggers) > 0 {
		triggerEngine = newWorkflowTriggerEngine(cfg, state, sem, childSem, state.broker)
	}

	// --- Skill list for editor dropdowns ---
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		type SkillInfo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		dir := skillsDir(cfg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			json.NewEncoder(w).Encode([]SkillInfo{})
			return
		}
		var skills []SkillInfo
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			desc := ""
			metaPath := filepath.Join(dir, name, "metadata.json")
			if data, rerr := os.ReadFile(metaPath); rerr == nil {
				var meta struct {
					Description string `json:"description"`
				}
				if json.Unmarshal(data, &meta) == nil {
					desc = meta.Description
				}
			}
			skills = append(skills, SkillInfo{Name: name, Description: desc})
		}
		if skills == nil {
			skills = []SkillInfo{}
		}
		sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
		json.NewEncoder(w).Encode(skills)
	})

	// /api/tools is registered in http_tools.go (registerToolRoutes)

	mux.HandleFunc("/api/triggers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if triggerEngine == nil {
			json.NewEncoder(w).Encode(map[string]any{"triggers": []any{}, "count": 0})
			return
		}
		infos := triggerEngine.ListTriggers()
		if infos == nil {
			infos = []TriggerInfo{}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"triggers": infos,
			"count":    len(infos),
		})
	})

	// --- External Step: List pending callbacks ---
	mux.HandleFunc("/api/callbacks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		sql := `SELECT key, run_id, step_id, mode, auth_mode, status, timeout_at, post_sent, created_at
				FROM workflow_callbacks WHERE status='waiting' ORDER BY created_at DESC LIMIT 100`
		rows, err := queryDB(cfg.HistoryDB, sql)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		json.NewEncoder(w).Encode(map[string]any{"callbacks": rows, "count": len(rows)})
	})

	// --- External Step: Callback endpoint ---
	mux.HandleFunc("/api/callbacks/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		// Extract callback key from path.
		key := strings.TrimPrefix(r.URL.Path, "/api/callbacks/")
		if key == "" {
			http.Error(w, `{"error":"callback key required"}`, http.StatusBadRequest)
			return
		}

		// Validate key format to prevent path traversal and injection.
		if !isValidCallbackKey(key) {
			http.Error(w, `{"error":"invalid callback key format"}`, http.StatusBadRequest)
			return
		}

		// Look up callback record in DB for auth mode.
		record := queryPendingCallback(cfg.HistoryDB, key)
		if record == nil {
			// Check if it was already delivered or completed.
			existing := queryPendingCallbackByKey(cfg.HistoryDB, key)
			if existing != nil && (existing.Status == "delivered" || existing.Status == "completed") {
				json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
				return
			}
			http.Error(w, `{"error":"callback not found or expired"}`, http.StatusNotFound)
			return
		}

		// Read body upfront (1MB limit) — used by both auth verification and callback result.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil {
			http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
			return
		}
		if len(body) > 1<<20 {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			json.NewEncoder(w).Encode(map[string]string{"error": "callback body exceeds 1MB"})
			return
		}

		// Auth check based on callback auth mode.
		switch record.AuthMode {
		case "bearer":
			auth := r.Header.Get("Authorization")
			if auth == "" || auth != "Bearer "+cfg.APIToken {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		case "signature":
			// HMAC-SHA256 signature verification.
			sig := r.Header.Get("X-Callback-Signature")
			if sig == "" {
				http.Error(w, `{"error":"missing X-Callback-Signature header"}`, http.StatusUnauthorized)
				return
			}
			secret := callbackSignatureSecret(cfg.APIToken, key)
			if !verifyCallbackSignature(body, secret, sig) {
				http.Error(w, `{"error":"invalid signature"}`, http.StatusUnauthorized)
				return
			}
		case "open":
			// No auth required.
		default:
			// Default to bearer.
			auth := r.Header.Get("Authorization")
			if cfg.APIToken != "" && (auth == "" || auth != "Bearer "+cfg.APIToken) {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}

		cbResult := CallbackResult{
			Status:      200,
			Body:        string(body),
			ContentType: r.Header.Get("Content-Type"),
			RecvAt:      time.Now().Format(time.RFC3339),
		}

		// Path A: try in-memory delivery first (skip HasChannel pre-check to avoid TOCTOU).
		if callbackMgr != nil {
			out := callbackMgr.DeliverAndSeq(key, cbResult)
			switch out.Result {
			case DeliverOK:
				status := "delivered"
				if out.Mode == "streaming" {
					status = "accumulated"
					// Persist streaming callback immediately for crash recovery (#6).
					// Seq allocated atomically with Deliver to prevent race (#R2-1).
					appendStreamingCallback(cfg.HistoryDB, key, out.Seq, cbResult)
				}
				auditLog(cfg.HistoryDB, "callback."+status, "http",
					fmt.Sprintf("key=%s", key), clientIP(r))
				json.NewEncoder(w).Encode(map[string]string{"status": status})
				return
			case DeliverDup:
				// Single mode: already delivered — idempotent.
				json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
				return
			case DeliverFull:
				// Streaming buffer full — store to DB with seq allocated atomically (#R2-2).
				appendStreamingCallback(cfg.HistoryDB, key, out.Seq, cbResult)
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "streaming buffer full, stored to DB"})
				return
			case DeliverNoEntry:
				// Channel not registered — fall through to Path B (DB-only).
			}
		}

		// Path B: channel not alive — record to DB for recovery.
		// Re-check status to avoid overwriting completed/delivered records (#R2-8).
		current := queryPendingCallbackByKey(cfg.HistoryDB, key)
		if current != nil && (current.Status == "completed" || current.Status == "delivered") {
			json.NewEncoder(w).Encode(map[string]string{"status": "already_delivered"})
			return
		}
		markCallbackDelivered(cfg.HistoryDB, key, 0, cbResult)
		auditLog(cfg.HistoryDB, "callback.stored", "http",
			fmt.Sprintf("key=%s (no active channel)", key), clientIP(r))
		json.NewEncoder(w).Encode(map[string]string{"status": "stored"})
	})

	mux.HandleFunc("/api/triggers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		path := strings.TrimPrefix(r.URL.Path, "/api/triggers/")
		parts := strings.SplitN(path, "/", 2)
		name := parts[0]
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}

		if name == "" {
			http.Error(w, `{"error":"trigger name required"}`, http.StatusBadRequest)
			return
		}

		// Handle webhook trigger: POST /api/triggers/webhook/{id}
		if name == "webhook" && action != "" && r.Method == http.MethodPost {
			webhookID := action
			if triggerEngine == nil {
				http.Error(w, `{"error":"no triggers configured"}`, http.StatusNotFound)
				return
			}
			// Parse JSON payload into vars.
			var payload map[string]string
			if r.Body != nil {
				json.NewDecoder(r.Body).Decode(&payload)
			}
			if payload == nil {
				payload = make(map[string]string)
			}
			payload["_webhook_remote"] = clientIP(r)

			if err := triggerEngine.HandleWebhookTrigger(webhookID, payload); err != nil {
				status := http.StatusNotFound
				if strings.Contains(err.Error(), "cooldown") || strings.Contains(err.Error(), "disabled") {
					status = http.StatusTooManyRequests
				}
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.webhook", "http",
				fmt.Sprintf("trigger=%s", webhookID), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "accepted",
				"trigger": webhookID,
			})
			return
		}

		switch {
		case action == "fire" && r.Method == http.MethodPost:
			if triggerEngine == nil {
				http.Error(w, `{"error":"no triggers configured"}`, http.StatusNotFound)
				return
			}
			if err := triggerEngine.FireTrigger(name); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			auditLog(cfg.HistoryDB, "trigger.fire", "http",
				fmt.Sprintf("trigger=%s", name), clientIP(r))
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "accepted",
				"trigger": name,
			})

		case action == "runs" && r.Method == http.MethodGet:
			limit := 20
			if l := r.URL.Query().Get("limit"); l != "" {
				if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
					limit = n
				}
			}
			runs, err := queryTriggerRuns(cfg.HistoryDB, name, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
				return
			}
			if runs == nil {
				runs = []map[string]any{}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"runs":  runs,
				"count": len(runs),
			})

		default:
			http.Error(w, `{"error":"use POST .../fire or GET .../runs"}`, http.StatusMethodNotAllowed)
		}
	})
}
