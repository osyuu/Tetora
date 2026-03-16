package scheduling

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// CalendarProvider fetches calendar events for a time range.
type CalendarProvider interface {
	ListEvents(ctx context.Context, timeMin, timeMax string, maxResults int) ([]CalendarEvent, error)
}

// CalendarEvent is a simplified calendar event used by the scheduling service.
type CalendarEvent struct {
	Summary string
	Start   string // RFC3339
	End     string // RFC3339
	AllDay  bool
}

// TaskProvider fetches tasks.
type TaskProvider interface {
	ListTasks(userID string, filter TaskFilter) ([]Task, error)
}

// TaskFilter describes task list filtering criteria.
type TaskFilter struct {
	DueDate string
	Status  string
	Limit   int
}

// Task is a simplified task used by the scheduling service.
type Task struct {
	Title    string
	Priority int
	DueAt    string
	Project  string
}

// LogFunc is a logging function signature.
type LogFunc func(msg string, keyvals ...any)

// TimeSlot represents a block of time with a type classification.
type TimeSlot struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Duration int       `json:"duration_minutes"`
	Type     string    `json:"type"` // free, busy, focus, break
}

// DaySchedule is a single day's schedule with events, free slots, and statistics.
type DaySchedule struct {
	Date         string          `json:"date"` // YYYY-MM-DD
	Events       []ScheduleEvent `json:"events"`
	FreeSlots    []TimeSlot      `json:"free_slots"`
	BusyHours    float64         `json:"busy_hours"`
	FreeHours    float64         `json:"free_hours"`
	MeetingCount int             `json:"meeting_count"`
}

// ScheduleEvent is a unified event from either the calendar or task deadlines.
type ScheduleEvent struct {
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Source   string    `json:"source"` // calendar, task_deadline
	Priority string    `json:"priority,omitempty"`
}

// ScheduleSuggestion is a recommended time slot with a preference score.
type ScheduleSuggestion struct {
	Slot   TimeSlot `json:"slot"`
	Reason string   `json:"reason"`
	Score  float64  `json:"score"` // 0-1 preference score
}

const (
	defaultWorkStart = 9  // 09:00
	defaultWorkEnd   = 18 // 18:00
)

// Service provides intelligent schedule analysis, free-slot detection, and weekly planning.
type Service struct {
	calendar CalendarProvider
	tasks    TaskProvider
	logWarn  LogFunc
}

// New creates a new scheduling Service.
func New(calendar CalendarProvider, tasks TaskProvider, logWarn LogFunc) *Service {
	if logWarn == nil {
		logWarn = func(string, ...any) {}
	}
	return &Service{
		calendar: calendar,
		tasks:    tasks,
		logWarn:  logWarn,
	}
}

// workingHours returns the start and end hour for the working day.
func (s *Service) workingHours() (int, int) {
	return defaultWorkStart, defaultWorkEnd
}

// ViewSchedule returns schedules for N days starting from the given date string (YYYY-MM-DD).
func (s *Service) ViewSchedule(date string, days int) ([]DaySchedule, error) {
	loc := time.Now().Location()
	if days <= 0 {
		days = 1
	}

	startDate, err := s.parseDate(date, loc)
	if err != nil {
		return nil, fmt.Errorf("parse date: %w", err)
	}

	rangeStart := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, loc)
	rangeEnd := rangeStart.AddDate(0, 0, days)

	calEvents := s.fetchCalendarEvents(rangeStart, rangeEnd)
	taskEvents := s.fetchTaskDeadlines(rangeStart, rangeEnd)

	schedules := make([]DaySchedule, 0, days)
	for i := 0; i < days; i++ {
		dayStart := rangeStart.AddDate(0, 0, i)
		dayEnd := dayStart.AddDate(0, 0, 1)
		dateStr := dayStart.Format("2006-01-02")

		var dayEvents []ScheduleEvent
		for _, ev := range calEvents {
			if ev.End.After(dayStart) && ev.Start.Before(dayEnd) {
				dayEvents = append(dayEvents, ev)
			}
		}
		for _, ev := range taskEvents {
			if ev.End.After(dayStart) && ev.Start.Before(dayEnd) {
				dayEvents = append(dayEvents, ev)
			}
		}

		sort.Slice(dayEvents, func(i, j int) bool {
			return dayEvents[i].Start.Before(dayEvents[j].Start)
		})

		workStart, workEnd := s.workingHours()
		whStart := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), workStart, 0, 0, 0, loc)
		whEnd := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), workEnd, 0, 0, 0, loc)

		freeSlots := s.FindFreeSlotsInDay(dayEvents, whStart, whEnd)

		busyMinutes := 0
		meetingCount := 0
		for _, ev := range dayEvents {
			evStart := ev.Start
			evEnd := ev.End
			if evStart.Before(dayStart) {
				evStart = dayStart
			}
			if evEnd.After(dayEnd) {
				evEnd = dayEnd
			}
			busyMinutes += int(evEnd.Sub(evStart).Minutes())
			if ev.Source == "calendar" {
				meetingCount++
			}
		}

		totalWorkMinutes := (workEnd - workStart) * 60
		freeMinutes := totalWorkMinutes - busyMinutes
		if freeMinutes < 0 {
			freeMinutes = 0
		}

		if dayEvents == nil {
			dayEvents = []ScheduleEvent{}
		}
		if freeSlots == nil {
			freeSlots = []TimeSlot{}
		}

		schedules = append(schedules, DaySchedule{
			Date:         dateStr,
			Events:       dayEvents,
			FreeSlots:    freeSlots,
			BusyHours:    math.Round(float64(busyMinutes)/60.0*100) / 100,
			FreeHours:    math.Round(float64(freeMinutes)/60.0*100) / 100,
			MeetingCount: meetingCount,
		})
	}

	return schedules, nil
}

