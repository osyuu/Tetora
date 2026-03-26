package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// CanvasDeps holds dependencies for canvas HTTP handlers.
type CanvasDeps struct {
	ListSessions func() (sessions any, count int)
	GetSession   func(id string) (any, error)
	SendMessage  func(sessionID string, msg json.RawMessage) error
	CloseSession func(id string) error
}

// RegisterCanvasRoutes registers canvas API routes.
func RegisterCanvasRoutes(mux *http.ServeMux, d CanvasDeps) {
	mux.HandleFunc("/api/canvas/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sessions, count := d.ListSessions()
		json.NewEncoder(w).Encode(map[string]any{
			"sessions": sessions,
			"count":    count,
		})
	})

	mux.HandleFunc("/api/canvas/get", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, `{"error":"id parameter required"}`, http.StatusBadRequest)
			return
		}
		session, err := d.GetSession(id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(session)
	})

	mux.HandleFunc("/api/canvas/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		var msg struct {
			SessionID string          `json:"sessionId"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if msg.SessionID == "" {
			http.Error(w, `{"error":"sessionId required"}`, http.StatusBadRequest)
			return
		}
		if err := d.SendMessage(msg.SessionID, msg.Message); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "message received",
		})
	})

	mux.HandleFunc("/api/canvas/close", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, `{"error":"id parameter required"}`, http.StatusBadRequest)
			return
		}
		if err := d.CloseSession(id); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "closed",
			"id":     id,
		})
	})
}
