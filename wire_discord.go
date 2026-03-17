package main

// wire_discord.go — type/constant/function aliases + constructors for internal/discord.

import "tetora/internal/discord"

// --- Type Aliases ---

type discordUser = discord.User
type discordAttachment = discord.Attachment
type discordMessage = discord.Message
type discordEmbed = discord.Embed
type discordEmbedField = discord.EmbedField
type discordEmbedFooter = discord.EmbedFooter
type discordMessageRef = discord.MessageRef
type discordComponent = discord.Component
type discordSelectOption = discord.SelectOption
type discordModalData = discord.ModalData
type discordInteraction = discord.Interaction
type discordInteractionData = discord.InteractionData
type discordInteractionResponse = discord.InteractionResponse
type discordInteractionResponseData = discord.InteractionResponseData
type gatewayPayload = discord.GatewayPayload
type helloData = discord.HelloData
type identifyData = discord.IdentifyData
type resumePayload = discord.ResumePayload
type readyData = discord.ReadyData
type discordReactionManager = discord.ReactionManager
type discordTaskNotifier = discord.TaskNotifier
type discordForumBoard = discord.ForumBoard
type discordProgressBuilder = discord.ProgressBuilder

// --- Constant Aliases ---

const (
	discordGatewayURL = discord.GatewayURL
	discordAPIBase    = discord.APIBase

	opDispatch       = discord.OpDispatch
	opHeartbeat      = discord.OpHeartbeat
	opIdentify       = discord.OpIdentify
	opResume         = discord.OpResume
	opReconnect      = discord.OpReconnect
	opInvalidSession = discord.OpInvalidSession
	opHello          = discord.OpHello
	opHeartbeatAck   = discord.OpHeartbeatAck

	intentGuildMessages  = discord.IntentGuildMessages
	intentDirectMessages = discord.IntentDirectMessages
	intentMessageContent = discord.IntentMessageContent

	interactionTypePing             = discord.InteractionTypePing
	interactionTypeApplicationCmd   = discord.InteractionTypeApplicationCmd
	interactionTypeMessageComponent = discord.InteractionTypeMessageComponent
	interactionTypeModalSubmit      = discord.InteractionTypeModalSubmit

	componentTypeActionRow     = discord.ComponentTypeActionRow
	componentTypeButton        = discord.ComponentTypeButton
	componentTypeStringSelect  = discord.ComponentTypeStringSelect
	componentTypeTextInput     = discord.ComponentTypeTextInput
	componentTypeUserSelect    = discord.ComponentTypeUserSelect
	componentTypeRoleSelect    = discord.ComponentTypeRoleSelect
	componentTypeMentionSelect = discord.ComponentTypeMentionSelect
	componentTypeChannelSelect = discord.ComponentTypeChannelSelect

	buttonStylePrimary   = discord.ButtonStylePrimary
	buttonStyleSecondary = discord.ButtonStyleSecondary
	buttonStyleSuccess   = discord.ButtonStyleSuccess
	buttonStyleDanger    = discord.ButtonStyleDanger
	buttonStyleLink      = discord.ButtonStyleLink

	interactionResponsePong           = discord.InteractionResponsePong
	interactionResponseMessage        = discord.InteractionResponseMessage
	interactionResponseDeferredUpdate = discord.InteractionResponseDeferredUpdate
	interactionResponseUpdateMessage  = discord.InteractionResponseUpdateMessage
	interactionResponseModal          = discord.InteractionResponseModal

	textInputStyleShort     = discord.TextInputStyleShort
	textInputStyleParagraph = discord.TextInputStyleParagraph

	// Reaction phases.
	reactionPhaseQueued   = discord.ReactionPhaseQueued
	reactionPhaseThinking = discord.ReactionPhaseThinking
	reactionPhaseTool     = discord.ReactionPhaseTool
	reactionPhaseDone     = discord.ReactionPhaseDone
	reactionPhaseError    = discord.ReactionPhaseError

	// Forum statuses.
	forumStatusBacklog = discord.ForumStatusBacklog
	forumStatusTodo    = discord.ForumStatusTodo
	forumStatusDoing   = discord.ForumStatusDoing
	forumStatusReview  = discord.ForumStatusReview
	forumStatusDone    = discord.ForumStatusDone
)

// --- Function Aliases ---

var (
	defaultReactionEmojis    = discord.DefaultReactionEmojis
	validReactionPhases      = discord.ValidReactionPhases
	validForumStatuses       = discord.ValidForumStatuses
	isValidForumStatus       = discord.IsValidForumStatus
	validateForumBoardConfig = discord.ValidateForumBoardConfig
)

// --- Constructors ---

func newDiscordReactionManager(bot *DiscordBot, overrides map[string]string) *discordReactionManager {
	return discord.NewReactionManager(bot.api, overrides)
}

func newDiscordTaskNotifier(bot *DiscordBot, channelID string) *discordTaskNotifier {
	return discord.NewTaskNotifier(bot.api, channelID)
}

func newDiscordProgressBuilder() *discordProgressBuilder {
	return discord.NewProgressBuilder()
}

func newDiscordForumBoard(bot *DiscordBot, cfg DiscordForumBoardConfig) *discordForumBoard {
	var deps discord.ForumBoardDeps
	var client *discord.Client

	if bot != nil {
		client = bot.api

		if bot.cfg != nil {
			deps.ThreadBindingsEnabled = bot.cfg.Discord.ThreadBindings.Enabled

			deps.ValidateAgent = func(name string) bool {
				if bot.cfg.Agents == nil {
					return false
				}
				_, ok := bot.cfg.Agents[name]
				return ok
			}
		}

		deps.AvailableRoles = func() []string {
			return bot.availableRoleNames()
		}

		if bot.threads != nil && bot.cfg != nil {
			deps.BindThread = func(guildID, threadID, role string) string {
				ttl := bot.cfg.Discord.ThreadBindings.ThreadBindingsTTL()
				return bot.threads.bind(guildID, threadID, role, ttl)
			}
		}
	}

	return discord.NewForumBoard(client, cfg, deps)
}

// --- Discord API Request Helper ---

// discordRequest delegates to the api client's RequestRaw method.
func (db *DiscordBot) discordRequest(method, path string, payload any) (int, []byte) {
	if db == nil || db.api == nil {
		return 0, nil
	}
	return db.api.RequestRaw(method, path, payload)
}
