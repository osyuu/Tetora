package main

// taskboard.go — thin shim over internal/taskboard.
//
// All types are aliases to internal/taskboard so callers see no type change.
// All engine methods are inherited automatically via type alias.
// Only root-specific wiring (constructors, cmdTask CLI) lives here.

import (
	"fmt"
	"os"
	"strings"

	"tetora/internal/config"
	"tetora/internal/taskboard"
	"tetora/internal/worktree"
)

// --- Type aliases (zero-cost, no wrapper overhead) ---

type TaskBoardEngine = taskboard.Engine
type TaskBoardDispatcher = taskboard.Dispatcher
type TaskBoard = taskboard.TaskBoard
type TaskComment = taskboard.TaskComment
type TaskListResult = taskboard.TaskListResult
type Pagination = taskboard.Pagination
type BoardView = taskboard.BoardView
type BoardStats = taskboard.BoardStats
type BoardFilter = taskboard.BoardFilter
type ProjectStats = taskboard.ProjectStats

// --- Constructor shims ---

func newTaskBoardEngine(dbPath string, cfg TaskBoardConfig, webhooks []WebhookConfig) *TaskBoardEngine {
	return taskboard.NewEngine(dbPath, cfg, webhooks)
}

// hasBlockingDeps returns true if any dependency of the task is not yet done.
func hasBlockingDeps(tb *TaskBoardEngine, t TaskBoard) bool {
	return taskboard.HasBlockingDeps(tb, t)
}

// normalizeTaskID adds "task-" prefix if the ID looks like a bare number.
func normalizeTaskID(id string) string {
	return taskboard.NormalizeTaskID(id)
}

func generateID(prefix string) string {
	return taskboard.GenerateID(prefix)
}

func detectDefaultBranch(workdir string) string {
	return taskboard.DetectDefaultBranch(workdir)
}

// --- initTaskBoardSchema is an alias for InitSchema ---
// (The method name changed between root and internal; keep backward-compat for callers.)
// Note: *TaskBoardEngine has InitSchema() directly via alias, but callers still use
// the old lowercase name in tests and tool_taskboard.go. Provide a package-level shim.

// --- cmdTask CLI ---

