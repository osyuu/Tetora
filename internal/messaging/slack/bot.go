package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"tetora/internal/messaging"
)

// --- Slack Event Types ---

type eventWrapper struct {
	Token     string          `json:"token"`
	Challenge string          `json:"challenge"` // URL verification
	Type      string          `json:"type"`      // "url_verification", "event_callback"
	Event     json.RawMessage `json:"event"`
	TeamID    string          `json:"team_id"`
	EventID   string          `json:"event_id"`
}

// File represents a Slack file attachment.
type File struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Mimetype           string `json:"mimetype"`
	URLPrivateDownload string `json:"url_private_download"`
	Size               int64  `json:"size"`
}

type event struct {
	Type     string `json:"type"`                // "message", "app_mention"
	Text     string `json:"text"`
	User     string `json:"user"`
	Channel  string `json:"channel"`
	TS       string `json:"ts"`                  // message timestamp (used as thread ID)
	ThreadTS string `json:"thread_ts,omitempty"` // parent thread
	BotID    string `json:"bot_id,omitempty"`    // non-empty if from a bot
	SubType  string `json:"subtype,omitempty"`   // e.g. "bot_message", "message_changed"
	Files    []File `json:"files,omitempty"`
}

// --- Slack Bot ---

// Bot handles incoming Slack Events API requests and routes them
// through the smart dispatch system.
type Bot struct {
	cfg Config
	rt  messaging.BotRuntime

	// Dedup: track recently processed event IDs to handle Slack retries.
	processed     map[string]time.Time
	processedSize int
	approvalGate  *ApprovalGate
}

// NewBot creates a new Slack bot.
func NewBot(cfg Config, rt messaging.BotRuntime) *Bot {
	b := &Bot{
		cfg:       cfg,
		rt:        rt,
		processed: make(map[string]time.Time),
	}
	if rt != nil && rt.ApprovalGatesEnabled() && cfg.DefaultChannel != "" {
		b.approvalGate = NewApprovalGate(b, cfg.DefaultChannel, rt.ApprovalGatesAutoApproveTools())
	}
	return b
}

