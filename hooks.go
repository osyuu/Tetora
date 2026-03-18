package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"tetora/internal/discord"
	"tetora/internal/hooks"
	"tetora/internal/log"
)

// --- Type aliases to internal/hooks ---

type HookEvent = hooks.HookEvent
type HookToolInfo = hooks.HookToolInfo
type HookSession = hooks.HookSession
type HookStopInfo = hooks.HookStopInfo
type planGateDecision = hooks.PlanGateDecision
type hookWorkerEvent = hooks.WorkerEvent
type workerOrigin = hooks.WorkerOrigin
type hookWorkerInfo = hooks.WorkerInfo
type cachedPlan = hooks.CachedPlan
type hookReceiver = hooks.Receiver

const hookWorkerEventsMax = hooks.WorkerEventsMax

func newHookReceiver(broker *sseBroker, cfg *Config) *hookReceiver {
	return hooks.NewReceiver(broker, cfg)
}

func isHooksPath(path string) bool {
	return hooks.IsHooksPath(path)
}

// --- Server hook routes registration ---

// registerHookRoutes registers /api/hooks/* endpoints on the given mux.
func (s *Server) registerHookRoutes(mux *http.ServeMux) {
	if s.hookReceiver == nil {
		return
	}
	mux.HandleFunc("/api/hooks/event", s.hookReceiver.HandleEvent)
	mux.HandleFunc("/api/hooks/status", s.hookReceiver.HandleStatus)
	mux.HandleFunc("/api/hooks/notify", s.handleHookNotify)
	mux.HandleFunc("/api/hooks/install", s.handleHookInstall)
	mux.HandleFunc("/api/hooks/remove", s.handleHookRemove)
	mux.HandleFunc("/api/hooks/install-status", s.handleHookInstallStatus)
	mux.HandleFunc("/api/hooks/plan-gate", s.handlePlanGate)
	mux.HandleFunc("/api/hooks/ask-user", s.handleAskUser)
	mux.HandleFunc("/api/hooks/usage", s.hookReceiver.HandleUsageUpdate)
}

// handleHookInstall installs Tetora hooks into Claude Code settings.
// POST /api/hooks/install
func (s *Server) handleHookInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	cfg := s.Cfg()
	if err := hooks.Install(cfg.ListenAddr); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	resp := map[string]any{"ok": true}

	// Also generate MCP bridge config.
	if err := generateMCPBridgeConfig(cfg); err != nil {
		resp["mcpBridgeError"] = err.Error()
	} else {
		homeDir, _ := os.UserHomeDir()
		resp["mcpBridge"] = filepath.Join(homeDir, ".tetora", "mcp", "bridge.json")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleHookRemove removes Tetora hooks from Claude Code settings.
// POST /api/hooks/remove
func (s *Server) handleHookRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := hooks.Remove(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
}

// handleHookInstallStatus checks whether hooks are currently installed.
// GET /api/hooks/install-status
func (s *Server) handleHookInstallStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}

	installed := false
	hookCount := 0

	// Check Claude Code settings for Tetora hooks.
	settings, _, err := hooks.LoadSettings()
	if err == nil {
		raw, ok := settings.Raw["hooks"]
		if ok {
			var hcfg hooks.HooksConfig
			if json.Unmarshal(raw, &hcfg) == nil {
				for _, r := range hcfg.PreToolUse {
					if hooks.IsTetoraRule(r) {
						installed = true
						hookCount++
					}
				}
				for _, r := range hcfg.PostToolUse {
					if hooks.IsTetoraRule(r) {
						installed = true
						hookCount++
					}
				}
				for _, r := range hcfg.Stop {
					if hooks.IsTetoraRule(r) {
						hookCount++
					}
				}
				for _, r := range hcfg.Notification {
					if hooks.IsTetoraRule(r) {
						hookCount++
					}
				}
			}
		}
	}

	// Check MCP bridge config.
	homeDir, _ := os.UserHomeDir()
	bridgePath := filepath.Join(homeDir, ".tetora", "mcp", "bridge.json")
	_, mcpErr := os.Stat(bridgePath)
	mcpBridge := mcpErr == nil

	// Get event count from hook receiver.
	var eventCount int64
	if s.hookReceiver != nil {
		eventCount = s.hookReceiver.EventCount()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"installed":  installed,
		"hookCount":  hookCount,
		"mcpBridge":  mcpBridge,
		"eventCount": eventCount,
	})
}

