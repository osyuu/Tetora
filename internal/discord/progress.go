package discord

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// ProgressBuilder accumulates SSE events and renders a progress display for Discord.
type ProgressBuilder struct {
	mu      sync.Mutex
	startAt time.Time
	tools   []string
	text    strings.Builder
	dirty   bool
}

// NewProgressBuilder creates a new builder.
func NewProgressBuilder() *ProgressBuilder {
	return &ProgressBuilder{
		startAt: time.Now(),
	}
}

// AddToolCall records a tool invocation.
func (b *ProgressBuilder) AddToolCall(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tools = append(b.tools, name)
	b.dirty = true
}

// AddText appends text content (strips ANSI escapes).
func (b *ProgressBuilder) AddText(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = ansiEscapeRe.ReplaceAllString(text, "")
	if text == "" {
		return
	}
	b.text.WriteString(text)
	b.dirty = true
}

// ReplaceText replaces all accumulated text (strips ANSI escapes).
func (b *ProgressBuilder) ReplaceText(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text = ansiEscapeRe.ReplaceAllString(text, "")
	b.text.Reset()
	b.text.WriteString(text)
	b.dirty = true
}

// Render returns the current progress display string and clears dirty flag.
func (b *ProgressBuilder) Render() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dirty = false

	elapsed := time.Since(b.startAt).Round(time.Second)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Working... (%s)\n", elapsed))

	start := 0
	if len(b.tools) > 5 {
		start = len(b.tools) - 5
		sb.WriteString(fmt.Sprintf("... and %d earlier steps\n", start))
	}
	for _, t := range b.tools[start:] {
		sb.WriteString(fmt.Sprintf("> %s\n", t))
	}

	accumulated := b.text.String()
	if accumulated != "" {
		sb.WriteString("\n")
		header := sb.String()
		maxText := 2000 - len(header) - 10
		if maxText < 100 {
			maxText = 100
		}
		if len(accumulated) > maxText {
			trimmed := accumulated[len(accumulated)-maxText:]
			if idx := strings.Index(trimmed, "\n"); idx >= 0 && idx < len(trimmed)/2 {
				trimmed = trimmed[idx+1:]
			}
			sb.WriteString("..." + trimmed)
		} else {
			sb.WriteString(accumulated)
		}
	}

	return sb.String()
}

// GetText returns the current accumulated text.
func (b *ProgressBuilder) GetText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text.String()
}

// IsDirty returns whether content changed since last render.
func (b *ProgressBuilder) IsDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dirty
}
