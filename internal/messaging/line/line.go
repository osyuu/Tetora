// Package line provides configuration types for LINE Messaging API integration.
package line

// Config holds configuration for LINE Messaging API integration.
type Config struct {
	Enabled            bool   `json:"enabled,omitempty"`
	ChannelSecret      string `json:"channelSecret,omitempty"`      // $ENV_VAR supported, for webhook signature
	ChannelAccessToken string `json:"channelAccessToken,omitempty"` // $ENV_VAR supported, for API calls
	WebhookPath        string `json:"webhookPath,omitempty"`        // default "/api/line/webhook"
	DefaultAgent       string `json:"defaultAgent,omitempty"`       // agent role for LINE messages
}

// WebhookPathOrDefault returns the configured webhook path or default "/api/line/webhook".
func (c Config) WebhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/line/webhook"
}
