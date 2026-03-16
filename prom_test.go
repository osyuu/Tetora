package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFullMetricsOutput(t *testing.T) {
	// Initialize full metrics like in production.
	initMetrics()

	// Record some sample data.
	metricsGlobal.CounterInc("tetora_dispatch_total", "琉璃", "success")
	metricsGlobal.HistogramObserve("tetora_dispatch_duration_seconds", 1.5, "琉璃")
	metricsGlobal.CounterAdd("tetora_dispatch_cost_usd", 0.05, "琉璃")
	metricsGlobal.CounterInc("tetora_provider_requests_total", "claude", "success")
	metricsGlobal.GaugeSet("tetora_session_active", 2, "琉璃")
	metricsGlobal.GaugeSet("tetora_queue_depth", 5)
	metricsGlobal.CounterInc("tetora_cron_runs_total", "success")

	var buf bytes.Buffer
	metricsGlobal.WriteMetrics(&buf)
	output := buf.String()

	// Check all registered metrics are present.
	expectedMetrics := []string{
		"tetora_dispatch_total",
		"tetora_dispatch_duration_seconds",
		"tetora_dispatch_cost_usd",
		"tetora_provider_requests_total",
		"tetora_provider_latency_seconds",
		"tetora_provider_tokens_total",
		"tetora_circuit_state",
		"tetora_session_active",
		"tetora_queue_depth",
		"tetora_cron_runs_total",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(output, "# TYPE "+metric) {
			t.Errorf("missing metric in output: %s", metric)
		}
	}

	// Check actual values.
	if !strings.Contains(output, `tetora_dispatch_total{role="琉璃",status="success"} 1`) {
		t.Error("dispatch_total value missing")
	}
	if !strings.Contains(output, `tetora_session_active{role="琉璃"} 2`) {
		t.Error("session_active value missing")
	}
	if !strings.Contains(output, "tetora_queue_depth 5") {
		t.Error("queue_depth value missing")
	}
}