// SuggestSlots finds optimal time slots of the given duration (minutes).
func (s *Service) SuggestSlots(duration int, preferMorning bool, days int) ([]ScheduleSuggestion, error) {
	if duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	if days <= 0 {
		days = 5
	}

	schedules, err := s.ViewSchedule("", days)
	if err != nil {
		return nil, fmt.Errorf("view schedule: %w", err)
	}

	var suggestions []ScheduleSuggestion

	for _, day := range schedules {
		for _, slot := range day.FreeSlots {
			if slot.Duration < duration {
				continue
			}

			windowStart := slot.Start
			for windowStart.Add(time.Duration(duration) * time.Minute).Before(slot.End) ||
				windowStart.Add(time.Duration(duration)*time.Minute).Equal(slot.End) {

				windowEnd := windowStart.Add(time.Duration(duration) * time.Minute)
				score := s.scoreSlot(windowStart, windowEnd, day, preferMorning)
				reason := s.slotReason(windowStart, windowEnd, day, preferMorning)

				suggestions = append(suggestions, ScheduleSuggestion{
					Slot: TimeSlot{
						Start:    windowStart,
						End:      windowEnd,
						Duration: duration,
						Type:     "free",
					},
					Score:  score,
					Reason: reason,
				})

				windowStart = windowStart.Add(30 * time.Minute)
			}
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})

	if len(suggestions) > 5 {
		suggestions = suggestions[:5]
	}

	return suggestions, nil
}

// PlanWeek generates a weekly plan for the user.
func (s *Service) PlanWeek(userID string) (map[string]any, error) {
	schedules, err := s.ViewSchedule("", 7)
	if err != nil {
		return nil, fmt.Errorf("view schedule: %w", err)
	}

	warnings := s.DetectOvercommitment(schedules)

	var focusBlocks []map[string]any
	for _, day := range schedules {
		for _, slot := range day.FreeSlots {
			if slot.Duration >= 90 {
				focusBlocks = append(focusBlocks, map[string]any{
					"date":     day.Date,
					"start":    slot.Start.Format("15:04"),
					"end":      slot.End.Format("15:04"),
					"duration": slot.Duration,
				})
			}
		}
	}
	if focusBlocks == nil {
		focusBlocks = []map[string]any{}
	}

	var urgentTasks []map[string]any
	if s.tasks != nil {
		nextWeek := time.Now().AddDate(0, 0, 7).Format(time.RFC3339)
		uid := userID
		if uid == "" {
			uid = "default"
		}
		tasks, taskErr := s.tasks.ListTasks(uid, TaskFilter{
			DueDate: nextWeek,
			Status:  "todo",
			Limit:   20,
		})
		if taskErr == nil {
			for _, t := range tasks {
				urgentTasks = append(urgentTasks, map[string]any{
					"title":    t.Title,
					"priority": t.Priority,
					"dueAt":    t.DueAt,
					"project":  t.Project,
				})
			}
		}
	}
	if urgentTasks == nil {
		urgentTasks = []map[string]any{}
	}

	dailySummaries := make([]map[string]any, 0, len(schedules))
	totalMeetings := 0
	totalBusyHours := 0.0
	totalFreeHours := 0.0
	for _, day := range schedules {
		totalMeetings += day.MeetingCount
		totalBusyHours += day.BusyHours
		totalFreeHours += day.FreeHours
		dailySummaries = append(dailySummaries, map[string]any{
			"date":        day.Date,
			"meetings":    day.MeetingCount,
			"busy_hours":  day.BusyHours,
			"free_hours":  day.FreeHours,
			"event_count": len(day.Events),
			"free_slots":  len(day.FreeSlots),
		})
	}

	plan := map[string]any{
		"period":           fmt.Sprintf("%s to %s", schedules[0].Date, schedules[len(schedules)-1].Date),
		"total_meetings":   totalMeetings,
		"total_busy_hours": math.Round(totalBusyHours*100) / 100,
		"total_free_hours": math.Round(totalFreeHours*100) / 100,
		"daily_summaries":  dailySummaries,
		"focus_blocks":     focusBlocks,
		"urgent_tasks":     urgentTasks,
		"warnings":         warnings,
	}

	return plan, nil
}

