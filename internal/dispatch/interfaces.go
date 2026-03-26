package dispatch

import (
	"context"
	"encoding/json"
)

// ChannelNotifier sends typing indicators and status updates to a messaging channel.
type ChannelNotifier interface {
	SendTyping(ctx context.Context) error
	SendStatus(ctx context.Context, msg string) error
}

// ApprovalGate requests user confirmation before executing a tool.
type ApprovalGate interface {
	// RequestApproval blocks until user approves/rejects or context expires.
	RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error)
	// AutoApprove adds a tool to the runtime auto-approved list.
	AutoApprove(toolName string)
	// IsAutoApproved checks if a tool has been auto-approved.
	IsAutoApproved(toolName string) bool
}

// ApprovalRequest describes a tool call pending user approval.
type ApprovalRequest struct {
	ID      string          `json:"id"`
	Tool    string          `json:"tool"`
	Input   json.RawMessage `json:"input"`
	Summary string          `json:"summary"` // human-readable description
	TaskID  string          `json:"taskId"`
	Role    string          `json:"role"`
}
