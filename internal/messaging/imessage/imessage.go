// Package imessage provides configuration types for BlueBubbles iMessage integration.
package imessage

// Config holds configuration for BlueBubbles iMessage integration.
type Config struct {
	Enabled      bool     `json:"enabled,omitempty"`
	ServerURL    string   `json:"serverUrl,omitempty"`    // BlueBubbles server URL, e.g. "http://localhost:1234"
	Password     string   `json:"password,omitempty"`     // BlueBubbles server password ($ENV_VAR)
	AllowedChats []string `json:"allowedChats,omitempty"` // allowed chat GUIDs or phone numbers
	WebhookPath  string   `json:"webhookPath,omitempty"`  // default "/api/imessage/webhook"
	DefaultAgent string   `json:"defaultAgent,omitempty"` // agent role for iMessage messages
}

// WebhookPathOrDefault returns the configured webhook path or default.
func (c Config) WebhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/imessage/webhook"
}
