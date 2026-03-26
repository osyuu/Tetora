// Package metrics provides a Prometheus-compatible metrics registry
// using text exposition format, with no external dependencies.
package metrics

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// DefaultBuckets is the default histogram bucket configuration.
var DefaultBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// --- Metric Types ---

type metricType int

const (
	metricCounter metricType = iota
	metricGauge
	metricHistogram
)

type metricDef struct {
	name    string
	help    string
	typ     metricType
	labels  []string
	buckets []float64 // histogram only
}

type labelKey struct {
	name   string
	labels string // sorted label=value pairs
}

type counterValue struct {
	value float64
}

type gaugeValue struct {
	value float64
}

type histogramValue struct {
	count   uint64
	sum     float64
	buckets []histBucket
}

type histBucket struct {
	le    float64
	count uint64
}

// Registry holds all metrics.
type Registry struct {
	mu         sync.RWMutex
	defs       []metricDef
	counters   map[labelKey]*counterValue
	gauges     map[labelKey]*gaugeValue
	histograms map[labelKey]*histogramValue
	startTime  time.Time
}

// NewRegistry creates a new metrics registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[labelKey]*counterValue),
		gauges:     make(map[labelKey]*gaugeValue),
		histograms: make(map[labelKey]*histogramValue),
		startTime:  time.Now(),
	}
}

func (r *Registry) RegisterCounter(name, help string, labels []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, metricDef{name: name, help: help, typ: metricCounter, labels: labels})
}

func (r *Registry) RegisterGauge(name, help string, labels []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, metricDef{name: name, help: help, typ: metricGauge, labels: labels})
}

func (r *Registry) RegisterHistogram(name, help string, labels []string, buckets []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs = append(r.defs, metricDef{name: name, help: help, typ: metricHistogram, labels: labels, buckets: buckets})
}

// --- Counter Operations ---

func (r *Registry) CounterInc(name string, labelValues ...string) {
	r.CounterAdd(name, 1, labelValues...)
}

func (r *Registry) CounterAdd(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[key]
	if !ok {
		c = &counterValue{}
		r.counters[key] = c
	}
	c.value += val
}

// --- Gauge Operations ---

func (r *Registry) GaugeSet(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[key]
	if !ok {
		g = &gaugeValue{}
		r.gauges[key] = g
	}
	g.value = val
}

func (r *Registry) GaugeInc(name string, labelValues ...string) {
	r.GaugeAdd(name, 1, labelValues...)
}

func (r *Registry) GaugeDec(name string, labelValues ...string) {
	r.GaugeAdd(name, -1, labelValues...)
}

func (r *Registry) GaugeAdd(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[key]
	if !ok {
		g = &gaugeValue{}
		r.gauges[key] = g
	}
	g.value += val
}

// --- Histogram Operations ---

func (r *Registry) HistogramObserve(name string, val float64, labelValues ...string) {
	key := r.makeKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.histograms[key]
	if !ok {
		// Find the metric def for bucket configuration.
		var buckets []float64
		for _, d := range r.defs {
			if d.name == name && d.typ == metricHistogram {
				buckets = d.buckets
				break
			}
		}
		if buckets == nil {
			buckets = DefaultBuckets
		}
		h = &histogramValue{
			buckets: make([]histBucket, len(buckets)),
		}
		for i, b := range buckets {
			h.buckets[i].le = b
		}
		r.histograms[key] = h
	}
	h.count++
	h.sum += val
	for i := range h.buckets {
		if val <= h.buckets[i].le {
			h.buckets[i].count++
		}
	}
}

// --- Key Helpers ---

func (r *Registry) makeKey(name string, labelValues []string) labelKey {
	// Find the corresponding metric definition.
	var labels []string
	for _, d := range r.defs {
		if d.name == name {
			labels = d.labels
			break
		}
	}

	var labelStr string
	if len(labels) > 0 && len(labelValues) > 0 {
		pairs := make([]string, 0, len(labels))
		for i, l := range labels {
			val := ""
			if i < len(labelValues) {
				val = labelValues[i]
			}
			pairs = append(pairs, fmt.Sprintf(`%s="%s"`, l, val))
		}
		labelStr = strings.Join(pairs, ",")
	}

	return labelKey{name: name, labels: labelStr}
}

// --- Exposition ---

// WriteMetrics writes all metrics in Prometheus text exposition format.
func (r *Registry) WriteMetrics(w io.Writer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, def := range r.defs {
		switch def.typ {
		case metricCounter:
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s counter\n", def.name)
			r.writeCounterValues(w, def.name)

		case metricGauge:
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s gauge\n", def.name)
			r.writeGaugeValues(w, def.name)

		case metricHistogram:
			fmt.Fprintf(w, "# HELP %s %s\n", def.name, def.help)
			fmt.Fprintf(w, "# TYPE %s histogram\n", def.name)
			r.writeHistogramValues(w, def.name)
		}
		fmt.Fprintln(w)
	}
}

func (r *Registry) writeCounterValues(w io.Writer, name string) {
	for key, val := range r.counters {
		if key.name == name {
			if key.labels != "" {
				fmt.Fprintf(w, "%s{%s} %g\n", name, key.labels, val.value)
			} else {
				fmt.Fprintf(w, "%s %g\n", name, val.value)
			}
		}
	}
}

func (r *Registry) writeGaugeValues(w io.Writer, name string) {
	for key, val := range r.gauges {
		if key.name == name {
			if key.labels != "" {
				fmt.Fprintf(w, "%s{%s} %g\n", name, key.labels, val.value)
			} else {
				fmt.Fprintf(w, "%s %g\n", name, val.value)
			}
		}
	}
}

func (r *Registry) writeHistogramValues(w io.Writer, name string) {
	for key, val := range r.histograms {
		if key.name == name {
			labelPrefix := ""
			if key.labels != "" {
				labelPrefix = key.labels + ","
			}
			for _, b := range val.buckets {
				fmt.Fprintf(w, "%s_bucket{%sle=\"%g\"} %d\n", name, labelPrefix, b.le, b.count)
			}
			fmt.Fprintf(w, "%s_bucket{%sle=\"+Inf\"} %d\n", name, labelPrefix, val.count)
			if key.labels != "" {
				fmt.Fprintf(w, "%s_sum{%s} %g\n", name, key.labels, val.sum)
				fmt.Fprintf(w, "%s_count{%s} %d\n", name, key.labels, val.count)
			} else {
				fmt.Fprintf(w, "%s_sum %g\n", name, val.sum)
				fmt.Fprintf(w, "%s_count %d\n", name, val.count)
			}
		}
	}
}
