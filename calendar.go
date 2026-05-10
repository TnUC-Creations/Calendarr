package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ---- Admin logging ----------------------------------------------------------

// logEvent writes a timestamped separator block to the daily log file only.
// Use this for non-sync events (startup, scheduler, update checks).
// Unlike adminLog it does NOT write to history.
func logEvent(msg string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	entry := fmt.Sprintf("\n%s\n%s %s\n", separator(), ts, msg)
	if f, err := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		_, _ = f.WriteString(entry)
		f.Close()
	}
}

// adminLog writes a block to sync.log and appends a system entry to history.json.
func adminLog(action, detail string) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	line := "[System] " + action
	if detail != "" {
		line += ": " + detail
	}
	entry := fmt.Sprintf("\n%s\n%s %s\n", separator(), ts, line)
	if f, err := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		_, _ = f.WriteString(entry)
		f.Close()
	}
	appendHistory([]HistoryEntry{{
		Timestamp: ts,
		Action:    "system",
		Message:   line,
	}})
}

func separator() string {
	const sep = "============================================================"
	return sep
}

func cleanupEventSource(summary string, cfg Config) string {
	source := cleanupEventKind(summary, cfg)
	switch source {
	case "radarr_theater", "radarr_digital":
		return "radarr"
	default:
		return source
	}
}

func cleanupEventKind(summary string, cfg Config) string {
	if summary == "" {
		return ""
	}
	theaterSuffix := templateSuffix(cfg.MovieTheaterTemplate)
	digitalSuffix := templateSuffix(cfg.MovieDigitalTemplate)
	if theaterSuffix != "" && hasSuffix(summary, theaterSuffix) {
		return "radarr_theater"
	}
	if digitalSuffix != "" && hasSuffix(summary, digitalSuffix) {
		return "radarr_digital"
	}
	if episodeRe.MatchString(summary) {
		return "sonarr"
	}
	return ""
}

func sourceSet(sources []string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, source := range sources {
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "radarr":
			set["radarr"] = struct{}{}
		case "sonarr":
			set["sonarr"] = struct{}{}
		}
	}
	if len(set) == 0 {
		set["radarr"] = struct{}{}
		set["sonarr"] = struct{}{}
	}
	return set
}

func normalizeCleanupMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "future":
		return "future"
	case "all":
		return "all"
	default:
		return "past"
	}
}

func cleanupModeLabel(mode string) string {
	switch normalizeCleanupMode(mode) {
	case "future":
		return "Future events"
	case "all":
		return "All events"
	default:
		return "Past events"
	}
}

func cleanupTargetLogName(cfg Config, calendarID string) string {
	for _, target := range calendarTargets(cfg) {
		if strings.EqualFold(target.ID, calendarID) {
			if target.Name != "" {
				return fmt.Sprintf("%s (%s)", target.Name, target.ID)
			}
			return target.ID
		}
	}
	return calendarID
}

func cleanupTodayRFC3339() string {
	now := time.Now()
	todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return todayMidnight.Format(time.RFC3339)
}

// ---- Cleanup worker ---------------------------------------------------------

// runCleanup runs as a goroutine. mode is "past", "future", or "all".
// Returns the list of deleted event messages so callers can merge them into
// dashboard stats (auto-cleanup path). Goroutine callers safely discard it.
func runCleanup(cfg Config, mode string) ([]string, error) {
	mode = normalizeCleanupMode(mode)
	label := cleanupModeLabel(mode)

	targets := calendarTargets(cfg)
	multiTarget := len(targets) > 1

	adminLog(label+" cleanup started", "")

	calSvc, err := getCalService(cfg)
	if err != nil {
		msg := fmt.Sprintf("Cleanup error: %v", err)
		adminLog(msg, "")
		finishCleanup(false, 0, 0, msg)
		return nil, err
	}

	today := cleanupTodayRFC3339()
	var deleted, scanned int
	var deletedMessages []string

	for _, target := range targets {
		var pageToken string
		for {
			call := calSvc.Events.List(target.ID).
				SingleEvents(true).MaxResults(2500)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			switch mode {
			case "past":
				call = call.TimeMax(today)
			case "future":
				call = call.TimeMin(today)
			}

			res, err := call.Do()
			if err != nil {
				msg := fmt.Sprintf("Cleanup error after %d deletions: %v", deleted, err)
				adminLog(msg, "")
				flushCleanupHistory(deletedMessages)
				finishCleanup(false, scanned, deleted, fmt.Sprintf("Cleanup error after %d deletions: %v", deleted, err))
				return deletedMessages, err
			}

			for _, ev := range res.Items {
				scanned++
				s := ev.Summary
				if s == "" {
					continue
				}
				if cleanupEventSource(s, cfg) != "" {
					if err := calSvc.Events.Delete(target.ID, ev.Id).Do(); err != nil {
						msg := fmt.Sprintf("Cleanup delete error after %d deletions: %v", deleted, err)
						adminLog(msg, "")
						flushCleanupHistory(deletedMessages)
						finishCleanup(false, scanned, deleted, msg)
						return deletedMessages, err
					}
					deleted++
					deletedMsg := s + " removed from calendar" + targetLabel(target, multiTarget)
					deletedMessages = append(deletedMessages, deletedMsg)
					ts := time.Now().Format("2006-01-02 15:04:05")
					if f, err2 := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err2 == nil {
						fmt.Fprintf(f, "%s [Cleanup] Deleted: %s%s\n", ts, s, targetLabel(target, multiTarget))
						f.Close()
					}
				}
				updateCleanupProgress(scanned, deleted)
			}

			pageToken = res.NextPageToken
			if pageToken == "" {
				break
			}
		}
	}

	flushCleanupHistory(deletedMessages)
	msg := fmt.Sprintf("Done. Scanned %d events, deleted %d.", scanned, deleted)
	adminLog(fmt.Sprintf("%s cleanup finished: scanned %d, deleted %d", label, scanned, deleted), "")
	finishCleanup(true, scanned, deleted, msg)
	return deletedMessages, nil
}

