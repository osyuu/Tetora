// Package messaging defines shared interfaces for messaging platform integrations.
package messaging

import "context"

// PresenceSetter is implemented by channel bots that support typing indicators.
type PresenceSetter interface {
	// SetTyping sends a typing indicator to the specified channel reference.
	// channelRef is the channel-specific identifier (chat ID, channel ID, etc.).
	SetTyping(ctx context.Context, channelRef string) error
	// PresenceName returns the channel name (e.g., "telegram", "slack").
	PresenceName() string
}
