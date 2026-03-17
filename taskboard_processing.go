package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

// =============================================================================
// Section: Post-Task Git Operations (from taskboard_git.go)
// =============================================================================

// postTaskWorkspaceGit commits workspace changes after a task completes (done or failed).
// The workspace (memory, rules, knowledge, skills, agent todo.md) is modified during task
// execution. This ensures those changes are tracked in git with the task ID in the commit message.
func (d *TaskBoardDispatcher) postTaskWorkspaceGit(t TaskBoard) {
	wsDir := d.cfg.WorkspaceDir
	if wsDir == "" {
		return
	}

	// Verify workspace is a git repo.
	if err := exec.Command("git", "-C", wsDir, "rev-parse", "--git-dir").Run(); err != nil {
		return
	}

	// Clean stale lock before git operations.
	cleanStaleLock(wsDir, t.ID, d.engine)

	// Check for uncommitted changes.
	statusOut, err := exec.Command("git", "-C", wsDir, "status", "--porcelain").Output()
	if err != nil {
		log.Warn("postTaskWorkspaceGit: git status failed", "task", t.ID, "error", err)
		d.engine.AddComment(t.ID, "system", "[WARNING] workspace git status failed: "+err.Error())
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		return
	}

	if out, err := exec.Command("git", "-C", wsDir, "add", "-A").CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[WARNING] workspace git add failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskWorkspaceGit: git add failed", "task", t.ID, "error", string(out))
		d.engine.AddComment(t.ID, "system", msg)
		if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
			log.Warn("postTaskWorkspaceGit: failed to move to partial-done", "task", t.ID, "error", moveErr)
		}
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[WARNING] workspace git commit failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskWorkspaceGit: git commit failed", "task", t.ID, "error", string(out))
		d.engine.AddComment(t.ID, "system", msg)
		if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
			log.Warn("postTaskWorkspaceGit: failed to move to partial-done", "task", t.ID, "error", moveErr)
		}
		return
	}

	log.Info("postTaskWorkspaceGit: committed workspace changes", "task", t.ID)
}

// postTaskGit commits and optionally pushes changes after a task completes.
// Only runs when gitCommit is enabled, the task has a project with a workdir
// that is a git repo, and there are uncommitted changes.
func (d *TaskBoardDispatcher) postTaskGit(t TaskBoard) {
	if !d.engine.config.GitCommit {
		return
	}
	if t.Project == "" || t.Project == "default" {
		return
	}
	if t.Assignee == "" {
		return
	}

	p, err := getProject(d.cfg.HistoryDB, t.Project)
	if err != nil || p == nil || p.Workdir == "" {
		return
	}
	workdir := p.Workdir

	// Verify workdir is a git repo.
	if err := exec.Command("git", "-C", workdir, "rev-parse", "--git-dir").Run(); err != nil {
		return
	}

	// Check for uncommitted changes.
	statusOut, err := exec.Command("git", "-C", workdir, "status", "--porcelain").Output()
	if err != nil {
		log.Warn("postTaskGit: git status failed", "task", t.ID, "error", err)
		return
	}
	if len(bytes.TrimSpace(statusOut)) == 0 {
		log.Info("postTaskGit: no changes to commit", "task", t.ID, "project", t.Project)
		return
	}

	// Branch name from configured convention.
	branch := buildBranchName(d.engine.config.GitWorkflow, t)

	if out, err := exec.Command("git", "-C", workdir, "checkout", "-B", branch).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] checkout -B %s failed: %s", branch, strings.TrimSpace(string(out)))
		log.Warn("postTaskGit: checkout failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	if out, err := exec.Command("git", "-C", workdir, "add", "-A").CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] add -A failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGit: add failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
	if out, err := exec.Command("git", "-C", workdir, "commit", "-m", commitMsg).CombinedOutput(); err != nil {
		msg := fmt.Sprintf("[post-task-git] commit failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGit: commit failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	log.Info("postTaskGit: committed", "task", t.ID, "branch", branch)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] Committed to branch %s", branch))

	// Capture full diff for review panel.
	baseBranch := detectDefaultBranch(workdir)
	diffOut, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()
	if diff := string(diffOut); diff != "" {
		if len(diff) > 100000 {
			diff = diff[:100000] + "\n... (truncated)"
		}
		d.engine.AddComment(t.ID, "system", diff, "diff")
	}

	// Push if enabled.
	if d.engine.config.GitPush {
		if out, err := exec.Command("git", "-C", workdir, "push", "-u", "origin", branch).CombinedOutput(); err != nil {
			msg := fmt.Sprintf("[post-task-git] push failed: %s", strings.TrimSpace(string(out)))
			log.Warn("postTaskGit: push failed", "task", t.ID, "error", msg)
			d.engine.AddComment(t.ID, "system", msg)
			return
		}
		log.Info("postTaskGit: pushed", "task", t.ID, "branch", branch)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] Pushed to origin/%s", branch))

		// Auto-create PR if enabled.
		if d.engine.config.GitPR {
			d.postTaskGitPR(t, workdir, branch)
		}
	}
}

