package schedule

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDailyFirstRunUsesConfiguredTimezone(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 0, 0, 0, time.UTC)
	next, err := FirstRun("daily", json.RawMessage(`{"time":"10:30"}`), DefaultTimezone, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 5, 2, 30, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

func TestWeeklyFirstRunSelectsNextWeekday(t *testing.T) {
	now := time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC)
	next, err := FirstRun(
		"weekly", json.RawMessage(`{"time":"09:00","weekdays":[5,1]}`), DefaultTimezone, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

func TestIntervalNextAfterSkipsMissedRuns(t *testing.T) {
	scheduled := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	now := scheduled.Add(5*time.Minute + 10*time.Second)
	next, err := NextAfter(
		"interval", json.RawMessage(`{"interval_seconds":60}`), DefaultTimezone, scheduled, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := scheduled.Add(6 * time.Minute)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

func TestIntervalRejectsTooShortValue(t *testing.T) {
	_, err := NormalizeConfig("interval", json.RawMessage(`{"interval_seconds":30}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
}