// EventHandler handles incoming Slack Events API requests.
// Register this at /slack/events in the HTTP server.
func (b *Bot) EventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Verify Slack signature if signing secret is configured.
	secret := b.cfg.SigningSecret
	if secret != "" {
		if !VerifySignature(r, body, secret) {
			b.rt.LogWarn("slack invalid signature", "remoteAddr", r.RemoteAddr)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var wrapper eventWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Handle URL verification challenge (Slack sends this during app setup).
	if wrapper.Type == "url_verification" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"challenge": wrapper.Challenge})
		return
	}

	// Handle event callbacks.
	if wrapper.Type == "event_callback" {
		// Dedup: Slack may retry if we respond slowly.
		if wrapper.EventID != "" && b.isDuplicate(wrapper.EventID) {
			w.WriteHeader(http.StatusOK)
			return
		}

		var ev event
		if err := json.Unmarshal(wrapper.Event, &ev); err != nil {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}

		// Acknowledge immediately (Slack requires response within 3 seconds).
		w.WriteHeader(http.StatusOK)

		// Process event asynchronously.
		go b.handleEvent(ev)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (b *Bot) isDuplicate(eventID string) bool {
	now := time.Now()

	if b.processedSize > 500 {
		for id, ts := range b.processed {
			if now.Sub(ts) > 10*time.Minute {
				delete(b.processed, id)
			}
		}
		b.processedSize = len(b.processed)
	}

	if _, exists := b.processed[eventID]; exists {
		return true
	}
	b.processed[eventID] = now
	b.processedSize++
	return false
}

func (b *Bot) handleEvent(ev event) {
	if ev.BotID != "" {
		return
	}
	if ev.SubType != "" {
		return
	}

	switch ev.Type {
	case "app_mention", "message":
		text := StripMentions(ev.Text)

		// Download attached files and inject into prompt.
		var filePaths []string
		for _, f := range ev.Files {
			if path, err := b.downloadFile(f); err != nil {
				b.rt.LogWarn("slack: file download failed", "name", f.Name, "err", err)
			} else {
				filePaths = append(filePaths, path)
			}
		}
		if prefix := b.rt.BuildFilePromptPrefix(filePaths); prefix != "" {
			text = prefix + text
		}

		if text == "" {
			return
		}

		// Handle approval gate replies.
		if b.approvalGate != nil {
			if strings.HasPrefix(text, "approve ") {
				reqID := strings.TrimPrefix(text, "approve ")
				b.approvalGate.HandleCallback(strings.TrimSpace(reqID), true)
				b.Reply(ev.Channel, threadTS(ev), "Approved.")
				return
			}
			if strings.HasPrefix(text, "reject ") {
				reqID := strings.TrimPrefix(text, "reject ")
				b.approvalGate.HandleCallback(strings.TrimSpace(reqID), false)
				b.Reply(ev.Channel, threadTS(ev), "Rejected.")
				return
			}
			if strings.HasPrefix(text, "always ") {
				toolName := strings.TrimSpace(strings.TrimPrefix(text, "always "))
				b.approvalGate.AutoApprove(toolName)
				b.Reply(ev.Channel, threadTS(ev), fmt.Sprintf("Auto-approved `%s` for this runtime.", toolName))
				return
			}
		}

		// Parse commands.
		if strings.HasPrefix(text, "!") {
			b.handleCommand(ev, text[1:])
			return
		}

		// Smart dispatch.
		if b.rt.SmartDispatchEnabled() {
			b.handleRoute(ev, text)
		} else {
			b.Reply(ev.Channel, threadTS(ev), "Smart dispatch is not enabled. Use `!help` for commands.")
		}
	}
}

func (b *Bot) handleCommand(ev event, cmdText string) {
	parts := strings.SplitN(cmdText, " ", 2)
	command := strings.ToLower(parts[0])
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	switch command {
	case "status":
		b.cmdStatus(ev)
	case "jobs", "cron":
		b.cmdJobs(ev)
	case "cost":
		b.cmdCost(ev)
	case "model":
		b.cmdModel(ev, args)
	case "new":
		b.cmdNew(ev, args)
	case "help":
		b.cmdHelp(ev)
	default:
		if args != "" {
			b.handleRoute(ev, cmdText)
		} else {
			b.Reply(ev.Channel, threadTS(ev),
				"Unknown command `!"+command+"`. Use `!help` for available commands.")
		}
	}
}

func (b *Bot) handleRoute(ev event, prompt string) {
	ts := threadTS(ev)

	// Send initial "thinking" message.
	thinkingTS := b.PostMessage(ev.Channel, ts, "Routing...")

	traceID := b.rt.NewTraceID("slack")
	ctx := b.rt.WithTraceID(context.Background(), traceID)

	// Step 1: Route to determine agent.
	role, err := b.rt.Route(ctx, prompt, "slack")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "slack route error", err)
	}
	b.rt.LogInfoCtx(ctx, "slack route result", "prompt", b.rt.Truncate(prompt, 60), "agent", role)

	// Step 2: Find or create channel session for this thread.
	chKey := "slack:" + ev.Channel + ":" + ts
	sessID, err := b.rt.GetOrCreateSession("slack", chKey, role, "")
	if err != nil {
		b.rt.LogErrorCtx(ctx, "slack route session error", err)
	}

	// Step 3: Build context-aware prompt.
	contextPrompt := prompt
	if sessID != "" {
		if !b.rt.ProviderHasNativeSession(role) {
			sessionCtx := b.rt.BuildSessionContext(sessID, b.rt.SessionContextLimit())
			if sessionCtx != "" {
				contextPrompt = sessionCtx + "\n\n" + prompt
			}
		}

		// Record user message to session.
		b.rt.AddSessionMessage(sessID, "user", messaging.TruncateStr(prompt, 5000))
		b.rt.UpdateSessionStats(sessID, 0, 0, 0, 1)

		title := prompt
		if len(title) > 100 {
			title = title[:100]
		}
		b.rt.UpdateSessionTitle(sessID, title)
	}

	// Step 4: Build and run task.
	soulPrompt, _ := b.rt.LoadAgentPrompt(role)
	model, permMode, _ := b.rt.AgentConfig(role)

	contextPrompt = b.rt.ExpandPrompt(contextPrompt, role)

	result, _ := b.rt.Submit(ctx, messaging.TaskRequest{
		AgentRole:      role,
		Content:        contextPrompt,
		SessionID:      sessID,
		SystemPrompt:   soulPrompt,
		Model:          model,
		PermissionMode: permMode,
		Meta:           map[string]string{"source": "route:slack"},
	})

	// Record to history.
	b.rt.RecordHistory(result.TaskID, "", "route:slack", role, result.OutputFile, nil, result)

	// Step 5: Record assistant response to session.
	if sessID != "" {
		msgRole := "assistant"
		content := messaging.TruncateStr(result.Output, 5000)
		if result.Status != "success" {
			msgRole = "system"
			errMsg := result.Error
			if errMsg == "" {
				errMsg = result.Status
			}
			content = fmt.Sprintf("[%s] %s", result.Status, messaging.TruncateStr(errMsg, 2000))
		}
		b.rt.AddSessionMessage(sessID, msgRole, content)
		b.rt.UpdateSessionStats(sessID, result.CostUSD, result.TokensIn, result.TokensOut, 0)

		b.rt.MaybeCompactSession(sessID, 0, result.TokensIn)
	}

	// Store in agent memory.
	if result.Status == "success" {
		b.rt.SetMemory(role, "last_route_output", b.rt.Truncate(result.Output, 500))
		b.rt.SetMemory(role, "last_route_prompt", b.rt.Truncate(prompt, 200))
		b.rt.SetMemory(role, "last_route_time", time.Now().Format(time.RFC3339))
	}

	// Audit + webhooks.
	b.rt.AuditLog("route.dispatch", "slack",
		fmt.Sprintf("agent=%s session=%s", role, sessID), "")
	b.rt.SendWebhooks(result.Status, map[string]interface{}{
		"job_id": result.TaskID, "source": "route:slack",
		"status": result.Status, "cost": result.CostUSD, "duration": result.DurationMs,
		"model": result.Model, "output": b.rt.Truncate(result.Output, 500),
		"error": b.rt.Truncate(result.Error, 300),
	})

	// Format response.
	var text strings.Builder
	fmt.Fprintf(&text, "*Route:* %s\n", role)

	if result.Status == "success" {
		fmt.Fprintf(&text, "\n%s", b.rt.Truncate(result.Output, 3000))
	} else {
		fmt.Fprintf(&text, "\n*[%s]* %s", result.Status, b.rt.Truncate(result.Error, 500))
	}

	dur := time.Duration(result.DurationMs) * time.Millisecond
	fmt.Fprintf(&text, "\n\n_$%.2f | %s_", result.CostUSD, dur.Round(time.Second))

	if thinkingTS != "" {
		b.UpdateMessage(ev.Channel, thinkingTS, text.String())
	} else {
		b.Reply(ev.Channel, ts, text.String())
	}
}