// flushCleanupHistory writes deleted event entries to history in one batch.
func flushCleanupHistory(messages []string) {
	if len(messages) == 0 {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	entries := make([]HistoryEntry, len(messages))
	for i, msg := range messages {
		entries[i] = HistoryEntry{Timestamp: ts, Action: "deleted", Message: msg}
	}
	appendHistory(entries)
}

// simulateCleanup returns what a past-events cleanup would remove without
// touching the calendar — used by dry-run preview so results are complete.
func simulateCleanup(cfg Config) []string {
	targets := calendarTargets(cfg)
	multiTarget := len(targets) > 1
	calSvc, err := getCalService(cfg)
	if err != nil {
		return nil
	}
	today := cleanupTodayRFC3339()

	var wouldDelete []string
	for _, target := range targets {
		var pageToken string
		for {
			call := calSvc.Events.List(target.ID).
				SingleEvents(true).MaxResults(2500).TimeMax(today)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			res, err := call.Do()
			if err != nil {
				break
			}
			for _, ev := range res.Items {
				s := ev.Summary
				if s == "" {
					continue
				}
				if cleanupEventSource(s, cfg) != "" {
					wouldDelete = append(wouldDelete, s+" removed from calendar"+targetLabel(target, multiTarget))
				}
			}
			pageToken = res.NextPageToken
			if pageToken == "" {
				break
			}
		}
	}
	return wouldDelete
}

func cleanupTargetCalendar(cfg Config, calendarID, mode string, sources []string) ([]string, int, int, error) {
	calendarID = strings.TrimSpace(calendarID)
	mode = normalizeCleanupMode(mode)
	label := cleanupModeLabel(mode)
	sourceDetail := strings.Join(sources, ", ")
	if sourceDetail == "" {
		sourceDetail = "radarr, sonarr"
	}
	if calendarID == "" {
		adminLog(label+" target cleanup failed", "missing calendar ID")
		return nil, 0, 0, fmt.Errorf("calendar ID is required")
	}
	targetName := cleanupTargetLogName(cfg, calendarID)
	adminLog(fmt.Sprintf("%s target cleanup started: calendar %s", label, targetName), sourceDetail)

	calSvc, err := getCalService(cfg)
	if err != nil {
		adminLog(fmt.Sprintf("%s target cleanup failed: calendar %s: %v", label, targetName, err), sourceDetail)
		return nil, 0, 0, err
	}
	wantedSources := sourceSet(sources)
	today := cleanupTodayRFC3339()
	var deletedMessages []string
	var scanned, deleted int
	var pageToken string
	for {
		call := calSvc.Events.List(calendarID).
			SingleEvents(true).MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		switch mode {
		case "past":
			call = call.TimeMax(today)
		case "future":
			call = call.TimeMin(today)
		}
		res, err := call.Do()
		if err != nil {
			adminLog(fmt.Sprintf("%s target cleanup failed: calendar %s, scanned %d, deleted %d: %v", label, targetName, scanned, deleted, err), sourceDetail)
			return deletedMessages, scanned, deleted, err
		}
		for _, ev := range res.Items {
			scanned++
			source := cleanupEventSource(ev.Summary, cfg)
			if source == "" {
				continue
			}
			if _, ok := wantedSources[source]; !ok {
				continue
			}
			if err := calSvc.Events.Delete(calendarID, ev.Id).Do(); err != nil {
				adminLog(fmt.Sprintf("%s target cleanup failed deleting %q from calendar %s after %d deletions: %v", label, ev.Summary, targetName, deleted, err), sourceDetail)
				return deletedMessages, scanned, deleted, err
			}
			deleted++
			deletedMessages = append(deletedMessages, ev.Summary+" removed from calendar")
		}
		updateCleanupProgress(scanned, deleted)
		pageToken = res.NextPageToken
		if pageToken == "" {
			break
		}
	}
	flushCleanupHistory(deletedMessages)
	adminLog(fmt.Sprintf("%s target cleanup finished: calendar %s, scanned %d, deleted %d", label, targetName, scanned, deleted), sourceDetail)
	return deletedMessages, scanned, deleted, nil
}

func hasSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// templateSuffix extracts the text that follows {title} in an event naming
// template. Used so cleanup matches events by the same patterns the user
// configured, rather than hardcoded strings.
func templateSuffix(tmpl string) string {
	const marker = "{title}"
	if idx := strings.Index(tmpl, marker); idx >= 0 {
		return strings.TrimSpace(tmpl[idx+len(marker):])
	}
	return ""
}
