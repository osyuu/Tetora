package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/log"
)

// Voice-specific gateway opcodes.
const (
	OpVoiceStateUpdate  = 4
	OpVoiceServerUpdate = 0
)

// Voice gateway intent.
const IntentGuildVoiceStates = 1 << 7

// VoiceStateUpdatePayload is sent to the gateway to join/leave voice channels.
type VoiceStateUpdatePayload struct {
	GuildID   string  `json:"guild_id"`
	ChannelID *string `json:"channel_id"`
	SelfMute  bool    `json:"self_mute"`
	SelfDeaf  bool    `json:"self_deaf"`
}

// VoiceServerUpdateData is received from gateway when joining a voice channel.
type VoiceServerUpdateData struct {
	Token    string `json:"token"`
	GuildID  string `json:"guild_id"`
	Endpoint string `json:"endpoint"`
}

// VoiceStateUpdateData is received from gateway for voice state changes.
type VoiceStateUpdateData struct {
	GuildID   string `json:"guild_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id,omitempty"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

// VoiceDeps holds external dependencies for the voice manager.
type VoiceDeps struct {
	// SendGateway sends a payload to the Discord gateway.
	SendGateway func(payload GatewayPayload) error
	// BotUserID is the bot's user ID for filtering events.
	BotUserID string
	// VoiceEnabled is whether voice is enabled.
	VoiceEnabled bool
	// AutoJoin channels.
	AutoJoin []config.DiscordVoiceAutoJoin
	// TTS config.
	TTS config.DiscordVoiceTTSConfig
}

// VoiceManager manages voice channel connections and state.
type VoiceManager struct {
	deps VoiceDeps
	mu   sync.RWMutex

	currentGuildID   string
	currentChannelID string
	voiceToken       string
	voiceEndpoint    string
	voiceSessionID   string
	connected        bool
}

// NewVoiceManager creates a new voice manager.
func NewVoiceManager(deps VoiceDeps) *VoiceManager {
	return &VoiceManager{deps: deps}
}

// JoinVoiceChannel sends a voice state update to join a channel.
func (vm *VoiceManager) JoinVoiceChannel(guildID, channelID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	payload := VoiceStateUpdatePayload{
		GuildID:   guildID,
		ChannelID: &channelID,
		SelfMute:  false,
		SelfDeaf:  false,
	}
	d, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal voice state update: %w", err)
	}

	if err := vm.deps.SendGateway(GatewayPayload{Op: OpVoiceStateUpdate, D: d}); err != nil {
		return fmt.Errorf("send voice state update: %w", err)
	}

	vm.currentGuildID = guildID
	vm.currentChannelID = channelID
	log.Info("discord voice: requested join", "guild", guildID, "channel", channelID)
	return nil
}

// LeaveVoiceChannel disconnects from the current voice channel.
func (vm *VoiceManager) LeaveVoiceChannel() error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if vm.currentGuildID == "" {
		return fmt.Errorf("not connected to any voice channel")
	}

	payload := VoiceStateUpdatePayload{
		GuildID:   vm.currentGuildID,
		ChannelID: nil,
		SelfMute:  false,
		SelfDeaf:  false,
	}
	d, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal voice state update: %w", err)
	}

	if err := vm.deps.SendGateway(GatewayPayload{Op: OpVoiceStateUpdate, D: d}); err != nil {
		return fmt.Errorf("send voice state update: %w", err)
	}

	guildID := vm.currentGuildID
	channelID := vm.currentChannelID
	vm.currentGuildID = ""
	vm.currentChannelID = ""
	vm.connected = false
	log.Info("discord voice: requested leave", "guild", guildID, "channel", channelID)
	return nil
}

// GetStatus returns current voice connection status.
func (vm *VoiceManager) GetStatus() map[string]interface{} {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return map[string]interface{}{
		"connected": vm.connected,
		"guildId":   vm.currentGuildID,
		"channelId": vm.currentChannelID,
	}
}

// HandleVoiceServerUpdate processes VOICE_SERVER_UPDATE events.
func (vm *VoiceManager) HandleVoiceServerUpdate(data VoiceServerUpdateData) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	vm.voiceToken = data.Token
	vm.voiceEndpoint = data.Endpoint
	vm.currentGuildID = data.GuildID
	vm.connected = true

	log.Info("discord voice: server update received", "guild", data.GuildID, "endpoint", data.Endpoint)
}

// HandleVoiceStateUpdate processes VOICE_STATE_UPDATE events.
func (vm *VoiceManager) HandleVoiceStateUpdate(data VoiceStateUpdateData) {
	if data.UserID != vm.deps.BotUserID {
		return
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	vm.voiceSessionID = data.SessionID

	if data.ChannelID == "" {
		vm.connected = false
		vm.currentChannelID = ""
		log.Info("discord voice: disconnected from voice channel")
		return
	}

	vm.currentChannelID = data.ChannelID
	log.Info("discord voice: state updated", "channel", data.ChannelID, "session", data.SessionID)
}

// AutoJoinChannels joins configured auto-join voice channels.
func (vm *VoiceManager) AutoJoinChannels() {
	if !vm.deps.VoiceEnabled {
		return
	}
	for _, aj := range vm.deps.AutoJoin {
		if aj.GuildID == "" || aj.ChannelID == "" {
			continue
		}
		time.Sleep(2 * time.Second)
		if err := vm.JoinVoiceChannel(aj.GuildID, aj.ChannelID); err != nil {
			log.Warn("discord voice: auto-join failed", "guild", aj.GuildID, "channel", aj.ChannelID, "error", err)
		} else {
			log.Info("discord voice: auto-joined", "guild", aj.GuildID, "channel", aj.ChannelID)
		}
	}
}

// PlayTTS generates TTS audio and plays it (placeholder).
func (vm *VoiceManager) PlayTTS(ctx context.Context, text string) error {
	vm.mu.RLock()
	connected := vm.connected
	vm.mu.RUnlock()

	if !connected {
		return fmt.Errorf("not connected to voice channel")
	}

	log.Info("discord voice: TTS playback requested (placeholder)",
		"text_length", len(text),
		"provider", vm.deps.TTS.Provider,
		"voice", vm.deps.TTS.Voice)
	return nil
}