// postTaskWorktree handles the worktree lifecycle after a task completes:
//   - done/review: commits any uncommitted agent changes, merges into main, logs diff
//   - failed/cancelled: discards the worktree without merging
//   - always: removes the worktree directory
//
// This is always called even on failure so the worktree doesn't accumulate on disk.
func (d *TaskBoardDispatcher) postTaskWorktree(t TaskBoard, projectWorkdir, worktreeDir, newStatus string) {
	if worktreeDir == "" || projectWorkdir == "" {
		return
	}

	// Track whether merge succeeded — only cleanup on success.
	mergeOK := false
	defer func() {
		if mergeOK {
			if err := d.worktreeMgr.Remove(projectWorkdir, worktreeDir); err != nil {
				log.Warn("worktree: cleanup failed", "task", t.ID, "path", worktreeDir, "error", err)
				d.engine.AddComment(t.ID, "system",
					fmt.Sprintf("[worktree] Cleanup failed: %v", err))
			} else {
				log.Info("worktree: cleaned up", "task", t.ID, "path", worktreeDir)
			}
		} else if newStatus == "done" || newStatus == "review" {
			// Merge failed — preserve worktree for manual recovery.
			log.Warn("worktree: preserved for recovery", "task", t.ID, "path", worktreeDir)
		}
	}()

	switch newStatus {
	case "done", "review":
		commitCount := d.worktreeMgr.CommitCount(worktreeDir)
		hasChanges := d.worktreeMgr.HasChanges(worktreeDir)

		if commitCount == 0 && !hasChanges {
			mergeOK = true // nothing to preserve
			d.engine.AddComment(t.ID, "system",
				"[worktree] No changes committed. Worktree discarded.")
			return
		}

		// Get full diff for review panel (before merge destroys the branch diff).
		d.captureTaskDiff(t, projectWorkdir, worktreeDir)

		// Commit any uncommitted changes, then merge.
		commitMsg := fmt.Sprintf("[%s] %s", t.ID, t.Title)
		diffSummary, err := d.worktreeMgr.Merge(projectWorkdir, worktreeDir, commitMsg)
		if err != nil {
			log.Warn("worktree: merge failed", "task", t.ID, "error", err)
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[worktree] ⚠️ Merge failed: %v\nBranch preserved: task/%s\nWorktree preserved: %s\nRecover manually: git -C %s merge task/%s",
					err, t.ID, worktreeDir, projectWorkdir, t.ID))
			// Move task to partial-done so it's visible in triage.
			if _, moveErr := d.engine.MoveTask(t.ID, "partial-done"); moveErr != nil {
				log.Warn("worktree: failed to move to partial-done", "task", t.ID, "error", moveErr)
			}
			return // mergeOK stays false → worktree preserved
		}

		mergeOK = true
		comment := "[worktree] Changes merged into main."
		if diffSummary != "" {
			comment += "\n```\n" + diffSummary + "\n```"
		}
		d.engine.AddComment(t.ID, "system", comment)
		log.Info("worktree: merge complete", "task", t.ID)

	default: // failed, cancelled
		mergeOK = true // discard worktree on failure (no data to preserve)
		d.engine.AddComment(t.ID, "system",
			"[worktree] Task failed — worktree discarded without merge.")
	}
}

// captureTaskDiff captures the full unified diff for the review panel.
// Stored as a type="diff" comment so it survives worktree removal.
func (d *TaskBoardDispatcher) captureTaskDiff(t TaskBoard, repoDir, wtDir string) string {
	if wtDir == "" {
		return ""
	}
	taskID := filepath.Base(wtDir)
	branch := "task/" + taskID
	baseBranch := detectDefaultBranch(repoDir)

	// Get merge base.
	mergeBase, err := exec.Command("git", "-C", wtDir, "merge-base", baseBranch, branch).Output()
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(string(mergeBase))

	// Get full unified diff.
	diffOut, err := exec.Command("git", "-C", wtDir, "diff", base+"..."+branch).Output()
	if err != nil {
		return ""
	}

	diff := string(diffOut)
	if len(diff) > 100000 { // 100KB cap
		diff = diff[:100000] + "\n... (truncated, diff too large)"
	}

	if diff != "" {
		d.engine.AddComment(t.ID, "system", diff, "diff")
	}
	return diff
}

// prDescSem limits concurrent PR/MR description generation LLM calls.
var prDescSem = make(chan struct{}, 2)

// detectRemoteHost inspects the origin remote URL and returns "github", "gitlab", or "unknown".
func detectRemoteHost(workdir string) string {
	out, err := exec.Command("git", "-C", workdir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "unknown"
	}
	url := strings.ToLower(strings.TrimSpace(string(out)))
	switch {
	case strings.Contains(url, "github.com"):
		return "github"
	case strings.Contains(url, "gitlab"):
		return "gitlab"
	default:
		return "unknown"
	}
}

// postTaskGitPR is the entry point for auto-creating a PR or MR after a task push.
// It detects whether the remote is GitHub or GitLab and delegates accordingly.
func (d *TaskBoardDispatcher) postTaskGitPR(t TaskBoard, workdir, branch string) {
	host := detectRemoteHost(workdir)
	switch host {
	case "github":
		d.postTaskGitHubPR(t, workdir, branch)
	case "gitlab":
		d.postTaskGitLabMR(t, workdir, branch)
	default:
		log.Warn("postTaskGitPR: remote host not recognized, skipping PR/MR creation", "task", t.ID)
		d.engine.AddComment(t.ID, "system", "[post-task-git] Remote host not recognized (not GitHub or GitLab). Skipping PR/MR creation.")
	}
}

// postTaskGitHubPR creates a GitHub PR with an AI-generated title and description.
func (d *TaskBoardDispatcher) postTaskGitHubPR(t TaskBoard, workdir, branch string) {
	// Detect the default branch (main or master).
	baseBranch := detectDefaultBranch(workdir)

	// Check if a PR already exists for this branch.
	prViewCmd := exec.Command("gh", "pr", "view", branch, "--json", "url", "-q", ".url")
	prViewCmd.Dir = workdir
	existingPR, _ := prViewCmd.Output()
	if url := strings.TrimSpace(string(existingPR)); url != "" {
		log.Info("postTaskGitHubPR: PR already exists", "task", t.ID, "url", url)
		d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] PR already exists: %s", url))
		return
	}

	// Gather diff for LLM context.
	diffOut, err := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch, "--stat").Output()
	if err != nil {
		log.Warn("postTaskGitHubPR: diff stat failed", "task", t.ID, "error", err)
	}
	diffDetail, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()

	// Gather commit log.
	logOut, _ := exec.Command("git", "-C", workdir, "log", baseBranch+".."+branch, "--oneline").Output()

	// Generate PR title and body via LLM.
	title, body := d.generatePRDescription(t, string(diffOut), string(diffDetail), string(logOut))

	// Create PR via gh CLI.
	args := []string{"pr", "create",
		"--head", branch,
		"--base", baseBranch,
		"--title", title,
		"--body", body,
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("[post-task-git] PR creation failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGitHubPR: gh pr create failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	prURL := strings.TrimSpace(string(out))
	log.Info("postTaskGitHubPR: PR created", "task", t.ID, "url", prURL)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] PR created: %s", prURL))
}

