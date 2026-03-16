package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// parseGCalEvent, buildGCalBody, calendarID, calendarMaxResults, calendarTimeZone
// tests moved to internal/life/calendar/calendar_test.go.

// --- Tool Handler Input Validation Tests ---

func TestToolCalendarList_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarList(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("error should mention not enabled, got: %v", err)
	}
}

func TestToolCalendarCreate_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarCreate(context.Background(), cfg, json.RawMessage(`{"summary":"test","start":"2024-01-15T14:00:00Z"}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
}

func TestToolCalendarCreate_MissingSummary(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = newCalendarService(cfg)

	_, err := toolCalendarCreate(context.Background(), cfg, json.RawMessage(`{"start":"2024-01-15T14:00:00Z"}`))
	if err == nil {
		t.Error("expected error for missing summary")
	}
	if !strings.Contains(err.Error(), "summary is required") {
		t.Errorf("error should mention summary, got: %v", err)
	}
}

func TestToolCalendarCreate_MissingStart(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = newCalendarService(cfg)

	_, err := toolCalendarCreate(context.Background(), cfg, json.RawMessage(`{"summary":"test"}`))
	if err == nil {
		t.Error("expected error for missing start")
	}
	if !strings.Contains(err.Error(), "start time is required") {
		t.Errorf("error should mention start, got: %v", err)
	}
}

func TestToolCalendarDelete_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarDelete(context.Background(), cfg, json.RawMessage(`{"eventId":"ev1"}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
}

func TestToolCalendarDelete_MissingEventID(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = newCalendarService(cfg)

	_, err := toolCalendarDelete(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing eventId")
	}
	if !strings.Contains(err.Error(), "eventId is required") {
		t.Errorf("error should mention eventId, got: %v", err)
	}
}

func TestToolCalendarUpdate_MissingEventID(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = newCalendarService(cfg)

	_, err := toolCalendarUpdate(context.Background(), cfg, json.RawMessage(`{"summary":"updated"}`))
	if err == nil {
		t.Error("expected error for missing eventId")
	}
}

func TestToolCalendarSearch_NotEnabled(t *testing.T) {
	cfg := &Config{Calendar: CalendarConfig{Enabled: false}}
	_, err := toolCalendarSearch(context.Background(), cfg, json.RawMessage(`{"query":"test"}`))
	if err == nil {
		t.Error("expected error when not enabled")
	}
}

func TestToolCalendarSearch_MissingQuery(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	globalCalendarService = newCalendarService(cfg)

	_, err := toolCalendarSearch(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing query")
	}
	if !strings.Contains(err.Error(), "query is required") {
		t.Errorf("error should mention query, got: %v", err)
	}
}

func TestToolCalendarList_NotInitialized(t *testing.T) {
	oldSvc := globalCalendarService
	defer func() { globalCalendarService = oldSvc }()
	globalCalendarService = nil

	cfg := &Config{Calendar: CalendarConfig{Enabled: true}}
	_, err := toolCalendarList(context.Background(), cfg, json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error when service not initialized")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("error should mention not initialized, got: %v", err)
	}
}
