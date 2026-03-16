package scheduling

import (
	"testing"
	"time"
)

func newTestService() *Service {
	return New(nil, nil, nil)
}

func TestNew(t *testing.T) {
	svc := newTestService()
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestViewSchedule_NoServices(t *testing.T) {
	svc := newTestService()

	schedules, err := svc.ViewSchedule("", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 day, got %d", len(schedules))
	}

	day := schedules[0]
	today := time.Now().Format("2006-01-02")
	if day.Date != today {
		t.Errorf("expected date %s, got %s", today, day.Date)
	}
	if len(day.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(day.Events))
	}
	if day.BusyHours != 0 {
		t.Errorf("expected 0 busy hours, got %f", day.BusyHours)
	}
	if len(day.FreeSlots) != 1 {
		t.Errorf("expected 1 free slot (full working day), got %d", len(day.FreeSlots))
	}
	if day.FreeHours != 9 {
		t.Errorf("expected 9 free hours, got %f", day.FreeHours)
	}
}

func TestViewSchedule_MultipleDays(t *testing.T) {
	svc := newTestService()

	schedules, err := svc.ViewSchedule("2026-03-01", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(schedules) != 3 {
		t.Fatalf("expected 3 days, got %d", len(schedules))
	}
	expected := []string{"2026-03-01", "2026-03-02", "2026-03-03"}
	for i, day := range schedules {
		if day.Date != expected[i] {
			t.Errorf("day %d: expected %s, got %s", i, expected[i], day.Date)
		}
	}
}

func TestViewSchedule_InvalidDate(t *testing.T) {
	svc := newTestService()

	_, err := svc.ViewSchedule("not-a-date", 1)
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestViewSchedule_WithEvents(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "Standup",
			Start:  time.Date(2026, 3, 15, 10, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 10, 30, 0, 0, loc),
			Source: "calendar",
		},
		{
			Title:  "Design Review",
			Start:  time.Date(2026, 3, 15, 14, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 15, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.FindFreeSlotsInDay(events, whStart, whEnd)

	if len(freeSlots) != 3 {
		t.Fatalf("expected 3 free slots, got %d", len(freeSlots))
	}
	if freeSlots[0].Duration != 60 {
		t.Errorf("slot 0: expected 60 min, got %d", freeSlots[0].Duration)
	}
	if freeSlots[1].Duration != 210 {
		t.Errorf("slot 1: expected 210 min, got %d", freeSlots[1].Duration)
	}
	if freeSlots[2].Duration != 180 {
		t.Errorf("slot 2: expected 180 min, got %d", freeSlots[2].Duration)
	}
}

func TestFindFreeSlots_FullDay(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	slots, err := svc.FindFreeSlots(start, end, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("expected 1 free slot, got %d", len(slots))
	}
	if slots[0].Duration != 540 {
		t.Errorf("expected 540 min, got %d", slots[0].Duration)
	}
}

func TestFindFreeSlots_NoSpace(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 9, 10, 0, 0, loc)

	slots, err := svc.FindFreeSlots(start, end, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slots) != 0 {
		t.Errorf("expected 0 slots, got %d", len(slots))
	}
}

func TestFindFreeSlots_InvalidRange(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	start := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)
	end := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	_, err := svc.FindFreeSlots(start, end, 30)
	if err == nil {
		t.Fatal("expected error for invalid range")
	}
}

func TestSuggestSlots_Basic(t *testing.T) {
	svc := newTestService()

	suggestions, err := svc.SuggestSlots(60, false, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 suggestion")
	}
	if len(suggestions) > 5 {
		t.Errorf("expected at most 5 suggestions, got %d", len(suggestions))
	}

	for i, s := range suggestions {
		if s.Slot.Duration != 60 {
			t.Errorf("suggestion %d: expected 60 min, got %d", i, s.Slot.Duration)
		}
		if s.Score < 0 || s.Score > 1 {
			t.Errorf("suggestion %d: score %f out of [0,1] range", i, s.Score)
		}
		if s.Reason == "" {
			t.Errorf("suggestion %d: empty reason", i)
		}
	}

	for i := 1; i < len(suggestions); i++ {
		if suggestions[i].Score > suggestions[i-1].Score {
			t.Errorf("suggestions not sorted: [%d].Score=%f > [%d].Score=%f", i, suggestions[i].Score, i-1, suggestions[i-1].Score)
		}
	}
}

func TestSuggestSlots_PreferMorning(t *testing.T) {
	svc := newTestService()

	suggestions, err := svc.SuggestSlots(60, true, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("expected at least 1 suggestion")
	}

	topHour := suggestions[0].Slot.Start.Hour()
	if topHour >= 12 {
		t.Errorf("expected morning slot as top suggestion, got hour %d", topHour)
	}
}

func TestSuggestSlots_InvalidDuration(t *testing.T) {
	svc := newTestService()

	_, err := svc.SuggestSlots(0, false, 1)
	if err == nil {
		t.Fatal("expected error for zero duration")
	}
}

func TestPlanWeek_Basic(t *testing.T) {
	svc := newTestService()

	plan, err := svc.PlanWeek("default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan == nil {
		t.Fatal("expected non-nil plan")
	}

	requiredKeys := []string{"period", "total_meetings", "total_busy_hours", "total_free_hours", "daily_summaries", "focus_blocks", "urgent_tasks", "warnings"}
	for _, key := range requiredKeys {
		if _, ok := plan[key]; !ok {
			t.Errorf("missing key in plan: %s", key)
		}
	}

	summaries, ok := plan["daily_summaries"].([]map[string]any)
	if !ok {
		t.Fatalf("daily_summaries wrong type: %T", plan["daily_summaries"])
	}
	if len(summaries) != 7 {
		t.Errorf("expected 7 daily summaries, got %d", len(summaries))
	}

	totalMeetings, ok := plan["total_meetings"].(int)
	if !ok {
		t.Fatalf("total_meetings wrong type: %T", plan["total_meetings"])
	}
	if totalMeetings != 0 {
		t.Errorf("expected 0 total meetings, got %d", totalMeetings)
	}

	totalFree, ok := plan["total_free_hours"].(float64)
	if !ok {
		t.Fatalf("total_free_hours wrong type: %T", plan["total_free_hours"])
	}
	if totalFree != 63 {
		t.Errorf("expected 63 total free hours, got %f", totalFree)
	}
}

func TestDetectOvercommitment(t *testing.T) {
	svc := newTestService()

	schedules := []DaySchedule{
		{Date: "2026-03-15", BusyHours: 7.5, MeetingCount: 6, FreeHours: 0.5},
	}

	warnings := svc.DetectOvercommitment(schedules)
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d", len(warnings))
	}
}

