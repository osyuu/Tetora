package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *Server) registerWorkersRoutes(mux *http.ServeMux) {
	// GET /api/workers — list all active hook-based workers.
	mux.HandleFunc("/api/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		type workerInfo struct {
			SessionId    string  `json:"sessionId"`
			Name         string  `json:"name"`
			State        string  `json:"state"`
			Workdir      string  `json:"workdir"`
			Uptime       string  `json:"uptime"`
			ToolCount    int     `json:"toolCount"`
			LastTool     string  `json:"lastTool,omitempty"`
			Source       string  `json:"source"`
			Agent        string  `json:"agent,omitempty"`
			TaskName     string  `json:"taskName,omitempty"`
			TaskID       string  `json:"taskId,omitempty"`
			JobID        string  `json:"jobId,omitempty"`
			CostUSD      float64 `json:"costUsd,omitempty"`
			InputTokens  int     `json:"inputTokens,omitempty"`
			OutputTokens int     `json:"outputTokens,omitempty"`
			ContextPct   int     `json:"contextPct,omitempty"`
			Model        string  `json:"model,omitempty"`
		}
		var out []workerInfo

		if s.hookReceiver != nil {
			hookWorkers := s.hookReceiver.ListHookWorkers()
			for _, hw := range hookWorkers {
				// Skip "done" workers older than 2 minutes.
				if hw.State == "done" && time.Since(hw.LastSeen) > 2*time.Minute {
					continue
				}
				sessionShort := hw.SessionID
				if len(sessionShort) > 12 {
					sessionShort = sessionShort[:12]
				}
				wi := workerInfo{
					SessionId: sessionShort,
					Name:      "hook-" + sessionShort,
					State:     hw.State,
					Workdir:   hw.Cwd,
					Uptime:    time.Since(hw.FirstSeen).Round(time.Second).String(),
					ToolCount: hw.ToolCount,
					LastTool:  hw.LastTool,
					Source:    "manual",
				}
				wi.CostUSD = hw.CostUSD
				wi.InputTokens = hw.InputTokens
				wi.OutputTokens = hw.OutputTokens
				wi.ContextPct = hw.ContextPct
				wi.Model = hw.Model
				if o := hw.Origin; o != nil {
					wi.Source = o.Source
					wi.Agent = o.Agent
					wi.TaskName = o.TaskName
					wi.TaskID = o.TaskID
					wi.JobID = o.JobID
					if o.TaskName != "" {
						wi.Name = o.TaskName
					}
				}
				out = append(out, wi)
			}
		}

		if out == nil {
			out = []workerInfo{}
		}
		json.NewEncoder(w).Encode(map[string]any{"workers": out, "count": len(out)})
	})

	// GET /api/workers/{id}/events — event log for a specific worker.
	mux.HandleFunc("/api/workers/events/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// Path: /api/workers/events/{sessionIdPrefix}
		idPrefix := strings.TrimPrefix(r.URL.Path, "/api/workers/events/")
		if idPrefix == "" {
			http.NotFound(w, r)
			return
		}

		if s.hookReceiver == nil {
			json.NewEncoder(w).Encode(map[string]any{"events": []any{}})
			return
		}

		worker, events := s.hookReceiver.FindHookWorkerByPrefix(idPrefix)
		if worker == nil {
			json.NewEncoder(w).Encode(map[string]any{"events": []any{}})
			return
		}

		resp := map[string]any{
			"sessionId": idPrefix,
			"state":     worker.State,
			"workdir":   worker.Cwd,
			"toolCount": worker.ToolCount,
			"lastTool":  worker.LastTool,
			"uptime":    time.Since(worker.FirstSeen).Round(time.Second).String(),
			"events":    events,
		}
		resp["costUsd"] = worker.CostUSD
		resp["inputTokens"] = worker.InputTokens
		resp["outputTokens"] = worker.OutputTokens
		resp["contextPct"] = worker.ContextPct
		resp["model"] = worker.Model
		if o := worker.Origin; o != nil {
			resp["source"] = o.Source
			resp["agent"] = o.Agent
			resp["taskName"] = o.TaskName
			resp["taskId"] = o.TaskID
			resp["jobId"] = o.JobID
		} else {
			resp["source"] = "manual"
		}
		json.NewEncoder(w).Encode(resp)
	})

	// GET /api/workers/agents — list agents with their provider info.
	mux.HandleFunc("/api/workers/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		type agentInfo struct {
			Name     string `json:"name"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		cfg := s.cfg
		agents := make([]agentInfo, 0, len(cfg.Agents))
		for name, rc := range cfg.Agents {
			p := rc.Provider
			if p == "" {
				p = cfg.DefaultProvider
			}
			if p == "" {
				p = "claude"
			}
			agents = append(agents, agentInfo{
				Name:     name,
				Provider: p,
				Model:    rc.Model,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"agents": agents})
	})

	// GET /api/codex/status — Codex quota status (cached 5 min).
	mux.HandleFunc("/api/codex/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// Find the codex binary path from registry or default.
		binaryPath := "codex"
		if s.cfg.registry != nil {
			if p, err := s.cfg.registry.get("codex"); err == nil {
				if cp, ok := p.(*CodexProvider); ok {
					binaryPath = cp.binaryPath
				}
			}
		}

		q, err := fetchCodexQuota(binaryPath)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(q)
	})

	// GET/PATCH /api/settings/discord — Discord display settings.
	mux.HandleFunc("/api/settings/discord", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			showProgress := s.cfg.Discord.ShowProgress == nil || *s.cfg.Discord.ShowProgress
			json.NewEncoder(w).Encode(map[string]any{
				"showProgress": showProgress,
			})

		case http.MethodPatch:
			var body struct {
				ShowProgress *bool `json:"showProgress"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid body"})
				return
			}
			if body.ShowProgress != nil {
				s.cfg.Discord.ShowProgress = body.ShowProgress
				configPath := findConfigPath()
				if configPath != "" {
					updateConfigField(configPath, func(raw map[string]any) {
						disc, _ := raw["discord"].(map[string]any)
						if disc == nil {
							disc = map[string]any{}
							raw["discord"] = disc
						}
						disc["showProgress"] = *body.ShowProgress
					})
				}
			}
			showProgress := s.cfg.Discord.ShowProgress == nil || *s.cfg.Discord.ShowProgress
			json.NewEncoder(w).Encode(map[string]any{"showProgress": showProgress})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

}
