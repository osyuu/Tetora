// Package gchat provides configuration types for Google Chat integration.
package gchat

// Config holds configuration for Google Chat integration.
type Config struct {
	Enabled           bool   `json:"enabled,omitempty"`
	ServiceAccountKey string `json:"serviceAccountKey,omitempty"` // JSON key file path or $ENV_VAR
	WebhookPath       string `json:"webhookPath,omitempty"`       // default "/api/gchat/webhook"
	DefaultAgent      string `json:"defaultAgent,omitempty"`      // agent role for Google Chat messages
}

// WebhookPathOrDefault returns the configured webhook path or default.
func (c Config) WebhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/gchat/webhook"
}