// postTaskGitLabMR creates a GitLab MR with an AI-generated title and description.
func (d *TaskBoardDispatcher) postTaskGitLabMR(t TaskBoard, workdir, branch string) {
	// Detect the default branch (main or master).
	baseBranch := detectDefaultBranch(workdir)

	// Check if an MR already exists for this branch.
	mrViewCmd := exec.Command("glab", "mr", "view", branch)
	mrViewCmd.Dir = workdir
	mrViewOut, mrViewErr := mrViewCmd.Output()
	if mrViewErr == nil && len(strings.TrimSpace(string(mrViewOut))) > 0 {
		// glab mr view exits 0 and prints details when the MR exists.
		// Extract the web URL from the output if present, otherwise just note it exists.
		url := ""
		for _, line := range strings.Split(string(mrViewOut), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "https://") {
				url = strings.TrimSpace(line)
				break
			}
		}
		msg := "[post-task-git] MR already exists"
		if url != "" {
			msg += ": " + url
		}
		log.Info("postTaskGitLabMR: MR already exists", "task", t.ID, "url", url)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	// Gather diff for LLM context.
	diffOut, err := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch, "--stat").Output()
	if err != nil {
		log.Warn("postTaskGitLabMR: diff stat failed", "task", t.ID, "error", err)
	}
	diffDetail, _ := exec.Command("git", "-C", workdir, "diff", baseBranch+"..."+branch).Output()

	// Gather commit log.
	logOut, _ := exec.Command("git", "-C", workdir, "log", baseBranch+".."+branch, "--oneline").Output()

	// Generate MR title and body via LLM (reuses the same generator as GitHub).
	title, body := d.generatePRDescription(t, string(diffOut), string(diffDetail), string(logOut))

	// Create MR via glab CLI.
	args := []string{"mr", "create",
		"--head", branch,
		"--base", baseBranch,
		"--title", title,
		"--description", body,
		"--yes",
	}
	cmd := exec.Command("glab", args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("[post-task-git] MR creation failed: %s", strings.TrimSpace(string(out)))
		log.Warn("postTaskGitLabMR: glab mr create failed", "task", t.ID, "error", msg)
		d.engine.AddComment(t.ID, "system", msg)
		return
	}

	mrURL := strings.TrimSpace(string(out))
	log.Info("postTaskGitLabMR: MR created", "task", t.ID, "url", mrURL)
	d.engine.AddComment(t.ID, "system", fmt.Sprintf("[post-task-git] MR created: %s", mrURL))
}