func TestDetectOvercommitment_NormalDay(t *testing.T) {
	svc := newTestService()

	schedules := []DaySchedule{
		{Date: "2026-03-15", BusyHours: 3, MeetingCount: 2, FreeHours: 6},
	}

	warnings := svc.DetectOvercommitment(schedules)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for normal day, got %d: %v", len(warnings), warnings)
	}
}

func TestMergeEvents(t *testing.T) {
	loc := time.Now().Location()
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{Title: "A", Start: base, End: base.Add(60 * time.Minute)},
		{Title: "B", Start: base.Add(30 * time.Minute), End: base.Add(90 * time.Minute)},
		{Title: "C", Start: base.Add(120 * time.Minute), End: base.Add(150 * time.Minute)},
	}

	merged := MergeEvents(events)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged events, got %d", len(merged))
	}
	if merged[0].End != base.Add(90*time.Minute) {
		t.Errorf("expected first merged end at 10:30, got %s", merged[0].End.Format("15:04"))
	}
	if merged[1].Start != base.Add(120*time.Minute) {
		t.Errorf("expected second event at 11:00, got %s", merged[1].Start.Format("15:04"))
	}
}

func TestMergeEvents_Empty(t *testing.T) {
	merged := MergeEvents(nil)
	if merged != nil {
		t.Errorf("expected nil for empty input, got %v", merged)
	}
}

func TestMergeEvents_Adjacent(t *testing.T) {
	loc := time.Now().Location()
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{Title: "A", Start: base, End: base.Add(60 * time.Minute)},
		{Title: "B", Start: base.Add(60 * time.Minute), End: base.Add(120 * time.Minute)},
	}

	merged := MergeEvents(events)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged event for adjacent, got %d", len(merged))
	}
	if merged[0].End != base.Add(120*time.Minute) {
		t.Errorf("expected end at 11:00, got %s", merged[0].End.Format("15:04"))
	}
}

func TestScoreSlot(t *testing.T) {
	svc := newTestService()
	loc := time.Now().Location()

	day := DaySchedule{
		Date:         "2026-03-15",
		Events:       []ScheduleEvent{},
		FreeSlots:    []TimeSlot{},
		BusyHours:    2,
		FreeHours:    7,
		MeetingCount: 0,
	}

	morningStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	morningEnd := time.Date(2026, 3, 15, 10, 0, 0, 0, loc)

	scoreMorningPref := svc.scoreSlot(morningStart, morningEnd, day, true)
	scoreMorningNoPref := svc.scoreSlot(morningStart, morningEnd, day, false)

	if scoreMorningPref <= scoreMorningNoPref {
		t.Errorf("morning slot should score higher with morning preference: pref=%f, nopref=%f", scoreMorningPref, scoreMorningNoPref)
	}
}

