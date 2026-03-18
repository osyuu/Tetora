package main

// wire_life.go wires the life service internal packages to the root package
// by providing constructors and type aliases that keep the root API surface stable.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"tetora/internal/automation/insights"
	"tetora/internal/db"
	idispatch "tetora/internal/dispatch"
	"tetora/internal/log"
	"tetora/internal/nlp"
	"tetora/internal/notify"
	"tetora/internal/project"
	"tetora/internal/push"
	"tetora/internal/reflection"
	"tetora/internal/review"
	"tetora/internal/roles"
	"tetora/internal/tool"
	"tetora/internal/workspace"

	"tetora/internal/life/calendar"
	"tetora/internal/life/contacts"
	"tetora/internal/life/dailynotes"
	"tetora/internal/life/family"
	"tetora/internal/life/finance"
	"tetora/internal/life/goals"
	"tetora/internal/life/habits"
	"tetora/internal/life/lifedb"
	"tetora/internal/life/pricewatch"
	"tetora/internal/life/profile"
	"tetora/internal/life/reminder"
	"tetora/internal/life/tasks"
	"tetora/internal/life/timetracking"
)

// --- Service type aliases ---

type UserProfileService = profile.Service
type UserProfile = profile.UserProfile
type ChannelIdentity = profile.ChannelIdentity
type UserPreference = profile.UserPreference

type TaskManagerService = tasks.Service
type UserTask = tasks.UserTask
type TaskProject = tasks.TaskProject
type TaskReview = tasks.TaskReview
type TaskFilter = tasks.TaskFilter
type TodoistTask = tasks.TodoistTask

type FinanceService = finance.Service
type HabitsService = habits.Service
type GoalsService = goals.Service
type CalendarService = calendar.Service
type ContactsService = contacts.Service
type FamilyService = family.Service
type PriceWatchEngine = pricewatch.Service
type ReminderEngine = reminder.Engine
type TimeTrackingService = timetracking.Service
type DailyNotesService = dailynotes.Service

// --- Data type aliases ---

// Finance types
type Expense = finance.Expense
type Budget = finance.Budget
type ExpenseReport = finance.ExpenseReport
type ExpenseBudgetStatus = finance.ExpenseBudgetStatus
type PriceWatch = pricewatch.PriceWatch

// Goals types
type Goal = goals.Goal
type Milestone = goals.Milestone
type ReviewNote = goals.ReviewNote

// Contacts types
type Contact = contacts.Contact
type ContactInteraction = contacts.ContactInteraction

// Family types
type FamilyUser = family.FamilyUser
type SharedList = family.SharedList
type SharedListItem = family.SharedListItem

// Calendar types
type CalendarEvent = calendar.Event
type CalendarEventInput = calendar.EventInput

// TimeTracking types
type TimeEntry = timetracking.TimeEntry
type TimeReport = timetracking.TimeReport
type ActivitySummary = timetracking.ActivitySummary

// Reminder types
type Reminder = reminder.Reminder

// --- makeLifeDB ---

// makeLifeDB returns a lifedb.DB wired to the root package helpers.
func makeLifeDB() lifedb.DB {
	return lifedb.DB{
		Query:   db.Query,
		Exec:    db.Exec,
		Escape:  db.Escape,
		LogInfo: log.Info,
		LogWarn: log.Warn,
	}
}

// --- Constructors ---

func newFinanceService(cfg *Config) *FinanceService {
	encFn := func(v string) string { return encryptField(cfg, v) }
	decFn := func(v string) string { return decryptField(cfg, v) }
	return finance.New(cfg.HistoryDB, cfg.Finance.DefaultCurrencyOrTWD(), makeLifeDB(), encFn, decFn)
}

func initFinanceDB(dbPath string) error {
	return finance.InitDB(dbPath)
}

func newHabitsService(cfg *Config) *HabitsService {
	return habits.New(cfg.HistoryDB, makeLifeDB())
}

func initHabitsDB(dbPath string) error {
	return habits.InitDB(dbPath)
}

func newGoalsService(cfg *Config) *GoalsService {
	return goals.New(cfg.HistoryDB, makeLifeDB())
}

func initGoalsDB(dbPath string) error {
	return goals.InitDB(dbPath)
}

