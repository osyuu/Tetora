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
	"runtime"
	"strings"
	"time"

	"tetora/internal/cli"
)

var tetoraVersion = "dev"

func printUsage() {
	fmt.Fprintf(os.Stderr, `tetora v%s — AI Agent Orchestrator

Usage:
  tetora <command> [options]

Commands:
  serve              Start daemon (Telegram + Slack + HTTP + Cron)
  run                Dispatch tasks (CLI mode)
  dispatch           Run an ad-hoc task via the daemon
  route              Smart dispatch (auto-route to best agent)
  init               Interactive setup wizard
  doctor             Setup checks and diagnostics
  health             Runtime health (daemon, workers, taskboard, disk)
  status             Quick overview (daemon, jobs, cost)
  drain              Graceful shutdown: stop new tasks, wait for running agents to finish
  service <action>   Manage launchd service (install|uninstall|status)
  job <action>       Manage cron jobs (list|add|enable|disable|remove|trigger)
  agent <action>     Manage agents (list|add|show|set|remove)
  history <action>   View execution history (list|show|cost)
  config <action>    Manage config (show|set|validate|migrate)
  logs               View daemon logs ([-f] [-n N] [--err] [--trace ID] [--json])
  prompt <action>    Manage prompt templates (list|show|add|edit|remove)
  memory <action>    Manage agent memory (list|get|set|delete [--agent AGENT])
  mcp <action>       Manage MCP configs (list|show|add|remove|test)
  session <action>   View agent sessions (list|show)
  knowledge <action> Manage knowledge base (list|add|remove|path)
  skill <action>     Manage skills (list|run|test)
  workflow <action>  Manage workflows (list|show|validate|create|delete)
  budget <action>    Cost governance (show|pause|resume)
  webhook <action>   Manage incoming webhooks (list|show|test)
  data <action>      Data retention & privacy (status|cleanup|export|purge)
  security <action>  Security scanning (scan|baseline)
  plugin <action>    Manage external plugins (list|start|stop)
  access <action>    Manage agent directory access (list|add|remove)
  import <source>    Import data (config)
  release            Build, tag, and publish a release (atomic pipeline)
  upgrade [--force]  Upgrade to the latest release version
  backup             Create backup of tetora data
  restore            Restore from a backup file
  dashboard          Open web dashboard in browser
  completion <shell> Generate shell completion (bash|zsh|fish)
  version            Show version

Examples:
  tetora init                          Create config interactively
  tetora serve                         Start daemon
  tetora dispatch "Summarize README"    Run ad-hoc task via daemon
  tetora route "Review code security"  Auto-route to best agent
  tetora run --file tasks.json         Dispatch tasks from file
  tetora job list                      List all cron jobs
  tetora job trigger heartbeat         Manually trigger a job
  tetora agent list                    List all agents
  tetora agent add                     Create a new agent (interactive)
  tetora agent show <name>             Show agent details + soul preview
  tetora agent set <name> <field> <val> Update agent field (model, permission, description)
  tetora agent remove <name>           Remove an agent
  tetora history list                  Show recent execution history
  tetora history cost                  Show cost summary
  tetora config migrate --dry-run      Preview config migrations
  tetora session list                  List recent sessions
  tetora session list --agent <name>   List sessions for a specific agent
  tetora session show <id>            Show session conversation
  tetora backup                        Create backup
  tetora restore backup.tar.gz         Restore from backup
  tetora service install               Install as launchd service

`, tetoraVersion)
}

func cmdVersion() {
	fmt.Printf("tetora v%s (%s/%s)\n", tetoraVersion, runtime.GOOS, runtime.GOARCH)
}

// parseVersion splits a version string like "2.0.3" or "2.0.3.1" into numeric parts.
// Returns nil on parse failure.
func parseVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if v == "" || v == "dev" {
		return nil
	}
	parts := strings.Split(v, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return nil
			}
			n = n*10 + int(ch-'0')
		}
		nums[i] = n
	}
	return nums
}

// isDevVersion returns true if the version has a 4th segment (e.g. "2.0.3.1").
func isDevVersion(v string) bool {
	parts := parseVersion(v)
	return len(parts) > 3
}

// devBaseVersion returns the first 3 segments of a version string.
// e.g. "2.0.3.1" → "2.0.3", "2.0.3" → "2.0.3".
func devBaseVersion(v string) string {
	parts := parseVersion(v)
	if len(parts) < 3 {
		return v
	}
	return fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2])
}

// versionNewerThan returns true if a > b using semver comparison.
// A 3-part release (2.0.3) is considered newer than a 4-part dev (2.0.2.12).
// A release (2.0.3) is considered newer than a dev of the same base (2.0.3.1)
// because dev builds should always upgrade to matching releases.
func versionNewerThan(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	if pa == nil || pb == nil {
		return a != b // fallback: if unparseable, assume different = newer
	}
	// Compare up to the shorter length.
	maxLen := len(pa)
	if len(pb) > maxLen {
		maxLen = len(pb)
	}
	for i := 0; i < maxLen; i++ {
		va, vb := 0, 0
		if i < len(pa) {
			va = pa[i]
		}
		if i < len(pb) {
			vb = pb[i]
		}
		if va > vb {
			return true
		}
		if va < vb {
			return false
		}
	}
	return false // equal
}