func TestScoreSlot_BufferPenalty(t *testing.T) {
	svc := newTestService()
	loc := time.Now().Location()

	day := DaySchedule{
		Date: "2026-03-15",
		Events: []ScheduleEvent{
			{
				Title:  "Long meeting",
				Start:  time.Date(2026, 3, 15, 9, 30, 0, 0, loc),
				End:    time.Date(2026, 3, 15, 11, 0, 0, 0, loc),
				Source: "calendar",
			},
		},
		FreeHours:    5,
		MeetingCount: 1,
	}

	rightAfter := time.Date(2026, 3, 15, 11, 5, 0, 0, loc)
	rightAfterEnd := time.Date(2026, 3, 15, 12, 5, 0, 0, loc)
	scoreRightAfter := svc.scoreSlot(rightAfter, rightAfterEnd, day, false)

	withBuffer := time.Date(2026, 3, 15, 11, 30, 0, 0, loc)
	withBufferEnd := time.Date(2026, 3, 15, 12, 30, 0, 0, loc)
	scoreWithBuffer := svc.scoreSlot(withBuffer, withBufferEnd, day, false)

	if scoreRightAfter >= scoreWithBuffer {
		t.Errorf("slot right after long meeting should score lower: rightAfter=%f, withBuffer=%f", scoreRightAfter, scoreWithBuffer)
	}
}

func TestWorkingHours(t *testing.T) {
	svc := newTestService()
	start, end := svc.workingHours()
	if start != 9 {
		t.Errorf("expected work start 9, got %d", start)
	}
	if end != 18 {
		t.Errorf("expected work end 18, got %d", end)
	}
}

func TestParseDate(t *testing.T) {
	svc := newTestService()
	loc := time.Now().Location()

	d, err := svc.parseDate("2026-06-15", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Year() != 2026 || d.Month() != 6 || d.Day() != 15 {
		t.Errorf("expected 2026-06-15, got %s", d.Format("2006-01-02"))
	}

	d, err = svc.parseDate("", loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	today := time.Now().In(loc)
	if d.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Errorf("expected today %s, got %s", today.Format("2006-01-02"), d.Format("2006-01-02"))
	}

	_, err = svc.parseDate("xyz", loc)
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestFindFreeSlotsInDay_OverlappingEvents(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "Meeting A",
			Start:  time.Date(2026, 3, 15, 10, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 11, 30, 0, 0, loc),
			Source: "calendar",
		},
		{
			Title:  "Meeting B (overlaps A)",
			Start:  time.Date(2026, 3, 15, 11, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 12, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.FindFreeSlotsInDay(events, whStart, whEnd)

	if len(freeSlots) != 2 {
		t.Fatalf("expected 2 free slots, got %d", len(freeSlots))
	}
	if freeSlots[0].Duration != 60 {
		t.Errorf("slot 0: expected 60 min, got %d", freeSlots[0].Duration)
	}
	if freeSlots[1].Duration != 360 {
		t.Errorf("slot 1: expected 360 min, got %d", freeSlots[1].Duration)
	}
}

func TestFindFreeSlotsInDay_FullyBooked(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "All day meeting",
			Start:  time.Date(2026, 3, 15, 9, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 18, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.FindFreeSlotsInDay(events, whStart, whEnd)
	if len(freeSlots) != 0 {
		t.Errorf("expected 0 free slots for fully booked day, got %d", len(freeSlots))
	}
}

func TestFindFreeSlotsInDay_SmallGap(t *testing.T) {
	svc := newTestService()

	loc := time.Now().Location()
	whStart := time.Date(2026, 3, 15, 9, 0, 0, 0, loc)
	whEnd := time.Date(2026, 3, 15, 18, 0, 0, 0, loc)

	events := []ScheduleEvent{
		{
			Title:  "Meeting A",
			Start:  time.Date(2026, 3, 15, 9, 0, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 12, 0, 0, 0, loc),
			Source: "calendar",
		},
		{
			Title:  "Meeting B",
			Start:  time.Date(2026, 3, 15, 12, 10, 0, 0, loc),
			End:    time.Date(2026, 3, 15, 18, 0, 0, 0, loc),
			Source: "calendar",
		},
	}

	freeSlots := svc.FindFreeSlotsInDay(events, whStart, whEnd)
	if len(freeSlots) != 0 {
		t.Errorf("expected 0 free slots (gap too small), got %d", len(freeSlots))
	}
}