func newCalendarService(cfg *Config) *CalendarService {
	var oauth calendar.OAuthRequester
	if globalOAuthManager != nil {
		oauth = &oauthAdapter{mgr: globalOAuthManager}
	}
	return calendar.New(
		cfg.Calendar.CalendarID,
		cfg.Calendar.TimeZone,
		cfg.Calendar.MaxResults,
		oauth,
	)
}

func newContactsService(cfg *Config) *ContactsService {
	dbPath := filepath.Join(filepath.Dir(cfg.HistoryDB), "contacts.db")
	if err := contacts.InitDB(dbPath); err != nil {
		log.Error("contacts service init failed", "error", err)
		return nil
	}
	encFn := func(v string) string { return encryptField(cfg, v) }
	decFn := func(v string) string { return decryptField(cfg, v) }
	log.Info("contacts service initialized", "db", dbPath)
	return contacts.New(dbPath, makeLifeDB(), encFn, decFn)
}

func initContactsDB(dbPath string) error {
	return contacts.InitDB(dbPath)
}

func newFamilyService(cfg *Config, familyCfg FamilyConfig) (*FamilyService, error) {
	dbPath := filepath.Join(filepath.Dir(cfg.HistoryDB), "family.db")
	internalCfg := family.Config{
		MaxUsers:         familyCfg.MaxUsers,
		DefaultBudget:    familyCfg.DefaultBudget,
		DefaultRateLimit: familyCfg.DefaultRateLimit,
	}
	return family.New(dbPath, cfg.HistoryDB, internalCfg, makeLifeDB())
}

func initFamilyDB(dbPath string) error {
	return family.InitDB(dbPath)
}

func newPriceWatchEngine(cfg *Config) *PriceWatchEngine {
	return pricewatch.New(cfg.HistoryDB, tool.CurrencyBaseURL, makeLifeDB())
}

func newReminderEngine(cfg *Config, notifyFn func(string)) *ReminderEngine {
	internalCfg := reminder.Config{
		CheckInterval: cfg.Reminders.CheckIntervalOrDefault(),
		MaxPerUser:    cfg.Reminders.MaxPerUser,
	}
	return reminder.New(cfg.HistoryDB, internalCfg, makeLifeDB(), notifyFn, nextCronTime)
}

func initReminderDB(dbPath string) error {
	return reminder.InitDB(dbPath)
}

func newTimeTrackingService(cfg *Config) *TimeTrackingService {
	return timetracking.New(cfg.HistoryDB, makeLifeDB())
}

func initTimeTrackingDB(dbPath string) error {
	return timetracking.InitDB(dbPath)
}

func newDailyNotesService(cfg *Config) *DailyNotesService {
	notesDir := cfg.DailyNotes.DirOrDefault(cfg.BaseDir)
	return dailynotes.New(cfg.HistoryDB, notesDir, makeLifeDB())
}

// --- oauthAdapter wraps OAuthManager to satisfy calendar.OAuthRequester ---

type oauthAdapter struct {
	mgr *OAuthManager
}

func (a *oauthAdapter) Request(ctx context.Context, provider, method, url string, body io.Reader) (*calendar.OAuthResponse, error) {
	resp, err := a.mgr.Request(ctx, provider, method, url, body)
	if err != nil {
		return nil, err
	}
	return &calendar.OAuthResponse{
		StatusCode: resp.StatusCode,
		Body:       resp.Body,
	}, nil
}

// Ensure oauthAdapter satisfies the interface at compile time.
var _ calendar.OAuthRequester = (*oauthAdapter)(nil)

// --- Forwarding helpers used by tool handlers ---

// parseExpenseNL delegates to internal finance package.
func parseExpenseNL(text, defaultCurrency string) (amount float64, currency string, category string, description string) {
	return finance.ParseExpenseNL(text, defaultCurrency)
}

// periodToDateFilter delegates to internal finance package.
func periodToDateFilter(period string) string {
	return finance.PeriodToDateFilter(period)
}

// parseNaturalSchedule delegates to internal calendar package.
func parseNaturalSchedule(text string) (*CalendarEventInput, error) {
	return calendar.ParseNaturalSchedule(text)
}

// --- Goals helper wrappers ---

func parseMilestonesFromDescription(description string) []Milestone {
	return goals.ParseMilestonesFromDescription(description, newUUID)
}

func defaultMilestones() []Milestone {
	return goals.DefaultMilestones(newUUID)
}

