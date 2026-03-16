// Package provider defines the shared types, interfaces, and registry for LLM provider backends.
package provider

import (
	"fmt"
	"strings"
)

// --- Provider Registry ---

// Registry holds initialized provider instances.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds a provider to the registry.
func (r *Registry) Register(name string, p Provider) {
	r.providers[name] = p
}

// Get retrieves a provider by name.
func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", name)
	}
	return p, nil
}

// --- Provider Resolution ---

// HasNativeSession returns true if the provider maintains its own session state.
// For these providers, Tetora should NOT inject conversation history as text —
// the provider already resumes the session natively.
func HasNativeSession(providerName string) bool {
	return providerName == "claude-code" || providerName == "codex" ||
		strings.HasPrefix(providerName, "terminal-")
}
