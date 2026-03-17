package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/automation/briefing"
	"tetora/internal/db"
	"tetora/internal/life/reminder"
	"tetora/internal/log"
	"tetora/internal/nlp"
	"tetora/internal/tool"
)

// Global singletons for life services.
var (
	globalContactsService *ContactsService
	globalFinanceService  *FinanceService
	globalGoalsService    *GoalsService
	globalHabitsService   *HabitsService
	globalTimeTracking    *TimeTrackingService
	globalFamilyService      *FamilyService
	globalUserProfileService *UserProfileService
)

// registerLifeTools registers life management tools (tasks, expenses, contacts,
// habits, goals, briefing, insights, scheduling, lifecycle, quick capture, time tracking).
func registerLifeTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	// --- P23.2: Task Management Tools ---
	if enabled("task_create") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_create",
			Description: "Create a personal task with optional project, priority, due date, tags, and subtask decomposition",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Task title (required)"},
					"description": {"type": "string", "description": "Task description"},
					"project": {"type": "string", "description": "Project name (default: inbox)"},
					"priority": {"type": "number", "description": "Priority 1-4 (1=urgent, 4=low, default 2)"},
					"dueAt": {"type": "string", "description": "Due date/time (RFC3339 or YYYY-MM-DD)"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"},
					"decompose": {"type": "boolean", "description": "If true, also create subtasks"},
					"subtasks": {"type": "array", "items": {"type": "string"}, "description": "Subtask titles (used when decompose=true)"}
				},
				"required": ["title"]
			}`),
			Handler: toolTaskCreate,
			Builtin: true,
		})
	}
	if enabled("task_list") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_list",
			Description: "List personal tasks with optional filtering by status, project, priority, due date, or tag",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "description": "Filter by status (todo, in_progress, done, cancelled)"},
					"project": {"type": "string", "description": "Filter by project name"},
					"priority": {"type": "number", "description": "Filter by priority (1-4)"},
					"dueDate": {"type": "string", "description": "Filter tasks due before this date"},
					"tag": {"type": "string", "description": "Filter by tag"},
					"limit": {"type": "number", "description": "Max results (default 50)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolTaskList,
			Builtin: true,
		})
	}
	if enabled("task_complete") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_complete",
			Description: "Mark a task as done (also completes all subtasks)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"taskId": {"type": "string", "description": "Task ID to complete (required)"}
				},
				"required": ["taskId"]
			}`),
			Handler: toolTaskComplete,
			Builtin: true,
		})
	}
	if enabled("task_review") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_review",
			Description: "Generate a task review summary for daily or weekly periods",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Review period: 'daily' or 'weekly' (default: daily)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolTaskReview,
			Builtin: true,
		})
	}
	if enabled("todoist_sync") && cfg.TaskManager.Todoist.Enabled {
		r.Register(&ToolDef{
			Name:        "todoist_sync",
			Description: "Sync tasks with Todoist (pull, push, or full bidirectional sync)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: 'pull', 'push', or 'sync' (default: sync)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolTodoistSync,
			Builtin: true,
		})
	}
	if enabled("notion_sync") && cfg.TaskManager.Notion.Enabled {
		r.Register(&ToolDef{
			Name:        "notion_sync",
			Description: "Sync tasks with a Notion database (pull, push, or full bidirectional sync)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: 'pull', 'push', or 'sync' (default: sync)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolNotionSync,
			Builtin: true,
		})
	}

	// --- P23.4: Financial Tracking Tools ---
	if enabled("expense_add") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "expense_add",
			Description: "Record an expense using natural language or explicit fields (e.g. '午餐 350 元', 'coffee $5.50')",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Natural language expense (e.g. '午餐 350 元', 'coffee $5.50')"},
					"amount": {"type": "number", "description": "Expense amount (optional if using text)"},
					"currency": {"type": "string", "description": "Currency code (e.g. TWD, USD, JPY)"},
					"category": {"type": "string", "description": "Category (food, transport, shopping, etc.)"},
					"description": {"type": "string", "description": "Expense description"},
					"userId": {"type": "string", "description": "User ID (optional, defaults to 'default')"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags for the expense"}
				}
			}`),
			Handler: toolExpenseAdd,
			Builtin: true,
		})
	}
	if enabled("expense_report") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "expense_report",
			Description: "Generate an expense report for a period (today, week, month, year)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Report period: today, week, month, year (default: month)"},
					"category": {"type": "string", "description": "Filter by category (optional)"},
					"currency": {"type": "string", "description": "Report currency (optional)"},
					"userId": {"type": "string", "description": "User ID (optional)"}
				}
			}`),
			Handler: toolExpenseReport,
			Builtin: true,
		})
	}
	if enabled("expense_budget") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "expense_budget",
			Description: "Manage monthly budgets per category (set/list/check)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: set, list, check"},
					"category": {"type": "string", "description": "Budget category (required for set)"},
					"limit": {"type": "number", "description": "Monthly limit (required for set)"},
					"currency": {"type": "string", "description": "Currency (optional)"},
					"userId": {"type": "string", "description": "User ID (optional)"}
				},
				"required": ["action"]
			}`),
			Handler: toolExpenseBudget,
			Builtin: true,
		})
	}
	if enabled("price_watch") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "price_watch",
			Description: "Monitor currency exchange rates with alerts (add/list/cancel)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: add, list, cancel"},
					"from": {"type": "string", "description": "From currency code (e.g. USD)"},
					"to": {"type": "string", "description": "To currency code (e.g. JPY)"},
					"condition": {"type": "string", "description": "Condition: lt (less than) or gt (greater than)"},
					"threshold": {"type": "number", "description": "Price threshold to trigger alert"},
					"id": {"type": "number", "description": "Watch ID (for cancel)"},
					"userId": {"type": "string", "description": "User ID (optional)"},
					"notifyChannel": {"type": "string", "description": "Notification channel (optional)"}
				},
				"required": ["action"]
			}`),
			Handler: toolPriceWatch,
			Builtin: true,
		})
	}

	// --- P24.2: Contact & Social Graph Tools ---
	if enabled("contact_add") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_add",
			Description: "Add or update a contact with cross-channel identifiers, birthday, notes",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Contact display name"},
					"channel": {"type": "string", "description": "Channel (discord, line, telegram, etc.)"},
					"channelId": {"type": "string", "description": "Channel-specific user ID"},
					"birthday": {"type": "string", "description": "Birthday (YYYY-MM-DD or MM-DD)"},
					"notes": {"type": "string", "description": "Notes about this contact"},
					"tags": {"type": "string", "description": "Comma-separated tags"}
				},
				"required": ["name"]
			}`),
			Handler: toolContactAdd,
			Builtin: true,
		})
	}
	if enabled("contact_search") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_search",
			Description: "Search contacts by name, tag, or channel",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (name, tag, or channel)"},
					"limit": {"type": "integer", "description": "Max results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolContactSearch,
			Builtin: true,
		})
	}
	if enabled("contact_list") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_list",
			Description: "List all contacts, optionally filtered by tag",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"tag": {"type": "string", "description": "Filter by tag (optional)"},
					"limit": {"type": "integer", "description": "Max results (default 20)"}
				}
			}`),
			Handler: toolContactList,
			Builtin: true,
		})
	}
	if enabled("contact_upcoming") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_upcoming",
			Description: "Show upcoming contact birthdays and events in the next N days",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"days": {"type": "integer", "description": "Look-ahead days (default 30)"}
				}
			}`),
			Handler: toolContactUpcoming,
			Builtin: true,
		})
	}
	if enabled("contact_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_log",
			Description: "Log an interaction with a contact (call, meeting, chat, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"contactId": {"type": "integer", "description": "Contact ID"},
					"type": {"type": "string", "description": "Interaction type (call, meeting, chat, gift, etc.)"},
					"notes": {"type": "string", "description": "Interaction notes"}
				},
				"required": ["contactId", "type"]
			}`),
			Handler: toolContactLog,
			Builtin: true,
		})
	}

	// --- P24.3: Life Insights Engine Tools ---
	if enabled("life_report") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "life_report",
			Description: "Generate a life report (daily, weekly, monthly) combining activity, spending, habits, and goals",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Report period: daily, weekly, monthly"},
					"date": {"type": "string", "description": "Target date (YYYY-MM-DD, default today)"}
				}
			}`),
			Handler: toolLifeReport,
			Builtin: true,
		})
	}
	if enabled("life_insights") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "life_insights",
			Description: "Get AI-driven life insights: anomaly detection, spending forecast, behavioral patterns",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"type": {"type": "string", "description": "Insight type: anomalies, forecast, patterns, all"},
					"days": {"type": "integer", "description": "Analysis window in days (default 30)"}
				}
			}`),
			Handler: toolLifeInsights,
			Builtin: true,
		})
	}

	// --- P24.4: Smart Scheduling Tools ---
	if enabled("schedule_view") {
		r.Register(&ToolDef{
			Name:        "schedule_view",
			Description: "View schedule for a date range from calendar events",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default today)"},
					"days": {"type": "integer", "description": "Number of days to show (default 1)"}
				}
			}`),
			Handler: toolScheduleView,
			Builtin: true,
		})
	}
	if enabled("schedule_suggest") {
		r.Register(&ToolDef{
			Name:        "schedule_suggest",
			Description: "Find available time slots for a new event",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"duration": {"type": "integer", "description": "Duration in minutes"},
					"date": {"type": "string", "description": "Target date (YYYY-MM-DD, default today)"},
					"days": {"type": "integer", "description": "Look-ahead days (default 7)"},
					"preferMorning": {"type": "boolean", "description": "Prefer morning slots"}
				},
				"required": ["duration"]
			}`),
			Handler: toolScheduleSuggest,
			Builtin: true,
		})
	}
	if enabled("schedule_plan") {
		r.Register(&ToolDef{
			Name:        "schedule_plan",
			Description: "Analyze schedule for overcommitment, suggest time blocks, and plan the day",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date to plan (YYYY-MM-DD, default today)"},
					"tasks": {"type": "string", "description": "Comma-separated tasks to fit into schedule"}
				}
			}`),
			Handler: toolSchedulePlan,
			Builtin: true,
		})
	}

	// --- P24.5: Habit & Wellness Tracking Tools ---
	if enabled("habit_create") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_create",
			Description: "Create a new habit to track (daily, weekly frequency)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Habit name"},
					"frequency": {"type": "string", "description": "Frequency: daily, weekly (default daily)"},
					"target": {"type": "integer", "description": "Target count per period (default 1)"},
					"category": {"type": "string", "description": "Category (health, productivity, etc.)"}
				},
				"required": ["name"]
			}`),
			Handler: toolHabitCreate,
			Builtin: true,
		})
	}
	if enabled("habit_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_log",
			Description: "Log a habit completion",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"habitId": {"type": "integer", "description": "Habit ID"},
					"name": {"type": "string", "description": "Habit name (alternative to ID)"},
					"count": {"type": "integer", "description": "Count (default 1)"},
					"notes": {"type": "string", "description": "Optional notes"}
				}
			}`),
			Handler: toolHabitLog,
			Builtin: true,
		})
	}
	if enabled("habit_status") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_status",
			Description: "Show current habit streaks and progress",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"habitId": {"type": "integer", "description": "Specific habit ID (optional, shows all if omitted)"}
				}
			}`),
			Handler: toolHabitStatus,
			Builtin: true,
		})
	}
	if enabled("habit_report") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_report",
			Description: "Generate habit tracking report for a period",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"days": {"type": "integer", "description": "Report period in days (default 30)"},
					"category": {"type": "string", "description": "Filter by category (optional)"}
				}
			}`),
			Handler: toolHabitReport,
			Builtin: true,
		})
	}
	if enabled("health_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "health_log",
			Description: "Log health data (weight, blood pressure, sleep, steps, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"metric": {"type": "string", "description": "Metric name (weight, bp, sleep, steps, etc.)"},
					"value": {"type": "number", "description": "Metric value"},
					"unit": {"type": "string", "description": "Unit (kg, mmHg, hours, etc.)"},
					"notes": {"type": "string", "description": "Optional notes"}
				},
				"required": ["metric", "value"]
			}`),
			Handler: toolHealthLog,
			Builtin: true,
		})
	}
	if enabled("health_summary") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "health_summary",
			Description: "Get health data summary with trends",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"metric": {"type": "string", "description": "Specific metric (optional, shows all if omitted)"},
					"days": {"type": "integer", "description": "Period in days (default 30)"}
				}
			}`),
			Handler: toolHealthSummary,
			Builtin: true,
		})
	}

	// --- P24.6: Goal Planning & Autonomy Tools ---
	if enabled("goal_create") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_create",
			Description: "Create a new goal with milestones and deadline",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Goal title"},
					"description": {"type": "string", "description": "Goal description"},
					"deadline": {"type": "string", "description": "Deadline (YYYY-MM-DD, optional)"},
					"milestones": {"type": "string", "description": "Comma-separated milestone titles"},
					"category": {"type": "string", "description": "Category (career, health, finance, etc.)"}
				},
				"required": ["title"]
			}`),
			Handler: toolGoalCreate,
			Builtin: true,
		})
	}
	if enabled("goal_list") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_list",
			Description: "List goals, optionally filtered by status or category",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "description": "Filter: active, completed, abandoned (default active)"},
					"category": {"type": "string", "description": "Filter by category (optional)"}
				}
			}`),
			Handler: toolGoalList,
			Builtin: true,
		})
	}
	if enabled("goal_update") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_update",
			Description: "Update goal progress, status, or milestones",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"goalId": {"type": "integer", "description": "Goal ID"},
					"action": {"type": "string", "description": "Action: progress, complete, abandon, milestone_done"},
					"milestoneIndex": {"type": "integer", "description": "Milestone index (for milestone_done)"},
					"notes": {"type": "string", "description": "Progress notes"}
				},
				"required": ["goalId", "action"]
			}`),
			Handler: toolGoalUpdate,
			Builtin: true,
		})
	}
	if enabled("goal_review") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_review",
			Description: "Generate a goal review: stale goals, upcoming deadlines, weekly progress",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"type": {"type": "string", "description": "Review type: weekly, stale, deadlines, all (default all)"}
				}
			}`),
			Handler: toolGoalReview,
			Builtin: true,
		})
	}

	// --- P24.7: Morning Briefing & Evening Wrap Tools ---
	if enabled("briefing_morning") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "briefing_morning",
			Description: "Generate a morning briefing: schedule, tasks, habits, goals, reminders, birthdays",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default today)"}
				}
			}`),
			Handler: toolBriefingMorning,
			Builtin: true,
		})
	}
	if enabled("briefing_evening") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "briefing_evening",
			Description: "Generate an evening wrap-up: day summary, habits completed, spending, tasks done, tomorrow preview",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default today)"}
				}
			}`),
			Handler: toolBriefingEvening,
			Builtin: true,
		})
	}

	// --- P29.2: Time Tracking ---
	if enabled("time_start") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_start",
			Description: "Start a time tracking timer for a project/activity",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"project": {"type": "string", "description": "Project name (default: general)"},
					"activity": {"type": "string", "description": "Activity description"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags for categorization"},
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				}
			}`),
			Handler: toolTimeStart,
			Builtin: true,
		})
	}
	if enabled("time_stop") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_stop",
			Description: "Stop the currently running time tracking timer",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				}
			}`),
			Handler: toolTimeStop,
			Builtin: true,
		})
	}
	if enabled("time_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_log",
			Description: "Log a manual time entry (already completed work)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"project": {"type": "string", "description": "Project name"},
					"activity": {"type": "string", "description": "Activity description"},
					"duration": {"type": "number", "description": "Duration in minutes"},
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default: today)"},
					"note": {"type": "string", "description": "Notes about the work"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags"},
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				},
				"required": ["duration"]
			}`),
			Handler: toolTimeLog,
			Builtin: true,
		})
	}
	if enabled("time_report") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_report",
			Description: "Generate a time tracking report with hours by project, day, and top activities",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Report period: today, week, month, year (default: week)"},
					"project": {"type": "string", "description": "Filter by project (optional)"},
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				}
			}`),
			Handler: toolTimeReport,
			Builtin: true,
		})
	}

	// --- P29.1: Quick Capture ---
	if enabled("quick_capture") {
		r.Register(&ToolDef{
			Name:        "quick_capture",
			Description: "Quick-capture any text: auto-classifies as task, expense, reminder, contact, note, or idea and routes accordingly",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Free-form text to capture"},
					"category": {"type": "string", "description": "Override category: task, expense, reminder, contact, note, idea (optional, auto-detected if omitted)"}
				},
				"required": ["text"]
			}`),
			Handler: toolQuickCapture,
			Builtin: true,
		})
	}

	// --- P29.0: Lifecycle Automation ---
	if enabled("lifecycle_sync") {
		r.Register(&ToolDef{
			Name:        "lifecycle_sync",
			Description: "Run cross-module lifecycle sync: birthday reminders, insight-driven actions, or both",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Sync action: birthdays, insights, or all (default: all)"}
				}
			}`),
			Handler: toolLifecycleSync,
			Builtin: true,
		})
	}
	if enabled("lifecycle_suggest") {
		r.Register(&ToolDef{
			Name:        "lifecycle_suggest",
			Description: "Suggest habits based on a goal's title and category",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"goal_title": {"type": "string", "description": "Goal title to analyze"},
					"goal_category": {"type": "string", "description": "Goal category (optional)"}
				},
				"required": ["goal_title"]
			}`),
			Handler: toolLifecycleSuggest,
			Builtin: true,
		})
	}

	// --- P23.1: User Profile Tools ---
	if enabled("user_profile_get") {
		r.Register(&ToolDef{
			Name:        "user_profile_get",
			Description: "Get a user's profile including preferences and recent mood",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID"},
					"channelKey": {"type": "string", "description": "Channel key (e.g., 'tg:12345') - resolves to user"}
				}
			}`),
			Handler: toolUserProfileGet,
			Builtin: true,
		})
	}
	if enabled("user_profile_set") {
		r.Register(&ToolDef{
			Name:        "user_profile_set",
			Description: "Update user profile or link a channel identity",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID"},
					"displayName": {"type": "string", "description": "Display name"},
					"language": {"type": "string", "description": "Preferred language"},
					"timezone": {"type": "string", "description": "Timezone"},
					"channelKey": {"type": "string", "description": "Link this channel to user"},
					"channelName": {"type": "string", "description": "Channel display name"}
				},
				"required": ["userId"]
			}`),
			Handler: toolUserProfileSet,
			Builtin: true,
		})
	}
	if enabled("mood_check") {
		r.Register(&ToolDef{
			Name:        "mood_check",
			Description: "Check a user's recent mood trend",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID"},
					"channelKey": {"type": "string", "description": "Channel key to resolve user"},
					"days": {"type": "number", "description": "Number of days to look back (default 7)"}
				}
			}`),
			Handler: toolMoodCheck,
			Builtin: true,
		})
	}

	// --- P23.6: Multi-User / Family Mode Tools ---
	if enabled("family_list_add") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "family_list_add",
			Description: "Add an item to a shared family list (e.g. shopping list)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Item text"},
					"listId": {"type": "string", "description": "List ID (optional, uses default shopping list)"},
					"quantity": {"type": "string", "description": "Quantity (optional)"},
					"addedBy": {"type": "string", "description": "User who added (optional)"}
				},
				"required": ["text"]
			}`),
			Handler: toolFamilyListAdd,
			Builtin: true,
		})
	}
	if enabled("family_list_view") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "family_list_view",
			Description: "View shared family lists and their items",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"listId": {"type": "string", "description": "List ID to view items for (optional, lists all lists if empty)"},
					"listType": {"type": "string", "description": "Filter by list type (optional)"}
				}
			}`),
			Handler: toolFamilyListView,
			Builtin: true,
		})
	}
	if enabled("user_switch") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "user_switch",
			Description: "Switch to a different user context (shows profile, permissions, rate limit)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID to switch to"}
				},
				"required": ["userId"]
			}`),
			Handler: toolUserSwitch,
			Builtin: true,
		})
	}
	if enabled("family_manage") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "family_manage",
			Description: "Manage family users: add, remove, list, update, grant/revoke permissions",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: add, remove, list, update, grant, revoke"},
					"userId": {"type": "string", "description": "User ID"},
					"displayName": {"type": "string", "description": "Display name (for add/update)"},
					"role": {"type": "string", "description": "Role: admin, member, guest (for add/update)"},
					"permission": {"type": "string", "description": "Permission name (for grant/revoke)"},
					"rateLimit": {"type": "integer", "description": "Daily rate limit (for update)"},
					"budgetMonthly": {"type": "number", "description": "Monthly budget (for update)"}
				},
				"required": ["action"]
			}`),
			Handler: toolFamilyManage,
			Builtin: true,
		})
	}
}

// --- Contacts Tool Handlers ---

func toolContactAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactAdd(app.Contacts, newUUID, input)
}

func toolContactSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactSearch(app.Contacts, input)
}

func toolContactList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactList(app.Contacts, input)
}

func toolContactUpcoming(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactUpcoming(app.Contacts, input)
}

func toolContactLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Contacts == nil {
		return "", fmt.Errorf("contacts service not initialized")
	}
	return tool.ContactLog(app.Contacts, newUUID, input)
}

// --- Finance Tool Handlers ---

func toolExpenseAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseAdd(app.Finance, parseExpenseNL, cfg.Finance.DefaultCurrencyOrTWD(), input)
}

func toolExpenseReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseReport(app.Finance, input)
}

func toolExpenseBudget(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Finance == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}
	return tool.ExpenseBudget(app.Finance, cfg.Finance.DefaultCurrencyOrTWD(), input)
}

// --- Goals Tool Handlers ---

func toolGoalCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalCreate(app.Goals, newUUID, app.Lifecycle, cfg.Lifecycle.AutoHabitSuggest, input)
}

func toolGoalList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalList(app.Goals, input)
}

func toolGoalUpdate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalUpdate(app.Goals, newUUID, app.Lifecycle, log.Warn, input)
}

func toolGoalReview(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Goals == nil {
		return "", fmt.Errorf("goals service not initialized")
	}
	return tool.GoalReview(app.Goals, input)
}

// --- Habits Tool Handlers ---

func toolHabitCreate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitCreate(app.Habits, newUUID, input)
}

func toolHabitLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitLog(app.Habits, newUUID, input)
}

func toolHabitStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitStatus(app.Habits, log.Warn, input)
}

func toolHabitReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HabitReport(app.Habits, input)
}

func toolHealthLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HealthLog(app.Habits, newUUID, input)
}

func toolHealthSummary(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Habits == nil {
		return "", fmt.Errorf("habits service not initialized")
	}
	return tool.HealthSummary(app.Habits, input)
}

// --- Time Tracking Tool Handlers ---

func toolTimeStart(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeStart(app.TimeTracking, newUUID, input)
}

func toolTimeStop(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeStop(app.TimeTracking, input)
}

func toolTimeLog(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeLog(app.TimeTracking, newUUID, input)
}

func toolTimeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.TimeTracking == nil {
		return "", fmt.Errorf("time tracking not initialized")
	}
	return tool.TimeReport(app.TimeTracking, input)
}

// --- Family Tool Handlers ---

func toolFamilyListAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Family == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		Text     string `json:"text"`
		Quantity string `json:"quantity"`
		AddedBy  string `json:"addedBy"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.AddedBy == "" {
		args.AddedBy = "default"
	}

	// If listId not provided, use the first shopping list or create one.
	if args.ListID == "" {
		lists, err := app.Family.ListLists()
		if err != nil {
			return "", err
		}
		for _, l := range lists {
			if l.ListType == "shopping" {
				args.ListID = l.ID
				break
			}
		}
		if args.ListID == "" {
			list, err := app.Family.CreateList("Shopping", "shopping", args.AddedBy, newUUID)
			if err != nil {
				return "", fmt.Errorf("create default shopping list: %w", err)
			}
			args.ListID = list.ID
		}
	}

	item, err := app.Family.AddListItem(args.ListID, args.Text, args.Quantity, args.AddedBy)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "added",
		"item":   item,
	})
	return string(b), nil
}

