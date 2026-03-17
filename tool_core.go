package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/db"
)

// --- Tool Handlers ---

// toolExec executes a shell command.
func toolExec(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Command string  `json:"command"`
		Workdir string  `json:"workdir"`
		Timeout float64 `json:"timeout"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	if args.Timeout <= 0 {
		args.Timeout = 60
	}

	// Validate workdir is within allowedDirs.
	if args.Workdir != "" {
		if err := validateDirs(cfg, Task{Workdir: args.Workdir}, ""); err != nil {
			return "", fmt.Errorf("workdir not allowed: %w", err)
		}
	}

	timeout := time.Duration(args.Timeout) * time.Second
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", args.Command)
	if args.Workdir != "" {
		cmd.Dir = args.Workdir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("command failed: %w", err)
		}
	}

	// Limit output size.
	const maxOutput = 100 * 1024 // 100KB
	out := stdout.String()
	errOut := stderr.String()
	if len(out) > maxOutput {
		out = out[:maxOutput] + "\n[truncated]"
	}
	if len(errOut) > maxOutput {
		errOut = errOut[:maxOutput] + "\n[truncated]"
	}

	result := map[string]any{
		"stdout":   out,
		"stderr":   errOut,
		"exitCode": exitCode,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolRead reads file contents.
func toolRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Check file size.
	info, err := os.Stat(args.Path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	const maxSize = 1024 * 1024 // 1MB
	if info.Size() > maxSize {
		return "", fmt.Errorf("file too large (max 1MB)")
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if args.Offset > 0 {
		if args.Offset >= len(lines) {
			return "", nil
		}
		lines = lines[args.Offset:]
	}
	if args.Limit > 0 && args.Limit < len(lines) {
		lines = lines[:args.Limit]
	}

	return strings.Join(lines, "\n"), nil
}

// toolWrite writes content to a file.
func toolWrite(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Validate path is within allowedDirs.
	if err := validateDirs(cfg, Task{Workdir: filepath.Dir(args.Path)}, ""); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(args.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path), nil
}

// toolEdit performs string replacement in a file.
func toolEdit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" || args.OldString == "" {
		return "", fmt.Errorf("path and old_string are required")
	}

	// Validate path is within allowedDirs.
	if err := validateDirs(cfg, Task{Workdir: filepath.Dir(args.Path)}, ""); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	if !strings.Contains(content, args.OldString) {
		return "", fmt.Errorf("old_string not found in file")
	}

	// Check for unique match.
	count := strings.Count(content, args.OldString)
	if count > 1 {
		return "", fmt.Errorf("old_string appears %d times (not unique)", count)
	}

	newContent := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := os.WriteFile(args.Path, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("replaced 1 occurrence in %s", args.Path), nil
}

// toolWebFetch fetches a URL and returns plain text.
func toolWebFetch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL       string `json:"url"`
		MaxLength int    `json:"maxLength"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.MaxLength <= 0 {
		args.MaxLength = 50000 // default 50KB
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", args.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	// Limit response size.
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(args.MaxLength)))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	// Simple HTML tag stripping.
	text := stripHTMLTags(string(body))

	// Truncate to maxLength after stripping tags.
	if len(text) > args.MaxLength {
		text = text[:args.MaxLength]
	}

	return text, nil
}

// stripHTMLTags removes HTML tags from text (naive implementation).
func stripHTMLTags(html string) string {
	var result strings.Builder
	inTag := false
	for _, c := range html {
		if c == '<' {
			inTag = true
		} else if c == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(c)
		}
	}
	// Collapse multiple whitespace.
	text := result.String()
	text = strings.Join(strings.Fields(text), " ")
	return text
}