// FindFreeSlots finds all free slots of at least minMinutes within the given time range.
func (s *Service) FindFreeSlots(start, end time.Time, minMinutes int) ([]TimeSlot, error) {
	if end.Before(start) || end.Equal(start) {
		return nil, fmt.Errorf("end must be after start")
	}
	if minMinutes <= 0 {
		minMinutes = 15
	}

	calEvents := s.fetchCalendarEvents(start, end)
	taskEvents := s.fetchTaskDeadlines(start, end)

	allEvents := append(calEvents, taskEvents...)
	sort.Slice(allEvents, func(i, j int) bool {
		return allEvents[i].Start.Before(allEvents[j].Start)
	})

	merged := MergeEvents(allEvents)

	var slots []TimeSlot
	cursor := start
	for _, ev := range merged {
		if ev.Start.After(cursor) {
			gap := int(ev.Start.Sub(cursor).Minutes())
			if gap >= minMinutes {
				slots = append(slots, TimeSlot{
					Start:    cursor,
					End:      ev.Start,
					Duration: gap,
					Type:     "free",
				})
			}
		}
		if ev.End.After(cursor) {
			cursor = ev.End
		}
	}

	if cursor.Before(end) {
		gap := int(end.Sub(cursor).Minutes())
		if gap >= minMinutes {
			slots = append(slots, TimeSlot{
				Start:    cursor,
				End:      end,
				Duration: gap,
				Type:     "free",
			})
		}
	}

	if slots == nil {
		slots = []TimeSlot{}
	}
	return slots, nil
}

// DetectOvercommitment returns warning strings for overloaded days.
func (s *Service) DetectOvercommitment(schedules []DaySchedule) []string {
	var warnings []string
	for _, day := range schedules {
		if day.BusyHours > 6 {
			warnings = append(warnings, fmt.Sprintf("%s: %.1f busy hours (overcommitted, consider rescheduling)", day.Date, day.BusyHours))
		}
		if day.MeetingCount > 5 {
			warnings = append(warnings, fmt.Sprintf("%s: %d meetings (high context-switching cost)", day.Date, day.MeetingCount))
		}
		if day.FreeHours < 1 {
			warnings = append(warnings, fmt.Sprintf("%s: only %.1f free hours (no focus time available)", day.Date, day.FreeHours))
		}
	}
	if warnings == nil {
		warnings = []string{}
	}
	return warnings
}

// --- Internal Helpers ---

func (s *Service) parseDate(date string, loc *time.Location) (time.Time, error) {
	if date == "" {
		now := time.Now().In(loc)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc), nil
	}
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", date, err)
	}
	return t, nil
}

func (s *Service) fetchCalendarEvents(start, end time.Time) []ScheduleEvent {
	if s.calendar == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, err := s.calendar.ListEvents(ctx, start.Format(time.RFC3339), end.Format(time.RFC3339), 100)
	if err != nil {
		s.logWarn("scheduling: fetch calendar events", "error", err.Error())
		return nil
	}

	var result []ScheduleEvent
	for _, ev := range events {
		if ev.AllDay {
			continue
		}
		evStart, err1 := time.Parse(time.RFC3339, ev.Start)
		evEnd, err2 := time.Parse(time.RFC3339, ev.End)
		if err1 != nil || err2 != nil {
			continue
		}
		result = append(result, ScheduleEvent{
			Title:  ev.Summary,
			Start:  evStart,
			End:    evEnd,
			Source: "calendar",
		})
	}
	return result
}

func (s *Service) fetchTaskDeadlines(start, end time.Time) []ScheduleEvent {
	if s.tasks == nil {
		return nil
	}

	tasks, err := s.tasks.ListTasks("default", TaskFilter{
		DueDate: end.Format(time.RFC3339),
		Status:  "todo",
		Limit:   50,
	})
	if err != nil {
		s.logWarn("scheduling: fetch task deadlines", "error", err.Error())
		return nil
	}

	var result []ScheduleEvent
	for _, t := range tasks {
		if t.DueAt == "" {
			continue
		}
		dueTime, err := time.Parse(time.RFC3339, t.DueAt)
		if err != nil {
			dueTime, err = time.Parse("2006-01-02", t.DueAt)
			if err != nil {
				continue
			}
			_, workEnd := s.workingHours()
			dueTime = time.Date(dueTime.Year(), dueTime.Month(), dueTime.Day(), workEnd, 0, 0, 0, time.Now().Location())
		}
		if dueTime.Before(start) {
			continue
		}
		deadlineStart := dueTime.Add(-30 * time.Minute)
		if deadlineStart.Before(start) {
			deadlineStart = start
		}

		priority := "normal"
		switch t.Priority {
		case 1:
			priority = "urgent"
		case 2:
			priority = "high"
		case 3:
			priority = "normal"
		case 4:
			priority = "low"
		}

		result = append(result, ScheduleEvent{
			Title:    fmt.Sprintf("[Deadline] %s", t.Title),
			Start:    deadlineStart,
			End:      dueTime,
			Source:   "task_deadline",
			Priority: priority,
		})
	}
	return result
}

