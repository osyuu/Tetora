package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// AcquireResult is returned by AcquireSlot with optional warning text.
type AcquireResult struct {
	Warning string // non-empty when pressure is high (for interactive sessions)
}

// SlotPressureGuard wraps sem acquisition with interactive reservation.
type SlotPressureGuard struct {
	Cfg      config.SlotPressureConfig
	Sem      chan struct{} // the parent semaphore (depth-0 tasks only)
	SemCap   int          // capacity of Sem (== MaxConcurrent)
	active   atomic.Int32 // shadows channel usage for non-blocking pressure check
	waiting  atomic.Int32 // number of non-interactive tasks in poll queue
	NotifyFn func(string) // notification chain (Telegram/Discord)
	Broker   SSEBrokerPublisher

	lastAlertAt atomic.Int64 // unix seconds, cooldown for proactive alerts
}

// ReservedSlots returns the configured reserved slots or the default (2).
func (g *SlotPressureGuard) ReservedSlots() int {
	if g.Cfg.ReservedSlots > 0 {
		return g.Cfg.ReservedSlots
	}
	return 2
}

// WarnThreshold returns the configured warn threshold or the default (3).
func (g *SlotPressureGuard) WarnThreshold() int {
	if g.Cfg.WarnThreshold > 0 {
		return g.Cfg.WarnThreshold
	}
	return 3
}

// NonInteractiveTimeout returns the configured timeout or the default (5m).
func (g *SlotPressureGuard) NonInteractiveTimeout() time.Duration {
	if g.Cfg.NonInteractiveTimeout != "" {
		d, err := time.ParseDuration(g.Cfg.NonInteractiveTimeout)
		if err == nil && d > 0 {
			return d
		}
	}
	return 5 * time.Minute
}

// PollInterval returns the configured poll interval or the default (2s).
func (g *SlotPressureGuard) PollInterval() time.Duration {
	if g.Cfg.PollInterval != "" {
		d, err := time.ParseDuration(g.Cfg.PollInterval)
		if err == nil && d > 0 {
			return d
		}
	}
	return 2 * time.Second
}

// MonitorInterval returns the configured monitor interval or the default (30s).
func (g *SlotPressureGuard) MonitorInterval() time.Duration {
	if g.Cfg.MonitorInterval != "" {
		d, err := time.ParseDuration(g.Cfg.MonitorInterval)
		if err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// available returns the number of free slots (non-blocking).
func (g *SlotPressureGuard) available() int {
	return g.SemCap - int(g.active.Load())
}

// IsInteractiveSource classifies a task source as interactive or non-interactive.
func IsInteractiveSource(source string) bool {
	switch {
	case source == "ask", source == "chat":
		return true
	case strings.HasPrefix(source, "route:discord"),
		strings.HasPrefix(source, "route:telegram"),
		strings.HasPrefix(source, "route:slack"),
		strings.HasPrefix(source, "route:line"),
		strings.HasPrefix(source, "route:imessage"),
		strings.HasPrefix(source, "route:matrix"),
		strings.HasPrefix(source, "route:signal"),
		strings.HasPrefix(source, "route:teams"),
		strings.HasPrefix(source, "route:whatsapp"),
		strings.HasPrefix(source, "route:googlechat"):
		return true
	default:
		return false
	}
}

// AcquireSlot acquires a slot from the semaphore with pressure awareness.
// For interactive sources: always acquires immediately, returns warning if pressure is high.
// For non-interactive sources: if available <= reservedSlots, polls until a slot frees or timeout.
func (g *SlotPressureGuard) AcquireSlot(ctx context.Context, sem chan struct{}, source string) (*AcquireResult, error) {
	if IsInteractiveSource(source) {
		return g.acquireInteractive(ctx, sem, source)
	}
	return g.acquireNonInteractive(ctx, sem, source)
}

func (g *SlotPressureGuard) acquireInteractive(ctx context.Context, sem chan struct{}, _ string) (*AcquireResult, error) {
	select {
	case sem <- struct{}{}:
		g.active.Add(1)
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	result := &AcquireResult{}

	avail := g.available()
	if avail <= g.WarnThreshold() {
		used := int(g.active.Load())
		result.Warning = fmt.Sprintf("⚠️ 排程接近滿載（%d/%d slots 使用中），回應可能延遲", used, g.SemCap)
	}

	return result, nil
}

func (g *SlotPressureGuard) acquireNonInteractive(ctx context.Context, sem chan struct{}, source string) (*AcquireResult, error) {
	if g.available() > g.ReservedSlots() {
		select {
		case sem <- struct{}{}:
			g.active.Add(1)
			return &AcquireResult{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	g.waiting.Add(1)
	defer g.waiting.Add(-1)

	log.Info("slot pressure: non-interactive task waiting",
		"source", source,
		"available", g.available(),
		"reserved", g.ReservedSlots())

	timeout := time.NewTimer(g.NonInteractiveTimeout())
	defer timeout.Stop()

	poll := time.NewTicker(g.PollInterval())
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-timeout.C:
			log.Warn("slot pressure: non-interactive task force-acquiring after timeout",
				"source", source, "timeout", g.NonInteractiveTimeout().String())
			select {
			case sem <- struct{}{}:
				g.active.Add(1)
				return &AcquireResult{}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}

		case <-poll.C:
			if g.available() > g.ReservedSlots() {
				select {
				case sem <- struct{}{}:
					g.active.Add(1)
					return &AcquireResult{}, nil
				default:
				}
			}
		}
	}
}

// ReleaseSlot decrements the active counter. Must be called (via defer) after AcquireSlot.
func (g *SlotPressureGuard) ReleaseSlot() {
	g.active.Add(-1)
}

// RunMonitor is a background goroutine that periodically checks slot pressure
// and publishes SSE events / sends notifications when thresholds are crossed.
func (g *SlotPressureGuard) RunMonitor(ctx context.Context) {
	ticker := time.NewTicker(g.MonitorInterval())
	defer ticker.Stop()

	const alertCooldown = 60 // seconds between proactive alerts

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			avail := g.available()
			used := int(g.active.Load())
			waiting := int(g.waiting.Load())

			if g.Broker != nil {
				g.Broker.Publish(SSEDashboardKey, SSEEvent{
					Type: "slot_pressure",
					Data: map[string]any{
						"available": avail,
						"used":      used,
						"capacity":  g.SemCap,
						"waiting":   waiting,
					},
				})
			}

			if avail <= g.WarnThreshold() && g.NotifyFn != nil {
				now := time.Now().Unix()
				last := g.lastAlertAt.Load()
				if now-last >= alertCooldown {
					if g.lastAlertAt.CompareAndSwap(last, now) {
						msg := fmt.Sprintf("⚠️ 排程即將滿載（%d/%d slots），%d 個非互動任務已進入等待佇列",
							used, g.SemCap, waiting)
						g.NotifyFn(msg)
					}
				}
			}
		}
	}
}
