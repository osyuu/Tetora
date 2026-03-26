// Package telegram provides configuration types for Telegram bot integration.
package telegram

// Config holds configuration for the Telegram bot integration.
type Config struct {
	Enabled     bool   `json:"enabled,omitempty"`
	BotToken    string `json:"botToken,omitempty"`    // Telegram bot token ($ENV_VAR)
	ChatID      int64  `json:"chatID,omitempty"`      // allowed chat ID (0 = unrestricted)
	PollTimeout int    `json:"pollTimeout,omitempty"` // long-poll timeout in seconds (default 30)
	WebhookMode bool   `json:"webhookMode,omitempty"` // use webhook instead of polling
	WebhookPath string `json:"webhookPath,omitempty"` // webhook URL path
}

// PollTimeoutOrDefault returns the poll timeout or default 30 seconds.
func (c Config) PollTimeoutOrDefault() int {
	if c.PollTimeout > 0 {
		return c.PollTimeout
	}
	return 30
}