func cmdTask(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora task <list|create|show|update|move|assign|comment|thread>")
		fmt.Println("\nCommands:")
		fmt.Println("  list [--status=STATUS] [--assignee=AGENT] [--project=PROJECT]")
		fmt.Println("  create --title=TITLE [--description=DESC] [--priority=PRIORITY] [--assignee=AGENT] [--type=TYPE] [--depends-on=ID]...")
		fmt.Println("  show TASK_ID [--full]")
		fmt.Println("  update TASK_ID [--title=TITLE] [--description=DESC] [--priority=PRIORITY]")
		fmt.Println("  move TASK_ID --status=STATUS")
		fmt.Println("  assign TASK_ID --assignee=AGENT")
		fmt.Println("  comment TASK_ID --author=AUTHOR --content=CONTENT [--type=TYPE]")
		fmt.Println("  thread TASK_ID")
		os.Exit(0)
	}

	cfg := loadConfig("")
	if !cfg.TaskBoard.Enabled {
		fmt.Println("Error: task board not enabled in config")
		os.Exit(1)
	}

	tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)
	if err := tb.InitSchema(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	cmd := args[0]
	args = args[1:]

	switch cmd {
	case "list":
		var status, assignee, project string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--status=") {
				status = strings.TrimPrefix(arg, "--status=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			} else if strings.HasPrefix(arg, "--project=") {
				project = strings.TrimPrefix(arg, "--project=")
			}
		}

		tasks, err := tb.ListTasks(status, assignee, project)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return
		}

		fmt.Printf("Found %d tasks:\n\n", len(tasks))
		for _, t := range tasks {
			fmt.Printf("ID: %s\n", t.ID)
			fmt.Printf("Title: %s\n", t.Title)
			fmt.Printf("Status: %s | Priority: %s | Assignee: %s\n", t.Status, t.Priority, t.Assignee)
			if t.Description != "" {
				fmt.Printf("Description: %s\n", t.Description)
			}
			fmt.Printf("Created: %s | Updated: %s\n", t.CreatedAt, t.UpdatedAt)
			fmt.Println()
		}

	case "create":
		var title, description, priority, assignee, taskType string
		var dependsOn []string
		for _, arg := range args {
			if strings.HasPrefix(arg, "--title=") {
				title = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				description = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				priority = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			} else if strings.HasPrefix(arg, "--type=") {
				taskType = strings.TrimPrefix(arg, "--type=")
			} else if strings.HasPrefix(arg, "--depends-on=") {
				depID := strings.TrimPrefix(arg, "--depends-on=")
				if depID != "" {
					dependsOn = append(dependsOn, depID)
				}
			}
		}

		if title == "" {
			fmt.Println("Error: --title is required")
			os.Exit(1)
		}

		task, err := tb.CreateTask(TaskBoard{
			Title:       title,
			Description: description,
			Priority:    priority,
			Assignee:    assignee,
			Type:        taskType,
			DependsOn:   dependsOn,
		})
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created task: %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Status: %s\n", task.Status)

	case "show":
		if len(args) < 1 {
			fmt.Println("Usage: tetora task show TASK_ID [--full]")
			os.Exit(1)
		}

		taskID := args[0]
		full := false
		for _, arg := range args[1:] {
			if arg == "--full" {
				full = true
			}
		}

		task, err := tb.GetTask(taskID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			normalizedID := normalizeTaskID(taskID)
			if candidates := tb.SuggestTasks(normalizedID); len(candidates) > 0 {
				fmt.Fprintln(os.Stderr, "Did you mean:")
				for _, c := range candidates {
					fmt.Fprintf(os.Stderr, "  %s  %s  (%s)\n", c.ID, c.Title, c.Status)
				}
			}
			os.Exit(1)
		}

		fmt.Printf("# %s\n\n", task.Title)
		fmt.Printf("- **ID**: %s\n", task.ID)
		fmt.Printf("- **Status**: %s\n", task.Status)
		fmt.Printf("- **Priority**: %s\n", task.Priority)
		fmt.Printf("- **Assignee**: %s\n", task.Assignee)
		fmt.Printf("- **Project**: %s\n", task.Project)
		if task.ParentID != "" {
			fmt.Printf("- **Parent**: %s\n", task.ParentID)
		}
		if len(task.DependsOn) > 0 {
			fmt.Printf("- **Depends On**: %s\n", strings.Join(task.DependsOn, ", "))
		}
		fmt.Printf("- **Created**: %s\n", task.CreatedAt)
		fmt.Printf("- **Updated**: %s\n", task.UpdatedAt)
		if task.CompletedAt != "" {
			fmt.Printf("- **Completed**: %s\n", task.CompletedAt)
		}
		if task.Description != "" {
			fmt.Printf("\n## Description\n\n%s\n", task.Description)
		}

		if full {
			comments, err := tb.GetThread(task.ID)
			if err != nil {
				fmt.Printf("\nError loading comments: %v\n", err)
			} else if len(comments) > 0 {
				fmt.Printf("\n## Comments (%d)\n\n", len(comments))
				for _, c := range comments {
					fmt.Printf("### [%s] %s (type: %s)\n\n%s\n\n", c.CreatedAt, c.Author, c.Type, c.Content)
				}
			}
		}

	case "update":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task update TASK_ID [--title=TITLE] [--description=DESC] [--priority=PRIORITY]")
			os.Exit(1)
		}

		taskID := args[0]
		updates := make(map[string]any)
		var dependsOn []string
		hasDeps := false
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--title=") {
				updates["title"] = strings.TrimPrefix(arg, "--title=")
			} else if strings.HasPrefix(arg, "--description=") {
				updates["description"] = strings.TrimPrefix(arg, "--description=")
			} else if strings.HasPrefix(arg, "--priority=") {
				updates["priority"] = strings.TrimPrefix(arg, "--priority=")
			} else if strings.HasPrefix(arg, "--depends-on=") {
				depID := strings.TrimPrefix(arg, "--depends-on=")
				if depID != "" {
					dependsOn = append(dependsOn, depID)
				}
				hasDeps = true
			}
		}
		if hasDeps {
			updates["dependsOn"] = dependsOn
		}

		if len(updates) == 0 {
			fmt.Println("Error: at least one update field is required")
			os.Exit(1)
		}

		task, err := tb.UpdateTask(taskID, updates)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Updated task %s\n", task.ID)
		fmt.Printf("Title: %s\n", task.Title)
		fmt.Printf("Priority: %s\n", task.Priority)

	case "move":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task move TASK_ID --status=STATUS")
			os.Exit(1)
		}

		taskID := args[0]
		var newStatus string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--status=") {
				newStatus = strings.TrimPrefix(arg, "--status=")
			}
		}

		if newStatus == "" {
			fmt.Println("Error: --status is required")
			os.Exit(1)
		}

		task, err := tb.MoveTask(taskID, newStatus)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Moved task %s to %s\n", task.ID, task.Status)

	case "assign":
		if len(args) < 2 {
			fmt.Println("Usage: tetora task assign TASK_ID --assignee=AGENT")
			os.Exit(1)
		}

		taskID := args[0]
		var assignee string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--assignee=") {
				assignee = strings.TrimPrefix(arg, "--assignee=")
			}
		}

		if assignee == "" {
			fmt.Println("Error: --assignee is required")
			os.Exit(1)
		}

		task, err := tb.AssignTask(taskID, assignee)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Assigned task %s to %s\n", task.ID, task.Assignee)

	case "comment":
		if len(args) < 3 {
			fmt.Println("Usage: tetora task comment TASK_ID --author=AUTHOR --content=CONTENT [--type=TYPE]")
			os.Exit(1)
		}

		taskID := args[0]
		var author, content, commentType string
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "--author=") {
				author = strings.TrimPrefix(arg, "--author=")
			} else if strings.HasPrefix(arg, "--content=") {
				content = strings.TrimPrefix(arg, "--content=")
			} else if strings.HasPrefix(arg, "--type=") {
				commentType = strings.TrimPrefix(arg, "--type=")
			}
		}

		if author == "" || content == "" {
			fmt.Println("Error: --author and --content are required")
			os.Exit(1)
		}

		comment, err := tb.AddComment(taskID, author, content, commentType)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Added comment %s (type: %s) to task %s\n", comment.ID, comment.Type, taskID)

	case "thread":
		if len(args) < 1 {
			fmt.Println("Usage: tetora task thread TASK_ID")
			os.Exit(1)
		}

		taskID := args[0]
		comments, err := tb.GetThread(taskID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		if len(comments) == 0 {
			fmt.Println("No comments found")
			return
		}

		fmt.Printf("Thread for task %s (%d comments):\n\n", taskID, len(comments))
		for _, c := range comments {
			fmt.Printf("[%s] %s (type: %s):\n%s\n\n", c.CreatedAt, c.Author, c.Type, c.Content)
		}

	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		fmt.Println("Use 'tetora task' to see available commands")
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// worktree.go — thin shim over internal/worktree.
//
// Types are aliases so callers (including package main tests) see no type change.
// Unexported helper shims (buildBranchName, slugifyBranch, isGitRepo, detectDefaultBranch)
// are kept here because worktree_test.go (package main) references them directly.
// ---------------------------------------------------------------------------

// --- Type aliases ---

type WorktreeManager = worktree.WorktreeManager
type WorktreeInfo = worktree.WorktreeInfo

// --- Constructor shim ---

func NewWorktreeManager(baseDir string) *WorktreeManager {
	return worktree.NewWorktreeManager(baseDir)
}

// --- Unexported shims for package main tests and internal callers ---

func buildBranchName(cfg config.GitWorkflowConfig, t taskboard.TaskBoard) string {
	return worktree.BuildBranchName(cfg, t)
}

func slugify(s string) string {
	return worktree.Slugify(s)
}

func slugifyBranch(s string) string {
	return worktree.SlugifyBranch(s)
}

func isGitRepo(dir string) bool {
	return worktree.IsGitRepo(dir)
}
