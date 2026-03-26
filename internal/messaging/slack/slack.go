// Package slack provides configuration types for Slack bot integration.
package slack

// Config holds configuration for the Slack bot integration.
// Uses the Slack Events API (HTTP push mode) for receiving messages.
type Config struct {
	Enabled        bool   `json:"enabled"`
	BotToken       string `json:"botToken"`                // xoxb-... ($ENV_VAR supported)
	SigningSecret  string `json:"signingSecret"`           // for request verification ($ENV_VAR supported)
	AppToken       string `json:"appToken,omitempty"`      // for Socket Mode (optional, $ENV_VAR)
	DefaultChannel string `json:"defaultChannel,omitempty"` // channel ID for notifications
}
