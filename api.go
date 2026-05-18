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

func jsonStatus(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonStatus(w, status, map[string]interface{}{"ok": false, "message": msg})
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
		jsonError(w, http.StatusConflict, "Preview already in progress.")
		return
	}
	if isRunning() {
		jsonError(w, http.StatusConflict, "A sync is running. Try again once it finishes.")
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
	if err := mutateConfig(func(c *Config) error {
		c.GoogleRefreshToken = ""
		return nil
	}); err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	logEvent("[UI] Google Calendar disconnected")
	jsonOK(w, map[string]interface{}{"ok": true})
}

// ---- /api/auth/password -----------------------------------------------------

func apiSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			return
		}
		jsonError(w, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}
	if len(body.NewPassword) < minPasswordLen {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Password must be at least %d characters.", minPasswordLen))
		return
	}
	if len(body.NewPassword) > maxPasswordLen {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Password must be no more than %d characters.", maxPasswordLen))
		return
	}
	hash, err := hashPassword(body.NewPassword)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var unauthorized bool
	err = mutateConfig(func(c *Config) error {
		if c.WebUIPasswordHash != "" && !checkPassword(c.WebUIPasswordHash, body.OldPassword) {
			unauthorized = true
			return fmt.Errorf("Current password is incorrect.")
		}
		c.WebUIPasswordHash = hash
		return nil
	})
	if err != nil {
		if unauthorized {
			jsonError(w, http.StatusUnauthorized, "Current password is incorrect.")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logEvent("[Auth] Web UI password changed")
	clearSessions()
	createSession(w)
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Password updated."})
}

func apiClearPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		OldPassword string `json:"old_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			return
		}
		jsonError(w, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}
	var unauthorized, alreadyClear, lanBlocked bool
	err := mutateConfig(func(c *Config) error {
		if c.WebUIPasswordHash == "" {
			alreadyClear = true
			return fmt.Errorf("no password set")
		}
		if !checkPassword(c.WebUIPasswordHash, body.OldPassword) {
			unauthorized = true
			return fmt.Errorf("Current password is incorrect.")
		}
		if c.WebBindAddress == "0.0.0.0" {
			lanBlocked = true
			return fmt.Errorf("Cannot remove the password while LAN access is enabled. Switch to Local only first.")
		}
		c.WebUIPasswordHash = ""
		return nil
	})
	if err != nil {
		if alreadyClear {
			jsonOK(w, map[string]interface{}{"ok": true})
			return
		}
		if unauthorized {
			jsonError(w, http.StatusUnauthorized, "Current password is incorrect.")
			return
		}
		if lanBlocked {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logEvent("[Auth] Web UI password removed")
	clearSessions()
	destroySession(r, w)
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Password removed."})
}

// ---- /api/run ---------------------------------------------------------------

func apiRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if !runSyncJob() {
		jsonError(w, http.StatusConflict, "Already running")
		return
	}
	logEvent("[UI] Manual sync triggered")
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Sync started"})
}

// ---- /api/cleanup -----------------------------------------------------------

func apiCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			return
		}
		// Decoding errors here are non-fatal — Mode falls back to "past".
	}
	if body.Mode == "" {
		body.Mode = "past"
	}
	if body.Mode != "past" && body.Mode != "future" && body.Mode != "all" {
		body.Mode = "past"
	}

	cs := getCleanupState()
	if cs.Running {
		jsonError(w, http.StatusConflict, "Cleanup already in progress.")
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
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		CalendarID string   `json:"calendar_id"`
		Mode       string   `json:"mode"`
		Sources    []string `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			return
		}
		adminLog("Target cleanup rejected", err.Error())
		jsonError(w, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}

	cs := getCleanupState()
	if cs.Running {
		jsonError(w, http.StatusConflict, "Cleanup already in progress.")
		return
	}
	setCleanupRunning()
	cfg, _ := loadConfig()
	calendarID := body.CalendarID
	sources := body.Sources
	mode := targetCleanupMode(body.Mode, sources)
	safeGo(func() {
		_, scanned, deleted, err := cleanupTargetCalendar(cfg, calendarID, mode, sources)
		if err != nil {
			finishCleanup(false, scanned, deleted, err.Error())
			return
		}
		finishCleanup(true, scanned, deleted, fmt.Sprintf("Done. Scanned %d events, deleted %d.", scanned, deleted))
	})
	jsonOK(w, map[string]interface{}{"ok": true, "started": true})
}

