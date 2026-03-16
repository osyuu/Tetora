package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestCounterIncrement(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("test_counter", "Test counter", []string{"status"})

	r.CounterInc("test_counter", "success")
	r.CounterInc("test_counter", "success")
	r.CounterInc("test_counter", "failure")

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	if !strings.Contains(output, "# HELP test_counter Test counter") {
		t.Error("missing HELP line")
	}
	if !strings.Contains(output, "# TYPE test_counter counter") {
		t.Error("missing TYPE line")
	}
	if !strings.Contains(output, `test_counter{status="success"} 2`) {
		t.Errorf("expected success=2, got: %s", output)
	}
	if !strings.Contains(output, `test_counter{status="failure"} 1`) {
		t.Errorf("expected failure=1, got: %s", output)
	}
}

func TestCounterAdd(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("test_cost", "Test cost", []string{"role"})

	r.CounterAdd("test_cost", 1.5, "琉璃")
	r.CounterAdd("test_cost", 2.3, "琉璃")

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	if !strings.Contains(output, `test_cost{role="琉璃"} 3.8`) {
		t.Errorf("expected cost=3.8, got: %s", output)
	}
}

func TestGaugeSet(t *testing.T) {
	r := NewRegistry()
	r.RegisterGauge("test_gauge", "Test gauge", []string{"server"})

	r.GaugeSet("test_gauge", 5, "mcp1")
	r.GaugeSet("test_gauge", 10, "mcp2")
	r.GaugeSet("test_gauge", 7, "mcp1") // overwrite

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	if !strings.Contains(output, "# TYPE test_gauge gauge") {
		t.Error("missing TYPE gauge")
	}
	if !strings.Contains(output, `test_gauge{server="mcp1"} 7`) {
		t.Errorf("expected mcp1=7, got: %s", output)
	}
	if !strings.Contains(output, `test_gauge{server="mcp2"} 10`) {
		t.Errorf("expected mcp2=10, got: %s", output)
	}
}

func TestGaugeIncDec(t *testing.T) {
	r := NewRegistry()
	r.RegisterGauge("test_active", "Test active", nil)

	r.GaugeInc("test_active")
	r.GaugeInc("test_active")
	r.GaugeDec("test_active")

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	if !strings.Contains(output, "test_active 1") {
		t.Errorf("expected active=1, got: %s", output)
	}
}

func TestHistogramObserve(t *testing.T) {
	r := NewRegistry()
	buckets := []float64{0.1, 0.5, 1, 5}
	r.RegisterHistogram("test_duration", "Test duration", []string{"role"}, buckets)

	r.HistogramObserve("test_duration", 0.05, "琉璃")
	r.HistogramObserve("test_duration", 0.3, "琉璃")
	r.HistogramObserve("test_duration", 2.5, "琉璃")

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	if !strings.Contains(output, "# TYPE test_duration histogram") {
		t.Error("missing TYPE histogram")
	}

	expected := map[string]bool{
		`test_duration_bucket{role="琉璃",le="0.1"} 1`:  true,
		`test_duration_bucket{role="琉璃",le="0.5"} 2`:  true,
		`test_duration_bucket{role="琉璃",le="1"} 2`:    true,
		`test_duration_bucket{role="琉璃",le="5"} 3`:    true,
		`test_duration_bucket{role="琉璃",le="+Inf"} 3`: true,
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "test_duration_bucket") {
			delete(expected, line)
		}
	}
	if len(expected) > 0 {
		t.Errorf("missing expected bucket lines: %v\nGot:\n%s", expected, output)
	}

	if !strings.Contains(output, `test_duration_sum{role="琉璃"} 2.85`) {
		t.Errorf("expected sum=2.85, got: %s", output)
	}
	if !strings.Contains(output, `test_duration_count{role="琉璃"} 3`) {
		t.Errorf("expected count=3, got: %s", output)
	}
}

func TestLabelFormatting(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("test_multi", "Test multi-label", []string{"role", "status", "provider"})

	r.CounterInc("test_multi", "琉璃", "success", "claude")

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	expected := `test_multi{role="琉璃",status="success",provider="claude"} 1`
	if !strings.Contains(output, expected) {
		t.Errorf("expected label format: %s\nGot: %s", expected, output)
	}
}

func TestNoLabelsCounter(t *testing.T) {
	r := NewRegistry()
	r.RegisterCounter("test_simple", "Simple counter", nil)

	r.CounterInc("test_simple")
	r.CounterInc("test_simple")

	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	output := buf.String()

	if !strings.Contains(output, "test_simple 2") {
		t.Errorf("expected simple counter output, got: %s", output)
	}
}
