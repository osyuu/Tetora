package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"tetora/internal/log"
)

// PushDeps holds dependencies for push notification HTTP handlers.
type PushDeps struct {
	Enabled       bool
	VAPIDKey      string
	Subscribe     func(endpoint, p256dh, auth, userAgent string) error
	Unsubscribe   func(endpoint string) error
	SendTest      func(title, body, icon string) error
	ListSubs      func() any
}

// PairingDeps holds dependencies for pairing HTTP handlers.
type PairingDeps struct {
	ListPending  func() any
	Approve      func(code string) (any, error)
	Reject       func(code string) error
	ListApproved func() (any, error)
	Revoke       func(channel, userID string) error
}

// RegisterPushRoutes registers push notification and pairing API routes.
func RegisterPushRoutes(mux *http.ServeMux, push PushDeps, pairing PairingDeps) {
	// --- Web Push ---
	mux.HandleFunc("/api/push/vapid-key", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !push.Enabled {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"publicKey": push.VAPIDKey,
		})
	})

	mux.HandleFunc("/api/push/subscribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !push.Enabled {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var sub struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256dh string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		}
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}

		if err := push.Subscribe(sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth, r.Header.Get("User-Agent")); err != nil {
			log.ErrorCtx(r.Context(), "push subscribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"subscribed"}`))
	})

	mux.HandleFunc("/api/push/unsubscribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !push.Enabled {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var req struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Endpoint == "" {
			http.Error(w, `{"error":"endpoint required"}`, http.StatusBadRequest)
			return
		}

		if err := push.Unsubscribe(req.Endpoint); err != nil {
			log.ErrorCtx(r.Context(), "push unsubscribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"unsubscribed"}`))
	})

	mux.HandleFunc("/api/push/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !push.Enabled {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var notif struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Icon  string `json:"icon"`
		}
		if err := json.NewDecoder(r.Body).Decode(&notif); err != nil || notif.Title == "" {
			notif.Title = "Tetora Test Notification"
			notif.Body = "This is a test push notification from Tetora"
			notif.Icon = "/dashboard/icon-192.png"
		}

		if err := push.SendTest(notif.Title, notif.Body, notif.Icon); err != nil {
			log.ErrorCtx(r.Context(), "push test failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"sent"}`))
	})

	mux.HandleFunc("/api/push/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !push.Enabled {
			http.Error(w, `{"error":"push notifications not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(push.ListSubs())
	})

	// --- Pairing ---
	mux.HandleFunc("/api/pairing/pending", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		pending := pairing.ListPending()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pending)
	})

	mux.HandleFunc("/api/pairing/approve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Code == "" {
			http.Error(w, `{"error":"code required"}`, http.StatusBadRequest)
			return
		}

		approved, err := pairing.Approve(req.Code)
		if err != nil {
			log.ErrorCtx(r.Context(), "pairing approve failed", "code", req.Code, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(approved)
	})

	mux.HandleFunc("/api/pairing/reject", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Code == "" {
			http.Error(w, `{"error":"code required"}`, http.StatusBadRequest)
			return
		}

		if err := pairing.Reject(req.Code); err != nil {
			log.ErrorCtx(r.Context(), "pairing reject failed", "code", req.Code, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"rejected"}`))
	})

	mux.HandleFunc("/api/pairing/approved", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		approved, err := pairing.ListApproved()
		if err != nil {
			log.ErrorCtx(r.Context(), "list approved failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(approved)
	})

	mux.HandleFunc("/api/pairing/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Channel string `json:"channel"`
			UserID  string `json:"userId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Channel == "" || req.UserID == "" {
			http.Error(w, `{"error":"channel and userId required"}`, http.StatusBadRequest)
			return
		}

		if err := pairing.Revoke(req.Channel, req.UserID); err != nil {
			log.ErrorCtx(r.Context(), "pairing revoke failed", "channel", req.Channel, "userId", req.UserID, "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"revoked"}`))
	})
}