func cmdUpgrade(args []string) {
	force := false
	for _, arg := range args {
		if arg == "--force" || arg == "-f" {
			force = true
		}
	}

	fmt.Printf("Current: v%s (%s/%s)\n", tetoraVersion, runtime.GOOS, runtime.GOARCH)
	if isDevVersion(tetoraVersion) {
		fmt.Println("  (dev build detected)")
	}

	// Fetch latest release tag from GitHub API.
	ghClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := ghClient.Get("https://api.github.com/repos/TakumaLee/Tetora/releases/latest")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking latest release: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Error checking latest release: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing release info: %v\n", err)
		os.Exit(1)
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == "" {
		fmt.Fprintf(os.Stderr, "Error: release tag_name is empty\n")
		os.Exit(1)
	}
	fmt.Printf("Latest:  v%s\n", latest)

	if !force {
		if latest == tetoraVersion {
			fmt.Println("Already up to date.")
			return
		}
		if isDevVersion(tetoraVersion) {
			// Dev build: upgrade if release matches or is newer than the base.
			// e.g. 2.0.3.1 → 2.0.3 (same base, upgrade to release) ✓
			// e.g. 2.0.2.12 → 2.0.3 (newer release) ✓
			// e.g. 2.0.4.1 → 2.0.3 (older release, skip) ✗
			base := devBaseVersion(tetoraVersion)
			if base != latest && !versionNewerThan(latest, base) {
				fmt.Printf("Dev build v%s is ahead of release v%s. Use --force to downgrade.\n", tetoraVersion, latest)
				return
			}
		} else if !versionNewerThan(latest, tetoraVersion) {
			fmt.Println("Already up to date (or newer). Use --force to override.")
			return
		}
	} else {
		fmt.Println("  (--force: skipping version check)")
	}

	// Determine binary name.
	arch := runtime.GOARCH
	binaryName := fmt.Sprintf("tetora-%s-%s", runtime.GOOS, arch)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	dlURL := fmt.Sprintf("https://github.com/TakumaLee/Tetora/releases/download/%s/%s", release.TagName, binaryName)

	fmt.Printf("Downloading %s...\n", dlURL)
	dlClient := &http.Client{Timeout: 120 * time.Second} // binary ~15MB, allow time for slow connections
	dlResp, err := dlClient.Get(dlURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Download failed: HTTP %d\n", dlResp.StatusCode)
		os.Exit(1)
	}

	// Write to temp file then replace.
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine binary path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	tmpPath := selfPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create temp file: %v\n", err)
		os.Exit(1)
	}
	if _, err := io.Copy(tmp, dlResp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		os.Exit(1)
	}
	tmp.Close()

	// Verify downloaded binary: size sanity check + run it to confirm version.
	info, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Cannot stat downloaded binary: %v\n", err)
		os.Exit(1)
	}
	if info.Size() < 1024*1024 {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Downloaded binary too small (%d bytes)\n", info.Size())
		os.Exit(1)
	}

	// Run the temp binary to verify its embedded version before replacing.
	verCtx, verCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer verCancel()
	verOut, err := exec.CommandContext(verCtx, tmpPath, "version").Output()
	if err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Downloaded binary failed to execute: %v\n", err)
		os.Exit(1)
	}
	verStr := strings.TrimSpace(string(verOut))
	fmt.Printf("Downloaded binary reports: %s\n", verStr)
	if !strings.Contains(verStr, latest) {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Version mismatch: expected v%s in output %q\n", latest, verStr)
		os.Exit(1)
	}

	// Replace old binary.
	if err := os.Rename(tmpPath, selfPath); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "Cannot replace binary: %v\n", err)
		os.Exit(1)
	}

	// Post-replace verification: confirm the file on disk is the new version.
	postOut, err := exec.Command(selfPath, "version").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: replaced binary but post-check failed: %v\n", err)
	} else {
		postStr := strings.TrimSpace(string(postOut))
		if !strings.Contains(postStr, latest) {
			fmt.Fprintf(os.Stderr, "WARNING: binary replaced but still reports %q (expected v%s)\n", postStr, latest)
			fmt.Fprintf(os.Stderr, "The file at %s may not have been updated. Try: tetora upgrade --force\n", selfPath)
		} else {
			fmt.Printf("Verified: %s\n", postStr)
		}
	}

	fmt.Printf("Binary replaced: v%s → v%s (%s)\n", tetoraVersion, latest, selfPath)

	// Check for running jobs before restarting — avoid killing in-flight tasks.
	if names := checkRunningJobs(); len(names) > 0 {
		fmt.Printf("\nWARNING: Running jobs detected: %s\n", strings.Join(names, ", "))
		fmt.Println("Binary on disk is updated, but daemon still runs the old version.")
		fmt.Println("Run 'tetora restart' manually when jobs finish.")
		return
	}

	// Restart daemon automatically.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.tetora.daemon.plist")
	if _, err := os.Stat(plist); err == nil {
		fmt.Println("Restarting service (launchd)...")
		if err := cli.RestartLaunchd(plist); err != nil {
			fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
			fmt.Println("Start manually with: tetora serve")
			return
		}
		waitForHealthy()
		return
	}

	// No launchd — find running daemon process and restart it.
	if restartDaemonProcess(selfPath) {
		waitForHealthy()
		return
	}

	fmt.Println("\nNo running daemon found. Start with:")
	fmt.Println("  tetora serve")
}