func toolFamilyListView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Family == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		ListType string `json:"listType"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if args.ListID != "" {
		items, err := app.Family.GetListItems(args.ListID)
		if err != nil {
			return "", err
		}
		list, _ := app.Family.GetList(args.ListID)
		result := map[string]any{
			"items": items,
		}
		if list != nil {
			result["list"] = list
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	lists, err := app.Family.ListLists()
	if err != nil {
		return "", err
	}
	if args.ListType != "" {
		var filtered []SharedList
		for _, l := range lists {
			if l.ListType == args.ListType {
				filtered = append(filtered, l)
			}
		}
		lists = filtered
	}

	b, _ := json.Marshal(map[string]any{"lists": lists})
	return string(b), nil
}

func toolUserSwitch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Family == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	user, err := app.Family.GetUser(args.UserID)
	if err != nil {
		return "", fmt.Errorf("user not found or inactive: %w", err)
	}

	allowed, remaining, _ := app.Family.CheckRateLimit(args.UserID)
	perms, _ := app.Family.GetPermissions(args.UserID)

	b, _ := json.Marshal(map[string]any{
		"status":      "switched",
		"user":        user,
		"permissions": perms,
		"rateLimit": map[string]any{
			"allowed":   allowed,
			"remaining": remaining,
		},
	})
	return string(b), nil
}

func toolFamilyManage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Family == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		Action      string  `json:"action"`
		UserID      string  `json:"userId"`
		DisplayName string  `json:"displayName"`
		Role        string  `json:"role"`
		Permission  string  `json:"permission"`
		Grant       bool    `json:"grant"`
		RateLimit   int     `json:"rateLimit"`
		Budget      float64 `json:"budget"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "add":
		if args.Role == "" {
			args.Role = "member"
		}
		if err := app.Family.AddUser(args.UserID, args.DisplayName, args.Role); err != nil {
			return "", err
		}
		user, _ := app.Family.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "added", "user": user})
		return string(b), nil

	case "remove":
		if err := app.Family.RemoveUser(args.UserID); err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"status": "removed", "userId": args.UserID})
		return string(b), nil

	case "list":
		users, err := app.Family.ListUsers()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"users": users})
		return string(b), nil

	case "update":
		updates := make(map[string]any)
		if args.DisplayName != "" {
			updates["displayName"] = args.DisplayName
		}
		if args.Role != "" {
			updates["role"] = args.Role
		}
		if args.RateLimit > 0 {
			updates["rateLimitDaily"] = float64(args.RateLimit)
		}
		if args.Budget > 0 {
			updates["budgetMonthly"] = args.Budget
		}
		if err := app.Family.UpdateUser(args.UserID, updates); err != nil {
			return "", err
		}
		user, _ := app.Family.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "updated", "user": user})
		return string(b), nil

	case "permissions":
		if args.Permission != "" {
			if args.Grant {
				if err := app.Family.GrantPermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			} else {
				if err := app.Family.RevokePermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			}
		}
		perms, err := app.Family.GetPermissions(args.UserID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"userId": args.UserID, "permissions": perms})
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown action: %s (use add, remove, list, update, or permissions)", args.Action)
	}
}