func calculateMilestoneProgress(milestones []Milestone) int {
	return goals.CalculateMilestoneProgress(milestones)
}

// --- Profile ---

func newUserProfileService(cfg *Config) *UserProfileService {
	sentimentFn := func(text string) (float64, []string) {
		r := nlp.Analyze(text)
		return r.Score, r.Keywords
	}
	return profile.New(cfg.HistoryDB, profile.Config{
		Enabled:          cfg.UserProfile.Enabled,
		SentimentEnabled: cfg.UserProfile.SentimentEnabled,
	}, makeLifeDB(), newUUID, sentimentFn, nlp.Label)
}

func initUserProfileDB(dbPath string) error {
	return profile.InitDB(dbPath)
}

// --- Tasks ---

func newTaskManagerService(cfg *Config) *TaskManagerService {
	return tasks.New(cfg.HistoryDB, tasks.Config{
		DefaultProject: cfg.TaskManager.DefaultProjectOrInbox(),
	}, makeLifeDB(), newUUID)
}

func initTaskManagerDB(dbPath string) error {
	return tasks.InitDB(dbPath)
}

func newNotionSync(cfg *Config) *tasks.NotionSync {
	svc := globalTaskManager
	return tasks.NewNotionSync(svc, tasks.NotionConfig{
		APIKey:     cfg.TaskManager.Notion.APIKey,
		DatabaseID: cfg.TaskManager.Notion.DatabaseID,
	})
}

func newTodoistSync(cfg *Config) *tasks.TodoistSync {
	svc := globalTaskManager
	return tasks.NewTodoistSync(svc, tasks.TodoistConfig{
		APIKey: cfg.TaskManager.Todoist.APIKey,
	})
}

// taskFromRow delegates to tasks package.
func taskFromRow(row map[string]any) UserTask {
	return tasks.TaskFromRow(row)
}

// taskFieldToColumn delegates to tasks package.
func taskFieldToColumn(field string) string {
	return tasks.TaskFieldToColumn(field)
}

// findTaskByExternalID delegates to globalTaskManager.
func findTaskByExternalID(dbPath, source, externalID string) (*UserTask, error) {
	if globalTaskManager == nil {
		return nil, fmt.Errorf("task manager not initialized")
	}
	return globalTaskManager.FindByExternalID(source, externalID)
}

// --- P24.3: Life Insights Engine ---

var globalInsightsEngine *insights.Engine

// newInsightsEngine constructs an insights.Engine from Config + globals.
func newInsightsEngine(cfg *Config) *insights.Engine {
	deps := insights.Deps{
		Query:   db.Query,
		Escape:  db.Escape,
		LogWarn: log.Warn,
		UUID:    newUUID,
	}
	if globalFinanceService != nil {
		deps.FinanceDBPath = globalFinanceService.DBPath()
	}
	if globalTaskManager != nil {
		deps.TasksDBPath = globalTaskManager.DBPath()
	}
	if globalUserProfileService != nil {
		deps.ProfileDBPath = globalUserProfileService.DBPath()
	}
	if globalContactsService != nil {
		deps.ContactsDBPath = globalContactsService.DBPath()
	}
	if globalHabitsService != nil {
		deps.HabitsDBPath = globalHabitsService.DBPath()
		deps.GetHabitStreak = globalHabitsService.GetStreak
	}
	return insights.New(cfg.HistoryDB, deps)
}

func initInsightsDB(dbPath string) error {
	return insights.InitDB(dbPath)
}

// --- Tool Handlers ---