// generatePRDescription uses a lightweight LLM call to generate a PR/MR title and body.
func (d *TaskBoardDispatcher) generatePRDescription(t TaskBoard, diffStat, diffDetail, commitLog string) (title, body string) {
	// Truncate diff detail to keep cost low.
	if len(diffDetail) > 6000 {
		diffDetail = diffDetail[:6000] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(`Generate a Pull Request / Merge Request title and description for the following changes.

Task: %s
Description: %s

Commits:
%s

Diff summary:
%s

Diff detail:
%s

Respond with a JSON object:
{"title": "short PR title (under 70 chars)", "body": "markdown PR description with ## Summary section (2-4 bullet points) and ## Changes section"}

Rules:
- Title should be concise and describe the change (not the task ID)
- Body should explain what changed and why
- Use markdown formatting in body
- Keep it professional and clear`,
		truncateStr(t.Title, 200),
		truncateStr(t.Description, 500),
		truncateStr(commitLog, 500),
		truncateStr(diffStat, 1000),
		diffDetail)

	task := Task{
		ID:             newUUID(),
		Name:           "pr-desc-" + t.ID,
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.05,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "pr-description",
	}
	fillDefaults(d.cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.05

	result := runSingleTask(d.ctx, d.cfg, task, prDescSem, nil, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		// Fallback: use task title and simple description.
		return fmt.Sprintf("[%s] %s", t.ID, t.Title), fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	// Parse JSON from output.
	raw := result.Output
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title), fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	var pr struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &pr); err != nil || pr.Title == "" {
		return fmt.Sprintf("[%s] %s", t.ID, t.Title), fmt.Sprintf("## Summary\n- %s\n\nAuto-generated by Tetora task %s", t.Title, t.ID)
	}

	// Append task reference to body.
	pr.Body += fmt.Sprintf("\n\n---\nTask: `%s` — %s", t.ID, t.Title)

	return pr.Title, pr.Body
}

// detectDefaultBranch returns the default branch name (main or master) for a repo.
func detectDefaultBranch(workdir string) string {
	// Try git symbolic-ref for the remote HEAD.
	out, err := exec.Command("git", "-C", workdir, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		if parts := strings.Split(ref, "/"); len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	// Fallback: check if main exists.
	if exec.Command("git", "-C", workdir, "rev-parse", "--verify", "main").Run() == nil {
		return "main"
	}
	return "master"
}

// cleanStaleLock removes stale .git/index.lock files that are older than 1 hour.
// A stale lock blocks all git operations and causes silent dispatch failures.
func cleanStaleLock(repoDir, taskID string, engine *TaskBoardEngine) {
	lockPath := filepath.Join(repoDir, ".git", "index.lock")
	info, err := os.Stat(lockPath)
	if err != nil {
		return // no lock file
	}

	age := time.Since(info.ModTime())
	if age < time.Hour {
		log.Warn("cleanStaleLock: index.lock exists but is recent, skipping",
			"task", taskID, "path", lockPath, "age", age.Round(time.Second))
		if engine != nil {
			engine.AddComment(taskID, "system",
				fmt.Sprintf("[WARNING] git index.lock exists (age: %s). Waiting for other git operation to finish.", age.Round(time.Second)))
		}
		return
	}

	if err := os.Remove(lockPath); err != nil {
		log.Warn("cleanStaleLock: failed to remove stale lock", "task", taskID, "path", lockPath, "error", err)
		return
	}

	log.Info("cleanStaleLock: removed stale index.lock", "task", taskID, "path", lockPath, "age", age.Round(time.Second))
	if engine != nil {
		engine.AddComment(taskID, "system",
			fmt.Sprintf("[auto-fix] Removed stale git index.lock (age: %s)", age.Round(time.Second)))
	}
}

// =============================================================================
// Section: Dev↔QA Review Loop (from taskboard_review.go)
// =============================================================================

// --- Dev↔QA Loop ---

// devQALoopResult holds the outcome of the Dev↔QA retry loop.
type devQALoopResult struct {
	Result     TaskResult
	QAApproved bool    // true if QA review passed
	Attempts   int     // total execution attempts
	TotalCost  float64 // accumulated cost across all attempts (dev + QA)
}

// devQALoop executes a task and runs automated QA review in a loop.
// If QA fails, the reviewer's feedback is injected into the prompt and the task is retried.
// After maxRetries QA failures, the task is escalated to human review.
//
// Failure injection integration:
//   - QA rejections are recorded to skill failures.md so future executions learn from them.
//   - On retry, existing skill failures are loaded and injected into the prompt.
//
// Flow: Dev execute → QA review → (pass → done) | (fail → record failure → inject feedback + failures → retry)
func (d *TaskBoardDispatcher) devQALoop(ctx context.Context, t TaskBoard, task Task, usedWorkflow bool, workflowName string) devQALoopResult {
	maxRetries := d.engine.config.MaxRetriesOrDefault() // default 3

	reviewer := d.engine.config.AutoDispatch.ReviewAgent
	if reviewer == "" {
		reviewer = "ruri"
	}

	// Preserve the original prompt so QA feedback doesn't compound across retries.
	originalPrompt := task.Prompt

	var accumulated float64

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Step 1: Dev execution.
		var result TaskResult
		if usedWorkflow {
			result = d.runTaskWithWorkflow(ctx, t, task, workflowName)
		} else {
			result = runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, t.Assignee)
		}
		accumulated += result.CostUSD

		// If execution itself failed (crash/timeout/empty output), exit loop.
		// The caller's existing AutoRetryFailed path handles execution failures.
		if result.Status != "success" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}
		if strings.TrimSpace(result.Output) == "" {
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 2: QA review.
		reviewOK, reviewComment, reviewCost := d.reviewTaskOutput(ctx, originalPrompt, result.Output, t.Assignee, reviewer)
		accumulated += reviewCost

		if reviewOK {
			log.Info("devQA: review passed", "task", t.ID, "attempt", attempt+1)
			d.engine.AddComment(t.ID, reviewer,
				fmt.Sprintf("[QA PASS] (attempt %d/%d) %s", attempt+1, maxRetries+1, reviewComment))
			return devQALoopResult{Result: result, QAApproved: true, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// QA failed.
		log.Info("devQA: review failed, injecting feedback",
			"task", t.ID, "attempt", attempt+1, "maxAttempts", maxRetries+1, "comment", truncate(reviewComment, 200))

		d.engine.AddComment(t.ID, reviewer,
			fmt.Sprintf("[QA FAIL] (attempt %d/%d) %s", attempt+1, maxRetries+1, reviewComment))

		// Record QA rejection as skill failure for future context injection.
		qaFailMsg := fmt.Sprintf("[QA rejection attempt %d] %s", attempt+1, reviewComment)
		d.postTaskSkillFailures(t, task, qaFailMsg)

		if attempt == maxRetries {
			// All retries exhausted — escalate.
			d.engine.AddComment(t.ID, "system",
				fmt.Sprintf("[ESCALATE] Dev↔QA loop exhausted (%d attempts). Escalating to human review.", maxRetries+1))
			log.Warn("devQA: max retries exhausted, escalating", "task", t.ID, "attempts", maxRetries+1)
			return devQALoopResult{Result: result, Attempts: attempt + 1, TotalCost: accumulated}
		}

		// Step 3: Rebuild prompt with QA feedback + skill failure context for retry.
		task.Prompt = originalPrompt

		// Inject accumulated skill failures (includes QA rejections just recorded).
		if failureCtx := d.loadSkillFailureContext(task); failureCtx != "" {
			task.Prompt += "\n\n## Previous Failure Context\n"
			task.Prompt += failureCtx
		}

		// Inject QA reviewer's specific feedback for this attempt.
		task.Prompt += fmt.Sprintf("\n\n## QA Review Feedback (Attempt %d)\n", attempt+1)
		task.Prompt += "The QA reviewer rejected the output. Issues found:\n"
		task.Prompt += reviewComment
		task.Prompt += fmt.Sprintf("\n\nAddress ALL issues above. This is retry %d of %d.\n", attempt+2, maxRetries+1)

		// New IDs for the retry execution (fresh session, no session bleed).
		task.ID = newUUID()
		task.SessionID = newUUID()
	}

	// Unreachable, but satisfy the compiler.
	return devQALoopResult{}
}

// loadSkillFailureContext loads failure context for all skills matching the task.
// Returns the combined failure context string, or empty if none.
func (d *TaskBoardDispatcher) loadSkillFailureContext(task Task) string {
	skills := selectSkills(d.cfg, task)
	if len(skills) == 0 {
		return ""
	}

	var parts []string
	for _, s := range skills {
		failures := loadSkillFailuresByName(d.cfg, s.Name)
		if failures == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("### Skill: %s\n%s", s.Name, failures))
	}
	if len(parts) == 0 {
		return ""
	}

	combined := strings.Join(parts, "\n\n")
	if len(combined) > skillFailuresMaxInject {
		combined = combined[:skillFailuresMaxInject] + "\n... (truncated)"
	}
	return combined
}

// reviewTaskOutput asks the configured review agent to evaluate task output quality.
// Uses the taskboard's ReviewAgent config with fallback to SmartDispatch.ReviewAgent.
// Returns (approved, comment, costUSD).
// reviewVerdict represents a three-way review decision.
type reviewVerdict string

const (
	reviewApprove  reviewVerdict = "approve"  // quality OK → done
	reviewFix      reviewVerdict = "fix"      // issues found but agent can fix → retry
	reviewEscalate reviewVerdict = "escalate" // needs human judgment → assign to user
)

type reviewResult struct {
	Verdict  reviewVerdict
	Comment  string
	CostUSD  float64
}

func (d *TaskBoardDispatcher) reviewTaskOutput(ctx context.Context, originalPrompt, output, agentRole, reviewer string) (bool, string, float64) {
	r := d.thoroughReview(ctx, originalPrompt, output, agentRole, reviewer)
	return r.Verdict == reviewApprove, r.Comment, r.CostUSD
}

// thoroughReview runs a comprehensive code quality review using sonnet/opus.
// Returns a three-way verdict: approve, fix (send back to agent), or escalate (needs human).
func (d *TaskBoardDispatcher) thoroughReview(ctx context.Context, originalPrompt, output, agentRole, reviewer string) reviewResult {
	reviewPrompt := fmt.Sprintf(
		`You are a senior staff engineer conducting a thorough code review.

## Original Task
%s

## Agent (%s) Output
%s

## Review Checklist
Evaluate ALL of the following:

1. **Correctness**: Does the output fully address the original request? Any logical errors?
2. **Completeness**: Any TODO, placeholder, stub, or unfinished work left behind?
3. **Code Quality**: Redundant code? Copy-paste duplication? Poor naming? Over-engineering?
4. **Efficiency**: Unnecessary allocations, O(n²) where O(n) is possible, repeated work?
5. **Security**: SQL injection, XSS, command injection, hardcoded secrets, path traversal?
6. **Error Handling**: Missing error checks? Silent failures? Panics on edge cases?
7. **Breaking Changes**: Will this break existing functionality? Missing backwards compatibility?
8. **File Size**: Any single file growing beyond reasonable review size (>1500 lines)?

## Verdict Rules
- **approve**: Code is production-ready. Minor style nits are OK — don't block for cosmetics.
- **fix**: Issues found that the original agent CAN fix autonomously (bugs, missing error handling, code quality). Give specific, actionable feedback.
- **escalate**: ONLY use this when you genuinely cannot determine correctness — e.g., the spec is ambiguous, critical domain knowledge is missing, or the change could break production in ways you can't verify. Do NOT escalate for fixable code issues.

Reply with ONLY a JSON object:
{"verdict":"approve","comment":"brief summary"}
{"verdict":"fix","comment":"specific issues the agent must fix (be actionable)"}
{"verdict":"escalate","comment":"why human judgment is needed (be specific)"}`,
		truncate(originalPrompt, 2000),
		agentRole,
		truncate(output, 6000),
	)

	task := Task{
		Prompt:  reviewPrompt,
		Timeout: "120s",
		Budget:  d.cfg.SmartDispatch.ReviewBudget,
		Source:  "auto-review",
	}
	fillDefaults(d.cfg, &task)

	// Use sonnet for review — thorough but cost-effective.
	task.Model = "sonnet"

	// Apply reviewer's soul prompt (but keep sonnet model).
	if soulPrompt, err := loadAgentPrompt(d.cfg, reviewer); err == nil && soulPrompt != "" {
		task.SystemPrompt = soulPrompt
	}
	if rc, ok := d.cfg.Agents[reviewer]; ok {
		if rc.PermissionMode != "" {
			task.PermissionMode = rc.PermissionMode
		}
		// Use reviewer's model if it's opus (upgrade from sonnet is OK).
		if rc.Model == "opus" {
			task.Model = "opus"
		}
	}

	result := runSingleTask(ctx, d.cfg, task, d.sem, d.childSem, reviewer)
	if result.Status != "success" {
		return reviewResult{Verdict: reviewEscalate, Comment: "review skipped (execution error) — needs manual check", CostUSD: result.CostUSD}
	}

	// Parse review JSON.
	start := strings.Index(result.Output, "{")
	end := strings.LastIndex(result.Output, "}")
	if start >= 0 && end > start {
		var parsed struct {
			Verdict string `json:"verdict"`
			Comment string `json:"comment"`
			// Legacy format support.
			OK bool `json:"ok"`
		}
		if json.Unmarshal([]byte(result.Output[start:end+1]), &parsed) == nil {
			switch parsed.Verdict {
			case "approve":
				return reviewResult{Verdict: reviewApprove, Comment: parsed.Comment, CostUSD: result.CostUSD}
			case "fix":
				return reviewResult{Verdict: reviewFix, Comment: parsed.Comment, CostUSD: result.CostUSD}
			case "escalate":
				return reviewResult{Verdict: reviewEscalate, Comment: parsed.Comment, CostUSD: result.CostUSD}
			default:
				// Legacy bool format fallback.
				if parsed.OK {
					return reviewResult{Verdict: reviewApprove, Comment: parsed.Comment, CostUSD: result.CostUSD}
				}
				return reviewResult{Verdict: reviewFix, Comment: parsed.Comment, CostUSD: result.CostUSD}
			}
		}
	}

	return reviewResult{Verdict: reviewApprove, Comment: "review parse error", CostUSD: result.CostUSD}
}

// estimateTimeoutSem is a dedicated semaphore for timeout estimation LLM calls.
var estimateTimeoutSem = make(chan struct{}, 3)

// estimateTimeoutLLM uses a lightweight LLM call to estimate appropriate timeout
// for a taskboard task. Returns a duration string (e.g. "45m", "2h") or empty
// string on failure (caller should fall back to keyword-based estimation).
func estimateTimeoutLLM(ctx context.Context, cfg *Config, prompt string) string {
	estPrompt := fmt.Sprintf(`Estimate how long an AI coding agent will need to complete this task. Consider the complexity, number of files likely involved, and whether it requires research/analysis.

Task:
%s

Reply with ONLY a single integer: the estimated minutes needed. Examples:
- Simple bug fix or config change: 15
- Moderate feature or multi-file fix: 45
- Large feature, refactor, or codebase analysis: 120
- Major rewrite or multi-project task: 180

Minutes:`, truncateStr(prompt, 2000))

	task := Task{
		ID:             newUUID(),
		Name:           "timeout-estimate",
		Prompt:         estPrompt,
		Model:          "haiku",
		Budget:         0.02,
		Timeout:        "15s",
		PermissionMode: "plan",
		Source:         "timeout-estimate",
	}
	fillDefaults(cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.02

	result := runSingleTask(ctx, cfg, task, estimateTimeoutSem, nil, "")
	if result.Status != "success" || result.Output == "" {
		return ""
	}

	// Parse the integer from output.
	cleaned := strings.TrimSpace(result.Output)
	// Extract first number found.
	var numStr string
	for _, ch := range cleaned {
		if ch >= '0' && ch <= '9' {
			numStr += string(ch)
		} else if numStr != "" {
			break
		}
	}
	minutes, err := strconv.Atoi(numStr)
	if err != nil || minutes < 5 || minutes > 480 {
		return ""
	}

	// Apply 1.5x buffer to avoid premature timeout.
	buffered := int(float64(minutes) * 1.5)
	if buffered < 15 {
		buffered = 15
	}

	if buffered >= 60 {
		hours := buffered / 60
		rem := buffered % 60
		if rem == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, rem)
	}
	return fmt.Sprintf("%dm", buffered)
}

// idleAnalysisSem limits concurrent idle-analysis LLM calls.
var idleAnalysisSem = make(chan struct{}, 1)

// idleAnalysis generates backlog tasks when the board is idle.
// Conditions: idleAnalyze enabled, no doing/review/todo tasks, 24h cooldown per project.
func (d *TaskBoardDispatcher) idleAnalysis() {
	if !d.engine.config.IdleAnalyze {
		return
	}

	// Verify no doing or review tasks exist.
	for _, status := range []string{"doing", "review"} {
		tasks, err := d.engine.ListTasks(status, "", "")
		if err != nil || len(tasks) > 0 {
			return
		}
	}

	// Find active projects: distinct projects with tasks completed in last 7 days.
	cutoff7d := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339)
	projectSQL := fmt.Sprintf(`
		SELECT DISTINCT project FROM tasks
		WHERE status IN ('done','failed')
		AND completed_at > '%s'
		AND project != '' AND project != 'default'
		LIMIT 3
	`, db.Escape(cutoff7d))
	projectRows, err := db.Query(d.engine.dbPath, projectSQL)
	if err != nil || len(projectRows) == 0 {
		return
	}

	cutoff24h := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)

	for _, row := range projectRows {
		projectID := fmt.Sprintf("%v", row["project"])

		// 24h cooldown: check for recent [idle-analysis] comments in this project.
		cooldownSQL := fmt.Sprintf(`
			SELECT COUNT(*) as cnt FROM task_comments
			WHERE content LIKE '%%[idle-analysis]%%'
			AND created_at > '%s'
			AND task_id IN (SELECT id FROM tasks WHERE project = '%s')
		`, db.Escape(cutoff24h), db.Escape(projectID))
		cooldownRows, err := db.Query(d.engine.dbPath, cooldownSQL)
		if err == nil && len(cooldownRows) > 0 && getFloat64(cooldownRows[0], "cnt") > 0 {
			log.Debug("idleAnalysis: 24h cooldown active", "project", projectID)
			continue
		}

		d.runIdleAnalysisForProject(projectID)
	}
}

