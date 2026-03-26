package tmux

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
)

// ProfileRequest contains the fields from ProviderRequest needed by CLI profiles.
type ProfileRequest struct {
	Model          string
	PermissionMode string
	SystemPrompt   string
	AddDirs        []string
	MCPPath        string
}

// CLIProfile abstracts tool-specific behavior of interactive CLI tools
// running inside tmux sessions.
type CLIProfile interface {
	Name() string
	BuildCommand(binaryPath string, req ProfileRequest) string
	DetectState(capture string) ScreenState
	ApproveKeys() []string
	RejectKeys() []string
}

// --- Claude Code Profile ---

type ClaudeProfile struct{}

func NewClaudeProfile() CLIProfile { return &ClaudeProfile{} }

func (p *ClaudeProfile) Name() string { return "claude" }

func (p *ClaudeProfile) BuildCommand(binaryPath string, req ProfileRequest) string {
	args := []string{
		"--model", req.Model,
		"--permission-mode", cmp.Or(req.PermissionMode, "acceptEdits"),
	}

	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", shellQuote(req.SystemPrompt))
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	if req.MCPPath != "" {
		args = append(args, "--mcp-config", req.MCPPath)
	} else {
		homeDir, _ := os.UserHomeDir()
		bridgePath := filepath.Join(homeDir, ".tetora", "mcp", "bridge.json")
		if _, err := os.Stat(bridgePath); err == nil {
			args = append(args, "--mcp-config", bridgePath)
		}
	}

	return "env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_TEAM_MODE " + binaryPath + " " + strings.Join(args, " ")
}

func (p *ClaudeProfile) DetectState(capture string) ScreenState {
	lastLines := lastNonEmptyLines(capture, 12)
	if len(lastLines) == 0 {
		return StateUnknown
	}

	bottom := strings.Join(lastLines, "\n")
	bottomLower := strings.ToLower(bottom)

	approvalPatterns := []string{
		"(y/n)", "do you want to", "approve",
		"yes/no", "allow once", "allow all",
	}
	for _, pat := range approvalPatterns {
		if strings.Contains(bottomLower, pat) {
			return StateApproval
		}
	}
	if strings.Contains(bottomLower, " allow ") || strings.HasSuffix(bottomLower, " allow") {
		return StateApproval
	}

	lastLine := lastLines[len(lastLines)-1]
	if IsShellPrompt(lastLine) {
		return StateDone
	}

	if detectQuestionBlock(capture) {
		return StateQuestion
	}

	for _, line := range lastLines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "✽") {
			return StateWorking
		}
	}

	for _, line := range lastLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "❯" {
			return StateWaiting
		}
		if strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0") || strings.HasPrefix(trimmed, "❯\t") {
			afterPrompt := strings.TrimSpace(trimmed[len("❯ "):])
			if len(afterPrompt) <= 2 {
				return StateWaiting
			}
			continue
		}
		lineLower := strings.ToLower(trimmed)
		if strings.HasPrefix(lineLower, "> ") ||
			strings.Contains(lineLower, "what would you like") ||
			strings.Contains(lineLower, "how can i help") {
			return StateWaiting
		}
	}

	return StateWorking
}

func (p *ClaudeProfile) ApproveKeys() []string { return []string{"y", "Enter"} }
func (p *ClaudeProfile) RejectKeys() []string  { return []string{"n", "Enter"} }

// --- Codex CLI Profile ---

type CodexProfile struct{}

func NewCodexProfile() CLIProfile { return &CodexProfile{} }

func (p *CodexProfile) Name() string { return "codex" }

func (p *CodexProfile) BuildCommand(binaryPath string, req ProfileRequest) string {
	args := []string{
		"--no-alt-screen",
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	switch req.PermissionMode {
	case "bypassPermissions":
		args = append(args, "--full-auto")
	case "acceptEdits":
		args = append(args, "--sandbox", "workspace-write")
	default:
		args = append(args, "--sandbox", "read-only")
	}

	for _, dir := range req.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	return binaryPath + " " + strings.Join(args, " ")
}

func (p *CodexProfile) DetectState(capture string) ScreenState {
	lastLines := lastNonEmptyLines(capture, 5)
	if len(lastLines) == 0 {
		return StateUnknown
	}

	bottom := strings.Join(lastLines, "\n")
	bottomLower := strings.ToLower(bottom)

	for _, pat := range []string{"(y/n)", "approve", "allow"} {
		if strings.Contains(bottomLower, pat) {
			return StateApproval
		}
	}

	lastLine := lastLines[len(lastLines)-1]
	if IsShellPrompt(lastLine) {
		return StateDone
	}

	lastLineLower := strings.ToLower(lastLine)
	if strings.HasPrefix(strings.TrimSpace(lastLineLower), ">") ||
		strings.Contains(lastLineLower, "what would you like") {
		return StateWaiting
	}

	return StateWorking
}

func (p *CodexProfile) ApproveKeys() []string { return []string{"y", "Enter"} }
func (p *CodexProfile) RejectKeys() []string  { return []string{"n", "Enter"} }

// --- Question & Subagent Parsing ---

func detectQuestionBlock(capture string) bool {
	lines := strings.Split(capture, "\n")

	i := len(lines) - 1
	for i >= 0 && strings.TrimSpace(lines[i]) == "" {
		i--
	}
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.Contains(trimmed, "───") || strings.Contains(trimmed, "⏵") ||
			isStatusBarEmoji(trimmed) || isHintOrChipLine(trimmed) {
			i--
			continue
		}
		break
	}

	optionCount := 0
	hasCursor := false
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			break
		}
		if isHintOrChipLine(trimmed) {
			i--
			continue
		}
		if (strings.HasPrefix(trimmed, "❯ ") || strings.HasPrefix(trimmed, "❯\u00a0")) && len(trimmed) > 3 {
			hasCursor = true
			optionCount++
			i--
			continue
		}
		if len(lines[i]) > 0 && (lines[i][0] == ' ' || lines[i][0] == '\t') &&
			len(trimmed) < 100 && !strings.Contains(trimmed, "───") &&
			!strings.HasPrefix(trimmed, "❯") && !strings.HasPrefix(trimmed, "?") {
			optionCount++
			i--
			continue
		}
		break
	}

	if !hasCursor || optionCount < 2 {
		return false
	}

	if i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "? ") && len(trimmed) > 3 {
			return true
		}
	}
	return false
}

// --- Shared Utilities ---

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func lastNonEmptyLines(capture string, n int) []string {
	if capture == "" {
		return nil
	}
	lines := strings.Split(capture, "\n")
	var result []string
	for i := len(lines) - 1; i >= 0 && len(result) < n; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			result = append([]string{trimmed}, result...)
		}
	}
	return result
}

func isStatusBarEmoji(trimmed string) bool {
	return strings.Contains(trimmed, "🤖") || strings.Contains(trimmed, "📝") ||
		strings.Contains(trimmed, "🆔") || strings.Contains(trimmed, "💻") ||
		strings.Contains(trimmed, "📁") || strings.Contains(trimmed, "⏰")
}

func isHintOrChipLine(s string) bool {
	if strings.Contains(s, "←") && strings.Contains(s, "→") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "↑/↓") ||
		strings.Contains(lower, "enter to select") ||
		strings.Contains(lower, "esc to cancel") ||
		strings.Contains(lower, "space toggles")
}
