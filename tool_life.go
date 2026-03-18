package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

// --- Lesson Tool Handler ---

func toolStoreLesson(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string   `json:"category"`
		Lesson   string   `json:"lesson"`
		Source   string   `json:"source"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if args.Lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}

	category := sanitizeLessonCategory(args.Category)
	noteName := "lessons/" + category

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	now := time.Now().Format("2006-01-02 15:04")
	var entry strings.Builder
	entry.WriteString(fmt.Sprintf("\n## %s\n", now))
	entry.WriteString(fmt.Sprintf("- %s\n", args.Lesson))
	if args.Source != "" {
		entry.WriteString(fmt.Sprintf("- Source: %s\n", args.Source))
	}
	if len(args.Tags) > 0 {
		entry.WriteString(fmt.Sprintf("- Tags: %s\n", strings.Join(args.Tags, ", ")))
	}

	if err := svc.AppendNote(noteName, entry.String()); err != nil {
		return "", fmt.Errorf("append to vault: %w", err)
	}

	lessonsFile := "tasks/lessons.md"
	if _, err := os.Stat(lessonsFile); err == nil {
		sectionHeader := "## " + args.Category
		line := fmt.Sprintf("- %s", args.Lesson)
		if err := appendToLessonSection(lessonsFile, sectionHeader, line); err != nil {
			log.Warn("append to lessons.md failed", "error", err)
		}
	}

	if cfg.HistoryDB != "" {
		recordSkillEvent(cfg.HistoryDB, category, "lesson", args.Lesson, args.Source)
	}

	log.InfoCtx(ctx, "lesson stored", "category", category, "tags", args.Tags)

	result := map[string]any{
		"status":   "stored",
		"category": category,
		"vault":    noteName,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func sanitizeLessonCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	cat = re.ReplaceAllString(cat, "-")
	cat = strings.Trim(cat, "-")
	if cat == "" {
		cat = "general"
	}
	return cat
}

func appendToLessonSection(filePath, sectionHeader, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	inserted := false

	for i, line := range lines {
		result = append(result, line)
		if strings.TrimSpace(line) == sectionHeader {
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				j++
			}
			insertIdx := j
			for insertIdx > i+1 && strings.TrimSpace(lines[insertIdx-1]) == "" {
				insertIdx--
			}
			for k := i + 1; k < insertIdx; k++ {
				result = append(result, lines[k])
			}
			result = append(result, content)
			for k := insertIdx; k < len(lines); k++ {
				result = append(result, lines[k])
			}
			inserted = true
			break
		}
	}

	if !inserted {
		result = append(result, "", sectionHeader, content)
	}

	return os.WriteFile(filePath, []byte(strings.Join(result, "\n")), 0o644)
}

// --- Note Dedup Tool Handler ---

func toolNoteDedup(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		AutoDelete bool   `json:"auto_delete"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()

	type fileHash struct {
		Path string
		Hash string
		Size int64
	}
	var files []fileHash
	hashMap := make(map[string][]string)

	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if args.Prefix != "" {
			rel, _ := filepath.Rel(vaultPath, path)
			if !strings.HasPrefix(rel, args.Prefix) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:16])
		rel, _ := filepath.Rel(vaultPath, path)
		files = append(files, fileHash{Path: rel, Hash: hash, Size: info.Size()})
		hashMap[hash] = append(hashMap[hash], rel)
		return nil
	})

	var duplicates []map[string]any
	deleted := 0
	for hash, paths := range hashMap {
		if len(paths) <= 1 {
			continue
		}
		if args.AutoDelete {
			for _, p := range paths[1:] {
				fullPath := filepath.Join(vaultPath, p)
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
		duplicates = append(duplicates, map[string]any{
			"hash":  hash,
			"files": paths,
			"count": len(paths),
		})
	}

	result := map[string]any{
		"total_files":      len(files),
		"duplicate_groups": len(duplicates),
		"duplicates":       duplicates,
	}
	if args.AutoDelete {
		result["deleted"] = deleted
	}

	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "note dedup scan complete", "total_files", len(files), "duplicate_groups", len(duplicates))
	return string(b), nil
}

// --- Source Audit Tool Handler ---

