package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"tetora/internal/provider/codex"
)

// WorkersDeps holds dependencies for workers HTTP handlers.
type WorkersDeps struct {
	ListWorkers       func() any
	FindWorkerEvents  func(idPrefix string) any // returns full response map or nil
	ListAgentInfos    func() any
	GetDiscordShowProgress func() bool
	SetDiscordShowProgress func(val bool)
}

// RegisterWorkersRoutes registers workers, agents, codex, and settings API routes.
func RegisterWorkersRoutes(mux *http.ServeMux, d WorkersDeps) {
	mux.HandleFunc("/api/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.ListWorkers())
	})

	mux.HandleFunc("/api/workers/events/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		idPrefix := strings.TrimPrefix(r.URL.Path, "/api/workers/events/")
		if idPrefix == "" {
			http.NotFound(w, r)
			return
		}

		resp := d.FindWorkerEvents(idPrefix)
		if resp == nil {
			json.NewEncoder(w).Encode(map[string]any{"events": []any{}})
			return
		}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/workers/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d.ListAgentInfos())
	})

	mux.HandleFunc("/api/codex/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		q, err := codex.FetchQuota("codex")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(q)
	})

	mux.HandleFunc("/api/settings/discord", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{
				"showProgress": d.GetDiscordShowProgress(),
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
				d.SetDiscordShowProgress(*body.ShowProgress)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"showProgress": d.GetDiscordShowProgress(),
			})

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}
