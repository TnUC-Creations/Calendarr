package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"context"

	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// newRequestWithKey builds an HTTP request and attaches the given API key
// header in one step. Surfaces NewRequest errors (bad URL, bad method) so
// callers no longer panic on `req.Header.Set` against a nil request.
func newRequestWithKey(method, url, apiKey string) (*http.Request, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid request to %q: %w", url, err)
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	return req, nil
}

// ---- Template formatting ----------------------------------------------------

// fmtMovieTitle replaces {title} in a template string.
func fmtMovieTitle(tmpl, title string) string {
	return strings.ReplaceAll(tmpl, "{title}", title)
}

// fmtEpisodeTitle replaces {title}, {season:02d}, {episode:02d}.
func fmtEpisodeTitle(tmpl, title string, season, episode int) string {
	r := strings.ReplaceAll(tmpl, "{title}", title)
	r = strings.ReplaceAll(r, "{season:02d}", fmt.Sprintf("%02d", season))
	r = strings.ReplaceAll(r, "{episode:02d}", fmt.Sprintf("%02d", episode))
	return r
}

func localDateFromAPI(dateStr string) (string, bool) {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return "", false
	}
	if !strings.Contains(dateStr, "T") {
		if _, err := time.Parse("2006-01-02", dateStr); err == nil {
			return dateStr, true
		}
		return "", false
	}
	if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
		return t.Local().Format("2006-01-02"), true
	}
	if t, err := time.Parse("2006-01-02T15:04:05Z", dateStr); err == nil {
		return t.Local().Format("2006-01-02"), true
	}
	return "", false
}

func allDayEndDate(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

func applyDayOffset(date string, offset int) string {
	if offset == 0 {
		return date
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, offset).Format("2006-01-02")
}

func radarrDigitalReleaseDate(movie map[string]interface{}) string {
	if v, ok := movie["digitalRelease"].(string); ok && v != "" {
		return v
	}
	if v, ok := movie["physicalRelease"].(string); ok && v != "" {
		return v
	}
	return ""
}

func shouldTrackRadarrRelease(cfg Config, releaseType string) bool {
	switch releaseType {
	case "theater":
		return cfg.RadarrTrackTheater
	case "digital":
		return cfg.RadarrTrackDigital
	default:
		return false
	}
}

func deleteRadarrEventsByKind(calSvc *calendar.Service, calendarID string, allEvents *[]*calendar.Event, cfg Config, kind string, dryRun bool) ([]string, error) {
	var deleted []string
	kept := (*allEvents)[:0]
	for _, ev := range *allEvents {
		if cleanupEventKind(ev.Summary, cfg) != kind {
			kept = append(kept, ev)
			continue
		}
		ok := dryRun
		if !dryRun {
			if err := calSvc.Events.Delete(calendarID, ev.Id).Do(); err == nil {
				ok = true
			} else {
				return deleted, fmt.Errorf("delete %q from %s: %w", ev.Summary, calendarID, err)
			}
		}
		if ok {
			deleted = append(deleted, ev.Summary+" removed from calendar")
			continue
		}
		kept = append(kept, ev)
	}
	*allEvents = kept
	return deleted, nil
}

func allDayCalendarEvent(summary, description, date, colorID string) *calendar.Event {
	ev := &calendar.Event{
		Summary:     summary,
		Description: description,
		Start:       &calendar.EventDateTime{Date: date},
		End:         &calendar.EventDateTime{Date: allDayEndDate(date)},
	}
	if colorID != "" {
		ev.ColorId = colorID
	}
	return ev
}

func allDayEventNeedsUpdate(existing *calendar.Event, date, colorID string) bool {
	if existing == nil || existing.Start == nil || existing.End == nil {
		return true
	}
	return existing.Start.Date != date || existing.End.Date != allDayEndDate(date) || existing.ColorId != colorID
}

func targetLabel(t CalendarTarget, multi bool) string {
	if !multi {
		return ""
	}
	name := t.Name
	if name == "" {
		name = t.ID
	}
	return " [" + name + "]"
}

// ---- Ignored shows ----------------------------------------------------------

func loadIgnoredShows(path string) map[string]struct{} {
	set := make(map[string]struct{})
	data, err := os.ReadFile(path)
	if err != nil {
		return set
	}
	// Try JSON format first (new default).
	var list []string
	if json.Unmarshal(data, &list) == nil {
		for _, s := range list {
			s = strings.TrimSpace(s)
			if s != "" {
				set[s] = struct{}{}
			}
		}
		return set
	}
	// Fall back to line-by-line text format for backward compatibility.
	for _, line := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(line)
		if s != "" && !strings.HasPrefix(s, "#") {
			set[s] = struct{}{}
		}
	}
	return set
}