// handleHookNotify receives notifications from Claude Code via MCP bridge
// and forwards them to Discord/Telegram.
// POST /api/hooks/notify
func (s *Server) handleHookNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Message string `json:"message"`
		Level   string `json:"level"` // info, warn, error
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.Message == "" {
		http.Error(w, `{"error":"message is required"}`, http.StatusBadRequest)
		return
	}
	if body.Level == "" {
		body.Level = "info"
	}

	// Send notification via configured channels.
	cfg := s.Cfg()
	prefix := ""
	switch body.Level {
	case "warn":
		prefix = "[WARN] "
	case "error":
		prefix = "[ERROR] "
	}
	msg := prefix + body.Message

	// Try Discord notification channel.
	if cfg.Runtime.DiscordBot != nil {
		cfg.Runtime.DiscordBot.(*DiscordBot).sendNotify(msg)
	}

	// Publish to SSE for dashboard.
	if s.hookReceiver != nil && s.hookReceiver.Broker() != nil {
		s.hookReceiver.Broker().Publish(SSEDashboardKey, SSEEvent{
			Type: SSEHookEvent,
			Data: map[string]any{
				"hookType": "notification",
				"message":  body.Message,
				"level":    body.Level,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// --- Plan Gate (PreToolUse:ExitPlanMode long-poll) ---

// handlePlanGate handles POST /api/hooks/plan-gate.
// Called by the PreToolUse:ExitPlanMode hook script. Blocks until Discord approval.
func (s *Server) handlePlanGate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse hook event from Claude Code.
	var event HookEvent
	json.Unmarshal(body, &event)
	sessionID := event.ResolvedSessionID()
	if sessionID == "" {
		sessionID = s.hookReceiver.ExtractSessionID(&event, body)
	}

	hr := s.hookReceiver
	cfg := s.Cfg()

	// Read cached plan content.
	planText := ""
	if sessionID != "" {
		if plan := hr.GetCachedPlan(sessionID); plan != nil {
			planText = plan.Content
		}
	}

	// --- Keyboard mode: allow immediately (no terminal UI in --print mode) ---
	if cfg.PlanGate.Mode == "keyboard" {
		log.Info("plan gate: keyboard mode, allowing immediately", "session", sessionID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
			},
		})
		return
	}

	// Generate gate ID.
	sessionShort := sessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	gateID := fmt.Sprintf("pg-%s-%d", sessionShort, time.Now().Unix())

	// Create decision channel.
	ch := make(chan planGateDecision, 1)
	hr.RegisterPlanGate(gateID, ch)
	defer hr.UnregisterPlanGate(gateID)

	// Insert plan review DB record.
	review := &PlanReview{
		ID:        gateID,
		SessionID: sessionID,
		PlanText:  planText,
		Status:    "pending",
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if cfg.HistoryDB != "" {
		insertPlanReview(cfg.HistoryDB, review)
	}

	// Send to Discord if available.
	if bot, _ := cfg.Runtime.DiscordBot.(*DiscordBot); bot != nil {
		embed := buildPlanReviewEmbed(review)
		customApprove := "pgate_approve:" + gateID
		customReject := "pgate_reject:" + gateID
		components := []discord.Component{
			discordActionRow(
				discordButton(customApprove, "Approve Plan", discord.ButtonStyleSuccess),
				discordButton(customReject, "Reject Plan", discord.ButtonStyleDanger),
			),
		}

		bot.interactions.register(&pendingInteraction{
			CustomID:  customApprove,
			CreatedAt: time.Now(),
			Response: &discord.InteractionResponse{
				Type: discord.InteractionResponseUpdateMessage,
				Data: &discord.InteractionResponseData{
					Content: "✅ Plan approved.",
				},
			},
			Callback: func(data discord.InteractionData) {
				if cfg.HistoryDB != "" {
					updatePlanReviewStatus(cfg.HistoryDB, gateID, "approved", "discord", "")
				}
				select {
				case ch <- planGateDecision{Approved: true}:
				default:
				}
			},
		})
		bot.interactions.register(&pendingInteraction{
			CustomID:  customReject,
			CreatedAt: time.Now(),
			Response: &discord.InteractionResponse{
				Type: discord.InteractionResponseUpdateMessage,
				Data: &discord.InteractionResponseData{
					Content: "❌ Plan rejected.",
				},
			},
			Callback: func(data discord.InteractionData) {
				if cfg.HistoryDB != "" {
					updatePlanReviewStatus(cfg.HistoryDB, gateID, "rejected", "discord", "")
				}
				select {
				case ch <- planGateDecision{Approved: false, Reason: "Rejected via Discord"}:
				default:
				}
			},
		})
		defer func() {
			bot.interactions.remove(customApprove)
			bot.interactions.remove(customReject)
		}()

		notifyCh := bot.notifyChannelID()
		if notifyCh != "" {
			bot.sendEmbedWithComponents(notifyCh, embed, components)
		}

		log.Info("plan gate: waiting for Discord approval", "gateId", gateID, "session", sessionID)
	} else {
		// No Discord — auto-approve.
		log.Info("plan gate: no Discord bot, auto-approving", "gateId", gateID)
		ch <- planGateDecision{Approved: true}
	}

	// Publish to SSE for dashboard.
	if hr.Broker() != nil {
		hr.Broker().Publish(SSEDashboardKey, SSEEvent{
			Type:      SSEPlanReview,
			SessionID: sessionID,
			Data: map[string]any{
				"gateId":    gateID,
				"sessionId": sessionID,
				"status":    "waiting",
			},
		})
	}

	// Long-poll: wait for decision or timeout (5 minutes).
	var decision planGateDecision
	select {
	case decision = <-ch:
		log.Info("plan gate: decision received", "gateId", gateID, "approved", decision.Approved)
	case <-time.After(5 * time.Minute):
		log.Warn("plan gate: timeout, auto-approving", "gateId", gateID)
		decision = planGateDecision{Approved: true, Reason: "timeout"}
	}

	// Clear cached plan.
	if sessionID != "" {
		hr.ClearPlanCache(sessionID)
	}

	// Return Claude Code hook response.
	w.Header().Set("Content-Type", "application/json")
	if decision.Approved {
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
			},
		})
	} else {
		reason := decision.Reason
		if reason == "" {
			reason = "Plan rejected by reviewer"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "deny",
				"reason":             reason,
			},
		})
	}
}