// FindFreeSlotsInDay computes free slots within working hours for a day.
func (s *Service) FindFreeSlotsInDay(events []ScheduleEvent, whStart, whEnd time.Time) []TimeSlot {
	var relevant []ScheduleEvent
	for _, ev := range events {
		if ev.End.After(whStart) && ev.Start.Before(whEnd) {
			relevant = append(relevant, ev)
		}
	}

	sort.Slice(relevant, func(i, j int) bool {
		return relevant[i].Start.Before(relevant[j].Start)
	})

	merged := MergeEvents(relevant)

	var slots []TimeSlot
	cursor := whStart
	for _, ev := range merged {
		evStart := ev.Start
		evEnd := ev.End
		if evStart.Before(whStart) {
			evStart = whStart
		}
		if evEnd.After(whEnd) {
			evEnd = whEnd
		}

		if evStart.After(cursor) {
			gap := int(evStart.Sub(cursor).Minutes())
			if gap >= 15 {
				slots = append(slots, TimeSlot{
					Start:    cursor,
					End:      evStart,
					Duration: gap,
					Type:     "free",
				})
			}
		}
		if evEnd.After(cursor) {
			cursor = evEnd
		}
	}

	if cursor.Before(whEnd) {
		gap := int(whEnd.Sub(cursor).Minutes())
		if gap >= 15 {
			slots = append(slots, TimeSlot{
				Start:    cursor,
				End:      whEnd,
				Duration: gap,
				Type:     "free",
			})
		}
	}

	return slots
}

// MergeEvents merges overlapping schedule events into non-overlapping blocks.
// Input must be sorted by start time.
func MergeEvents(events []ScheduleEvent) []ScheduleEvent {
	if len(events) == 0 {
		return nil
	}

	merged := []ScheduleEvent{events[0]}
	for _, ev := range events[1:] {
		last := &merged[len(merged)-1]
		if ev.Start.Before(last.End) || ev.Start.Equal(last.End) {
			if ev.End.After(last.End) {
				last.End = ev.End
			}
		} else {
			merged = append(merged, ev)
		}
	}
	return merged
}

func (s *Service) scoreSlot(start, end time.Time, day DaySchedule, preferMorning bool) float64 {
	score := 0.5

	hour := start.Hour()

	if preferMorning {
		if hour >= 9 && hour < 12 {
			score += 0.3
		} else if hour >= 12 && hour < 14 {
			score += 0.1
		}
	} else {
		if hour >= 14 && hour < 17 {
			score += 0.3
		} else if hour >= 10 && hour < 14 {
			score += 0.1
		}
	}

	for _, ev := range day.Events {
		evDuration := ev.End.Sub(ev.Start).Minutes()
		gap := start.Sub(ev.End).Minutes()
		if gap >= 0 && gap < 15 && evDuration > 60 {
			score -= 0.2
		}
	}

	if day.FreeHours > 4 {
		score += 0.1
	}

	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return math.Round(score*100) / 100
}

func (s *Service) slotReason(start, end time.Time, day DaySchedule, preferMorning bool) string {
	var parts []string

	hour := start.Hour()
	if preferMorning && hour >= 9 && hour < 12 {
		parts = append(parts, "morning slot as preferred")
	} else if !preferMorning && hour >= 14 && hour < 17 {
		parts = append(parts, "afternoon slot as preferred")
	}

	if day.FreeHours > 4 {
		parts = append(parts, fmt.Sprintf("%.1f free hours on %s", day.FreeHours, day.Date))
	}

	if day.MeetingCount == 0 {
		parts = append(parts, "no other meetings")
	} else if day.MeetingCount <= 2 {
		parts = append(parts, "light meeting day")
	}

	hasBuffer := true
	for _, ev := range day.Events {
		gap := start.Sub(ev.End).Minutes()
		if gap >= 0 && gap < 15 {
			hasBuffer = false
			break
		}
	}
	if hasBuffer {
		parts = append(parts, "good buffer from other events")
	}

	if len(parts) == 0 {
		return "available time slot"
	}
	return strings.Join(parts, "; ")
}