// runIdleAnalysisForProject gathers context and asks an LLM to suggest next tasks.
func (d *TaskBoardDispatcher) runIdleAnalysisForProject(projectID string) {
	// Gather recently completed tasks.
	recentSQL := fmt.Sprintf(`
		SELECT id, title, status FROM tasks
		WHERE project = '%s' AND status IN ('done','failed')
		ORDER BY completed_at DESC LIMIT 10
	`, db.Escape(projectID))
	recentRows, err := db.Query(d.engine.dbPath, recentSQL)
	if err != nil || len(recentRows) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("Recently completed tasks:\n")
	for _, r := range recentRows {
		sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n",
			fmt.Sprintf("%v", r["id"]),
			fmt.Sprintf("%v", r["title"]),
			fmt.Sprintf("%v", r["status"])))
	}

	// Gather git log if project has a workdir.
	p, _ := getProject(d.cfg.HistoryDB, projectID)
	if p != nil && p.Workdir != "" {
		if gitOut, err := exec.Command("git", "-C", p.Workdir, "log", "--oneline", "-20").Output(); err == nil {
			sb.WriteString("\nRecent git activity:\n")
			sb.WriteString(string(gitOut))
		}
	}

	projectName := projectID
	if p != nil && p.Name != "" {
		projectName = p.Name
	}

	prompt := fmt.Sprintf(`Based on the completed tasks and recent git activity for project "%s", identify 1-3 logical next tasks.

%s

Output ONLY a JSON array of objects with keys: title, description, priority (low/normal/high).
Example: [{"title":"...","description":"...","priority":"normal"}]`, projectName, sb.String())

	task := Task{
		ID:             newUUID(),
		Name:           "idle-analysis-" + projectID,
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.10,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "idle-analysis",
	}
	fillDefaults(d.cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.10

	log.Info("idleAnalysis: analyzing project", "project", projectID)
	result := runSingleTask(d.ctx, d.cfg, task, idleAnalysisSem, nil, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		log.Warn("idleAnalysis: LLM call failed", "project", projectID, "error", result.Error)
		return
	}

	// Parse JSON array from output.
	type suggestion struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	var suggestions []suggestion

	output := result.Output
	start := strings.Index(output, "[")
	end := strings.LastIndex(output, "]")
	if start < 0 || end <= start {
		log.Warn("idleAnalysis: no JSON array in output", "project", projectID)
		return
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &suggestions); err != nil {
		log.Warn("idleAnalysis: JSON parse failed", "project", projectID, "error", err)
		return
	}

	// Cap at 3 suggestions.
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}

	created := 0
	for _, s := range suggestions {
		if s.Title == "" {
			continue
		}
		priority := s.Priority
		if priority == "" {
			priority = "normal"
		}
		newTask, err := d.engine.CreateTask(TaskBoard{
			Project:     projectID,
			Title:       s.Title,
			Description: s.Description,
			Priority:    priority,
			Status:      "backlog",
		})
		if err != nil {
			log.Warn("idleAnalysis: failed to create task", "project", projectID, "title", s.Title, "error", err)
			continue
		}
		d.engine.AddComment(newTask.ID, "system", "[idle-analysis] Auto-generated from project analysis")
		created++
	}

	log.Info("idleAnalysis: created backlog tasks", "project", projectID, "count", created)
}