// --- Price Watch Tool Handler ---

func toolPriceWatch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	fs := globalFinanceService
	if app != nil && app.Finance != nil {
		fs = app.Finance
	}
	if fs == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	engineCfg := cfg
	if engineCfg.HistoryDB == "" {
		engineCfg = &Config{HistoryDB: fs.DBPath()}
	}
	engine := newPriceWatchEngine(engineCfg)

	return tool.PriceWatch(engine, input)
}

// --- User Profile Tool Handlers ---

func toolUserProfileGet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := app.UserProfile.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	userCtx, err := app.UserProfile.GetUserContext(args.ChannelKey)
	if err != nil {
		profile, err2 := app.UserProfile.GetProfile(args.UserID)
		if err2 != nil {
			return "", fmt.Errorf("get profile: %w", err2)
		}
		if profile == nil {
			return "", fmt.Errorf("user not found")
		}
		b, _ := json.Marshal(profile)
		return string(b), nil
	}

	b, _ := json.Marshal(userCtx)
	return string(b), nil
}

func toolUserProfileSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
		Language    string `json:"language"`
		Timezone    string `json:"timezone"`
		ChannelKey  string `json:"channelKey"`
		ChannelName string `json:"channelName"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	p, _ := app.UserProfile.GetProfile(args.UserID)
	if p == nil {
		err := app.UserProfile.CreateProfile(UserProfile{ID: args.UserID})
		if err != nil {
			return "", fmt.Errorf("create profile: %w", err)
		}
	}

	updates := make(map[string]string)
	if args.DisplayName != "" {
		updates["displayName"] = args.DisplayName
	}
	if args.Language != "" {
		updates["preferredLanguage"] = args.Language
	}
	if args.Timezone != "" {
		updates["timezone"] = args.Timezone
	}
	if len(updates) > 0 {
		if err := app.UserProfile.UpdateProfile(args.UserID, updates); err != nil {
			return "", fmt.Errorf("update profile: %w", err)
		}
	}

	if args.ChannelKey != "" {
		if err := app.UserProfile.LinkChannel(args.UserID, args.ChannelKey, args.ChannelName); err != nil {
			return "", fmt.Errorf("link channel: %w", err)
		}
	}

	return fmt.Sprintf(`{"status":"ok","userId":"%s"}`, args.UserID), nil
}

func toolMoodCheck(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		UserID     string `json:"userId"`
		ChannelKey string `json:"channelKey"`
		Days       int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	app := appFromCtx(ctx)
	if app == nil || app.UserProfile == nil {
		return "", fmt.Errorf("user profile service not initialized")
	}

	if args.UserID == "" && args.ChannelKey != "" {
		uid, err := app.UserProfile.ResolveUser(args.ChannelKey)
		if err != nil {
			return "", fmt.Errorf("resolve user: %w", err)
		}
		args.UserID = uid
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId or channelKey is required")
	}

	if args.Days <= 0 {
		args.Days = 7
	}

	mood, err := app.UserProfile.GetMoodTrend(args.UserID, args.Days)
	if err != nil {
		return "", fmt.Errorf("get mood: %w", err)
	}

	var totalScore float64
	for _, m := range mood {
		if s, ok := m["sentimentScore"].(float64); ok {
			totalScore += s
		}
	}
	avg := 0.0
	if len(mood) > 0 {
		avg = totalScore / float64(len(mood))
	}

	result := map[string]any{
		"userId":       args.UserID,
		"days":         args.Days,
		"entries":      len(mood),
		"averageScore": avg,
		"label":        nlp.Label(avg),
		"trend":        mood,
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// --- Task Sync: Todoist ---

func toolTodoistSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if !cfg.TaskManager.Todoist.Enabled {
		return "", fmt.Errorf("todoist sync not enabled")
	}
	var args struct {
		Action string `json:"action"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	ts := newTodoistSync(cfg)

	switch args.Action {
	case "pull":
		n, err := ts.PullTasks(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Pulled %d tasks from Todoist.", n), nil
	case "push":
		if app == nil || app.TaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		localTasks, _ := app.TaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range localTasks {
			if task.ExternalSource == "todoist" || task.ExternalID != "" {
				continue
			}
			if err := ts.PushTask(task); err != nil {
				continue
			}
			pushed++
		}
		return fmt.Sprintf("Pushed %d tasks to Todoist.", pushed), nil
	case "sync", "":
		pulled, pushed, err := ts.SyncAll(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Todoist sync complete: pulled %d, pushed %d.", pulled, pushed), nil
	default:
		return "", fmt.Errorf("unknown action %q (use pull, push, or sync)", args.Action)
	}
}

// --- Task Sync: Notion ---

func toolNotionSync(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if !cfg.TaskManager.Notion.Enabled {
		return "", fmt.Errorf("notion sync not enabled")
	}
	var args struct {
		Action string `json:"action"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	ns := newNotionSync(cfg)

	switch args.Action {
	case "pull":
		n, err := ns.PullTasks(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Pulled %d tasks from Notion.", n), nil
	case "push":
		if app == nil || app.TaskManager == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		localTasks, _ := app.TaskManager.ListTasks(args.UserID, TaskFilter{})
		pushed := 0
		for _, task := range localTasks {
			if task.ExternalSource == "notion" || task.ExternalID != "" {
				continue
			}
			if err := ns.PushTask(task); err != nil {
				continue
			}
			pushed++
		}
		return fmt.Sprintf("Pushed %d tasks to Notion.", pushed), nil
	case "sync", "":
		pulled, pushed, err := ns.SyncAll(args.UserID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Notion sync complete: pulled %d, pushed %d.", pulled, pushed), nil
	default:
		return "", fmt.Errorf("unknown action %q (use pull, push, or sync)", args.Action)
	}
}

// --- Quick Capture Tool Handler ---

func classifyCapture(input string) string {
	lower := strings.ToLower(input)
	if strings.Contains(lower, "$") || strings.Contains(lower, "spent") ||
		strings.Contains(lower, "paid") || strings.Contains(lower, "bought") ||
		strings.Contains(lower, "cost") || strings.Contains(lower, "元") ||
		strings.Contains(lower, "円") {
		return "expense"
	}
	if strings.Contains(lower, "remind") || strings.Contains(lower, "deadline") ||
		strings.Contains(lower, "don't forget") || strings.Contains(lower, "dont forget") {
		return "reminder"
	}
	if strings.Contains(lower, "phone") || strings.Contains(lower, "email") ||
		strings.Contains(lower, "birthday") || strings.Contains(input, "@") {
		return "contact"
	}
	if strings.Contains(lower, "todo") || strings.Contains(lower, "need to") ||
		strings.Contains(lower, "must") || strings.Contains(lower, "should") ||
		strings.Contains(lower, "fix") {
		return "task"
	}
	if strings.HasPrefix(lower, "idea:") || strings.Contains(lower, "what if") {
		return "idea"
	}
	return "note"
}

func executeCapture(ctx context.Context, cfg *Config, category, text string) (string, error) {
	app := appFromCtx(ctx)
	switch category {
	case "task":
		tm := globalTaskManager
		if app != nil && app.TaskManager != nil {
			tm = app.TaskManager
		}
		if tm == nil {
			return "", fmt.Errorf("task manager not initialized")
		}
		task, err := tm.CreateTask(UserTask{
			Title:  text,
			Status: "todo",
		})
		if err != nil {
			return "", fmt.Errorf("create task: %w", err)
		}
		return fmt.Sprintf("Task created: %s (id=%s)", task.Title, task.ID), nil

	case "expense":
		input, _ := json.Marshal(map[string]string{"text": text})
		return toolExpenseAdd(ctx, cfg, input)

	case "reminder":
		re := globalReminderEngine
		if app != nil && app.Reminder != nil {
			re = app.Reminder
		}
		if re == nil {
			return "", fmt.Errorf("reminder engine not initialized")
		}
		due := time.Now().Add(24 * time.Hour)
		r, err := re.Add(text, due, "", "", "default")
		if err != nil {
			return "", fmt.Errorf("add reminder: %w", err)
		}
		return fmt.Sprintf("Reminder set: %s (due=%s)", r.Text, r.DueAt), nil

	case "contact":
		cs := globalContactsService
		if app != nil && app.Contacts != nil {
			cs = app.Contacts
		}
		if cs == nil {
			return "", fmt.Errorf("contacts service not initialized")
		}
		now := time.Now().UTC().Format(time.RFC3339)
		c := &Contact{ID: newUUID(), Name: text, CreatedAt: now, UpdatedAt: now}
		if err := cs.AddContact(c); err != nil {
			return "", fmt.Errorf("add contact: %w", err)
		}
		return fmt.Sprintf("Contact added: %s (id=%s)", c.Name, c.ID), nil

	case "note", "idea":
		if !cfg.Notes.Enabled {
			return "", fmt.Errorf("notes not enabled in config")
		}
		vaultPath := cfg.Notes.VaultPathResolved(cfg.BaseDir)
		prefix := "note"
		if category == "idea" {
			prefix = "idea"
		}
		filename := fmt.Sprintf("%s-%s.md", prefix, time.Now().Format("20060102-150405"))
		notePath := filepath.Join(vaultPath, filename)
		os.MkdirAll(vaultPath, 0o755)
		if err := os.WriteFile(notePath, []byte(text+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("write note: %w", err)
		}
		return fmt.Sprintf("Note saved: %s", notePath), nil

	default:
		return "", fmt.Errorf("unknown capture category: %s", category)
	}
}

func toolQuickCapture(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}

	category := args.Category
	if category == "" {
		category = classifyCapture(args.Text)
	}

	result, err := executeCapture(ctx, cfg, category, args.Text)
	if err != nil {
		return "", err
	}

	out := map[string]string{
		"category": category,
		"result":   result,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// --- P24.7: Morning Briefing & Evening Wrap ---

var globalBriefingService *briefing.Service

// newBriefingService constructs a briefing.Service from Config + globals.
func newBriefingService(cfg *Config) *briefing.Service {
	deps := briefing.Deps{
		Query:  db.Query,
		Escape: db.Escape,
	}
	if globalSchedulingService != nil {
		svc := globalSchedulingService
		deps.ViewSchedule = func(dateStr string, days int) ([]briefing.ScheduleDay, error) {
			schedules, err := svc.ViewSchedule(dateStr, days)
			if err != nil {
				return nil, err
			}
			result := make([]briefing.ScheduleDay, len(schedules))
			for i, s := range schedules {
				events := make([]briefing.ScheduleEvent, len(s.Events))
				for j, ev := range s.Events {
					events[j] = briefing.ScheduleEvent{
						Start: ev.Start,
						Title: ev.Title,
					}
				}
				result[i] = briefing.ScheduleDay{Events: events}
			}
			return result, nil
		}
	}
	if globalContactsService != nil {
		svc := globalContactsService
		deps.GetUpcomingEvents = func(days int) ([]map[string]any, error) {
			return svc.GetUpcomingEvents(days)
		}
	}
	deps.TasksAvailable = globalTaskManager != nil
	deps.HabitsAvailable = globalHabitsService != nil
	deps.GoalsAvailable = globalGoalsService != nil
	deps.FinanceAvailable = globalFinanceService != nil
	return briefing.New(cfg.HistoryDB, deps)
}

// --- Briefing Tool Handlers ---

func toolBriefingMorning(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Briefing == nil {
		return "", fmt.Errorf("briefing service not initialized")
	}
	var args struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	date := time.Now()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		date = parsed
	}
	br, err := app.Briefing.GenerateMorning(date)
	if err != nil {
		return "", err
	}
	return briefing.FormatBriefing(br), nil
}

func toolBriefingEvening(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Briefing == nil {
		return "", fmt.Errorf("briefing service not initialized")
	}
	var args struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	date := time.Now()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		date = parsed
	}
	br, err := app.Briefing.GenerateEvening(date)
	if err != nil {
		return "", err
	}
	return briefing.FormatBriefing(br), nil
}

// --- P19.3: Smart Reminders ---

// nextCronTime computes the next occurrence of a cron expression after the given time.
// Reuses parseCronExpr and nextRunAfter from cron.go.
func nextCronTime(expr string, after time.Time) time.Time {
	parsed, err := parseCronExpr(expr)
	if err != nil {
		log.Warn("reminder bad cron expr", "expr", expr, "error", err)
		return time.Time{}
	}
	return nextRunAfter(parsed, time.UTC, after)
}

// parseNaturalTime delegates to internal reminder package.
func parseNaturalTime(input string) (time.Time, error) {
	return reminder.ParseNaturalTime(input)
}

// --- Tool Handlers for Reminders ---

// Global reminder engine reference (set in main.go).
var globalReminderEngine *ReminderEngine

func toolReminderSet(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		Text      string `json:"text"`
		Time      string `json:"time"`
		Recurring string `json:"recurring"`
		Channel   string `json:"channel"`
		UserID    string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.Time == "" {
		return "", fmt.Errorf("time is required")
	}

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized (enable reminders in config)")
	}

	dueAt, err := parseNaturalTime(args.Time)
	if err != nil {
		return "", fmt.Errorf("parse time %q: %w", args.Time, err)
	}

	// Validate recurring expression if provided.
	if args.Recurring != "" {
		if _, err := parseCronExpr(args.Recurring); err != nil {
			return "", fmt.Errorf("invalid recurring cron expression %q: %w", args.Recurring, err)
		}
	}

	rem, err := app.Reminder.Add(args.Text, dueAt, args.Recurring, args.Channel, args.UserID)
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(rem)
	return string(out), nil
}

func toolReminderList(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		UserID string `json:"user_id"`
	}
	json.Unmarshal(input, &args)

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	reminders, err := app.Reminder.List(args.UserID)
	if err != nil {
		return "", err
	}
	if reminders == nil {
		reminders = []Reminder{}
	}

	out, _ := json.Marshal(map[string]any{
		"reminders": reminders,
		"count":     len(reminders),
	})
	return string(out), nil
}

func toolReminderCancel(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	var args struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.ID == "" {
		return "", fmt.Errorf("id is required")
	}

	if app == nil || app.Reminder == nil {
		return "", fmt.Errorf("reminder engine not initialized")
	}

	if err := app.Reminder.Cancel(args.ID, args.UserID); err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"ok":true,"id":"%s","status":"cancelled"}`, args.ID), nil
}
