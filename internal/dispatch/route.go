package dispatch

import "encoding/json"

// RouteRequest is the input for the routing engine.
type RouteRequest struct {
	Prompt    string `json:"prompt"`
	Source    string `json:"source,omitempty"`    // "telegram", "http", "cli", "slack", "discord"
	UserID    string `json:"userId,omitempty"`    // user ID (telegram, discord, etc.)
	ChannelID string `json:"channelId,omitempty"` // channel/chat ID (slack, telegram group, etc.)
	GuildID   string `json:"guildId,omitempty"`   // guild/server ID (discord)
}

// RouteResult represents the outcome of smart dispatch routing.
type RouteResult struct {
	Agent      string `json:"agent"`            // selected agent
	Method     string `json:"method"`           // "keyword", "llm", "default"
	Confidence string `json:"confidence"`       // "high", "medium", "low"
	Reason     string `json:"reason,omitempty"` // why this agent was selected
}

// UnmarshalJSON implements custom unmarshalling to accept both "role" and "agent" keys.
// This maintains backward compatibility with LLM output that uses "role".
func (r *RouteResult) UnmarshalJSON(data []byte) error {
	type Alias RouteResult
	aux := &struct {
		*Alias
		Role string `json:"role"`
	}{Alias: (*Alias)(r)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if r.Agent == "" && aux.Role != "" {
		r.Agent = aux.Role
	}
	return nil
}

// SmartDispatchResult is the full result of a routed task.
type SmartDispatchResult struct {
	Route    RouteResult `json:"route"`
	Task     TaskResult  `json:"task"`
	ReviewOK *bool       `json:"reviewOk,omitempty"` // nil if no review
	Review   string      `json:"review,omitempty"`   // review comment from coordinator
	Attempts int         `json:"attempts,omitempty"` // number of Dev↔QA loop attempts (0 = no loop)
}
