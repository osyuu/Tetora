// Package teams provides configuration types for Microsoft Teams Bot Framework integration.
package teams

// Config holds configuration for Microsoft Teams Bot Framework integration.
type Config struct {
	Enabled      bool   `json:"enabled,omitempty"`
	AppID        string `json:"appId,omitempty"`        // Azure AD App ID ($ENV_VAR)
	AppPassword  string `json:"appPassword,omitempty"`  // Azure AD App Secret ($ENV_VAR)
	TenantID     string `json:"tenantId,omitempty"`     // Azure AD Tenant ID ($ENV_VAR)
	DefaultAgent string `json:"defaultAgent,omitempty"` // agent role for Teams messages
}
