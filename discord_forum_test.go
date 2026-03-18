package main

// --- P14.4: Discord Forum Task Board Tests ---

import (
	"encoding/json"
	"strings"
	"testing"
	"tetora/internal/discord"
)

// --- Valid Forum Statuses ---

func TestValidForumStatuses(t *testing.T) {
	statuses := discord.ValidForumStatuses()
	if len(statuses) != 5 {
		t.Errorf("expected 5 statuses, got %d", len(statuses))
	}

	expected := []string{"backlog", "todo", "doing", "review", "done"}
	for i, s := range expected {
		if statuses[i] != s {
			t.Errorf("status[%d] = %q, want %q", i, statuses[i], s)
		}
	}
}

func TestIsValidForumStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"backlog", true},
		{"todo", true},
		{"doing", true},
		{"review", true},
		{"done", true},
		{"BACKLOG", false}, // case-sensitive
		{"unknown", false},
		{"", false},
		{"doing ", false}, // trailing space
	}
	for _, tt := range tests {
		got := discord.IsValidForumStatus(tt.status)
		if got != tt.expected {
			t.Errorf("discord.IsValidForumStatus(%q) = %v, want %v", tt.status, got, tt.expected)
		}
	}
}

// --- Status Constants ---

func TestForumStatusConstants(t *testing.T) {
	if discord.ForumStatusBacklog != "backlog" {
		t.Errorf("expected 'backlog', got %q", discord.ForumStatusBacklog)
	}
	if discord.ForumStatusTodo != "todo" {
		t.Errorf("expected 'todo', got %q", discord.ForumStatusTodo)
	}
	if discord.ForumStatusDoing != "doing" {
		t.Errorf("expected 'doing', got %q", discord.ForumStatusDoing)
	}
	if discord.ForumStatusReview != "review" {
		t.Errorf("expected 'review', got %q", discord.ForumStatusReview)
	}
	if discord.ForumStatusDone != "done" {
		t.Errorf("expected 'done', got %q", discord.ForumStatusDone)
	}
}

// --- Forum Board Creation ---

func TestNewDiscordForumBoard(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
		Tags: map[string]string{
			"backlog": "TAG1",
			"doing":   "TAG2",
			"done":    "TAG3",
		},
	}
	fb := newDiscordForumBoard(nil, cfg)
	if fb == nil {
		t.Fatal("expected non-nil forum board")
	}
}

// --- IsConfigured ---

func TestForumBoard_IsConfigured(t *testing.T) {
	tests := []struct {
		enabled   bool
		channelID string
		expected  bool
	}{
		{true, "F123", true},
		{true, "", false},
		{false, "F123", false},
		{false, "", false},
	}
	for _, tt := range tests {
		fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
			Enabled:        tt.enabled,
			ForumChannelID: tt.channelID,
		})
		got := fb.IsConfigured()
		if got != tt.expected {
			t.Errorf("IsConfigured(enabled=%v, channelID=%q) = %v, want %v",
				tt.enabled, tt.channelID, got, tt.expected)
		}
	}
}

// --- Config Validation ---

func TestValidateForumBoardConfig(t *testing.T) {
	// Valid config — no warnings.
	cfg := DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
		Tags: map[string]string{
			"backlog": "TAG1",
			"done":    "TAG2",
		},
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestValidateForumBoardConfig_MissingChannelID(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Enabled: true,
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "forumChannelId is empty") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}

func TestValidateForumBoardConfig_UnknownStatus(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Tags: map[string]string{
			"invalid_status": "TAG1",
		},
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown status") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown status warning, got %v", warnings)
	}
}

func TestValidateForumBoardConfig_EmptyTagID(t *testing.T) {
	cfg := DiscordForumBoardConfig{
		Tags: map[string]string{
			"doing": "",
		},
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "empty tag ID") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected empty tag ID warning, got %v", warnings)
	}
}

func TestValidateForumBoardConfig_Disabled(t *testing.T) {
	// Disabled config should not warn about missing channel ID.
	cfg := DiscordForumBoardConfig{
		Enabled: false,
	}
	warnings := discord.ValidateForumBoardConfig(cfg)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for disabled config, got %v", warnings)
	}
}

// --- Config Parsing ---

func TestDiscordForumBoardConfigParse(t *testing.T) {
	raw := `{"enabled":true,"forumChannelId":"F999","tags":{"backlog":"T1","done":"T2"}}`
	var cfg DiscordForumBoardConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.ForumChannelID != "F999" {
		t.Errorf("expected F999, got %q", cfg.ForumChannelID)
	}
	if cfg.Tags == nil {
		t.Fatal("expected tags map")
	}
	if cfg.Tags["backlog"] != "T1" {
		t.Errorf("expected T1 for backlog, got %q", cfg.Tags["backlog"])
	}
}

// --- Assign Command ---

func TestHandleAssignCommand_EmptyRole(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	msg := fb.HandleAssignCommand("T1", "G1", "")
	if !strings.Contains(msg, "Usage:") {
		t.Errorf("expected usage message for empty role, got %q", msg)
	}
}

// --- Status Command ---

func TestHandleStatusCommand_EmptyStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	msg := fb.HandleStatusCommand("T1", "")
	if !strings.Contains(msg, "Usage:") {
		t.Errorf("expected usage message for empty status, got %q", msg)
	}
}

func TestHandleStatusCommand_InvalidStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	msg := fb.HandleStatusCommand("T1", "invalid")
	if !strings.Contains(msg, "Invalid status") {
		t.Errorf("expected invalid status message, got %q", msg)
	}
}

// --- CreateThread Validation ---

func TestCreateThread_NoChannelID(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{Enabled: true})
	_, err := fb.CreateThread("Title", "Body", "backlog")
	if err == nil {
		t.Error("expected error for missing forum channel ID")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' error, got %v", err)
	}
}

func TestCreateThread_EmptyTitle(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
	})
	_, err := fb.CreateThread("", "Body", "backlog")
	if err == nil {
		t.Error("expected error for empty title")
	}
	if !strings.Contains(err.Error(), "title is required") {
		t.Errorf("expected 'title is required' error, got %v", err)
	}
}

func TestCreateThread_InvalidStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
	})
	_, err := fb.CreateThread("Title", "Body", "invalid_status")
	if err == nil {
		t.Error("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("expected 'invalid status' error, got %v", err)
	}
}

func TestCreateThread_DefaultStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{
		Enabled:        true,
		ForumChannelID: "F123",
	})
	// Will fail at API call (nil client), but should pass validation.
	_, err := fb.CreateThread("Title", "Body", "")
	// Will fail because client is nil, but error should be about API, not validation.
	if err != nil && strings.Contains(err.Error(), "invalid status") {
		t.Error("empty status should default to backlog, not be rejected")
	}
}

// --- SetStatus Validation ---

func TestSetStatus_EmptyThreadID(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.SetStatus("", "done")
	if err == nil {
		t.Error("expected error for empty thread ID")
	}
}

func TestSetStatus_InvalidStatus(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.SetStatus("T123", "invalid")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

// --- HandleAssign Validation ---

func TestHandleAssign_EmptyThreadID(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.HandleAssign("", "G1", "ruri")
	if err == nil {
		t.Error("expected error for empty thread ID")
	}
}

func TestHandleAssign_EmptyRole(t *testing.T) {
	fb := newDiscordForumBoard(nil, DiscordForumBoardConfig{})
	err := fb.HandleAssign("T123", "G1", "")
	if err == nil {
		t.Error("expected error for empty role")
	}
}