// prevIgnoredFile is stored as JSON (replacing the Python pickle).
const prevIgnoredFile = "ignored_shows_prev.json"

func loadPrevIgnored() map[string]struct{} {
	set := make(map[string]struct{})
	data, err := os.ReadFile(dataPath(prevIgnoredFile))
	if err != nil {
		return set // first run: treat everything as newly ignored
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return set
	}
	for _, s := range list {
		set[s] = struct{}{}
	}
	return set
}

func savePrevIgnored(set map[string]struct{}) {
	list := make([]string, 0, len(set))
	for s := range set {
		list = append(list, s)
	}
	sort.Strings(list)
	data, _ := json.MarshalIndent(list, "", "  ")
	_ = os.WriteFile(dataPath(prevIgnoredFile), data, 0644)
}

// ---- Pushover ---------------------------------------------------------------

// sendPushover splits messages that exceed 990 chars into multiple notifications.
func sendPushover(token, user, message, sound string) {
	if sound == "" {
		sound = "intermission"
	}
	const limit = 990
	lines := strings.Split(message, "\n")
	var chunks []string
	var cur []string
	curLen := 0
	for _, line := range lines {
		needed := len(line)
		if len(cur) > 0 {
			needed++ // newline separator
		}
		if len(cur) > 0 && curLen+needed > limit {
			chunks = append(chunks, strings.Join(cur, "\n"))
			cur = []string{line}
			curLen = len(line)
		} else {
			cur = append(cur, line)
			curLen += needed
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, strings.Join(cur, "\n"))
	}

	total := len(chunks)
	client := &http.Client{Timeout: 15 * time.Second}
	for i, chunk := range chunks {
		msg := chunk
		if total > 1 {
			msg = fmt.Sprintf("(%d/%d) %s", i+1, total, chunk)
		}
		resp, err := client.PostForm("https://api.pushover.net/1/messages.json", url.Values{
			"token":   {token},
			"user":    {user},
			"message": {msg},
			"sound":   {sound},
		})
		if err != nil {
			fmt.Printf("Pushover error: %v\n", err)
			continue
		}
		resp.Body.Close()
	}
}

