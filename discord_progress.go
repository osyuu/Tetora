package main

// discord_progress.go — progress updater loop (needs root SSE types).
// Type alias + constructor in wire_discord.go.

import (
	"time"

	"tetora/internal/log"
)

// runDiscordProgressUpdater subscribes to task SSE events and updates a Discord progress message.
func (db *DiscordBot) runDiscordProgressUpdater(
	channelID, progressMsgID, taskID, sessionID string,
	broker *sseBroker,
	stopCh <-chan struct{},
	builder *discordProgressBuilder,
	components []discordComponent,
) {
	eventCh, unsub := broker.Subscribe(taskID)
	defer unsub()

	log.Debug("discord progress updater started", "taskID", taskID, "sessionID", sessionID)

	var sessionEventCh chan SSEEvent
	if sessionID != "" && sessionID != taskID {
		ch, u := broker.Subscribe(sessionID)
		sessionEventCh = ch
		defer u()
		log.Debug("discord progress updater subscribed to session", "sessionID", sessionID)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastEdit time.Time

	tryEdit := func() {
		if builder.IsDirty() && time.Since(lastEdit) >= 1500*time.Millisecond {
			content := builder.Render()
			log.Debug("discord progress edit", "contentLen", len(content), "taskID", taskID)
			if err := db.editMessageWithComponents(channelID, progressMsgID, content, components); err != nil {
				log.Warn("discord progress edit failed", "error", err)
			}
			db.sendTyping(channelID)
			lastEdit = time.Now()
		}
	}

	handleEvent := func(ev SSEEvent) (done bool) {
		switch ev.Type {
		case SSEToolCall:
			if data, ok := ev.Data.(map[string]any); ok {
				if name, _ := data["name"].(string); name != "" {
					builder.AddToolCall(name)
					tryEdit()
				}
			}
		case SSEOutputChunk:
			if data, ok := ev.Data.(map[string]any); ok {
				if chunk, _ := data["chunk"].(string); chunk != "" {
					log.Debug("discord progress got chunk", "len", len(chunk), "taskID", taskID)
					if replace, _ := data["replace"].(bool); replace {
						builder.ReplaceText(chunk)
					} else {
						builder.AddText(chunk)
					}
					tryEdit()
				}
			}
		case SSECompleted, SSEError:
			log.Debug("discord progress completed/error event", "type", ev.Type, "taskID", taskID)
			return true
		}
		return false
	}

	for {
		select {
		case <-stopCh:
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			if handleEvent(ev) {
				return
			}
		case ev, ok := <-sessionEventCh:
			if !ok {
				sessionEventCh = nil
			} else {
				handleEvent(ev)
			}
		case <-ticker.C:
			tryEdit()
		}
	}
}