func toolSourceAudit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Expected []string `json:"expected"`
		Prefix   string   `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()
	prefix := args.Prefix
	if prefix == "" {
		prefix = "."
	}

	actualSet := make(map[string]bool)
	scanDir := filepath.Join(vaultPath, prefix)
	filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vaultPath, path)
		actualSet[rel] = true
		return nil
	})

	expectedSet := make(map[string]bool)
	for _, e := range args.Expected {
		expectedSet[e] = true
	}

	var missing, extra []string
	for e := range expectedSet {
		if !actualSet[e] {
			missing = append(missing, e)
		}
	}
	for a := range actualSet {
		if !expectedSet[a] {
			extra = append(extra, a)
		}
	}

	result := map[string]any{
		"expected_count": len(args.Expected),
		"actual_count":   len(actualSet),
		"missing_count":  len(missing),
		"extra_count":    len(extra),
		"missing":        missing,
		"extra":          extra,
	}
	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "source audit complete", "expected", len(args.Expected), "actual", len(actualSet))
	return string(b), nil
}

// --- P21.5: Sitemap Ingest Pipeline ---

// toolWebCrawl fetches a sitemap and imports pages into the notes vault.
func toolWebCrawl(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		URL         string   `json:"url"`
		Mode        string   `json:"mode"`        // "sitemap" (default), "single"
		Include     []string `json:"include"`      // glob patterns to include
		Exclude     []string `json:"exclude"`      // glob patterns to exclude
		Target      string   `json:"target"`       // "notes" (default)
		Prefix      string   `json:"prefix"`       // note path prefix
		Dedup       bool     `json:"dedup"`         // skip if same content hash exists
		MaxPages    int      `json:"max_pages"`
		Concurrency int      `json:"concurrency"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if args.Mode == "" {
		args.Mode = "sitemap"
	}
	if args.MaxPages <= 0 {
		args.MaxPages = 500
	}
	if args.Concurrency <= 0 {
		args.Concurrency = 3
	}
	if args.Target == "" {
		args.Target = "notes"
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service not enabled")
	}

	var urls []string
	switch args.Mode {
	case "sitemap":
		var err error
		urls, err = fetchSitemapURLs(ctx, args.URL)
		if err != nil {
			return "", fmt.Errorf("fetch sitemap: %w", err)
		}
	case "single":
		urls = []string{args.URL}
	default:
		return "", fmt.Errorf("unknown mode: %s", args.Mode)
	}

	// Apply filters.
	urls = filterURLs(urls, args.Include, args.Exclude)

	// Cap at max pages.
	if len(urls) > args.MaxPages {
		urls = urls[:args.MaxPages]
	}

	log.InfoCtx(ctx, "web_crawl starting", "urls", len(urls), "prefix", args.Prefix)

	// Fetch pages concurrently.
	type pageResult struct {
		URL    string
		Status string // "imported", "skipped", "failed"
		Error  string
	}

	results := make([]pageResult, len(urls))
	sem := make(chan struct{}, args.Concurrency)
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, pageURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			status, err := ingestPage(ctx, svc, pageURL, args.Prefix, args.Dedup)
			results[idx] = pageResult{URL: pageURL, Status: status}
			if err != nil {
				results[idx].Error = err.Error()
			}
		}(i, u)
	}
	wg.Wait()

	// Summarize.
	imported, skipped, failed := 0, 0, 0
	var errors []string
	for _, r := range results {
		switch r.Status {
		case "imported":
			imported++
		case "skipped":
			skipped++
		default:
			failed++
			if r.Error != "" {
				errors = append(errors, fmt.Sprintf("%s: %s", r.URL, r.Error))
			}
		}
	}

	summary := map[string]any{
		"total":    len(urls),
		"imported": imported,
		"skipped":  skipped,
		"failed":   failed,
	}
	if len(errors) > 0 {
		// Cap errors to avoid huge output.
		if len(errors) > 10 {
			errors = errors[:10]
		}
		summary["errors"] = errors
	}

	b, _ := json.Marshal(summary)
	return string(b), nil
}

// fetchSitemapURLs fetches and parses a sitemap (or sitemap index).
func fetchSitemapURLs(ctx context.Context, sitemapURL string) ([]string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", sitemapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return nil, err
	}

	content := string(body)

	// Check if this is a sitemap index.
	if strings.Contains(content, "<sitemapindex") {
		return parseSitemapIndex(ctx, content, client)
	}

	return parseSitemapURLs(content), nil
}