// problemScanSem limits concurrent problem-scan LLM calls.
var problemScanSem = make(chan struct{}, 2)

// postTaskProblemScan uses a lightweight LLM call to scan the task output for latent
// issues: error patterns, unresolved TODOs, test failures, warnings, partial implementations.
// If problems are found, adds a comment to the task and optionally creates follow-up tickets.
func (d *TaskBoardDispatcher) postTaskProblemScan(t TaskBoard, output string, newStatus string) {
	if !d.engine.config.ProblemScan {
		return
	}
	if strings.TrimSpace(output) == "" {
		return
	}

	// Truncate output to keep LLM cost low.
	scanInput := output
	if len(scanInput) > 4000 {
		scanInput = scanInput[:4000] + "\n... (truncated)"
	}

	prompt := fmt.Sprintf(`You are a post-task quality scanner. Analyze this task output and identify latent problems that may need follow-up.

Task: %s (status: %s)

Output:
%s

Look for:
1. Error messages or stack traces (even if the task "succeeded")
2. Unresolved TODOs or FIXMEs mentioned in the output
3. Test failures or skipped tests
4. Warnings that could become errors later
5. Partial implementations ("will do later", "not yet implemented", etc.)
6. Security concerns (hardcoded credentials, unsafe patterns)

If you find problems, respond with a JSON object:
{"problems": [{"severity": "high|medium|low", "summary": "one-line description", "detail": "brief explanation"}], "followup": [{"title": "follow-up task title", "description": "what needs to be done", "priority": "high|normal|low"}]}

If no problems found, respond with exactly: {"problems": [], "followup": []}`, truncateStr(t.Title, 200), newStatus, scanInput)

	task := Task{
		ID:             newUUID(),
		Name:           "problem-scan-" + t.ID,
		Prompt:         prompt,
		Model:          "haiku",
		Budget:         0.05,
		Timeout:        "30s",
		PermissionMode: "plan",
		Source:         "problem-scan",
	}
	fillDefaults(d.cfg, &task)
	task.Model = "haiku"
	task.Budget = 0.05

	result := runSingleTask(d.ctx, d.cfg, task, problemScanSem, nil, "")
	if result.Status != "success" || strings.TrimSpace(result.Output) == "" {
		log.Debug("postTaskProblemScan: LLM call failed or empty", "task", t.ID, "error", result.Error)
		return
	}

	// Parse JSON from output.
	type problem struct {
		Severity string `json:"severity"`
		Summary  string `json:"summary"`
		Detail   string `json:"detail"`
	}
	type followup struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}
	type scanResult struct {
		Problems []problem  `json:"problems"`
		Followup []followup `json:"followup"`
	}

	raw := result.Output
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		log.Debug("postTaskProblemScan: no JSON in output", "task", t.ID)
		return
	}

	var sr scanResult
	if err := json.Unmarshal([]byte(raw[start:end+1]), &sr); err != nil {
		log.Debug("postTaskProblemScan: JSON parse failed", "task", t.ID, "error", err)
		return
	}

	if len(sr.Problems) == 0 && len(sr.Followup) == 0 {
		log.Debug("postTaskProblemScan: no issues found", "task", t.ID)
		return
	}

	// Build comment with findings.
	var commentSb strings.Builder
	commentSb.WriteString("[problem-scan] Potential issues detected:\n")
	for _, p := range sr.Problems {
		commentSb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", p.Severity, p.Summary, p.Detail))
	}

	if _, err := d.engine.AddComment(t.ID, "system", commentSb.String()); err != nil {
		log.Warn("postTaskProblemScan: failed to add comment", "task", t.ID, "error", err)
	}

	// Create follow-up tickets (cap at 3).
	created := 0
	for _, f := range sr.Followup {
		if created >= 3 || f.Title == "" {
			break
		}
		priority := f.Priority
		if priority == "" {
			priority = "normal"
		}
		newTask, err := d.engine.CreateTask(TaskBoard{
			Project:     t.Project,
			Title:       f.Title,
			Description: f.Description,
			Priority:    priority,
			Status:      "backlog",
			DependsOn:   []string{t.ID},
		})
		if err != nil {
			log.Warn("postTaskProblemScan: failed to create follow-up", "task", t.ID, "title", f.Title, "error", err)
			continue
		}
		d.engine.AddComment(newTask.ID, "system",
			fmt.Sprintf("[problem-scan] Auto-created from scan of task %s (%s)", t.ID, t.Title))
		created++
	}

	log.Info("postTaskProblemScan: scan complete", "task", t.ID, "problems", len(sr.Problems), "followups", created)
}

