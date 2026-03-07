package main

import (
	"encoding/json"
	"net/http"
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
			Name    string `json:"name"`
			State   string `json:"state"`
			Workdir string `json:"workdir"`
			Uptime  string `json:"uptime"`
			Source  string `json:"source"`
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
				out = append(out, workerInfo{
					Name:    "hook-" + sessionShort,
					State:   hw.State,
					Workdir: hw.Cwd,
					Uptime:  time.Since(hw.FirstSeen).Round(time.Second).String(),
					Source:  "hooks",
				})
			}
		}

		if out == nil {
			out = []workerInfo{}
		}
		json.NewEncoder(w).Encode(map[string]any{"workers": out, "count": len(out)})
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