// parseSitemapIndex parses a <sitemapindex> and fetches child sitemaps.
func parseSitemapIndex(ctx context.Context, content string, client *http.Client) ([]string, error) {
	// Extract <loc> from <sitemap> entries.
	re := regexp.MustCompile(`<sitemap>[^<]*<loc>([^<]+)</loc>`)
	matches := re.FindAllStringSubmatch(content, -1)

	var allURLs []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		childURL := strings.TrimSpace(m[1])
		req, err := http.NewRequestWithContext(ctx, "GET", childURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Tetora/2.0")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			continue
		}
		urls := parseSitemapURLs(string(body))
		allURLs = append(allURLs, urls...)
	}
	return allURLs, nil
}

// parseSitemapURLs extracts <loc> URLs from a <urlset> sitemap.
func parseSitemapURLs(content string) []string {
	re := regexp.MustCompile(`<url>[^<]*<loc>([^<]+)</loc>`)
	matches := re.FindAllStringSubmatch(content, -1)
	var urls []string
	for _, m := range matches {
		if len(m) >= 2 {
			urls = append(urls, strings.TrimSpace(m[1]))
		}
	}
	return urls
}

// filterURLs applies include/exclude glob patterns to a URL list.
func filterURLs(urls, include, exclude []string) []string {
	if len(include) == 0 && len(exclude) == 0 {
		return urls
	}
	var result []string
	for _, u := range urls {
		// Check exclude first.
		excluded := false
		for _, pat := range exclude {
			if matched, _ := filepath.Match(pat, u); matched {
				excluded = true
				break
			}
			// Also try matching just the path portion.
			if idx := strings.Index(u, "://"); idx >= 0 {
				pathPart := u[idx+3:]
				if matched, _ := filepath.Match(pat, pathPart); matched {
					excluded = true
					break
				}
			}
		}
		if excluded {
			continue
		}

		// Check include (if any patterns specified, URL must match at least one).
		if len(include) > 0 {
			included := false
			for _, pat := range include {
				if matched, _ := filepath.Match(pat, u); matched {
					included = true
					break
				}
				if idx := strings.Index(u, "://"); idx >= 0 {
					pathPart := u[idx+3:]
					if matched, _ := filepath.Match(pat, pathPart); matched {
						included = true
						break
					}
				}
			}
			if !included {
				continue
			}
		}
		result = append(result, u)
	}
	return result
}

// ingestPage fetches a URL, strips HTML, and writes to notes vault.
func ingestPage(ctx context.Context, svc *NotesService, pageURL, prefix string, dedup bool) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return "failed", err
	}
	req.Header.Set("User-Agent", "Tetora/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return "failed", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "failed", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 5MB limit
	if err != nil {
		return "failed", err
	}

	text := stripHTMLTags(string(body))
	if strings.TrimSpace(text) == "" {
		return "skipped", nil
	}

	// Generate note name from URL.
	slug := urlToSlug(pageURL)
	noteName := slug
	if prefix != "" {
		noteName = prefix + "/" + slug
	}

	// Dedup check.
	if dedup {
		h := sha256.Sum256([]byte(text))
		hash := hex.EncodeToString(h[:16])
		// Check if note already exists with same hash.
		existing, err := svc.ReadNote(noteName)
		if err == nil && existing != "" {
			// Strip frontmatter before hashing to compare body only.
			body := stripFrontmatter(existing)
			existingH := sha256.Sum256([]byte(body))
			existingHash := hex.EncodeToString(existingH[:16])
			if existingHash == hash {
				return "skipped", nil
			}
		}
	}

	// Write as markdown with URL source header.
	content := fmt.Sprintf("---\nsource: %s\nimported: %s\n---\n\n%s", pageURL, time.Now().Format("2006-01-02"), text)
	if err := svc.CreateNote(noteName, content); err != nil {
		return "failed", err
	}

	return "imported", nil
}

