// Package whatsapp provides configuration types for WhatsApp Cloud API integration.
package whatsapp

// Config holds configuration for WhatsApp Cloud API integration.
type Config struct {
	Enabled       bool   `json:"enabled"`
	PhoneNumberID string `json:"phoneNumberId"` // WhatsApp Business phone number ID
	AccessToken   string `json:"accessToken"`   // Meta access token, supports $ENV_VAR
	VerifyToken   string `json:"verifyToken"`   // Webhook verification token
	AppSecret     string `json:"appSecret,omitempty"`  // For payload signature verification
	APIVersion    string `json:"apiVersion,omitempty"` // default "v21.0"
}

// APIVersion returns the configured API version or default "v21.0".
func (c Config) APIVersion_() string {
	if c.APIVersion != "" {
		return c.APIVersion
	}
	return "v21.0"
}