// buildPushoverMessage formats sync results into a grouped, readable message.
func buildPushoverMessage(added, updated, deleted []string) string {
	var sb strings.Builder
	if len(added) > 0 {
		fmt.Fprintf(&sb, "Added (%d):\n", len(added))
		for _, m := range added {
			sb.WriteString(m + "\n")
		}
	}
	if len(updated) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "Updated (%d):\n", len(updated))
		for _, m := range updated {
			sb.WriteString(m + "\n")
		}
	}
	if len(deleted) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "Removed (%d):\n", len(deleted))
		for _, m := range deleted {
			sb.WriteString(m + "\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ---- Google Calendar service ------------------------------------------------

func getCalService(cfg Config) (*calendar.Service, error) {
	if cfg.GoogleRefreshToken == "" {
		return nil, fmt.Errorf("Google Calendar not connected — go to Settings to connect your account")
	}
	token := &oauth2.Token{RefreshToken: cfg.GoogleRefreshToken}
	tokenSource := oauthConfig("").TokenSource(context.Background(), token)
	svc, err := calendar.NewService(context.Background(), option.WithTokenSource(tokenSource))
	if err != nil {
		return nil, fmt.Errorf("creating calendar service: %w", err)
	}
	return svc, nil
}

// ---- Core sync --------------------------------------------------------------

// SyncResult holds the categorised change messages from a sync run.
type SyncResult struct {
	Added   []string
	Updated []string
	Deleted []string
}

const (
	syncPreflightAttempts = 3
	syncPreflightDelay    = 10 * time.Second
)

func retryWithLog(w io.Writer, label string, attempts int, delay time.Duration, fn func() error) error {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		fmt.Fprintf(w, "[Check] %s attempt %d/%d\n", label, attempt, attempts)
		if err := fn(); err != nil {
			lastErr = err
			fmt.Fprintf(w, "[WARN] %s attempt %d/%d failed: %v\n", label, attempt, attempts, err)
			if attempt < attempts {
				fmt.Fprintf(w, "[Check] Retrying %s in %s\n", label, delay)
				if delay > 0 {
					time.Sleep(delay)
				}
			}
			continue
		}
		fmt.Fprintf(w, "[OK] %s connected\n", label)
		return nil
	}
	return fmt.Errorf("%s failed after %d attempt(s): %w", label, attempts, lastErr)
}

func checkHTTPStatus(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// checkConnectivity verifies that all enabled services and calendar targets are
// reachable before starting a sync. Temporary failures are retried first.
func checkConnectivity(cfg Config, calSvc *calendar.Service, targets []CalendarTarget, w io.Writer) error {
	client := &http.Client{Timeout: 10 * time.Second}

	if cfg.UseRadarr {
		err := retryWithLog(w, "Radarr", syncPreflightAttempts, syncPreflightDelay, func() error {
			req, err := newRequestWithKey("GET", cfg.RadarrURL+"/system/status", cfg.RadarrAPIKey)
			if err != nil {
				return err
			}
			return checkHTTPStatus(client, req)
		})
		if err != nil {
			fmt.Fprintf(w, "[ERROR] %v\n", err)
			return err
		}
	}

	if cfg.UseSonarr {
		err := retryWithLog(w, "Sonarr", syncPreflightAttempts, syncPreflightDelay, func() error {
			req, err := newRequestWithKey("GET", cfg.SonarrURL+"/system/status", cfg.SonarrAPIKey)
			if err != nil {
				return err
			}
			return checkHTTPStatus(client, req)
		})
		if err != nil {
			fmt.Fprintf(w, "[ERROR] %v\n", err)
			return err
		}
	}

	for _, target := range targets {
		label := "Google Calendar"
		if len(targets) > 1 {
			name := target.Name
			if name == "" {
				name = target.ID
			}
			label += " [" + name + "]"
		}
		targetID := target.ID
		err := retryWithLog(w, label, syncPreflightAttempts, syncPreflightDelay, func() error {
			_, err := calSvc.Calendars.Get(targetID).Do()
			return err
		})
		if err != nil {
			fmt.Fprintf(w, "[ERROR] %v\n", err)
			return err
		}
	}

	return nil

}

func listCalendarEvents(calSvc *calendar.Service, calendarID string) ([]*calendar.Event, error) {
	var events []*calendar.Event
	var pageToken string
	for {
		call := calSvc.Events.List(calendarID).SingleEvents(true).MaxResults(2500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		res, err := call.Do()
		if err != nil {
			return events, err
		}
		events = append(events, res.Items...)
		pageToken = res.NextPageToken
		if pageToken == "" {
			return events, nil
		}
	}
}

func indexEventsBySummary(events []*calendar.Event) map[string]*calendar.Event {
	index := make(map[string]*calendar.Event, len(events))
	for _, ev := range events {
		if ev == nil || ev.Summary == "" {
			continue
		}
		if _, exists := index[ev.Summary]; !exists {
			index[ev.Summary] = ev
		}
	}
	return index
}

// runSync executes the full Radarr + Sonarr sync. Log lines are written to w.
// When dryRun is true all calendar writes are skipped and the result reflects
// what would have changed — useful for the Preview Changes feature.
func runSync(cfg Config, w io.Writer, dryRun bool) (SyncResult, error) {
	var result SyncResult

	targets := calendarTargets(cfg)
	multiTarget := len(targets) > 1

	// Route progress updates to the appropriate state (sync vs preview).
	progress := setSyncProgress
	if dryRun {
		progress = setPreviewProgress
	}

	calSvc, err := getCalService(cfg)
	if err != nil {
		fmt.Fprintf(w, "[ERROR] Google Calendar: %v\n", err)
		fmt.Fprintf(w, "[Sync] Pre-flight failed — aborting\n")
		return result, fmt.Errorf("calendar service: %w", err)
	}

	// Pre-flight: verify all configured services are reachable before doing any work.
	if err := checkConnectivity(cfg, calSvc, targets, w); err != nil {
		fmt.Fprintf(w, "[Sync] Pre-flight failed — aborting\n")
		return result, err
	}

	currentIgnored := loadIgnoredShows(dataPath(cfg.IgnoredShowsFile))
	prevIgnored := loadPrevIgnored()

	// Newly ignored = in current but not in previous.
	newlyIgnored := make(map[string]struct{})
	for t := range currentIgnored {
		if _, wasPrev := prevIgnored[t]; !wasPrev {
			newlyIgnored[t] = struct{}{}
		}
	}

	// Log ignored show state.
	if len(currentIgnored) == 0 {
		fmt.Fprintf(w, "[Sync] No ignored shows\n")
	} else {
		ignoredTitles := make([]string, 0, len(currentIgnored))
		for t := range currentIgnored {
			ignoredTitles = append(ignoredTitles, t)
		}
		sort.Strings(ignoredTitles)
		fmt.Fprintf(w, "[Sync] Ignored shows (%d): %s\n", len(currentIgnored), compactNames(ignoredTitles, 10, len(ignoredTitles)))
	}
	if len(newlyIgnored) > 0 {
		newTitles := make([]string, 0, len(newlyIgnored))
		for t := range newlyIgnored {
			newTitles = append(newTitles, t)
		}
		sort.Strings(newTitles)
		fmt.Fprintf(w, "[Sync] Newly ignored this run (%d): %s\n", len(newlyIgnored), compactNames(newTitles, 10, len(newTitles)))
	}

	// deleteShowEvents removes all calendar events whose summary starts with title.
	// Returns the number of events deleted. Only logs when events are actually removed.
	deleteShowEvents := func(title string) (int, error) {
		removedCount := 0
		for _, target := range targets {
			var pageToken string
			for {
				call := calSvc.Events.List(target.ID).
					SingleEvents(true).MaxResults(2500)
				if pageToken != "" {
					call = call.PageToken(pageToken)
				}
				res, err := call.Do()
				if err != nil {
					fmt.Fprintf(w, "[Cleanup] Error listing events for %q on %s: %v\n", title, target.ID, err)
					return removedCount, fmt.Errorf("cleanup list events for %q on %s: %w", title, target.ID, err)
				}
				for _, ev := range res.Items {
					if strings.HasPrefix(ev.Summary, title) {
						ok := dryRun
						if !dryRun {
							if err := calSvc.Events.Delete(target.ID, ev.Id).Do(); err != nil {
								fmt.Fprintf(w, "[Cleanup] Error deleting %q: %v\n", ev.Summary, err)
								return removedCount, fmt.Errorf("cleanup delete %q: %w", ev.Summary, err)
							} else {
								ok = true
							}
						}
						if ok {
							removedCount++
							msg := fmt.Sprintf("%s removed from calendar%s", ev.Summary, targetLabel(target, multiTarget))
							result.Deleted = append(result.Deleted, msg)
							fmt.Fprintln(w, msg)
						}
					}
				}
				pageToken = res.NextPageToken
				if pageToken == "" {
					break
				}
			}
		}
		return removedCount, nil
	}

	// ---- Radarr ----------------------------------------------------------------
	if cfg.UseRadarr {
		radarrStart := time.Now()
		fmt.Fprintf(w, "[Sync] Starting Radarr phase...\n")
		progress("Fetching Radarr movies...")
		httpClient := &http.Client{Timeout: 30 * time.Second}
		req, err := newRequestWithKey("GET", cfg.RadarrURL+"/movie", cfg.RadarrAPIKey)
		if err != nil {
			fmt.Fprintf(w, "[ERROR] Radarr movie fetch: %v\n", err)
			return result, fmt.Errorf("Radarr movie fetch: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			fmt.Fprintf(w, "[ERROR] Radarr movie fetch: %v\n", err)
			return result, fmt.Errorf("Radarr movie fetch: %w", err)
		} else {
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				err := fmt.Errorf("Radarr movie fetch returned HTTP %d", resp.StatusCode)
				fmt.Fprintf(w, "[ERROR] %v\n", err)
				return result, err
			}
			var movies []map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&movies); err != nil {
				fmt.Fprintf(w, "[ERROR] Failed to parse Radarr response: %v\n", err)
				resp.Body.Close()
				return result, err
			}
			resp.Body.Close()

			fmt.Fprintf(w, "[Radarr] %d movie(s) returned\n", len(movies))
			fmt.Fprintf(w, "[Radarr] Tracking theater=%t (offset %+d), digital=%t (offset %+d)\n",
				cfg.RadarrTrackTheater, cfg.RadarrTheaterDayOffset, cfg.RadarrTrackDigital, cfg.RadarrDigitalDayOffset)
			progress(fmt.Sprintf("Processing %d Radarr movies...", len(movies)))

			today := time.Now().Truncate(24 * time.Hour)
			radarrSkippedDL, radarrSkippedIgnored, radarrNoFutureDates := 0, 0, 0
			radarrAddedCount, radarrUpdatedCount, radarrErrCount := 0, 0, 0
			var radarrDLNames, radarrIgnoredNames, radarrNoDatesNames, radarrAddedNames, radarrUpdatedNames []string
			radarrTargets := calendarTargetsForSource(targets, "radarr")

			for _, target := range radarrTargets {
				allEvents, err := listCalendarEvents(calSvc, target.ID)
				if err != nil {
					fmt.Fprintf(w, "[ERROR] Failed to load existing Radarr calendar events for %s: %v\n", target.ID, err)
					return result, fmt.Errorf("load existing Radarr events for %s: %w", target.ID, err)
				}
				if !cfg.RadarrTrackTheater {
					deleted, err := deleteRadarrEventsByKind(calSvc, target.ID, &allEvents, cfg, "radarr_theater", dryRun)
					if err != nil {
						fmt.Fprintf(w, "[ERROR] Failed deleting disabled theater events for %s: %v\n", target.ID, err)
						return result, err
					}
					for _, msg := range deleted {
						msg += targetLabel(target, multiTarget)
						result.Deleted = append(result.Deleted, msg)
						fmt.Fprintf(w, "[Radarr] Deleted disabled theater event: %s\n", msg)
					}
				}
				if !cfg.RadarrTrackDigital {
					deleted, err := deleteRadarrEventsByKind(calSvc, target.ID, &allEvents, cfg, "radarr_digital", dryRun)
					if err != nil {
						fmt.Fprintf(w, "[ERROR] Failed deleting disabled digital events for %s: %v\n", target.ID, err)
						return result, err
					}
					for _, msg := range deleted {
						msg += targetLabel(target, multiTarget)
						result.Deleted = append(result.Deleted, msg)
						fmt.Fprintf(w, "[Radarr] Deleted disabled digital event: %s\n", msg)
					}
				}
				existingMovieEvents := indexEventsBySummary(allEvents)

				for _, movie := range movies {
					title, _ := movie["title"].(string)
					if hasFile, _ := movie["hasFile"].(bool); hasFile {
						radarrSkippedDL++
						if len(radarrDLNames) < 11 && title != "" {
							radarrDLNames = append(radarrDLNames, title)
						}
						continue
					}
					if _, ignored := currentIgnored[title]; ignored {
						radarrSkippedIgnored++
						if len(radarrIgnoredNames) < 11 {
							radarrIgnoredNames = append(radarrIgnoredNames, title)
						}
						continue
					}
					addedBefore := len(result.Added)
					updatedBefore := len(result.Updated)

					overview, _ := movie["overview"].(string)

					processMovieDate := func(dateStr, tmplStr string, offset int) error {
						rd, ok := localDateFromAPI(dateStr)
						if !ok {
							return nil
						}
						rd = applyDayOffset(rd, offset)
						t, err := time.Parse("2006-01-02", rd)
						if err != nil || !t.After(today) {
							return nil
						}
						summary := fmtMovieTitle(tmplStr, title)
						ev := allDayCalendarEvent(summary, overview, rd, target.RadarrColorID)
						existing := existingMovieEvents[summary]
						if existing == nil {
							msg := fmt.Sprintf("%s on %s%s", summary, rd, targetLabel(target, multiTarget))
							ok := dryRun
							if !dryRun {
								inserted, e2 := calSvc.Events.Insert(target.ID, ev).Do()
								ok = e2 == nil
								if ok {
									existingMovieEvents[summary] = inserted
								} else {
									radarrErrCount++
									fmt.Fprintf(w, "[ERROR] Failed adding %s: %v\n", msg, e2)
									return e2
								}
							} else {
								existingMovieEvents[summary] = ev
							}
							if ok {
								result.Added = append(result.Added, msg)
								radarrAddedCount++
								if len(radarrAddedNames) < 11 {
									radarrAddedNames = append(radarrAddedNames, msg)
								}
							}
						} else if allDayEventNeedsUpdate(existing, rd, target.RadarrColorID) {
							msg := fmt.Sprintf("%s date changed to %s%s", summary, rd, targetLabel(target, multiTarget))
							ok := dryRun
							if !dryRun {
								updated, e2 := calSvc.Events.Update(target.ID, existing.Id, ev).Do()
								ok = e2 == nil
								if ok {
									existingMovieEvents[summary] = updated
								} else {
									radarrErrCount++
									fmt.Fprintf(w, "[ERROR] Failed updating %s: %v\n", msg, e2)
									return e2
								}
							} else {
								existingMovieEvents[summary] = ev
							}
							if ok {
								result.Updated = append(result.Updated, msg)
								radarrUpdatedCount++
								if len(radarrUpdatedNames) < 11 {
									radarrUpdatedNames = append(radarrUpdatedNames, msg)
								}
							}
						}
						return nil
					}

					if shouldTrackRadarrRelease(cfg, "theater") {
						if v, ok := movie["inCinemas"].(string); ok {
							if err := processMovieDate(v, cfg.MovieTheaterTemplate, cfg.RadarrTheaterDayOffset); err != nil {
								return result, err
							}
						}
					}
					if shouldTrackRadarrRelease(cfg, "digital") {
						digitalDate := radarrDigitalReleaseDate(movie)
						if err := processMovieDate(digitalDate, cfg.MovieDigitalTemplate, cfg.RadarrDigitalDayOffset); err != nil {
							return result, err
						}
					}
					if len(result.Added)+len(result.Updated) == addedBefore+updatedBefore {
						radarrNoFutureDates++
						if len(radarrNoDatesNames) < 11 && title != "" {
							radarrNoDatesNames = append(radarrNoDatesNames, title)
						}
					}
				}
			}
			if len(radarrTargets) == 0 {
				fmt.Fprintf(w, "[Radarr] No calendar targets have Radarr enabled\n")
			} else {
				fmt.Fprintf(w, "[Radarr] Skipped — downloaded (%d): %s\n", radarrSkippedDL, compactNames(radarrDLNames, 10, radarrSkippedDL))
				fmt.Fprintf(w, "[Radarr] Skipped — no upcoming dates (%d): %s\n", radarrNoFutureDates, compactNames(radarrNoDatesNames, 10, radarrNoFutureDates))
				fmt.Fprintf(w, "[Radarr] Skipped — ignored (%d): %s\n", radarrSkippedIgnored, compactNames(radarrIgnoredNames, 10, radarrSkippedIgnored))
				fmt.Fprintf(w, "[Radarr] Added (%d): %s\n", radarrAddedCount, compactNames(radarrAddedNames, 10, radarrAddedCount))
				fmt.Fprintf(w, "[Radarr] Updated (%d): %s\n", radarrUpdatedCount, compactNames(radarrUpdatedNames, 10, radarrUpdatedCount))
				fmt.Fprintf(w, "[Radarr] Errors (%d)\n", radarrErrCount)
				fmt.Fprintf(w, "[Radarr] Phase complete in %s\n", time.Since(radarrStart).Round(time.Millisecond))
			}
		}
	}

	// ---- Sonarr ----------------------------------------------------------------
	if cfg.UseSonarr {
		sonarrStart := time.Now()
		fmt.Fprintf(w, "[Sync] Starting Sonarr phase...\n")
		progress("Fetching Sonarr shows...")
		httpClient := &http.Client{Timeout: 30 * time.Second}

		// Fetch all shows.
		req, err := newRequestWithKey("GET", cfg.SonarrURL+"/series", cfg.SonarrAPIKey)
		if err != nil {
			fmt.Fprintf(w, "Sonarr error building series request: %v\n", err)
			return result, fmt.Errorf("Sonarr series request: %w", err)
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			fmt.Fprintf(w, "Sonarr error fetching series: %v\n", err)
			return result, fmt.Errorf("Sonarr series fetch: %w", err)
		} else {
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				err := fmt.Errorf("Sonarr series fetch returned HTTP %d", resp.StatusCode)
				fmt.Fprintf(w, "[ERROR] %v\n", err)
				return result, err
			}
			var shows []map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&shows); err != nil {
				fmt.Fprintf(w, "[ERROR] Failed to parse Sonarr series response: %v\n", err)
				resp.Body.Close()
				return result, err
			}
			resp.Body.Close()

			// Fetch upcoming episodes ONCE for all shows.
			nowISO := time.Now().UTC().Format(time.RFC3339)
			endISO := time.Now().UTC().Add(365 * 24 * time.Hour).Format(time.RFC3339)
			calEp, err := newRequestWithKey("GET",
				cfg.SonarrURL+"/calendar?start="+url.QueryEscape(nowISO)+"&end="+url.QueryEscape(endISO),
				cfg.SonarrAPIKey)
			if err != nil {
				fmt.Fprintf(w, "Sonarr error building calendar request: %v\n", err)
				return result, fmt.Errorf("Sonarr calendar request: %w", err)
			}
			epResp, err := httpClient.Do(calEp)
			var allEpisodes []map[string]interface{}
			if err != nil {
				fmt.Fprintf(w, "Sonarr error fetching calendar: %v\n", err)
				return result, fmt.Errorf("Sonarr calendar fetch: %w", err)
			}
			if epResp.StatusCode != http.StatusOK {
				epResp.Body.Close()
				err := fmt.Errorf("Sonarr calendar fetch returned HTTP %d", epResp.StatusCode)
				fmt.Fprintf(w, "[ERROR] %v\n", err)
				return result, err
			}
			if decErr := json.NewDecoder(epResp.Body).Decode(&allEpisodes); decErr != nil {
				fmt.Fprintf(w, "[ERROR] Failed to parse Sonarr episode response: %v\n", decErr)
				epResp.Body.Close()
				return result, decErr
			}
			epResp.Body.Close()

			now := time.Now().UTC()

			fmt.Fprintf(w, "[Sonarr] %d show(s), %d upcoming episode(s) in next 365 days\n",
				len(shows), len(allEpisodes))
			if cfg.SonarrDayOffset != 0 {
				fmt.Fprintf(w, "[Sonarr] Episode day offset %+d\n", cfg.SonarrDayOffset)
			}

			sonarrSkippedIgnored := 0
			sonarrAddedTotal, sonarrUpdatedTotal, sonarrErrTotal := 0, 0, 0
			var sonarrIgnoredNames []string
			sonarrTargets := calendarTargetsForSource(targets, "sonarr")
			for _, target := range sonarrTargets {

				// Load all existing calendar events once for O(1) episode existence
				// checks instead of calling the Calendar API for every episode.
				existingEvents, err := listCalendarEvents(calSvc, target.ID)
				if err != nil {
					fmt.Fprintf(w, "[ERROR] Failed to load existing Sonarr calendar events for %s: %v\n", target.ID, err)
					return result, fmt.Errorf("load existing Sonarr events for %s: %w", target.ID, err)
				}
				existingEpEvents := indexEventsBySummary(existingEvents)

				progress(fmt.Sprintf("Processing %d shows, %d upcoming episodes...", len(shows), len(allEpisodes)))

				for _, show := range shows {
					title, _ := show["title"].(string)
					if _, ignored := currentIgnored[title]; ignored {
						sonarrSkippedIgnored++
						if len(sonarrIgnoredNames) < 11 {
							sonarrIgnoredNames = append(sonarrIgnoredNames, title)
						}
						continue
					}
					showID := show["id"]

					showUpcoming, showAdded, showUpdated, showSkipped := 0, 0, 0, 0
					for _, ep := range allEpisodes {
						if ep["seriesId"] != showID {
							continue
						}
						airStr, _ := ep["airDateUtc"].(string)
						if airStr == "" {
							continue
						}
						airTime, err := time.Parse("2006-01-02T15:04:05Z", airStr)
						if err != nil || !airTime.After(now) {
							continue
						}
						showUpcoming++
						season := int(toFloat(ep["seasonNumber"]))
						episode := int(toFloat(ep["episodeNumber"]))
						summary := fmtEpisodeTitle(cfg.EpisodeTemplate, title, season, episode)
						// Sonarr provides airDate as the local broadcast date; airDateUtc
						// is UTC and would produce the wrong calendar date for evening
						// airings in timezones behind UTC.
						airDate := ""
						if d, ok := ep["airDate"].(string); ok && d != "" {
							airDate = d
						} else {
							airDate = airTime.Local().Format("2006-01-02")
						}
						airDate = applyDayOffset(airDate, cfg.SonarrDayOffset)

						epOverview, _ := ep["overview"].(string)
						ev := allDayCalendarEvent(summary, epOverview, airDate, target.SonarrColorID)
						existing := existingEpEvents[summary]
						if existing == nil {
							msg := fmt.Sprintf("%s on %s added to calendar%s", summary, airDate, targetLabel(target, multiTarget))
							ok := dryRun
							if !dryRun {
								inserted, e2 := calSvc.Events.Insert(target.ID, ev).Do()
								ok = e2 == nil
								if ok {
									existingEpEvents[summary] = inserted
								} else {
									sonarrErrTotal++
									fmt.Fprintf(w, "[ERROR] Failed adding %s: %v\n", msg, e2)
									return result, e2
								}
							} else {
								existingEpEvents[summary] = ev
							}
							if ok {
								showAdded++
								result.Added = append(result.Added, msg)
							}
						} else if allDayEventNeedsUpdate(existing, airDate, target.SonarrColorID) {
							msg := fmt.Sprintf("%s date changed to %s%s", summary, airDate, targetLabel(target, multiTarget))
							ok := dryRun
							if !dryRun {
								updated, e2 := calSvc.Events.Update(target.ID, existing.Id, ev).Do()
								ok = e2 == nil
								if ok {
									existingEpEvents[summary] = updated
								} else {
									sonarrErrTotal++
									fmt.Fprintf(w, "[ERROR] Failed updating %s: %v\n", msg, e2)
									return result, e2
								}
							} else {
								existingEpEvents[summary] = ev
							}
							if ok {
								showUpdated++
								result.Updated = append(result.Updated, msg)
							}
						} else {
							showSkipped++
						}
					}
					if showUpcoming > 0 {
						fmt.Fprintf(w, "[Sonarr] %q — %d upcoming: %d added, %d updated, %d already on calendar\n",
							title, showUpcoming, showAdded, showUpdated, showSkipped)
						sonarrAddedTotal += showAdded
						sonarrUpdatedTotal += showUpdated
					}
				}
			}
			if len(sonarrTargets) == 0 {
				fmt.Fprintf(w, "[Sonarr] No calendar targets have Sonarr enabled\n")
			} else {
				fmt.Fprintf(w, "[Sonarr] Done — %d show(s) ignored (%s) | %d added, %d updated, %d errors\n",
					sonarrSkippedIgnored, compactNames(sonarrIgnoredNames, 10, sonarrSkippedIgnored),
					sonarrAddedTotal, sonarrUpdatedTotal, sonarrErrTotal)
				fmt.Fprintf(w, "[Sonarr] Phase complete in %s\n", time.Since(sonarrStart).Round(time.Millisecond))
			}
		}
	}

	// Cleanup: remove calendar events for all currently ignored shows.
	// This is the only cleanup pass — there is no separate "newly ignored" pre-pass,
	// which previously caused double-deletions when Google Calendar returned recently
	// deleted events in subsequent List calls.
	if len(currentIgnored) > 0 {
		progress(fmt.Sprintf("Cleaning up %d ignored show(s)...", len(currentIgnored)))
		fmt.Fprintf(w, "[Cleanup] Checking %d ignored show(s) for leftover calendar events\n", len(currentIgnored))
		cleanupTitles := make([]string, 0, len(currentIgnored))
		for t := range currentIgnored {
			cleanupTitles = append(cleanupTitles, t)
		}
		sort.Strings(cleanupTitles)
		cleanShowCount, dirtyShowCount, totalRemoved := 0, 0, 0
		for _, title := range cleanupTitles {
			n, err := deleteShowEvents(title)
			if err != nil {
				return result, err
			}
			totalRemoved += n
			if n > 0 {
				dirtyShowCount++
			} else {
				cleanShowCount++
			}
		}
		if dirtyShowCount > 0 {
			fmt.Fprintf(w, "[Cleanup] Done — %d event(s) removed from %d show(s); %d show(s) already clean\n",
				totalRemoved, dirtyShowCount, cleanShowCount)
		} else {
			fmt.Fprintf(w, "[Cleanup] Done — all %d ignored show(s) already clean\n", cleanShowCount)
		}
	}

	// Persist current ignored list so next run only acts on changes (skip in dry-run).
	if !dryRun {
		savePrevIgnored(currentIgnored)
	}

	return result, nil
}