func targetCleanupMode(mode string, sources []string) string {
	if strings.TrimSpace(mode) == "" && len(sources) > 0 {
		return "all"
	}
	return mode
}

func apiCalendarTargetsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		Targets []CalendarTarget `json:"targets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			return
		}
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	var saved []CalendarTarget
	err := mutateConfig(func(c *Config) error {
		c.CalendarTargets = body.Targets
		normalizeCalendarTargets(c)
		saved = c.CalendarTargets
		return nil
	})
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	jsonOK(w, map[string]interface{}{"ok": true, "targets": saved})
}

func apiSettingsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if err := parseSettingsRequest(r); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Settings payload too large.")
			return
		}
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	var restartRequired bool
	err := mutateConfig(func(c *Config) error {
		oldBind := c.WebBindAddress
		oldPort := c.WebPort
		applySettingsForm(c, r)
		if c.WebBindAddress == "0.0.0.0" && c.WebUIPasswordHash == "" {
			return fmt.Errorf("Set a Web UI password before enabling Local network access.")
		}
		restartRequired = oldBind != c.WebBindAddress || oldPort != c.WebPort
		return nil
	})
	if err != nil {
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	logEvent("[UI] Settings saved")
	msg := "Saved"
	if restartRequired {
		msg = "Saved. Restart the Calendarr service for Web UI access or port changes to take effect."
	}
	jsonOK(w, map[string]interface{}{"ok": true, "message": msg, "restart_required": restartRequired})
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
	go checkForUpdates(updateCheckManual)
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
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		Shows []string `json:"shows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			return
		}
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
			kind := upcomingEventKind(title, e.Description, cfg)
			if kind == "" {
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

func upcomingEventKind(summary, description string, cfg Config) string {
	switch cleanupEventKindForEvent(summary, description, cfg) {
	case "radarr_theater":
		return "theater"
	case "radarr_digital":
		return "digital"
	case "sonarr":
		return "episode"
	case "steam":
		return "steam"
	default:
		return ""
	}
}

// ---- /api/test/* ------------------------------------------------------------

func logSettingsTestStart(service string) {
	logEvent(fmt.Sprintf("[Settings Test] %s start", service))
}

func logSettingsTestSuccess(service string) {
	logEvent(fmt.Sprintf("[Settings Test] %s success", service))
}

func logSettingsTestFailure(service, reason string) {
	logEvent(fmt.Sprintf("[Settings Test] %s failure: %s", service, sanitizeSettingsTestReason(reason)))
}

func sanitizeSettingsTestReason(reason string) string {
	reason = strings.TrimSpace(reason)
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return "unknown error"
	}
	reason = settingsTestURLRe.ReplaceAllString(reason, "[url]")
	reason = redactSecretLikeText(reason)
	if len(reason) > 300 {
		reason = reason[:300] + "..."
	}
	return reason
}

var settingsTestURLRe = regexp.MustCompile(`(?i)\bhttps?://[^\s"']+|\b[a-z0-9.-]+\.[a-z]{2,}(/[^\s"']*)?`)

func settingsTestError(w http.ResponseWriter, service string, status int, msg string) {
	logSettingsTestFailure(service, msg)
	jsonError(w, status, msg)
}

