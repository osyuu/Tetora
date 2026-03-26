// Package quiet provides quiet-hours notification queueing and digest flushing.
package quiet

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Config holds the quiet hours configuration fields.
type Config struct {
	Enabled bool
	Start   string
	End     string
	TZ      string
	Digest  bool
}

// LogFn is the signature for a structured logger.
type LogFn func(msg string, kv ...any)

// State manages quiet hours notification queue.
type State struct {
	mu       sync.Mutex
	queue    []entry
	wasQuiet bool // was in quiet hours on last check
	logInfo  LogFn
}

type entry struct {
	message   string
	timestamp time.Time
}

// NewState creates a new quiet-hours state with the given logger.
func NewState(logInfo LogFn) *State {
	if logInfo == nil {
		logInfo = func(string, ...any) {}
	}
	return &State{logInfo: logInfo}
}

// IsQuietHours returns true if the current time is within the configured quiet period.
func IsQuietHours(cfg Config) bool {
	if !cfg.Enabled {
		return false
	}
	if cfg.Start == "" || cfg.End == "" {
		return false
	}

	loc := time.Local
	if cfg.TZ != "" {
		if l, err := time.LoadLocation(cfg.TZ); err == nil {
			loc = l
		}
	}

	now := time.Now().In(loc)
	startH, startM := ParseHHMM(cfg.Start)
	endH, endM := ParseHHMM(cfg.End)

	if startH < 0 || endH < 0 {
		return false
	}

	nowMin := now.Hour()*60 + now.Minute()
	startMin := startH*60 + startM
	endMin := endH*60 + endM

	if startMin <= endMin {
		// Same day: e.g. 09:00 - 17:00
		return nowMin >= startMin && nowMin < endMin
	}
	// Overnight: e.g. 23:00 - 08:00
	return nowMin >= startMin || nowMin < endMin
}

// ParseHHMM parses "HH:MM" format. Returns -1,-1 on error.
func ParseHHMM(s string) (int, int) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return -1, -1
	}
	h, m := 0, 0
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return -1, -1
		}
		h = h*10 + int(c-'0')
	}
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return -1, -1
		}
		m = m*10 + int(c-'0')
	}
	if h > 23 || m > 59 {
		return -1, -1
	}
	return h, m
}

// Enqueue adds a notification to the quiet hours queue.
func (qs *State) Enqueue(msg string) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.queue = append(qs.queue, entry{
		message:   msg,
		timestamp: time.Now(),
	})
	qs.logInfo("quiet hours notification queued", "queueSize", len(qs.queue))
}

// FlushDigest sends accumulated notifications as a digest and clears the queue.
func (qs *State) FlushDigest(cfg Config, notifyFn func(string)) {
	qs.mu.Lock()
	entries := qs.queue
	qs.queue = nil
	qs.mu.Unlock()

	if len(entries) == 0 || notifyFn == nil {
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Tetora Digest (%s - %s)\n",
		cfg.Start, cfg.End))
	lines = append(lines, fmt.Sprintf("%d notifications during quiet hours:\n", len(entries)))

	for _, e := range entries {
		// Truncate each entry to keep digest readable.
		msg := e.message
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		lines = append(lines, msg)
	}

	digest := strings.Join(lines, "\n")
	qs.logInfo("quiet hours digest flushing", "entries", len(entries))
	notifyFn(digest)
}

// QueuedCount returns the number of queued notifications.
func (qs *State) QueuedCount() int {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	return len(qs.queue)
}

// CheckTransition checks if we just left quiet hours and flushes digest.
// Called from cron tick. Returns true if currently in quiet hours.
func (qs *State) CheckTransition(cfg Config, notifyFn func(string)) bool {
	inQuiet := IsQuietHours(cfg)

	qs.mu.Lock()
	wasQuiet := qs.wasQuiet
	qs.wasQuiet = inQuiet
	qs.mu.Unlock()

	// Just left quiet hours — flush digest.
	if wasQuiet && !inQuiet && cfg.Digest {
		qs.FlushDigest(cfg, notifyFn)
	}

	return inQuiet
}
