package httpapi

import (
	"net/http"

	"tetora/internal/log"
)

// WebhookHandler represents an optional webhook endpoint.
type WebhookHandler struct {
	Path    string
	Handler http.HandlerFunc
}

// WebhookDeps holds dependencies for webhook route registration.
type WebhookDeps struct {
	Handlers []WebhookHandler
}

// RegisterWebhookRoutes registers webhook endpoints for messaging platforms.
func RegisterWebhookRoutes(mux *http.ServeMux, d WebhookDeps) {
	for _, h := range d.Handlers {
		mux.HandleFunc(h.Path, h.Handler)
		log.Info("webhook endpoint registered", "path", h.Path)
	}
}
