package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// jsonOK writes a JSON response with Content-Type set.
func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ---- /api/status ------------------------------------------------------------

func apiStatus(w http.ResponseWriter, r *http.Request) {
	s := getAppState()
	var lastRun, nextRun interface{}
	if s.LastRun != nil {
		lastRun = s.LastRun.Format(time.RFC3339)
	}
	if s.NextRun != nil {
		nextRun = s.NextRun.Format(time.RFC3339)
	}
	changes := s.LastRunChanges
	if changes == nil {
		changes = []RunChange{}
	}
	jsonOK(w, map[string]interface{}{
		"is_running":       s.IsRunning,
		"last_run":         lastRun,
		"last_run_status":  s.LastRunStatus,
		"last_run_stats":   map[string]int{"added": s.LastRunStats.Added, "updated": s.LastRunStats.Updated, "deleted": s.LastRunStats.Deleted},
		"last_run_changes": changes,
		"next_run":         nextRun,
		"sync_progress":    s.SyncProgress,
	})
}

// ---- /api/preview -----------------------------------------------------------

func apiPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	s := getPreviewState()
	if s.Running {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "Preview already in progress."})
		return
	}
	if isRunning() {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "A sync is running. Try again once it finishes."})
		return
	}
	cfg, _ := loadConfig()
	setPreviewRunning()
	go func() {
		var buf strings.Builder
		result, err := runSync(cfg, &buf, true)
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		if cfg.AutoCleanupPast && errMsg == "" {
			result.Deleted = append(result.Deleted, simulateCleanup(cfg)...)
		}
		finishPreview(&result, errMsg)
	}()
	jsonOK(w, map[string]interface{}{"ok": true, "started": true})
}

func apiPreviewStatus(w http.ResponseWriter, r *http.Request) {
	s := getPreviewState()
	resp := map[string]interface{}{
		"running":  s.Running,
		"done":     s.Done,
		"progress": s.Progress,
		"error":    s.Error,
	}
	if s.Done && s.Result != nil {
		resp["added"] = s.Result.Added
		resp["updated"] = s.Result.Updated
		resp["deleted"] = s.Result.Deleted
	}
	jsonOK(w, resp)
}

// ---- /api/auth/google -------------------------------------------------------

func apiGoogleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	jsonOK(w, map[string]interface{}{
		"connected": cfg.GoogleRefreshToken != "",
	})
}

func apiGoogleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	cfg, _ := loadConfig()
	cfg.GoogleRefreshToken = ""
	if err := saveConfig(cfg); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	logEvent("[UI] Google Calendar disconnected")
	jsonOK(w, map[string]interface{}{"ok": true})
}

// ---- /api/run ---------------------------------------------------------------

func apiRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if isRunning() {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "Already running"})
		return
	}
	logEvent("[UI] Manual sync triggered")
	go runSyncJob()
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Sync started"})
}

// ---- /api/cleanup -----------------------------------------------------------

func apiCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Mode == "" {
		body.Mode = "past"
	}
	if body.Mode != "past" && body.Mode != "future" && body.Mode != "all" {
		body.Mode = "past"
	}

	cs := getCleanupState()
	if cs.Running {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "Cleanup already in progress."})
		return
	}
	setCleanupRunning()
	cfg, _ := loadConfig()
	go func() {
		_, _ = runCleanup(cfg, body.Mode)
	}()
	jsonOK(w, map[string]interface{}{"ok": true, "started": true})
}

func apiCleanupTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		CalendarID string   `json:"calendar_id"`
		Mode       string   `json:"mode"`
		Sources    []string `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		adminLog("Target cleanup rejected", err.Error())
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	cfg, _ := loadConfig()
	deletedMessages, scanned, deleted, err := cleanupTargetCalendar(cfg, body.CalendarID, body.Mode, body.Sources)
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error(), "scanned": scanned, "deleted": deleted})
		return
	}
	jsonOK(w, map[string]interface{}{
		"ok":      true,
		"scanned": scanned,
		"deleted": deleted,
		"events":  deletedMessages,
	})
}

func apiCalendarTargetsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Targets []CalendarTarget `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	cfg.CalendarTargets = body.Targets
	if err := saveConfig(cfg); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "targets": cfg.CalendarTargets})
}

func apiSettingsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	if err := parseSettingsRequest(r); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	applySettingsForm(&cfg, r)
	if err := saveConfig(cfg); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	logEvent("[UI] Settings saved")
	jsonOK(w, map[string]interface{}{"ok": true})
}

func apiCleanupStatus(w http.ResponseWriter, r *http.Request) {
	cs := getCleanupState()
	var okVal interface{}
	if cs.Ok != nil {
		okVal = *cs.Ok
	}
	jsonOK(w, map[string]interface{}{
		"running": cs.Running,
		"done":    cs.Done,
		"ok":      okVal,
		"deleted": cs.Deleted,
		"scanned": cs.Scanned,
		"message": cs.Message,
	})
}

// ---- /api/restart -----------------------------------------------------------

func apiRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	adminLog("Service restart initiated", serviceName)
	bat := dataPath("_restart.bat")
	content := fmt.Sprintf("@echo off\r\ntimeout /t 5 /nobreak >nul\r\nnet stop \"%s\"\r\ntimeout /t 3 /nobreak >nul\r\nnet start \"%s\"\r\ndel /f /q \"%%~f0\"\r\n", serviceName, serviceName)
	if err := os.WriteFile(bat, []byte(content), 0644); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": fmt.Sprintf("Failed to write restart script: %v", err)})
		return
	}
	if err := startDetached("cmd", "/c", "start", "", "/min", bat); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": fmt.Sprintf("Restart failed: %v", err)})
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Restart initiated."})
}

// ---- /api/stop --------------------------------------------------------------

func apiStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	adminLog("Service stop initiated via tray", "")
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Service stopping..."})
	go func() {
		time.Sleep(500 * time.Millisecond) // let the response send first
		os.Exit(0)
	}()
}

// ---- /api/update/* ----------------------------------------------------------

func apiUpdateStatus(w http.ResponseWriter, r *http.Request) {
	s := getUpdateState()
	lastChecked := ""
	if !s.LastChecked.IsZero() {
		lastChecked = s.LastChecked.Format("2006-01-02 15:04:05")
	}
	jsonOK(w, map[string]interface{}{
		"available":    s.Available,
		"latest_ver":   s.LatestVer,
		"release_url":  s.ReleaseURL,
		"checking":     s.Checking,
		"last_checked": lastChecked,
		"error":        s.Error,
	})
}

func apiUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	go checkForUpdates()
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Check started"})
}

func apiUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if err := downloadUpdate(); err != nil {
		logEvent("[Updater] Apply failed: " + err.Error())
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Update downloading. The service will restart automatically in a few seconds."})
}

// ---- /api/ignored/save ------------------------------------------------------

func apiIgnoredSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Shows []string `json:"shows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	if err := saveIgnoredList(body.Shows); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	logEvent(fmt.Sprintf("[UI] Ignored shows updated (%d ignored)", len(body.Shows)))
	jsonOK(w, map[string]interface{}{"ok": true})
}

// ---- /api/sonarr-shows ------------------------------------------------------

func apiSonarrShows(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	shows, err := fetchSonarrShows(cfg)
	if err != nil {
		jsonOK(w, map[string]interface{}{"shows": []SonarrShowInfo{}, "error": err.Error()})
		return
	}
	jsonOK(w, map[string]interface{}{"shows": shows, "error": nil})
}

// ---- /api/upcoming ----------------------------------------------------------

func apiUpcoming(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	targets := calendarTargets(cfg)
	calSvc, err := getCalService(cfg)
	if err != nil {
		jsonOK(w, map[string]interface{}{"events": []interface{}{}, "error": err.Error()})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	end := time.Now().UTC().Add(120 * 24 * time.Hour).Format(time.RFC3339)
	epRe := regexp.MustCompile(`\bS\d{2}E\d{2}\b`)
	type event struct {
		Title    string `json:"title"`
		Date     string `json:"date"`
		Kind     string `json:"kind"`
		Calendar string `json:"calendar"`
	}
	var events []event
	for _, target := range targets {
		res, err := calSvc.Events.List(target.ID).
			TimeMin(now).TimeMax(end).
			SingleEvents(true).OrderBy("startTime").MaxResults(500).Do()
		if err != nil {
			jsonOK(w, map[string]interface{}{"events": []interface{}{}, "error": err.Error()})
			return
		}
		calName := target.Name
		if calName == "" {
			calName = target.ID
		}
		for _, e := range res.Items {
			title := e.Summary
			date := ""
			if e.Start != nil {
				date = e.Start.Date
			}
			var kind string
			switch {
			case strings.HasSuffix(title, "Theater Release"):
				kind = "theater"
			case strings.HasSuffix(title, "Digital Release"):
				kind = "digital"
			case epRe.MatchString(title):
				kind = "episode"
			default:
				continue
			}
			events = append(events, event{Title: title, Date: date, Kind: kind, Calendar: calName})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Date != events[j].Date {
			return events[i].Date < events[j].Date
		}
		return events[i].Title < events[j].Title
	})
	if events == nil {
		events = []event{}
	}
	jsonOK(w, map[string]interface{}{"events": events, "error": nil})
}

// ---- /api/test/* ------------------------------------------------------------

func apiTestRadarr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		URL    string `json:"radarr_url"`
		APIKey string `json:"radarr_api_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.URL == "" || body.APIKey == "" {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "URL and API Key are required."})
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", body.URL+"/system/status", nil)
	req.Header.Set("X-Api-Key", body.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		jsonOK(w, map[string]interface{}{"ok": false, "message": fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}
	var info map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&info)
	ver, _ := info["version"].(string)
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Connected! Radarr v" + ver})
}

func apiTestSonarr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		URL    string `json:"sonarr_url"`
		APIKey string `json:"sonarr_api_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.URL == "" || body.APIKey == "" {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "URL and API Key are required."})
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", body.URL+"/system/status", nil)
	req.Header.Set("X-Api-Key", body.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		jsonOK(w, map[string]interface{}{"ok": false, "message": fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}
	var info map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&info)
	ver, _ := info["version"].(string)
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Connected! Sonarr v" + ver})
}

func apiTestPushover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Token string `json:"pushover_app_token"`
		User  string `json:"pushover_user_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Token == "" || body.User == "" {
		jsonOK(w, map[string]interface{}{"ok": false, "message": "App Token and User Key are required."})
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm("https://api.pushover.net/1/users/validate.json", url.Values{
		"token": {body.Token},
		"user":  {body.User},
	})
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if status, _ := result["status"].(float64); status == 1 {
		jsonOK(w, map[string]interface{}{"ok": true, "message": "Credentials valid!"})
		return
	}
	msg := "Invalid credentials"
	if errs, ok := result["errors"].([]interface{}); ok && len(errs) > 0 {
		parts := make([]string, len(errs))
		for i, e := range errs {
			parts[i] = fmt.Sprint(e)
		}
		msg = strings.Join(parts, ", ")
	}
	jsonOK(w, map[string]interface{}{"ok": false, "message": msg})
}

func apiTestCalendar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	cfg, _ := loadConfig()
	calSvc, err := getCalService(cfg)
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	targets := calendarTargets(cfg)
	cal, err := calSvc.Calendars.Get(targets[0].ID).Do()
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	name := cal.Summary
	if name == "" {
		name = cal.Id
	}
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Connected! Calendar: " + name})
}

// ---- /api/calendars ---------------------------------------------------------

func apiCalendars(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	calSvc, err := getCalService(cfg)
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "calendars": []interface{}{}, "error": err.Error()})
		return
	}
	list, err := calSvc.CalendarList.List().Do()
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "calendars": []interface{}{}, "error": err.Error()})
		return
	}
	type calItem struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Primary bool   `json:"primary"`
	}
	var cals []calItem
	for _, c := range list.Items {
		cals = append(cals, calItem{ID: c.Id, Name: c.Summary, Primary: c.Primary})
	}
	sort.Slice(cals, func(i, j int) bool {
		if cals[i].Primary != cals[j].Primary {
			return cals[i].Primary
		}
		return cals[i].Name < cals[j].Name
	})
	if cals == nil {
		cals = []calItem{}
	}
	jsonOK(w, map[string]interface{}{"ok": true, "calendars": cals})
}