// toolLifeReport handles the life_report tool.
func toolLifeReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Insights == nil {
		return "", fmt.Errorf("insights engine not initialized")
	}

	var args struct {
		Period string `json:"period"`
		Date   string `json:"date"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	period := args.Period
	if period == "" {
		period = "weekly"
	}
	if period != "daily" && period != "weekly" && period != "monthly" {
		return "", fmt.Errorf("invalid period %q (use: daily, weekly, monthly)", period)
	}

	targetDate := time.Now().UTC()
	if args.Date != "" {
		parsed, err := time.Parse("2006-01-02", args.Date)
		if err != nil {
			return "", fmt.Errorf("invalid date format (expected YYYY-MM-DD): %w", err)
		}
		targetDate = parsed
	}

	report, err := app.Insights.GenerateReport(period, targetDate)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolLifeInsights handles the life_insights tool.
func toolLifeInsights(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Insights == nil {
		return "", fmt.Errorf("insights engine not initialized")
	}

	var args struct {
		Action    string `json:"action"`
		Days      int    `json:"days"`
		InsightID string `json:"insight_id"`
		Month     string `json:"month"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "detect":
		days := args.Days
		if days <= 0 {
			days = 7
		}
		insights, err := app.Insights.DetectAnomalies(days)
		if err != nil {
			return "", err
		}
		if len(insights) == 0 {
			return `{"message":"No anomalies detected","insights":[]}`, nil
		}
		out, _ := json.MarshalIndent(map[string]any{
			"insights": insights,
			"count":    len(insights),
		}, "", "  ")
		return string(out), nil

	case "list":
		insights, err := app.Insights.GetInsights(20, false)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(insights, "", "  ")
		return string(out), nil

	case "acknowledge":
		if args.InsightID == "" {
			return "", fmt.Errorf("insight_id is required for acknowledge action")
		}
		if err := app.Insights.AcknowledgeInsight(args.InsightID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Insight %s acknowledged.", args.InsightID), nil

	case "forecast":
		result, err := app.Insights.SpendingForecast(args.Month)
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(result, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: detect, list, acknowledge, forecast)", args.Action)
	}
}

// --- Helpers ---

// insightsDBPath returns the database path for insights.
func insightsDBPath(cfg *Config) string {
	if cfg.HistoryDB != "" {
		return cfg.HistoryDB
	}
	return filepath.Join(cfg.BaseDir, "history.db")
}

// ============================================================
// Merged shims: review, push, roles, projects, workspace, notify, reflection
// ============================================================

// --- Review (from review.go) ---

func buildReviewDigest(cfg *Config, days int) string {
	return review.BuildDigest(cfg, days)
}

// --- Push (from push.go) ---

type PushSubscription = push.Subscription
type PushKeys = push.SubscriptionKeys
type PushNotification = push.Notification
type PushManager = push.Manager

func newPushManager(cfg *Config) *PushManager {
	return push.NewManager(push.Config{
		HistoryDB:       cfg.HistoryDB,
		VAPIDPrivateKey: cfg.Push.VAPIDPrivateKey,
		VAPIDEmail:      cfg.Push.VAPIDEmail,
		TTL:             cfg.Push.TTL,
	})
}

// --- Roles (from roles.go) ---

type AgentArchetype = roles.AgentArchetype

var builtinArchetypes = roles.BuiltinArchetypes

func loadAgentPrompt(cfg *Config, agentName string) (string, error) {
	return roles.LoadAgentPrompt(cfg, agentName)
}

func generateSoulContent(archetype *AgentArchetype, agentName string) string {
	return roles.GenerateSoulContent(archetype, agentName)
}

func getArchetypeByName(name string) *AgentArchetype {
	return roles.GetArchetypeByName(name)
}

func writeSoulFile(cfg *Config, agentName, content string) error {
	return roles.WriteSoulFile(cfg, agentName, content)
}

// --- Projects (from projects.go) ---

type Project = project.Project
type WorkspaceProjectEntry = project.WorkspaceProjectEntry

func initProjectsDB(dbPath string) error   { return project.InitDB(dbPath) }
func listProjects(dbPath, status string) ([]Project, error) { return project.List(dbPath, status) }
func getProject(dbPath, id string) (*Project, error) { return project.Get(dbPath, id) }
func createProject(dbPath string, p Project) error   { return project.Create(dbPath, p) }
func updateProject(dbPath string, p Project) error    { return project.Update(dbPath, p) }
func deleteProject(dbPath, id string) error           { return project.Delete(dbPath, id) }
func parseProjectsMD(path string) ([]WorkspaceProjectEntry, error) { return project.ParseProjectsMD(path) }
func generateProjectID() string { return project.GenerateID() }

// --- Workspace (from workspace.go) ---

type SessionScope = workspace.SessionScope