// postTaskSkillFailures records the failure to each skill's failures.md
// so that subsequent executions of the same skill get injected failure context.
func (d *TaskBoardDispatcher) postTaskSkillFailures(t TaskBoard, task Task, errMsg string) {
	if errMsg == "" {
		return
	}

	skills := selectSkills(d.cfg, task)
	if len(skills) == 0 {
		return
	}

	for _, s := range skills {
		appendSkillFailure(d.cfg, s.Name, t.Title, t.Assignee, errMsg)
		log.Debug("skill failure recorded", "skill", s.Name, "task", t.ID)
	}
}

// =============================================================================
// Section: Backlog Triage (from taskboard_triage.go)
// =============================================================================

// triageBacklog analyzes backlog tasks and decides whether to assign, decompose, or clarify.
// Called as a special cron job (like daily_notes).
func triageBacklog(ctx context.Context, cfg *Config, sem, childSem chan struct{}) {
	if !cfg.TaskBoard.Enabled {
		return
	}

	tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
	if err := tb.initTaskBoardSchema(); err != nil {
		log.Error("triage: init schema failed", "error", err)
		return
	}

	tasks, err := tb.ListTasks("backlog", "", "")
	if err != nil {
		log.Error("triage: list backlog failed", "error", err)
		return
	}

	if len(tasks) == 0 {
		log.Debug("triage: no backlog tasks")
		return
	}

	roster := buildAgentRoster(cfg)
	if roster == "" {
		log.Warn("triage: no agents configured, skipping")
		return
	}

	// Build valid agent name set for validation.
	validAgents := make(map[string]bool, len(cfg.Agents))
	for name := range cfg.Agents {
		validAgents[name] = true
	}

	// Fast-path: promote assigned tasks with no blocking deps directly to todo.
	fastPromoted := 0
	for _, t := range tasks {
		if t.Assignee != "" && !hasBlockingDeps(tb, t) {
			if _, err := tb.MoveTask(t.ID, "todo"); err == nil {
				log.Info("triage: fast-path promote", "taskId", t.ID, "assignee", t.Assignee, "priority", t.Priority)
				tb.AddComment(t.ID, "triage", "[triage] Fast-path: already assigned, no blocking deps → todo")
				fastPromoted++
			}
		}
	}
	if fastPromoted > 0 {
		log.Info("triage: fast-path promoted tasks", "count", fastPromoted)
		// Re-fetch remaining backlog for LLM triage.
		tasks, err = tb.ListTasks("backlog", "", "")
		if err != nil {
			log.Error("triage: re-list backlog failed", "error", err)
			return
		}
		if len(tasks) == 0 {
			log.Debug("triage: all backlog tasks promoted via fast-path")
			return
		}
	}

	log.Info("triage: processing backlog", "count", len(tasks))

	for _, t := range tasks {
		if ctx.Err() != nil {
			return
		}

		comments, err := tb.GetThread(t.ID)
		if err != nil {
			log.Warn("triage: failed to get thread", "taskId", t.ID, "error", err)
			continue
		}
		if shouldSkipTriage(comments) {
			log.Debug("triage: skipping (already triaged, no new replies)", "taskId", t.ID)
			continue
		}

		result := triageOneTask(ctx, cfg, sem, childSem, tb, t, comments, roster)
		if result == nil {
			continue
		}

		applyTriageResult(tb, t, result, validAgents)
	}
}

// triageResult is the structured LLM response for triage decisions.
type triageResult struct {
	Action   string          `json:"action"`   // ready, decompose, clarify
	Assignee string          `json:"assignee"` // agent name (for ready)
	Subtasks []triageSubtask `json:"subtasks"` // (for decompose)
	Comment  string          `json:"comment"`  // reason or question
}

type triageSubtask struct {
	Title    string `json:"title"`
	Assignee string `json:"assignee"`
}