// restartDaemonProcess finds a running "tetora serve" process, kills it,
// and starts a new one in the background. Returns true if restart succeeded.
func restartDaemonProcess(binaryPath string) bool {
	// Check if there's a running daemon to restart.
	if len(cli.FindDaemonPIDs()) == 0 {
		return false
	}

	if !cli.KillDaemonProcess() {
		fmt.Fprintf(os.Stderr, "ERROR: could not stop old daemon, aborting restart\n")
		return true // old daemon exists but we couldn't kill it
	}

	// Start new daemon in background.
	fmt.Println("Starting daemon...")
	cmd := exec.Command(binaryPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		fmt.Println("Start manually with: tetora serve")
		return true // still return true since we killed the old one
	}

	// Release the child so it doesn't become a zombie.
	cmd.Process.Release()

	fmt.Printf("Daemon restarted (PID %d).\n", cmd.Process.Pid)
	return true
}

// cmdStart starts the tetora daemon if it is not already running.
func cmdStart() {
	out, _ := exec.Command("pgrep", "-f", "tetora serve").Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Println("Daemon is already running.")
		return
	}
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)
	fmt.Println("Starting daemon...")
	cmd := exec.Command(selfPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start daemon: %v\n", err)
		os.Exit(1)
	}
	cmd.Process.Release()
	fmt.Printf("Daemon started (PID %d).\n", cmd.Process.Pid)
	waitForHealthy()
}

// cmdRestart restarts the running tetora daemon.
func cmdRestart() {
	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	// Try launchd first.
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.tetora.daemon.plist")
	if _, err := os.Stat(plist); err == nil {
		fmt.Println("Restarting service (launchd)...")
		if err := cli.RestartLaunchd(plist); err != nil {
			fmt.Fprintf(os.Stderr, "Restart failed: %v\n", err)
			fmt.Println("Start manually with: tetora serve")
			return
		}
		waitForHealthy()
		return
	}

	// No launchd — find running daemon process and restart it.
	if restartDaemonProcess(selfPath) {
		waitForHealthy()
		return
	}

	fmt.Println("No running daemon found. Start with:")
	fmt.Println("  tetora serve")
}

// waitForHealthy polls /healthz for up to 10 seconds after a restart to confirm
// the daemon is up. Prints version on success or a warning on timeout.
func waitForHealthy() {
	cfg, _ := tryLoadConfig("")
	if cfg == nil || cfg.ListenAddr == "" {
		fmt.Println("Service restarted.")
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://%s/healthz", cfg.ListenAddr)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		var health map[string]any
		json.NewDecoder(resp.Body).Decode(&health)
		resp.Body.Close()
		if v, ok := health["version"].(string); ok {
			fmt.Printf("Service restarted. Health check: v%s OK\n", v)
			return
		}
	}
	fmt.Println("Service restarted (health check timed out — daemon may still be starting).")
}

// checkRunningJobs queries the daemon's /cron API and returns names of running jobs.
// Returns nil if daemon is unreachable or no jobs are running.
func checkRunningJobs() []string {
	cfg, _ := tryLoadConfig("")
	if cfg == nil || cfg.ListenAddr == "" {
		return nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://%s/cron", cfg.ListenAddr)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	if cfg.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var jobs []CronJobInfo
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil
	}
	var running []string
	for _, j := range jobs {
		if j.Running {
			running = append(running, j.Name)
		}
	}
	return running
}

func cmdOpenDashboard() {
	cfg := loadConfig("")
	url := fmt.Sprintf("http://%s/dashboard", cfg.ListenAddr)
	fmt.Printf("Opening %s\n", url)
	exec.Command("open", url).Start()
}

// apiClient creates an HTTP client and base URL for daemon communication.
// Includes API token from config if set.
type apiClient struct {
	client  *http.Client
	baseURL string
	token   string
}

func newAPIClient(cfg *Config) *apiClient {
	return &apiClient{
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: fmt.Sprintf("http://%s", cfg.ListenAddr),
		token:   cfg.APIToken,
	}
}

func (c *apiClient) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.client.Do(req)
}

func (c *apiClient) get(path string) (*http.Response, error) {
	return c.do("GET", path, nil)
}

func (c *apiClient) post(path string, body string) (*http.Response, error) {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	return c.do("POST", path, r)
}

func (c *apiClient) postJSON(path string, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return c.do("POST", path, strings.NewReader(string(b)))
}

func findConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "config.json")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, ".tetora", "config.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "config.json"
}
