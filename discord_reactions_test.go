package main

// --- P14.3: Lifecycle Reactions Tests ---

import (
	"encoding/json"
	"strings"
	"testing"

	"tetora/internal/discord"
)

// --- Default Emoji Map ---

func TestDefaultReactionEmojis(t *testing.T) {
	emojis := discord.DefaultReactionEmojis()

	// Must have all 5 phases.
	phases := discord.ValidReactionPhases()
	for _, phase := range phases {
		if emoji, ok := emojis[phase]; !ok || emoji == "" {
			t.Errorf("missing default emoji for phase %q", phase)
		}
	}

	// Verify specific defaults.
	if emojis[discord.ReactionPhaseQueued] != "\u23F3" {
		t.Errorf("expected hourglass for queued, got %q", emojis[discord.ReactionPhaseQueued])
	}
	if emojis[discord.ReactionPhaseDone] != "\u2705" {
		t.Errorf("expected check mark for done, got %q", emojis[discord.ReactionPhaseDone])
	}
	if emojis[discord.ReactionPhaseError] != "\u274C" {
		t.Errorf("expected cross mark for error, got %q", emojis[discord.ReactionPhaseError])
	}
}

// --- Reaction Manager Creation ---

func TestNewDiscordReactionManager(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	if rm == nil {
		t.Fatal("expected non-nil reaction manager")
	}
}

func TestNewDiscordReactionManager_WithOverrides(t *testing.T) {
	overrides := map[string]string{
		"queued": "\U0001F4E5", // inbox tray
	}
	rm := discord.NewReactionManager(nil, overrides)
	if rm.EmojiForPhase("queued") != "\U0001F4E5" {
		t.Errorf("expected override emoji, got %q", rm.EmojiForPhase("queued"))
	}
}

// --- Emoji For Phase ---

func TestEmojiForPhase_Default(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	tests := []struct {
		phase    string
		expected string
	}{
		{discord.ReactionPhaseQueued, "\u23F3"},
		{discord.ReactionPhaseThinking, "\U0001F914"},
		{discord.ReactionPhaseTool, "\U0001F527"},
		{discord.ReactionPhaseDone, "\u2705"},
		{discord.ReactionPhaseError, "\u274C"},
	}
	for _, tt := range tests {
		got := rm.EmojiForPhase(tt.phase)
		if got != tt.expected {
			t.Errorf("EmojiForPhase(%q) = %q, want %q", tt.phase, got, tt.expected)
		}
	}
}

func TestEmojiForPhase_Override(t *testing.T) {
	overrides := map[string]string{
		"queued": "\U0001F4E5", // inbox tray
		"done":   "\U0001F389", // party popper
	}
	rm := discord.NewReactionManager(nil, overrides)

	if got := rm.EmojiForPhase("queued"); got != "\U0001F4E5" {
		t.Errorf("expected override for queued, got %q", got)
	}
	if got := rm.EmojiForPhase("done"); got != "\U0001F389" {
		t.Errorf("expected override for done, got %q", got)
	}

	// Non-overridden phases fall back to default.
	if got := rm.EmojiForPhase("thinking"); got != "\U0001F914" {
		t.Errorf("expected default for thinking, got %q", got)
	}
}

func TestEmojiForPhase_UnknownPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	got := rm.EmojiForPhase("unknown_phase")
	if got != "" {
		t.Errorf("expected empty for unknown phase, got %q", got)
	}
}

func TestEmojiForPhase_EmptyOverride(t *testing.T) {
	overrides := map[string]string{
		"queued": "",
	}
	rm := discord.NewReactionManager(nil, overrides)
	got := rm.EmojiForPhase("queued")
	if got != "\u23F3" {
		t.Errorf("expected default for empty override, got %q", got)
	}
}

// --- Phase Tracking ---

func TestSetPhase_TracksCurrentPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)

	got := rm.GetCurrentPhase("C1", "M1")
	if got != discord.ReactionPhaseQueued {
		t.Errorf("expected phase %q, got %q", discord.ReactionPhaseQueued, got)
	}
}

func TestSetPhase_TransitionUpdatesPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)

	got := rm.GetCurrentPhase("C1", "M1")
	if got != discord.ReactionPhaseThinking {
		t.Errorf("expected phase %q after transition, got %q", discord.ReactionPhaseThinking, got)
	}
}

func TestSetPhase_IgnoresEmptyArgs(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", "")

	if got := rm.GetCurrentPhase("", "M1"); got != "" {
		t.Errorf("expected empty for empty channelID, got %q", got)
	}
	if got := rm.GetCurrentPhase("C1", ""); got != "" {
		t.Errorf("expected empty for empty messageID, got %q", got)
	}
}

func TestSetPhase_UnknownPhaseIgnored(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", "nonexistent_phase")
	got := rm.GetCurrentPhase("C1", "M1")
	if got != "" {
		t.Errorf("expected empty for unknown phase, got %q", got)
	}
}

// --- Clear Phase ---

func TestClearPhase(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.ClearPhase("C1", "M1")

	got := rm.GetCurrentPhase("C1", "M1")
	if got != "" {
		t.Errorf("expected empty after ClearPhase, got %q", got)
	}
}

