package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"tetora/internal/audit"
	"tetora/internal/circuit"
)

// HealthDeps holds dependencies for health HTTP handlers.
type HealthDeps struct {
	StartTime time.Time
	HistoryDB string

	// DefaultProvider returns the default provider name.
	DefaultProvider func() string

	// GetRunningAgents returns agent info for /api/health/agents.
	GetRunningAgents func() ([]map[string]any, bool) // agents, draining

	// SSEClientCount returns connected SSE client count (-1 if unavailable).
	SSEClientCount func() int

	// LastCronRun returns last cron run time.
	LastCronRun func() time.Time

	// DeepCheck returns the deep health check map for /healthz.
	DeepCheck func() map[string]any

	// WriteMetrics writes Prometheus metrics; returns false if unavailable.
	WriteMetrics func(w http.ResponseWriter) bool

	// CircuitStatus returns circuit breaker status map.
	CircuitStatus func() map[string]any

	// CircuitReset resets a circuit by provider. Returns false if provider not found.
	CircuitReset func(provider string) bool

	// CircuitRegistry is the raw circuit registry for type assertion (may be nil).
	CircuitRegistry interface{}
}

func formatDurationShort(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// RegisterHealthRoutes registers health, metrics, and circuit breaker routes.
func RegisterHealthRoutes(mux *http.ServeMux, d HealthDeps) {
	// --- Agent Count (for pre-update check) ---
	mux.HandleFunc("/api/health/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		agents, draining := d.GetRunningAgents()
		json.NewEncoder(w).Encode(map[string]any{
			"active":   len(agents),
			"draining": draining,
			"agents":   agents,
		})
	})

	// --- Dashboard Health Summary ---
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		uptime := time.Since(d.StartTime)
		uptimeStr := formatDurationShort(uptime)
		dbSize := "-"
		if d.HistoryDB != "" {
			if fi, err := os.Stat(d.HistoryDB); err == nil {
				mb := float64(fi.Size()) / (1024 * 1024)
				dbSize = fmt.Sprintf("%.1f MB", mb)
			}
		}
		sseClients := 0
		if d.SSEClientCount != nil {
			if n := d.SSEClientCount(); n >= 0 {
				sseClients = n
			}
		}
		lastCron := "-"
		if d.LastCronRun != nil {
			if last := d.LastCronRun(); !last.IsZero() {
				lastCron = formatDurationShort(time.Since(last)) + " ago"
			}
		}
		provider := ""
		if d.DefaultProvider != nil {
			provider = d.DefaultProvider()
		}
		json.NewEncoder(w).Encode(map[string]any{
			"uptime":     uptimeStr,
			"dbSize":     dbSize,
			"sseClients": sseClients,
			"lastCron":   lastCron,
			"provider":   provider,
		})
	})

	// --- Health ---
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		checks := d.DeepCheck()
		b, _ := json.MarshalIndent(checks, "", "  ")
		w.Write(b)
	})

	// --- Metrics ---
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if d.WriteMetrics == nil {
			http.Error(w, "metrics not initialized", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if !d.WriteMetrics(w) {
			http.Error(w, "metrics not initialized", http.StatusInternalServerError)
		}
	})

	// --- Circuit Breakers ---
	mux.HandleFunc("/circuits", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var status map[string]any
		if d.CircuitRegistry != nil {
			status = d.CircuitRegistry.(*circuit.Registry).Status()
		} else {
			status = map[string]any{}
		}
		b, _ := json.MarshalIndent(status, "", "  ")
		w.Write(b)
	})

	mux.HandleFunc("/circuits/", func(w http.ResponseWriter, r *http.Request) {
		// POST /circuits/{provider}/reset
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/circuits/")
		provider := strings.TrimSuffix(path, "/reset")
		if provider == "" || !strings.HasSuffix(path, "/reset") {
			http.Error(w, `{"error":"use POST /circuits/{provider}/reset"}`, http.StatusBadRequest)
			return
		}
		if d.CircuitRegistry == nil {
			http.Error(w, `{"error":"circuit breaker not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		if ok := d.CircuitRegistry.(*circuit.Registry).ResetKey(provider); !ok {
			http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
			return
		}
		audit.Log(d.HistoryDB, "circuit.reset", r.RemoteAddr, provider, "")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fmt.Sprintf(`{"provider":%q,"state":"closed"}`, provider)))
	})
}
