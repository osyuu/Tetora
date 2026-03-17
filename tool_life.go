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
