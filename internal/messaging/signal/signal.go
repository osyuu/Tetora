// Package signal provides configuration types for Signal (signal-cli-rest-api) integration.
package signal

// Config holds configuration for signal-cli-rest-api integration.
type Config struct {
	Enabled      bool   `json:"enabled,omitempty"`
	APIBaseURL   string `json:"apiBaseURL,omitempty"`   // default "http://localhost:8080"
	PhoneNumber  string `json:"phoneNumber,omitempty"`  // +1234567890 ($ENV_VAR)
	WebhookPath  string `json:"webhookPath,omitempty"`  // default "/api/signal/webhook"
	DefaultAgent string `json:"defaultAgent,omitempty"` // agent role for Signal messages
	PollingMode  bool   `json:"pollingMode,omitempty"`  // enable polling instead of webhook
	PollInterval int    `json:"pollInterval,omitempty"` // polling interval in seconds (default 5)
}

// WebhookPathOrDefault returns the configured webhook path or default.
func (c Config) WebhookPathOrDefault() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "/api/signal/webhook"
}

// APIBaseURLOrDefault returns the configured API base URL or default.
func (c Config) APIBaseURLOrDefault() string {
	if c.APIBaseURL != "" {
		return c.APIBaseURL
	}
	return "http://localhost:8080"
}

// PollIntervalOrDefault returns the polling interval in seconds (default 5).
func (c Config) PollIntervalOrDefault() int {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return 5
}
