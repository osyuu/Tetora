package main

// scheduling.go wires internal/scheduling into the root package.
// It provides adapters that bridge root globals to the scheduling.Service
// interfaces, and the tool handler functions registered in wire_tools.go.

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/log"
	"tetora/internal/scheduling"
)

// --- Type aliases ---

type TimeSlot = scheduling.TimeSlot
type DaySchedule = scheduling.DaySchedule
type ScheduleEvent = scheduling.ScheduleEvent
type ScheduleSuggestion = scheduling.ScheduleSuggestion

// --- Global ---

var globalSchedulingService *scheduling.Service

// newSchedulingService constructs a scheduling.Service wired to root globals.
func newSchedulingService(cfg *Config) *scheduling.Service {
	return scheduling.New(
		&schedulingCalendarAdapter{},
		&schedulingTaskAdapter{},
		log.Warn,
	)
}

// --- Adapter types ---

// schedulingCalendarAdapter implements scheduling.CalendarProvider using globalCalendarService.
type schedulingCalendarAdapter struct{}

func (a *schedulingCalendarAdapter) ListEvents(ctx context.Context, timeMin, timeMax string, maxResults int) ([]scheduling.CalendarEvent, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Calendar == nil {
		return nil, nil
	}
	events, err := app.Calendar.ListEvents(ctx, timeMin, timeMax, maxResults)
	if err != nil {
		return nil, err
	}
	var result []scheduling.CalendarEvent
	for _, ev := range events {
		result = append(result, scheduling.CalendarEvent{
			Summary: ev.Summary,
			Start:   ev.Start,
			End:     ev.End,
			AllDay:  ev.AllDay,
		})
	}
	return result, nil
}

// schedulingTaskAdapter implements scheduling.TaskProvider using globalTaskManager.
type schedulingTaskAdapter struct{}

func (a *schedulingTaskAdapter) ListTasks(userID string, filter scheduling.TaskFilter) ([]scheduling.Task, error) {
	if globalTaskManager == nil {
		return nil, nil
	}
	tasks, err := globalTaskManager.ListTasks(userID, TaskFilter{
		DueDate: filter.DueDate,
		Status:  filter.Status,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	var result []scheduling.Task
	for _, t := range tasks {
		result = append(result, scheduling.Task{
			Title:    t.Title,
			Priority: t.Priority,
			DueAt:    t.DueAt,
			Project:  t.Project,
		})
	}
	return result, nil
}

// --- Tool Handlers ---

// toolScheduleView handles the schedule_view tool.
func toolScheduleView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Scheduling == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		Date string `json:"date"`
		Days int    `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Days <= 0 {
		args.Days = 1
	}
	if args.Days > 30 {
		args.Days = 30
	}

	schedules, err := app.Scheduling.ViewSchedule(args.Date, args.Days)
	if err != nil {
		return "", err
	}

	out, err := json.MarshalIndent(schedules, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}

// toolScheduleSuggest handles the schedule_suggest tool.
func toolScheduleSuggest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Scheduling == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		DurationMinutes int  `json:"duration_minutes"`
		PreferMorning   bool `json:"prefer_morning"`
		Days            int  `json:"days"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.DurationMinutes <= 0 {
		args.DurationMinutes = 60
	}
	if args.Days <= 0 {
		args.Days = 5
	}
	if args.Days > 14 {
		args.Days = 14
	}

	suggestions, err := app.Scheduling.SuggestSlots(args.DurationMinutes, args.PreferMorning, args.Days)
	if err != nil {
		return "", err
	}

	if len(suggestions) == 0 {
		return "No available time slots found for the requested duration.", nil
	}

	out, err := json.MarshalIndent(suggestions, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return fmt.Sprintf("Found %d suggested slots:\n%s", len(suggestions), string(out)), nil
}

// toolSchedulePlan handles the schedule_plan tool.
func toolSchedulePlan(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	app := appFromCtx(ctx)
	if app == nil || app.Scheduling == nil {
		return "", fmt.Errorf("scheduling service not initialized")
	}

	var args struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		args.UserID = "default"
	}

	plan, err := app.Scheduling.PlanWeek(args.UserID)
	if err != nil {
		return "", err
	}

	out, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}