// --- Command Handlers ---

func (b *Bot) cmdStatus(ev event) {
	ts := threadTS(ev)

	if !b.rt.IsActive() {
		jobs := b.rt.ListCronJobs()
		running := 0
		for _, j := range jobs {
			if j.Running {
				running++
			}
		}
		if len(jobs) > 0 {
			b.Reply(ev.Channel, ts,
				fmt.Sprintf("No active dispatch.\nCron: %d jobs (%d running)", len(jobs), running))
		} else {
			b.Reply(ev.Channel, ts, "No active dispatch.")
		}
	} else {
		b.Reply(ev.Channel, ts, string(b.rt.StatusJSON()))
	}
}

func (b *Bot) cmdJobs(ev event) {
	ts := threadTS(ev)

	jobs := b.rt.ListCronJobs()
	if len(jobs) == 0 {
		b.Reply(ev.Channel, ts, "No cron jobs configured.")
		return
	}
	var lines []string
	for _, j := range jobs {
		icon := "[ ]"
		if j.Running {
			icon = "[>]"
		} else if j.Enabled {
			icon = "[*]"
		}
		nextStr := ""
		if j.NextRun != "" && j.Enabled {
			if t, err := time.Parse(time.RFC3339, j.NextRun); err == nil {
				nextStr = fmt.Sprintf(" next: %s", t.Format("15:04"))
			}
		}
		avgStr := ""
		if j.AvgCost > 0 {
			avgStr = fmt.Sprintf(" avg:$%.2f", j.AvgCost)
		}
		lines = append(lines, fmt.Sprintf("%s *%s* [%s]%s%s",
			icon, j.Name, j.Schedule, nextStr, avgStr))
	}
	b.Reply(ev.Channel, ts, strings.Join(lines, "\n"))
}

