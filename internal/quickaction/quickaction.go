package quickaction

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// --- Types ---

type Action struct {
	Name           string            `json:"name"`
	Label          string            `json:"label"`
	Icon           string            `json:"icon,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	Prompt         string            `json:"prompt,omitempty"`          // static prompt
	PromptTemplate string            `json:"promptTemplate,omitempty"` // Go template
	Params         map[string]Param  `json:"params,omitempty"`
	Shortcut       string            `json:"shortcut,omitempty"` // single key shortcut
}

type Param struct {
	Type    string `json:"type"`              // "string", "number", "boolean"
	Default any    `json:"default,omitempty"`
	Label   string `json:"label,omitempty"`
}

type Engine struct {
	actions      []Action
	defaultAgent string
}

// --- Core Functions ---

// NewEngine creates a new Engine with the given actions and default agent.
func NewEngine(actions []Action, defaultAgent string) *Engine {
	return &Engine{
		actions:      actions,
		defaultAgent: defaultAgent,
	}
}

// List returns all configured quick actions.
func (e *Engine) List() []Action {
	if e.actions == nil {
		return []Action{}
	}
	return e.actions
}

// Get finds a quick action by name.
func (e *Engine) Get(name string) (*Action, error) {
	for i := range e.actions {
		if e.actions[i].Name == name {
			return &e.actions[i], nil
		}
	}
	return nil, fmt.Errorf("quick action not found: %s", name)
}

// BuildPrompt renders the prompt for a quick action with provided params.
// Returns (prompt, role, error).
func (e *Engine) BuildPrompt(name string, params map[string]any) (string, string, error) {
	action, err := e.Get(name)
	if err != nil {
		return "", "", err
	}

	// Merge params with defaults.
	mergedParams := make(map[string]any)
	for k, param := range action.Params {
		if param.Default != nil {
			mergedParams[k] = param.Default
		}
	}
	for k, v := range params {
		mergedParams[k] = v
	}

	// Build prompt.
	var prompt string
	if action.PromptTemplate != "" {
		// Render template.
		tmpl, err := template.New(name).Parse(action.PromptTemplate)
		if err != nil {
			return "", "", fmt.Errorf("parse template: %w", err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, mergedParams); err != nil {
			return "", "", fmt.Errorf("execute template: %w", err)
		}
		prompt = buf.String()
	} else if action.Prompt != "" {
		// Static prompt.
		prompt = action.Prompt
	} else {
		return "", "", fmt.Errorf("no prompt or template defined for action: %s", name)
	}

	role := action.Agent
	if role == "" {
		role = e.defaultAgent
	}

	return prompt, role, nil
}

// Search performs fuzzy search on quick actions.
// Matches name, label, or shortcut (case-insensitive substring match).
func (e *Engine) Search(query string) []Action {
	if query == "" {
		return e.List()
	}

	query = strings.ToLower(query)
	var results []Action

	for _, action := range e.actions {
		if strings.Contains(strings.ToLower(action.Name), query) ||
			strings.Contains(strings.ToLower(action.Label), query) ||
			strings.Contains(strings.ToLower(action.Shortcut), query) {
			results = append(results, action)
		}
	}

	return results
}