// toolSessionList lists active sessions.
func toolSessionList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
	}
	json.Unmarshal(input, &args)

	query := `SELECT session_id, channel_type, channel_id, message_count, created_at, updated_at FROM sessions WHERE 1=1`
	if args.Channel != "" {
		query += fmt.Sprintf(` AND channel_type = '%s'`, db.Escape(args.Channel))
	}
	query += ` ORDER BY updated_at DESC LIMIT 20`

	rows, err := db.Query(cfg.HistoryDB, query)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	var results []map[string]string
	for _, row := range rows {
		results = append(results, map[string]string{
			"session_id":    fmt.Sprintf("%v", row["session_id"]),
			"channel_type":  fmt.Sprintf("%v", row["channel_type"]),
			"channel_id":    fmt.Sprintf("%v", row["channel_id"]),
			"message_count": fmt.Sprintf("%v", row["message_count"]),
			"created_at":    fmt.Sprintf("%v", row["created_at"]),
			"updated_at":    fmt.Sprintf("%v", row["updated_at"]),
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolMessage sends a message to a channel.
func toolMessage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Channel string `json:"channel"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Channel == "" || args.Message == "" {
		return "", fmt.Errorf("channel and message are required")
	}

	switch args.Channel {
	case "telegram":
		if cfg.Telegram.Enabled {
			err := sendTelegramNotify(&cfg.Telegram, args.Message)
			if err != nil {
				return "", fmt.Errorf("send telegram: %w", err)
			}
			return "message sent to telegram", nil
		}
		return "", fmt.Errorf("telegram not enabled")
	case "slack":
		if cfg.Slack.Enabled {
			// Use notification system.
			notifiers := buildNotifiers(cfg)
			for _, n := range notifiers {
				if n.Name() == "slack" {
					n.Send(args.Message)
				}
			}
			return "message sent to slack", nil
		}
		return "", fmt.Errorf("slack not enabled")
	case "discord":
		if cfg.Discord.Enabled {
			// Use notification system.
			notifiers := buildNotifiers(cfg)
			for _, n := range notifiers {
				if n.Name() == "discord" {
					n.Send(args.Message)
				}
			}
			return "message sent to discord", nil
		}
		return "", fmt.Errorf("discord not enabled")
	default:
		// Support discord-id:CHANNEL_ID for direct bot-token based sending.
		if strings.HasPrefix(args.Channel, "discord-id:") {
			channelID := strings.TrimPrefix(args.Channel, "discord-id:")
			if !cfg.Discord.Enabled {
				return "", fmt.Errorf("discord not enabled")
			}
			if cfg.Discord.BotToken == "" {
				return "", fmt.Errorf("discord bot token not configured")
			}
			if err := cronDiscordSendBotChannel(cfg.Discord.BotToken, channelID, args.Message); err != nil {
				return "", fmt.Errorf("send discord-id:%s: %w", channelID, err)
			}
			return "message sent to discord channel " + channelID, nil
		}
		// Support discord-<name> for named webhook channels, e.g. "discord-stock".
		if strings.HasPrefix(args.Channel, "discord-") {
			name := strings.TrimPrefix(args.Channel, "discord-")
			if !cfg.Discord.Enabled {
				return "", fmt.Errorf("discord not enabled")
			}
			webhookURL, ok := cfg.Discord.Webhooks[name]
			if !ok || webhookURL == "" {
				return "", fmt.Errorf("discord channel %q not configured (add to discord.webhooks in config.json)", name)
			}
			n := &DiscordNotifier{WebhookURL: webhookURL, client: &http.Client{Timeout: 10 * time.Second}}
			if err := n.Send(args.Message); err != nil {
				return "", fmt.Errorf("send discord-%s: %w", name, err)
			}
			return "message sent to discord-" + name, nil
		}
		return "", fmt.Errorf("unknown channel: %s", args.Channel)
	}
}

// toolCronList lists cron jobs.
func toolCronList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	// Read cron jobs from JobsFile.
	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		return "", fmt.Errorf("load jobs: %w", err)
	}

	var results []map[string]any
	for _, j := range jobs {
		results = append(results, map[string]any{
			"id":       j.ID,
			"name":     j.Name,
			"schedule": j.Schedule,
			"enabled":  j.Enabled,
			"agent":    j.Agent,
		})
	}

	b, _ := json.Marshal(results)
	return string(b), nil
}

// toolCronCreate creates or updates a cron job.
func toolCronCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		Agent    string `json:"agent"`
		Role     string `json:"role"` // backward compat
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Agent == "" {
		args.Agent = args.Role
	}
	if args.Name == "" || args.Schedule == "" || args.Prompt == "" {
		return "", fmt.Errorf("name, schedule, and prompt are required")
	}

	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		jobs = []CronJobConfig{}
	}

	// Check if job exists.
	found := false
	for i := range jobs {
		if jobs[i].Name == args.Name {
			jobs[i].Schedule = args.Schedule
			jobs[i].Task.Prompt = args.Prompt
			jobs[i].Agent = args.Agent
			jobs[i].Enabled = true
			found = true
			break
		}
	}

	if !found {
		newJob := CronJobConfig{
			ID:       newUUID(),
			Name:     args.Name,
			Schedule: args.Schedule,
			Enabled:  true,
			Agent:    args.Agent,
			Task: CronTaskConfig{
				Prompt: args.Prompt,
			},
		}
		jobs = append(jobs, newJob)
	}

	if err := saveCronJobs(cfg.JobsFile, jobs); err != nil {
		return "", fmt.Errorf("save jobs: %w", err)
	}

	msg := "created"
	if found {
		msg = "updated"
	}
	return fmt.Sprintf("cron job %q %s", args.Name, msg), nil
}

// toolCronDelete deletes a cron job.
func toolCronDelete(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}

	jobs, err := loadCronJobs(cfg.JobsFile)
	if err != nil {
		return "", fmt.Errorf("load jobs: %w", err)
	}

	found := false
	newJobs := make([]CronJobConfig, 0, len(jobs))
	for _, j := range jobs {
		if j.Name != args.Name {
			newJobs = append(newJobs, j)
		} else {
			found = true
		}
	}

	if !found {
		return "", fmt.Errorf("job %q not found", args.Name)
	}

	if err := saveCronJobs(cfg.JobsFile, newJobs); err != nil {
		return "", fmt.Errorf("save jobs: %w", err)
	}

	return fmt.Sprintf("cron job %q deleted", args.Name), nil
}

// --- Helper functions for cron job management ---

func loadCronJobs(path string) ([]CronJobConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var jobs []CronJobConfig
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func saveCronJobs(path string, jobs []CronJobConfig) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
