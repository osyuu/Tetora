package main

import "tetora/internal/metrics"

// Type alias so callers continue to compile unchanged.
type MetricsRegistry = metrics.Registry

// metrics is the global metrics registry.
var metricsGlobal *MetricsRegistry

// initMetrics creates and registers all Tetora metrics.
func initMetrics() {
	metricsGlobal = metrics.NewRegistry()

	// Dispatch metrics.
	metricsGlobal.RegisterCounter("tetora_dispatch_total", "Total dispatches", []string{"role", "status"})
	metricsGlobal.RegisterHistogram("tetora_dispatch_duration_seconds", "Dispatch latency", []string{"role"}, metrics.DefaultBuckets)
	metricsGlobal.RegisterCounter("tetora_dispatch_cost_usd", "Total cost in USD", []string{"role"})

	// Provider metrics.
	metricsGlobal.RegisterCounter("tetora_provider_requests_total", "Provider API calls", []string{"provider", "status"})
	metricsGlobal.RegisterHistogram("tetora_provider_latency_seconds", "Provider response time", []string{"provider"}, metrics.DefaultBuckets)
	metricsGlobal.RegisterCounter("tetora_provider_tokens_total", "Token usage", []string{"provider", "direction"})

	// Infrastructure metrics.
	metricsGlobal.RegisterGauge("tetora_circuit_state", "Circuit breaker state (0=closed,1=open,2=half-open)", []string{"provider"})
	metricsGlobal.RegisterGauge("tetora_session_active", "Active session count", []string{"role"})
	metricsGlobal.RegisterGauge("tetora_queue_depth", "Offline queue depth", nil)
	metricsGlobal.RegisterCounter("tetora_cron_runs_total", "Cron job executions", []string{"status"})
}

// --- Convenience: Record Dispatch Metrics ---

func recordDispatchMetrics(role, status string, durationSec float64, costUSD float64, tokensIn, tokensOut int, provider string) {
	if metricsGlobal == nil {
		return
	}
	metricsGlobal.CounterInc("tetora_dispatch_total", role, status)
	metricsGlobal.HistogramObserve("tetora_dispatch_duration_seconds", durationSec, role)
	if costUSD > 0 {
		metricsGlobal.CounterAdd("tetora_dispatch_cost_usd", costUSD, role)
	}
	if provider != "" {
		metricsGlobal.CounterInc("tetora_provider_requests_total", provider, status)
		metricsGlobal.HistogramObserve("tetora_provider_latency_seconds", durationSec, provider)
		if tokensIn > 0 {
			metricsGlobal.CounterAdd("tetora_provider_tokens_total", float64(tokensIn), provider, "input")
		}
		if tokensOut > 0 {
			metricsGlobal.CounterAdd("tetora_provider_tokens_total", float64(tokensOut), provider, "output")
		}
	}
}