func apiTestRadarr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	const service = "Radarr"
	logSettingsTestStart(service)
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		URL    string `json:"radarr_url"`
		APIKey string `json:"radarr_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			logSettingsTestFailure(service, "Request body too large.")
			return
		}
		settingsTestError(w, service, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}
	if body.URL == "" || body.APIKey == "" {
		settingsTestError(w, service, http.StatusBadRequest, "URL and API Key are required.")
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := newRequestWithKey("GET", body.URL+"/system/status", body.APIKey)
	if err != nil {
		settingsTestError(w, service, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		settingsTestError(w, service, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		settingsTestError(w, service, http.StatusBadGateway, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return
	}
	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		settingsTestError(w, service, http.StatusBadGateway, "Could not read Radarr response.")
		return
	}
	ver, _ := info["version"].(string)
	logSettingsTestSuccess(service)
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Connected! Radarr v" + ver})
}

func apiTestSonarr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	const service = "Sonarr"
	logSettingsTestStart(service)
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		URL    string `json:"sonarr_url"`
		APIKey string `json:"sonarr_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			logSettingsTestFailure(service, "Request body too large.")
			return
		}
		settingsTestError(w, service, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}
	if body.URL == "" || body.APIKey == "" {
		settingsTestError(w, service, http.StatusBadRequest, "URL and API Key are required.")
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := newRequestWithKey("GET", body.URL+"/system/status", body.APIKey)
	if err != nil {
		settingsTestError(w, service, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		settingsTestError(w, service, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		settingsTestError(w, service, http.StatusBadGateway, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return
	}
	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		settingsTestError(w, service, http.StatusBadGateway, "Could not read Sonarr response.")
		return
	}
	ver, _ := info["version"].(string)
	logSettingsTestSuccess(service)
	jsonOK(w, map[string]interface{}{"ok": true, "message": "Connected! Sonarr v" + ver})
}

func apiTestPushover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	const service = "Pushover"
	logSettingsTestStart(service)
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		Token string `json:"pushover_app_token"`
		User  string `json:"pushover_user_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			logSettingsTestFailure(service, "Request body too large.")
			return
		}
		settingsTestError(w, service, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}
	if body.Token == "" || body.User == "" {
		settingsTestError(w, service, http.StatusBadRequest, "App Token and User Key are required.")
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm("https://api.pushover.net/1/users/validate.json", url.Values{
		"token": {body.Token},
		"user":  {body.User},
	})
	if err != nil {
		settingsTestError(w, service, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		settingsTestError(w, service, http.StatusBadGateway, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		settingsTestError(w, service, http.StatusBadGateway, "Could not read Pushover response.")
		return
	}
	if status, _ := result["status"].(float64); status == 1 {
		logSettingsTestSuccess(service)
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
	settingsTestError(w, service, http.StatusBadGateway, msg)
}

func apiTestSteam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	const service = "Steam"
	logSettingsTestStart(service)
	limitBody(w, r, maxJSONBodyBytes)
	var body struct {
		SteamID string `json:"steam_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isBodyTooLarge(err) {
			writeTooLarge(w, "Request body too large.")
			logSettingsTestFailure(service, "Request body too large.")
			return
		}
		settingsTestError(w, service, http.StatusBadRequest, "Malformed JSON payload.")
		return
	}
	if strings.TrimSpace(body.SteamID) == "" {
		settingsTestError(w, service, http.StatusBadRequest, "Steam ID is required.")
		return
	}
	testCfg := Config{
		SteamID: body.SteamID,
	}
	var buf strings.Builder
	err := checkSteamConnectivity(testCfg, &buf)
	output := buf.String()
	if err != nil {
		logSettingsTestFailure(service, err.Error())
		jsonError(w, http.StatusBadGateway, output+"\nError: "+err.Error())
		return
	}
	logSettingsTestSuccess(service)
	jsonOK(w, map[string]interface{}{"ok": true, "message": output})
}

func apiTestCalendar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	const service = "Calendar"
	logSettingsTestStart(service)
	cfg, _ := loadConfig()
	calSvc, err := getCalService(cfg)
	if err != nil {
		logSettingsTestFailure(service, err.Error())
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	targets := calendarTargets(cfg)
	cal, err := calSvc.Calendars.Get(targets[0].ID).Do()
	if err != nil {
		logSettingsTestFailure(service, err.Error())
		jsonOK(w, map[string]interface{}{"ok": false, "message": err.Error()})
		return
	}
	name := cal.Summary
	if name == "" {
		name = cal.Id
	}
	logSettingsTestSuccess(service)
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
