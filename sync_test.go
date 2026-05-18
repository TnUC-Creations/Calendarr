package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"google.golang.org/api/calendar/v3"
)

func TestCheckConnectivityDoesNotPreflightSteam(t *testing.T) {
	bodyBytes, err := os.ReadFile("sync.go")
	if err != nil {
		t.Fatalf("read sync.go: %v", err)
	}
	body := string(bodyBytes)
	start := strings.Index(body, "func checkConnectivity(")
	if start < 0 {
		t.Fatal("checkConnectivity function not found in sync.go")
	}
	end := strings.Index(body[start:], "\nfunc listCalendarEvents(")
	if end < 0 {
		t.Fatal("checkConnectivity function end marker not found in sync.go")
	}
	checkBody := body[start : start+end]

	if strings.Contains(checkBody, "cfg.UseSteam") || strings.Contains(checkBody, "checkSteamConnectivity") {
		t.Fatal("checkConnectivity must not preflight Steam; Steam failures must stay non-fatal through syncSteam")
	}
}

func TestAllDayCalendarEventUsesExclusiveEndDate(t *testing.T) {
	ev := allDayCalendarEvent("Movie", "Overview", "2026-05-03", "9")

	if ev.Start == nil || ev.Start.Date != "2026-05-03" {
		t.Fatalf("start date = %#v, want 2026-05-03", ev.Start)
	}
	if ev.End == nil || ev.End.Date != "2026-05-04" {
		t.Fatalf("end date = %#v, want 2026-05-04", ev.End)
	}
	if ev.ColorId != "9" {
		t.Fatalf("color ID = %q, want 9", ev.ColorId)
	}
}

func TestApplyDayOffset(t *testing.T) {
	tests := []struct {
		name   string
		date   string
		offset int
		want   string
	}{
		{name: "zero", date: "2026-05-03", offset: 0, want: "2026-05-03"},
		{name: "forward", date: "2026-05-03", offset: 2, want: "2026-05-05"},
		{name: "backward", date: "2026-05-03", offset: -2, want: "2026-05-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyDayOffset(tt.date, tt.offset); got != tt.want {
				t.Fatalf("applyDayOffset(%q, %d) = %q, want %q", tt.date, tt.offset, got, tt.want)
			}
		})
	}
}

func TestRadarrDigitalReleaseDateFallsBackToPhysical(t *testing.T) {
	withDigital := map[string]interface{}{"digitalRelease": "2026-05-03", "physicalRelease": "2026-05-04"}
	withPhysical := map[string]interface{}{"physicalRelease": "2026-05-04"}
	empty := map[string]interface{}{}

	if got := radarrDigitalReleaseDate(withDigital); got != "2026-05-03" {
		t.Fatalf("digital date = %q, want digitalRelease", got)
	}
	if got := radarrDigitalReleaseDate(withPhysical); got != "2026-05-04" {
		t.Fatalf("digital fallback = %q, want physicalRelease", got)
	}
	if got := radarrDigitalReleaseDate(empty); got != "" {
		t.Fatalf("empty digital date = %q, want empty", got)
	}
}

func TestShouldTrackRadarrRelease(t *testing.T) {
	cfg := defaultConfig()
	cfg.RadarrTrackTheater = false
	cfg.RadarrTrackDigital = true

	if shouldTrackRadarrRelease(cfg, "theater") {
		t.Fatal("expected theater tracking disabled")
	}
	if !shouldTrackRadarrRelease(cfg, "digital") {
		t.Fatal("expected digital tracking enabled")
	}
	if shouldTrackRadarrRelease(cfg, "unknown") {
		t.Fatal("expected unknown release type disabled")
	}
}