// --- Ask User (long-poll question gate) ---

// handleAskUser handles POST /api/hooks/ask-user.
// MCP tool tetora_ask_user routes questions here. Blocks until Discord response.
func (s *Server) handleAskUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Question string   `json:"question"`
		Options  []string `json:"options,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if body.Question == "" {
		http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
		return
	}

	hr := s.hookReceiver
	cfg := s.Cfg()

	qID := fmt.Sprintf("q-%d", time.Now().UnixNano())

	// Create answer channel.
	ch := make(chan string, 1)
	hr.RegisterQuestionGate(qID, ch)
	defer hr.UnregisterQuestionGate(qID)

	// Send to Discord.
	var cleanupIDs []string
	var cleanupBot *DiscordBot
	if bot, _ := cfg.Runtime.DiscordBot.(*DiscordBot); bot != nil {
		notifyCh := bot.notifyChannelID()
		if notifyCh != "" {
			cleanupBot = bot

			// Build buttons for options.
			var buttons []discord.Component
			for i, opt := range body.Options {
				if i >= 4 {
					break // Discord max 5 buttons per row, keep room for "Type" button
				}
				customID := fmt.Sprintf("askuser_%s_%d", qID, i)
				answer := opt
				bot.interactions.register(&pendingInteraction{
					CustomID:  customID,
					CreatedAt: time.Now(),
					Callback: func(data discord.InteractionData) {
						select {
						case ch <- answer:
						default:
						}
					},
				})
				cleanupIDs = append(cleanupIDs, customID)
				buttons = append(buttons, discordButton(customID, truncate(opt, 80), discord.ButtonStylePrimary))
			}

			// Add "Type" button for free-text input.
			typeButtonID := "askuser_type_" + qID
			typeModalID := "askuser_modal_" + qID
			modalResp := discordBuildModal(typeModalID, "Your Answer",
				discordTextInput("answer_text", "Answer", true))
			bot.interactions.register(&pendingInteraction{
				CustomID:      typeButtonID,
				CreatedAt:     time.Now(),
				ModalResponse: &modalResp,
			})
			cleanupIDs = append(cleanupIDs, typeButtonID)

			bot.interactions.register(&pendingInteraction{
				CustomID:  typeModalID,
				CreatedAt: time.Now(),
				Callback: func(data discord.InteractionData) {
					values := extractModalValues(data.Components)
					text := values["answer_text"]
					if text == "" {
						text = "(empty)"
					}
					select {
					case ch <- text:
					default:
					}
				},
			})
			cleanupIDs = append(cleanupIDs, typeModalID)

			buttons = append(buttons, discordButton(typeButtonID, "Type...", discord.ButtonStyleSecondary))

			content := fmt.Sprintf("**Question from Claude Code:**\n%s", body.Question)
			components := []discord.Component{discordActionRow(buttons...)}
			bot.sendMessageWithComponents(notifyCh, content, components)

			log.Info("ask-user: waiting for Discord answer", "qId", qID)
		}
	} else {
		// No Discord — return empty answer.
		log.Info("ask-user: no Discord bot, returning empty", "qId", qID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"answer": "(no Discord configured)"})
		return
	}

	// Long-poll: wait for answer or timeout (6 minutes).
	var answer string
	select {
	case answer = <-ch:
		log.Info("ask-user: answer received", "qId", qID)
	case <-time.After(6 * time.Minute):
		log.Warn("ask-user: timeout", "qId", qID)
		answer = "(timeout: no answer received)"
	}

	// Cleanup all registered interactions.
	if cleanupBot != nil {
		for _, id := range cleanupIDs {
			cleanupBot.interactions.remove(id)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"answer": answer})
}