func (b *Bot) cmdCost(ev event) {
	ts := threadTS(ev)

	today, week, month := b.rt.QueryCostStats()
	text := fmt.Sprintf("*Cost Summary*\nToday: $%.2f\nWeek: $%.2f\nMonth: $%.2f",
		today, week, month)

	if limit := b.rt.CostAlertDailyLimit(); limit > 0 {
		pct := (today / limit) * 100
		text += fmt.Sprintf("\n\nDaily limit: $%.2f (%.0f%% used)", limit, pct)
	}

	b.Reply(ev.Channel, ts, text)
}

func (b *Bot) cmdNew(ev event, args string) {
	ts := threadTS(ev)

	chKey := "slack:" + ev.Channel + ":" + ts
	if err := b.rt.ArchiveSession(chKey); err != nil {
		b.Reply(ev.Channel, ts, "Error: "+err.Error())
		return
	}
	b.Reply(ev.Channel, ts, "Session archived. Next message starts a fresh conversation.")
}

func (b *Bot) cmdModel(ev event, args string) {
	parts := strings.Fields(args)

	if len(parts) == 0 {
		models := b.rt.AgentModels()
		var lines []string
		for name, m := range models {
			lines = append(lines, fmt.Sprintf("  %s: `%s`", name, m))
		}
		b.Reply(ev.Channel, threadTS(ev), "*Current models:*\n"+strings.Join(lines, "\n"))
		return
	}

	model := parts[0]
	agentName := b.rt.DefaultAgent()
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	if err := b.rt.UpdateAgentModel(agentName, model); err != nil {
		b.Reply(ev.Channel, threadTS(ev), fmt.Sprintf("Error: %v", err))
		return
	}
	b.Reply(ev.Channel, threadTS(ev), fmt.Sprintf("*%s* model changed to `%s`", agentName, model))
}

func (b *Bot) cmdHelp(ev event) {
	b.Reply(ev.Channel, threadTS(ev),
		"*Tetora Slack Bot*\n"+
			"`!status` -- Check running tasks\n"+
			"`!jobs` -- List cron jobs\n"+
			"`!cost` -- Cost summary\n"+
			"`!model [model] [agent]` -- Show/switch model\n"+
			"`!new` -- Start fresh session in this thread\n"+
			"`!help` -- This message\n"+
			"\nMessages in a thread share conversation context.\n"+
			"Just type a message to auto-route to the best agent.")
}

// --- Slack API ---

