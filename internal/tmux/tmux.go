// Package tmux provides low-level tmux session management primitives.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Bin returns the tmux binary path, resolving via common locations for
// environments with minimal PATH (e.g. launchd on macOS).
func Bin() string {
	for _, p := range []string{
		"/opt/homebrew/bin/tmux",
		"/usr/local/bin/tmux",
		"/usr/bin/tmux",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "tmux"
}

// Create creates a new tmux session with the given dimensions and command.
func Create(name string, cols, rows int, command, workdir string) error {
	args := []string{
		"new-session", "-d",
		"-s", name,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}
	if command != "" {
		args = append(args, command)
	}
	cmd := exec.Command(Bin(), args...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	cmd.Env = FilteredEnvForCLI()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, string(out))
	}
	return nil
}

// Capture captures the current visible content of a tmux pane (clean text, no ANSI).
func Capture(name string) (string, error) {
	cmd := exec.Command(Bin(), "capture-pane", "-t", name, "-p")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// CaptureHistory captures the full scrollback history of a tmux pane.
func CaptureHistory(name string) (string, error) {
	cmd := exec.Command(Bin(), "capture-pane", "-t", name, "-p", "-S", "-")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane history: %w", err)
	}
	return string(out), nil
}

// SendKeys sends key names (Up, Down, Enter, etc.) to a tmux session.
func SendKeys(name string, keys ...string) error {
	args := append([]string{"send-keys", "-t", name}, keys...)
	cmd := exec.Command(Bin(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, string(out))
	}
	return nil
}

// SendText sends literal text to a tmux session (uses -l flag).
func SendText(name string, text string) error {
	cmd := exec.Command(Bin(), "send-keys", "-t", name, "-l", text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys -l: %w: %s", err, string(out))
	}
	return nil
}

// LoadAndPaste sends arbitrary-length text via load-buffer + paste-buffer.
func LoadAndPaste(name, text string) error {
	f, err := os.CreateTemp("", "tetora-prompt-*.txt")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()
	defer os.Remove(tmpPath)
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	f.Close()

	cmd := exec.Command(Bin(), "load-buffer", tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %w: %s", err, string(out))
	}

	cmd = exec.Command(Bin(), "paste-buffer", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux paste-buffer: %w: %s", err, string(out))
	}
	return nil
}

// Kill kills a tmux session.
func Kill(name string) error {
	cmd := exec.Command(Bin(), "kill-session", "-t", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, string(out))
	}
	return nil
}

// HasSession checks if a tmux session exists.
func HasSession(name string) bool {
	cmd := exec.Command(Bin(), "has-session", "-t", name)
	return cmd.Run() == nil
}

// ListSessions returns the names of all active tmux sessions.
func ListSessions() []string {
	cmd := exec.Command(Bin(), "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}

// FilteredEnvForCLI returns os.Environ() with Claude Code session env vars removed.
func FilteredEnvForCLI() []string {
	rawEnv := os.Environ()
	filtered := make([]string, 0, len(rawEnv))
	for _, e := range rawEnv {
		if !strings.HasPrefix(e, "CLAUDECODE=") &&
			!strings.HasPrefix(e, "CLAUDE_CODE_ENTRYPOINT=") &&
			!strings.HasPrefix(e, "CLAUDE_CODE_TEAM_MODE=") {
			filtered = append(filtered, e)
		}
	}
	return EnsurePathDirs(filtered,
		"/opt/homebrew/bin", "/opt/homebrew/sbin",
		"/usr/local/bin",
		os.Getenv("HOME")+"/.nvm/versions/node",
	)
}

// EnsurePathDirs adds directories to the PATH env var if they exist and aren't already present.
func EnsurePathDirs(env []string, dirs ...string) []string {
	pathIdx := -1
	pathVal := ""
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			pathIdx = i
			pathVal = e[5:]
			break
		}
	}
	if pathIdx < 0 {
		return env
	}
	parts := strings.Split(pathVal, ":")
	existing := make(map[string]bool, len(parts))
	for _, p := range parts {
		existing[p] = true
	}
	for _, d := range dirs {
		if existing[d] {
			continue
		}
		if strings.Contains(d, ".nvm/versions/node") {
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			for i := len(entries) - 1; i >= 0; i-- {
				binDir := d + "/" + entries[i].Name() + "/bin"
				if _, err := os.Stat(binDir); err == nil && !existing[binDir] {
					parts = append(parts, binDir)
					existing[binDir] = true
					break
				}
			}
			continue
		}
		if _, err := os.Stat(d); err == nil {
			parts = append(parts, d)
			existing[d] = true
		}
	}
	env[pathIdx] = "PATH=" + strings.Join(parts, ":")
	return env
}