// urlToSlug converts a URL to a filesystem-safe slug.
// The slug intentionally avoids dots so ensureExt appends .md reliably.
func urlToSlug(u string) string {
	// Remove scheme.
	slug := u
	if idx := strings.Index(slug, "://"); idx >= 0 {
		slug = slug[idx+3:]
	}
	// Remove query/fragment.
	if idx := strings.IndexAny(slug, "?#"); idx >= 0 {
		slug = slug[:idx]
	}
	// Replace path separators and special chars.
	slug = strings.TrimRight(slug, "/")
	slug = strings.ReplaceAll(slug, "/", "_")
	// Remove non-alphanumeric chars except - _
	// (dots excluded so filepath.Ext returns "" and ensureExt adds .md)
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	slug = re.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "page"
	}
	// Cap length.
	if len(slug) > 100 {
		slug = slug[:100]
	}
	return slug
}

// toolSourceAuditURL compares a sitemap's URLs against imported notes.
func toolSourceAuditURL(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		SitemapURL string `json:"sitemap_url"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.SitemapURL == "" {
		return "", fmt.Errorf("sitemap_url is required")
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service not enabled")
	}

	// Fetch sitemap URLs.
	urls, err := fetchSitemapURLs(ctx, args.SitemapURL)
	if err != nil {
		return "", fmt.Errorf("fetch sitemap: %w", err)
	}

	// Build expected note names.
	expectedNotes := make(map[string]string) // noteName -> URL
	for _, u := range urls {
		slug := urlToSlug(u)
		noteName := slug
		if args.Prefix != "" {
			noteName = args.Prefix + "/" + slug
		}
		expectedNotes[noteName] = u
	}

	// Check which exist.
	var missing []map[string]string
	existing := 0
	for name, url := range expectedNotes {
		content, err := svc.ReadNote(name)
		if err != nil || content == "" {
			missing = append(missing, map[string]string{"note": name, "url": url})
		} else {
			existing++
		}
	}

	result := map[string]any{
		"total":         len(urls),
		"existing":      existing,
		"missing_count": len(missing),
	}
	// Cap missing list.
	if len(missing) > 50 {
		result["missing"] = missing[:50]
		result["missing_truncated"] = true
	} else {
		result["missing"] = missing
	}

	b, _ := json.Marshal(result)
	return string(b), nil
}

// stripFrontmatter removes YAML frontmatter (--- delimited) from content.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	// Find closing ---.
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return content
	}
	// Skip frontmatter + trailing newlines.
	body := content[4+end+5:]
	return strings.TrimLeft(body, "\n")
}

// Registration moved to internal/tools/taskboard.go.
// Handler factories below are passed via TaskboardDeps in wire_tools.go.

// --- Handler Factories ---

func toolTaskboardList(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			Status   string `json:"status"`
			Assignee string `json:"assignee"`
			Project  string `json:"project"`
			ParentID string `json:"parentId"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		// If parentId is specified, use ListChildren.
		if args.ParentID != "" {
			children, err := tb.ListChildren(args.ParentID)
			if err != nil {
				return "", err
			}
			out, _ := json.MarshalIndent(children, "", "  ")
			return string(out), nil
		}

		tasks, err := tb.ListTasks(args.Status, args.Assignee, args.Project)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(tasks, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardGet(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" {
			return "", fmt.Errorf("id is required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.GetTask(args.ID)
		if err != nil {
			// Suggest similar tasks on not-found.
			normalizedID := normalizeTaskID(args.ID)
			if candidates := tb.SuggestTasks(normalizedID); len(candidates) > 0 {
				lines := []string{err.Error(), "Did you mean:"}
				for _, c := range candidates {
					lines = append(lines, fmt.Sprintf("  %s  %s  (%s)", c.ID, c.Title, c.Status))
				}
				return "", fmt.Errorf("%s", strings.Join(lines, "\n"))
			}
			return "", err
		}
		// Use normalized ID (from task) for thread lookup.
		comments, err := tb.GetThread(task.ID)
		if err != nil {
			return "", err
		}

		result := map[string]any{
			"task":     task,
			"comments": comments,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardCreate(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Assignee    string   `json:"assignee"`
			Priority    string   `json:"priority"`
			Project     string   `json:"project"`
			ParentID    string   `json:"parentId"`
			Model       string   `json:"model"`
			DependsOn   []string `json:"dependsOn"`
			Workflow    string   `json:"workflow"`
			Type        string   `json:"type"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.Title == "" {
			return "", fmt.Errorf("title is required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.CreateTask(TaskBoard{
			Title:       args.Title,
			Description: args.Description,
			Assignee:    args.Assignee,
			Priority:    args.Priority,
			Project:     args.Project,
			ParentID:    args.ParentID,
			Model:       args.Model,
			DependsOn:   args.DependsOn,
			Workflow:    args.Workflow,
			Type:        args.Type,
		})
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(task, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardMove(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ID == "" || args.Status == "" {
			return "", fmt.Errorf("id and status are required")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		task, err := tb.MoveTask(args.ID, args.Status)
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(task, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardComment(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			TaskID  string `json:"taskId"`
			Content string `json:"content"`
			Author  string `json:"author"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.TaskID == "" || args.Content == "" {
			return "", fmt.Errorf("taskId and content are required")
		}
		if args.Author == "" {
			args.Author = "agent"
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		comment, err := tb.AddComment(args.TaskID, args.Author, args.Content, args.Type)
		if err != nil {
			return "", err
		}

		out, _ := json.MarshalIndent(comment, "", "  ")
		return string(out), nil
	}
}

func toolTaskboardDecompose(cfg *Config) ToolHandler {
	return func(ctx context.Context, _ *Config, input json.RawMessage) (string, error) {
		var args struct {
			ParentID string `json:"parentId"`
			Subtasks []struct {
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Assignee    string   `json:"assignee"`
				Priority    string   `json:"priority"`
				Model       string   `json:"model"`
				Type        string   `json:"type"`
				DependsOn   []string `json:"dependsOn"`
			} `json:"subtasks"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "", fmt.Errorf("invalid input: %w", err)
		}
		if args.ParentID == "" {
			return "", fmt.Errorf("parentId is required")
		}
		if len(args.Subtasks) == 0 {
			return "", fmt.Errorf("subtasks array is required and must not be empty")
		}

		tb := newTaskBoardEngine(cfg.HistoryDB, cfg.TaskBoard, cfg.Webhooks)

		// Verify parent exists.
		parent, err := tb.GetTask(args.ParentID)
		if err != nil {
			return "", fmt.Errorf("parent task not found: %w", err)
		}

		// Fetch existing children for idempotency check.
		existing, err := tb.ListChildren(args.ParentID)
		if err != nil {
			return "", fmt.Errorf("failed to list existing children: %w", err)
		}
		existingTitles := make(map[string]bool, len(existing))
		for _, e := range existing {
			existingTitles[e.Title] = true
		}

		var created, skipped int
		var subtaskIDs []string

		for _, sub := range args.Subtasks {
			if sub.Title == "" {
				continue
			}

			// Idempotency: skip if same title already exists under this parent.
			if existingTitles[sub.Title] {
				skipped++
				continue
			}

			priority := sub.Priority
			if priority == "" {
				priority = parent.Priority
			}

			subType := sub.Type
			if subType == "" {
				subType = parent.Type
			}

			task, err := tb.CreateTask(TaskBoard{
				Title:       sub.Title,
				Description: sub.Description,
				Assignee:    sub.Assignee,
				Priority:    priority,
				Project:     parent.Project,
				ParentID:    args.ParentID,
				Model:       sub.Model,
				Type:        subType,
				DependsOn:   sub.DependsOn,
			})
			if err != nil {
				log.Warn("taskboard_decompose: create subtask failed", "parent", args.ParentID, "title", sub.Title, "error", err)
				continue
			}

			created++
			subtaskIDs = append(subtaskIDs, task.ID)
			existingTitles[sub.Title] = true
		}

		// Move parent to "todo" (ready, waiting for children) if it was in backlog.
		if created > 0 && parent.Status == "backlog" {
			if _, err := tb.MoveTask(args.ParentID, "todo"); err != nil {
				log.Warn("taskboard_decompose: failed to move parent to todo", "parentId", args.ParentID, "error", err)
			}
		}

		// Add decomposition comment to parent.
		if created > 0 {
			comment := fmt.Sprintf("[decompose] Created %d subtasks (skipped %d existing): %s",
				created, skipped, strings.Join(subtaskIDs, ", "))
			if _, err := tb.AddComment(args.ParentID, "system", comment); err != nil {
				log.Warn("taskboard_decompose: add comment failed", "parentId", args.ParentID, "error", err)
			}
		}

		result := map[string]any{
			"created":    created,
			"skipped":    skipped,
			"subtaskIds": subtaskIDs,
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil
	}
}