// triageOneTask sends a single backlog task to LLM for triage analysis.
func triageOneTask(ctx context.Context, cfg *Config, sem, childSem chan struct{}, tb *TaskBoardEngine, t TaskBoard, comments []TaskComment, roster string) *triageResult {
	// Build conversation thread.
	threadText := "(no comments)"
	if len(comments) > 0 {
		var lines []string
		for _, c := range comments {
			lines = append(lines, fmt.Sprintf("[%s] %s: %s", c.CreatedAt, c.Author, c.Content))
		}
		threadText = strings.Join(lines, "\n")
	}

	prompt := fmt.Sprintf(`You are a task triage agent for the Tetora AI team.

Analyze the backlog task below and decide how to handle it.

## Available Agents
%s

## Task
- ID: %s
- Title: %s
- Description: %s
- Priority: %s
- Project: %s

## Conversation
%s

## Rules
1. If the task is clear and actionable as-is, respond "ready" and pick the best agent
2. If the task is complex and should be split into 2-5 subtasks, respond "decompose"
3. If critical information is missing, respond "clarify" and ask a specific question
4. Match agents by their expertise (description + keywords)
5. Each subtask must have a clear title and assigned agent

Respond with ONLY valid JSON (no markdown fences):
{"action":"ready|decompose|clarify","assignee":"agent_name","subtasks":[{"title":"...","assignee":"agent_name"}],"comment":"reason or question"}`,
		roster, t.ID, t.Title, t.Description, t.Priority, t.Project, threadText)

	task := Task{
		Name:    "triage:" + t.ID,
		Prompt:  prompt,
		Model:   "sonnet",
		Budget:  0.2,
		Timeout: "30s",
		Source:  "triage",
	}
	fillDefaults(cfg, &task)
	task.Model = "sonnet" // triage needs better judgement than haiku

	result := runSingleTask(ctx, cfg, task, sem, childSem, "")
	if result.Status != "success" {
		log.Warn("triage: LLM call failed", "taskId", t.ID, "error", result.Error)
		return nil
	}

	// Parse JSON response — extract JSON object from LLM output.
	output := strings.TrimSpace(result.Output)
	output = extractJSON(output)

	var tr triageResult
	if err := json.Unmarshal([]byte(output), &tr); err != nil {
		log.Warn("triage: failed to parse LLM response", "taskId", t.ID, "output", truncate(output, 200), "error", err)
		return nil
	}

	if tr.Action != "ready" && tr.Action != "decompose" && tr.Action != "clarify" {
		log.Warn("triage: unknown action", "taskId", t.ID, "action", tr.Action)
		return nil
	}

	return &tr
}

// applyTriageResult executes the triage decision on a task.
func applyTriageResult(tb *TaskBoardEngine, t TaskBoard, tr *triageResult, validAgents map[string]bool) {
	switch tr.Action {
	case "ready":
		if tr.Assignee == "" {
			log.Warn("triage: ready but no assignee", "taskId", t.ID)
			return
		}
		if !validAgents[tr.Assignee] {
			log.Warn("triage: assignee not a configured agent", "taskId", t.ID, "assignee", tr.Assignee)
			// Add as clarify instead.
			comment := fmt.Sprintf("[triage] Could not assign: agent %q not found. Reason: %s", tr.Assignee, tr.Comment)
			if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
				log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
			}
			return
		}
		if _, err := tb.AssignTask(t.ID, tr.Assignee); err != nil {
			log.Warn("triage: assign failed", "taskId", t.ID, "error", err)
			return
		}
		if _, err := tb.MoveTask(t.ID, "todo"); err != nil {
			log.Warn("triage: move to todo failed", "taskId", t.ID, "error", err)
			return
		}
		comment := fmt.Sprintf("[triage] Assigned to %s. Reason: %s", tr.Assignee, tr.Comment)
		if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
			log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
		}
		log.Info("triage: task ready", "taskId", t.ID, "assignee", tr.Assignee)

	case "decompose":
		if len(tr.Subtasks) == 0 {
			log.Warn("triage: decompose but no subtasks", "taskId", t.ID)
			return
		}
		var created []string
		for _, sub := range tr.Subtasks {
			if sub.Title == "" {
				log.Warn("triage: skipping subtask with empty title", "taskId", t.ID)
				continue
			}
			assignee := sub.Assignee
			if !validAgents[assignee] {
				log.Warn("triage: subtask assignee not found, leaving unassigned", "taskId", t.ID, "assignee", assignee)
				assignee = ""
			}
			newTask, err := tb.CreateTask(TaskBoard{
				Title:    sub.Title,
				Status:   "todo",
				Assignee: assignee,
				Priority: t.Priority,
				Project:  t.Project,
				ParentID: t.ID,
			})
			if err != nil {
				log.Warn("triage: create subtask failed", "taskId", t.ID, "title", sub.Title, "error", err)
				continue
			}
			created = append(created, fmt.Sprintf("- %s → %s (%s)", newTask.ID, sub.Title, assignee))
		}
		// Only move parent to done if at least one subtask was created.
		if len(created) == 0 {
			log.Warn("triage: all subtasks failed to create, keeping in backlog", "taskId", t.ID)
			if _, err := tb.AddComment(t.ID, "triage", "[triage] Decompose attempted but all subtasks failed to create."); err != nil {
				log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
			}
			return
		}
		comment := fmt.Sprintf("[triage] Decomposed into %d subtasks:\n%s\n\nReason: %s",
			len(created), strings.Join(created, "\n"), tr.Comment)
		if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
			log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
		}
		if _, err := tb.MoveTask(t.ID, "todo"); err != nil {
			log.Warn("triage: move decomposed task to todo failed", "taskId", t.ID, "error", err)
		}
		log.Info("triage: task decomposed", "taskId", t.ID, "subtasks", len(created))

	case "clarify":
		if tr.Comment == "" {
			log.Warn("triage: clarify but no comment", "taskId", t.ID)
			return
		}
		comment := fmt.Sprintf("[triage] Need clarification: %s", tr.Comment)
		if _, err := tb.AddComment(t.ID, "triage", comment); err != nil {
			log.Warn("triage: add comment failed", "taskId", t.ID, "error", err)
		}
		log.Info("triage: asked for clarification", "taskId", t.ID)
	}
}

// buildAgentRoster generates a deterministic summary of available agents for the triage prompt.
func buildAgentRoster(cfg *Config) string {
	if len(cfg.Agents) == 0 {
		return ""
	}
	// Sort agent names for deterministic prompt ordering.
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)

	var lines []string
	for _, name := range names {
		ac := cfg.Agents[name]
		line := fmt.Sprintf("- %s: %s", name, ac.Description)
		if len(ac.Keywords) > 0 {
			line += fmt.Sprintf(" (keywords: %s)", strings.Join(ac.Keywords, ", "))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// shouldSkipTriage returns true if triage has already commented and no human
// has replied since — prevents re-triaging the same task repeatedly.
// Comments are assumed to be in chronological order (ORDER BY created_at ASC).
func shouldSkipTriage(comments []TaskComment) bool {
	if len(comments) == 0 {
		return false // first triage
	}
	// Find the last triage comment index.
	lastTriageIdx := -1
	for i := len(comments) - 1; i >= 0; i-- {
		if comments[i].Author == "triage" {
			lastTriageIdx = i
			break
		}
	}
	if lastTriageIdx == -1 {
		return false // no triage comment yet
	}
	// Check if any non-triage comment exists after the last triage comment.
	for i := lastTriageIdx + 1; i < len(comments); i++ {
		if comments[i].Author != "triage" {
			return false // human replied after triage — re-triage
		}
	}
	return true // triage has the last word, skip
}