func resolveWorkspace(cfg *Config, agentName string) WorkspaceConfig { return workspace.ResolveWorkspace(cfg, agentName) }
func defaultWorkspace(cfg *Config) WorkspaceConfig                   { return workspace.DefaultWorkspace(cfg) }
func initDirectories(cfg *Config) error                              { return workspace.InitDirectories(cfg) }
func resolveSessionScope(cfg *Config, agentName string, sessionType string) SessionScope {
	return workspace.ResolveSessionScope(cfg, agentName, sessionType)
}
func defaultToolProfile(cfg *Config) string                  { return workspace.DefaultToolProfile(cfg) }
func minTrust(a, b string) string                            { return workspace.MinTrust(a, b) }
func resolveMCPServers(cfg *Config, agentName string) []string { return workspace.ResolveMCPServers(cfg, agentName) }
func loadSoulFile(cfg *Config, agentName string) string      { return workspace.LoadSoulFile(cfg, agentName) }
func getWorkspaceMemoryPath(cfg *Config) string              { return workspace.GetWorkspaceMemoryPath(cfg) }
func getWorkspaceSkillsPath(cfg *Config) string              { return workspace.GetWorkspaceSkillsPath(cfg) }

// --- Notify (from notify.go) ---

type Notifier = notify.Notifier
type SlackNotifier = notify.SlackNotifier
type DiscordNotifier = notify.DiscordNotifier
type MultiNotifier = notify.MultiNotifier
type WhatsAppNotifier = notify.WhatsAppNotifier
type NotifyMessage = notify.Message
type NotificationEngine = notify.Engine

const (
	PriorityCritical = notify.PriorityCritical
	PriorityHigh     = notify.PriorityHigh
	PriorityNormal   = notify.PriorityNormal
	PriorityLow      = notify.PriorityLow
)

func buildNotifiers(cfg *Config) []Notifier              { return notify.BuildNotifiers(cfg) }
func buildDiscordNotifierByName(cfg *Config, name string) *DiscordNotifier {
	return notify.BuildDiscordNotifierByName(cfg, name)
}
func NewNotificationEngine(cfg *Config, notifiers []Notifier, fallbackFn func(string)) *NotificationEngine {
	return notify.NewEngine(cfg, notifiers, fallbackFn)
}
func wrapNotifyFn(ne *NotificationEngine, defaultPriority string) func(string) {
	return notify.WrapNotifyFn(ne, defaultPriority)
}
func priorityRank(p string) int            { return notify.PriorityRank(p) }
func priorityFromRank(rank int) string     { return notify.PriorityFromRank(rank) }
func isValidPriority(p string) bool        { return notify.IsValidPriority(p) }
func newDiscordNotifier(webhookURL string, timeout time.Duration) *DiscordNotifier {
	return notify.NewDiscordNotifier(webhookURL, timeout)
}

// --- Reflection (from reflection.go) ---

type ReflectionResult = reflection.Result

func initReflectionDB(dbPath string) error { return reflection.InitDB(dbPath) }
func shouldReflect(cfg *Config, task Task, result TaskResult) bool {
	return reflection.ShouldReflect(cfg, task, result)
}
func performReflection(ctx context.Context, cfg *Config, task Task, result TaskResult, sem ...chan struct{}) (*ReflectionResult, error) {
	var taskSem chan struct{}
	if len(sem) > 0 && sem[0] != nil {
		taskSem = sem[0]
	} else {
		taskSem = make(chan struct{}, 1)
	}
	deps := reflection.Deps{
		Executor: idispatch.TaskExecutorFunc(func(ctx context.Context, t idispatch.Task, agentName string) idispatch.TaskResult {
			return runSingleTask(ctx, cfg, t, taskSem, nil, agentName)
		}),
		NewID:        newUUID,
		FillDefaults: fillDefaults,
	}
	return reflection.Perform(ctx, cfg, task, result, deps)
}
func parseReflectionOutput(output string) (*ReflectionResult, error) { return reflection.ParseOutput(output) }
func extractJSON(s string) string                                    { return reflection.ExtractJSON(s) }
func storeReflection(dbPath string, ref *ReflectionResult) error     { return reflection.Store(dbPath, ref) }
func queryReflections(dbPath, agent string, limit int) ([]ReflectionResult, error) {
	return reflection.Query(dbPath, agent, limit)
}
func buildReflectionContext(dbPath, role string, limit int) string {
	return reflection.BuildContext(dbPath, role, limit)
}
func reflectionBudgetOrDefault(cfg *Config) float64 { return reflection.BudgetOrDefault(cfg) }