func TestDeleteRadarrEventsByKindDryRun(t *testing.T) {
	cfg := defaultConfig()
	events := []*calendar.Event{
		{Summary: "Movie Theater Release"},
		{Summary: "Movie Digital Release"},
		{Summary: "Show S01E01"},
	}

	deleted, err := deleteRadarrEventsByKind(nil, "primary", &events, cfg, "radarr_digital", true)
	if err != nil {
		t.Fatalf("deleteRadarrEventsByKind returned error: %v", err)
	}

	if len(deleted) != 1 || deleted[0] != "Movie Digital Release removed from calendar" {
		t.Fatalf("deleted = %#v, want one digital deletion", deleted)
	}
	if len(events) != 2 {
		t.Fatalf("remaining events = %d, want 2", len(events))
	}
	for _, ev := range events {
		if ev.Summary == "Movie Digital Release" {
			t.Fatal("digital event should have been removed from event cache")
		}
	}
}

func TestRetryWithLogSucceedsAfterTransientFailures(t *testing.T) {
	var buf bytes.Buffer
	attempts := 0

	err := retryWithLog(&buf, "Test API", 3, 0, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary outage")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("retryWithLog returned error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	log := buf.String()
	if !strings.Contains(log, "Test API attempt 1/3 failed") || !strings.Contains(log, "[OK] Test API connected") {
		t.Fatalf("log output missing retry details:\n%s", log)
	}
}

func TestRetryWithLogFailsAfterAttempts(t *testing.T) {
	var buf bytes.Buffer
	attempts := 0

	err := retryWithLog(&buf, "Test API", 3, 0, func() error {
		attempts++
		return errors.New("still down")
	})

	if err == nil {
		t.Fatal("expected final error")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if !strings.Contains(err.Error(), "failed after 3 attempt") {
		t.Fatalf("error = %q, want failed attempts message", err)
	}
}

func TestIndexEventsBySummaryKeepsFirstEvent(t *testing.T) {
	first := &calendar.Event{Id: "first", Summary: "Movie Theater Release"}
	second := &calendar.Event{Id: "second", Summary: "Movie Theater Release"}
	index := indexEventsBySummary([]*calendar.Event{
		nil,
		{Id: "blank"},
		first,
		second,
	})

	if got := index["Movie Theater Release"]; got != first {
		t.Fatalf("indexed event = %#v, want first duplicate", got)
	}
	if _, ok := index[""]; ok {
		t.Fatal("blank summary should not be indexed")
	}
}

func TestAllDayEventDateMismatchDetectsWrongMovieEndDate(t *testing.T) {
	existing := &calendar.Event{
		Start: &calendar.EventDateTime{Date: "2026-05-03"},
		End:   &calendar.EventDateTime{Date: "2026-05-03"},
	}

	if !allDayEventNeedsUpdate(existing, "2026-05-03", "") {
		t.Fatal("expected wrong exclusive end date to require update")
	}
}

func TestAllDayEventDateMismatchDetectsWrongEpisodeStartOrEndDate(t *testing.T) {
	wrongStart := &calendar.Event{
		Start: &calendar.EventDateTime{Date: "2026-05-02"},
		End:   &calendar.EventDateTime{Date: "2026-05-04"},
	}
	wrongEnd := &calendar.Event{
		Start: &calendar.EventDateTime{Date: "2026-05-03"},
		End:   &calendar.EventDateTime{Date: "2026-05-05"},
	}

	if !allDayEventNeedsUpdate(wrongStart, "2026-05-03", "") {
		t.Fatal("expected wrong start date to require update")
	}
	if !allDayEventNeedsUpdate(wrongEnd, "2026-05-03", "") {
		t.Fatal("expected wrong end date to require update")
	}
}

func TestAllDayEventNeedsUpdateDetectsColorChange(t *testing.T) {
	existing := &calendar.Event{
		Start:   &calendar.EventDateTime{Date: "2026-05-03"},
		End:     &calendar.EventDateTime{Date: "2026-05-04"},
		ColorId: "4",
	}

	if !allDayEventNeedsUpdate(existing, "2026-05-03", "5") {
		t.Fatal("expected changed color to require update")
	}
}