// Reply sends a message to a Slack channel, optionally in a thread.
func (b *Bot) Reply(channel, threadTS, text string) {
	token := b.cfg.BotToken
	if token == "" {
		b.rt.LogWarn("slack cannot send message, botToken is empty")
		return
	}

	payload := map[string]string{
		"channel": channel,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage",
		strings.NewReader(string(body)))
	if err != nil {
		b.rt.LogError("slack send request error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.rt.LogError("slack send error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		b.rt.LogWarn("slack send non-200", "status", resp.StatusCode, "body", string(respBody))
	}
}

// PostMessage sends a message and returns the message timestamp (ts) for later updates.
func (b *Bot) PostMessage(channel, threadTS, text string) string {
	token := b.cfg.BotToken
	if token == "" {
		return ""
	}

	payload := map[string]string{
		"channel": channel,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.postMessage",
		strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		OK bool   `json:"ok"`
		TS string `json:"ts"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.OK {
		return result.TS
	}
	return ""
}

// UpdateMessage updates a previously sent message with new text.
func (b *Bot) UpdateMessage(channel, messageTS, text string) {
	token := b.cfg.BotToken
	if token == "" {
		return
	}

	payload := map[string]string{
		"channel": channel,
		"ts":      messageTS,
		"text":    text,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://slack.com/api/chat.update",
		strings.NewReader(string(body)))
	if err != nil {
		b.rt.LogError("slack update request error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.rt.LogError("slack update error", err)
		return
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK {
		b.rt.LogError("slack update API error", fmt.Errorf("%s", result.Error))
	}
}

// SendNotify sends a standalone notification to the configured default channel.
func (b *Bot) SendNotify(text string) {
	if b.cfg.DefaultChannel != "" {
		b.Reply(b.cfg.DefaultChannel, "", text)
	}
}

// downloadFile downloads a file attached to a Slack message using the bot token.
func (b *Bot) downloadFile(f File) (string, error) {
	if f.URLPrivateDownload == "" {
		return "", fmt.Errorf("slack file %s has no download URL", f.Name)
	}
	return b.rt.DownloadFile(f.URLPrivateDownload, f.Name, "Bearer "+b.cfg.BotToken)
}

// --- PresenceSetter ---

// SetTyping is a no-op for Slack (no official bot typing API).
func (b *Bot) SetTyping(ctx context.Context, channelRef string) error {
	return nil
}

// PresenceName returns the channel name.
func (b *Bot) PresenceName() string { return "slack" }

// --- Approval Gate ---

// ApprovalRequest represents a request for tool execution approval.
type ApprovalRequest struct {
	ID      string
	Tool    string
	Summary string
}

// ApprovalGate implements tool execution approval via Slack messages.
type ApprovalGate struct {
	bot          *Bot
	channel      string
	mu           sync.Mutex
	pending      map[string]chan bool
	autoApproved map[string]bool
}

// NewApprovalGate creates a new Slack approval gate.
func NewApprovalGate(bot *Bot, channel string, autoApproveTools []string) *ApprovalGate {
	g := &ApprovalGate{
		bot:          bot,
		channel:      channel,
		pending:      make(map[string]chan bool),
		autoApproved: make(map[string]bool),
	}
	for _, tool := range autoApproveTools {
		g.autoApproved[tool] = true
	}
	return g
}

// AutoApprove marks a tool as auto-approved.
func (g *ApprovalGate) AutoApprove(toolName string) {
	g.mu.Lock()
	g.autoApproved[toolName] = true
	g.mu.Unlock()
}

// IsAutoApproved returns whether a tool is auto-approved.
func (g *ApprovalGate) IsAutoApproved(toolName string) bool {
	g.mu.Lock()
	ok := g.autoApproved[toolName]
	g.mu.Unlock()
	return ok
}

// RequestApproval sends an approval request to Slack and waits for response.
func (g *ApprovalGate) RequestApproval(ctx context.Context, req ApprovalRequest) (bool, error) {
	ch := make(chan bool, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
	}()

	text := fmt.Sprintf("*Approval needed*\n\nTool: `%s`\n%s\n\nReply `approve %s` or `reject %s` or `always %s`",
		req.Tool, req.Summary, req.ID, req.ID, req.Tool)
	g.bot.Reply(g.channel, "", text)

	select {
	case approved := <-ch:
		return approved, nil
	case <-ctx.Done():
		return false, fmt.Errorf("approval timed out: %v", ctx.Err())
	}
}

// HandleCallback processes an approval/rejection callback.
func (g *ApprovalGate) HandleCallback(reqID string, approved bool) {
	g.mu.Lock()
	ch, ok := g.pending[reqID]
	g.mu.Unlock()
	if ok {
		select {
		case ch <- approved:
		default:
		}
	}
}

// --- Signature Verification ---

// VerifySignature verifies the HMAC-SHA256 signature from Slack.
func VerifySignature(r *http.Request, body []byte, signingSecret string) bool {
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")

	if timestamp == "" || signature == "" {
		return false
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	diff := time.Now().Unix() - ts
	if diff < 0 {
		diff = -diff
	}
	if diff > 300 {
		return false
	}

	baseStr := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(baseStr))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expected))
}

// --- Helpers ---

func threadTS(ev event) string {
	if ev.ThreadTS != "" {
		return ev.ThreadTS
	}
	return ev.TS
}

// StripMentions removes <@USERID> mentions from text.
func StripMentions(text string) string {
	for {
		start := strings.Index(text, "<@")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], ">")
		if end < 0 {
			break
		}
		text = text[:start] + text[start+end+1:]
	}
	return strings.TrimSpace(text)
}