// toFloat safely converts an interface{} JSON number to float64.
func toFloat(v interface{}) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// ---- Sonarr show list (for Ignored Shows page) ------------------------------

type SonarrShowInfo struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

func fetchSonarrShows(cfg Config) ([]SonarrShowInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", cfg.SonarrURL+"/series", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", cfg.SonarrAPIKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	shows := make([]SonarrShowInfo, 0, len(raw))
	for _, s := range raw {
		title, _ := s["title"].(string)
		status, _ := s["status"].(string)
		if title != "" {
			shows = append(shows, SonarrShowInfo{Title: title, Status: strings.ToLower(status)})
		}
	}
	sort.Slice(shows, func(i, j int) bool {
		return strings.ToLower(shows[i].Title) < strings.ToLower(shows[j].Title)
	})
	return shows, nil
}

// ---- Ignored shows file helpers ---------------------------------------------

func loadIgnoredList() []string {
	cfg, err := loadConfig()
	if err != nil {
		return nil
	}
	set := loadIgnoredShows(dataPath(cfg.IgnoredShowsFile))
	list := make([]string, 0, len(set))
	for s := range set {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i]) < strings.ToLower(list[j])
	})
	return list
}

func saveIgnoredList(shows []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	sort.Slice(shows, func(i, j int) bool {
		return strings.ToLower(shows[i]) < strings.ToLower(shows[j])
	})
	data, err := json.MarshalIndent(shows, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dataPath(sanitizedIgnoredShowsFile(cfg.IgnoredShowsFile)), data, 0644)
}

// compactNames joins names inline up to max, appending "+N more" based on total.
// Pass the real total count separately — callers may cap name collection for memory.
func compactNames(names []string, max, total int) string {
	if total == 0 {
		return "(none)"
	}
	if total <= max {
		return strings.Join(names[:total], ", ")
	}
	n := max
	if n > len(names) {
		n = len(names)
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(", +%d more", total-n)
}

// ---- Episode regex (shared with cleanup) ------------------------------------

var episodeRe = regexp.MustCompile(`\bS\d{2}E\d{2}\b`)