func TestClearPhase_NonExistent(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.ClearPhase("C999", "M999")
}

// --- Convenience Methods ---

func TestReactQueued(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.ReactQueued("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseQueued {
		t.Errorf("expected queued, got %q", got)
	}
}

func TestReactDone_ClearsTracking(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	rm.ReactDone("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after ReactDone, got %q", got)
	}
}

func TestReactError_ClearsTracking(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	rm.ReactError("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after ReactError, got %q", got)
	}
}

// --- Full Lifecycle ---

func TestReactionLifecycle_FullTransition(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseQueued {
		t.Fatalf("step 1: expected queued, got %q", got)
	}

	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseThinking {
		t.Fatalf("step 2: expected thinking, got %q", got)
	}

	rm.SetPhase("C1", "M1", discord.ReactionPhaseTool)
	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseTool {
		t.Fatalf("step 3: expected tool, got %q", got)
	}

	rm.ReactDone("C1", "M1")
	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Fatalf("step 4: expected empty after done, got %q", got)
	}
}

func TestReactionLifecycle_ErrorPath(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseThinking)
	rm.ReactError("C1", "M1")

	if got := rm.GetCurrentPhase("C1", "M1"); got != "" {
		t.Errorf("expected empty after error, got %q", got)
	}
}

// --- Multiple Messages ---

func TestReactionManager_MultipleMessages(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M2", discord.ReactionPhaseThinking)
	rm.SetPhase("C2", "M3", discord.ReactionPhaseTool)

	if got := rm.GetCurrentPhase("C1", "M1"); got != discord.ReactionPhaseQueued {
		t.Errorf("M1: expected queued, got %q", got)
	}
	if got := rm.GetCurrentPhase("C1", "M2"); got != discord.ReactionPhaseThinking {
		t.Errorf("M2: expected thinking, got %q", got)
	}
	if got := rm.GetCurrentPhase("C2", "M3"); got != discord.ReactionPhaseTool {
		t.Errorf("M3: expected tool, got %q", got)
	}
}

// --- Valid Phases ---

func TestValidReactionPhases(t *testing.T) {
	phases := discord.ValidReactionPhases()
	if len(phases) != 5 {
		t.Errorf("expected 5 phases, got %d", len(phases))
	}

	expected := []string{"queued", "thinking", "tool", "done", "error"}
	for i, p := range expected {
		if phases[i] != p {
			t.Errorf("phase[%d] = %q, want %q", i, phases[i], p)
		}
	}
}

// --- Config Parsing ---

func TestDiscordReactionsConfigParse(t *testing.T) {
	raw := `{"enabled":true,"emojis":{"queued":"\u2b50","done":"\ud83c\udf89"}}`
	var cfg DiscordReactionsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.Emojis == nil {
		t.Fatal("expected emojis map")
	}
	if cfg.Emojis["queued"] == "" {
		t.Error("expected queued emoji override")
	}
}

func TestDiscordReactionsConfigParse_Disabled(t *testing.T) {
	raw := `{}`
	var cfg DiscordReactionsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
	if cfg.Emojis != nil {
		t.Error("expected nil emojis by default")
	}
}

// --- Phase Constants ---

func TestReactionPhaseConstants(t *testing.T) {
	if discord.ReactionPhaseQueued != "queued" {
		t.Errorf("expected 'queued', got %q", discord.ReactionPhaseQueued)
	}
	if discord.ReactionPhaseThinking != "thinking" {
		t.Errorf("expected 'thinking', got %q", discord.ReactionPhaseThinking)
	}
	if discord.ReactionPhaseTool != "tool" {
		t.Errorf("expected 'tool', got %q", discord.ReactionPhaseTool)
	}
	if discord.ReactionPhaseDone != "done" {
		t.Errorf("expected 'done', got %q", discord.ReactionPhaseDone)
	}
	if discord.ReactionPhaseError != "error" {
		t.Errorf("expected 'error', got %q", discord.ReactionPhaseError)
	}
}

// --- Same Phase No-Op ---

func TestSetPhase_SamePhaseNoRemove(t *testing.T) {
	rm := discord.NewReactionManager(nil, nil)

	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)
	rm.SetPhase("C1", "M1", discord.ReactionPhaseQueued)

	got := rm.GetCurrentPhase("C1", "M1")
	if got != discord.ReactionPhaseQueued {
		t.Errorf("expected queued after re-set, got %q", got)
	}
}

// --- Helper: use strings.Contains for substring checks ---

func TestReactionKeyContainsSeparator(t *testing.T) {
	// reactionKey is unexported in internal/discord, test via SetPhase+GetCurrentPhase
	rm := discord.NewReactionManager(nil, nil)
	rm.SetPhase("C123", "M456", discord.ReactionPhaseQueued)
	if got := rm.GetCurrentPhase("C123", "M456"); got != discord.ReactionPhaseQueued {
		t.Error("expected phase tracking to work with specific channel/message IDs")
	}
	_ = strings.Contains("C123:M456", ":")
}
