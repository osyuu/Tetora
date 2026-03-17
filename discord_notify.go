package main

// discord_notify.go — thin wrapper around internal/discord.TaskNotifier.

import "tetora/internal/discord"

type discordTaskNotifier = discord.TaskNotifier

func newDiscordTaskNotifier(bot *DiscordBot, channelID string) *discordTaskNotifier {
	return discord.NewTaskNotifier(bot.api, channelID)
}
