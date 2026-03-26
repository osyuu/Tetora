// Package matrix provides configuration types for Matrix (Element/Synapse) integration.
package matrix

// Config holds configuration for Matrix (Element/Synapse) integration.
type Config struct {
	Enabled      bool   `json:"enabled,omitempty"`
	Homeserver   string `json:"homeserver,omitempty"`    // e.g. "https://matrix.example.com"
	UserID       string `json:"userId,omitempty"`        // e.g. "@tetora:example.com"
	AccessToken  string `json:"accessToken,omitempty"`   // $ENV_VAR supported
	AutoJoin     bool   `json:"autoJoin,omitempty"`      // auto-join invited rooms
	DefaultAgent string `json:"defaultAgent,omitempty"`  // agent role for Matrix messages
}
