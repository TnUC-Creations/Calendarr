package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed favicon.ico
var faviconData []byte

//go:embed assets/sidebar-logo.png
var sidebarLogoData []byte

//go:embed assets/sidebar-logo-v2.png
var sidebarLogoV2Data []byte

//go:embed assets/about-banner-dark.png
var aboutBannerDarkData []byte

//go:embed assets/about-banner-light.png
var aboutBannerLightData []byte

// ---- Flash messages (cookie-based) ------------------------------------------

type FlashMsg struct {
	Category string // "success", "danger", "warning"
	Message  string
}

const flashCookie = "calendarr_flash"

func setFlash(w http.ResponseWriter, category, message string) {
	val := url.QueryEscape(category + "|" + message)
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    val,
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
	})
}

func getFlash(r *http.Request, w http.ResponseWriter) *FlashMsg {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	// Clear immediately so it shows only once.
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1})
	v, err := url.QueryUnescape(c.Value)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(v, "|", 2)
	if len(parts) != 2 {
		return nil
	}
	return &FlashMsg{Category: parts[0], Message: parts[1]}
}

// ---- Template rendering -----------------------------------------------------

// PageBase is embedded in every page data struct.
type PageBase struct {
	AppName           string
	AppVersion        string
	CurrentPage       string
	Flash             *FlashMsg
	CSRFToken         string
	UpdateAvailable   bool
	UpdateVersion     string
	CalendarConnected bool
	AuthEnabled       bool
}

func newBase(page string, r *http.Request, w http.ResponseWriter) PageBase {
	u := getUpdateState()
	cfg, _ := loadConfig()
	return PageBase{
		AppName:           appName,
		AppVersion:        appVersion,
		CurrentPage:       page,
		Flash:             getFlash(r, w),
		CSRFToken:         csrfToken,
		UpdateAvailable:   u.Available,
		UpdateVersion:     u.LatestVer,
		CalendarConnected: cfg.GoogleRefreshToken != "",
		AuthEnabled:       cfg.WebUIPasswordHash != "",
	}
}

func render(w http.ResponseWriter, name string, data interface{}) {
	t, ok := pageTemplates[name]
	if !ok {
		http.Error(w, "template not found: "+name, 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// ---- Page data structs -------------------------------------------------------

type DashboardData struct {
	PageBase
	IsRunning      bool
	LastRunStr     string
	NextRunStr     string
	LastRunStatus  string
	StatusDotClass string
	LastRunChanges []RunChange
	Added          int
	Updated        int
	Deleted        int
	SyncProgress   string
	Config         Config
	TheaterSuffix  string
	DigitalSuffix  string
}

type HistoryData struct {
	PageBase
	History []HistoryEntry
}

type SettingsData struct {
	PageBase
	Config Config
}

type IgnoredData struct {
	PageBase
	Shows []string
}

type LogFile struct {
	Name    string // "sync-2026-04-29.log"
	Display string // "2026-04-29"
}

type LogsData struct {
	PageBase
	LogContent   string
	LogFiles     []LogFile
	SelectedFile string
}

type LoginData struct {
	AppVersion string
	SetupMode  bool
	Error      string
	CSRFToken  string
}

type AboutDeps struct {
	GoAPI  string
	OAuth2 string
}

type AboutData struct {
	PageBase
	AppAuthor    string
	AppCreated   string
	GoVersion    string
	PlatformInfo string
	UptimeStr    string
	Deps         AboutDeps
	Config       Config
	Update       UpdateState
}

// ---- Helpers ----------------------------------------------------------------

func fmtTime(t *time.Time, fallback string) string {
	if t == nil {
		return fallback
	}
	return t.Format("2006-01-02 15:04:05")
}

func statusDotClass(status string) string {
	if strings.Contains(status, "Error") {
		return "dot-error"
	}
	if status == "Never run" {
		return "dot-never"
	}
	return "dot-success"
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d day(s), %d hour(s), %d minute(s)", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d hour(s), %d minute(s)", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d minute(s)", minutes)
	}
	return "Just started"
}

func depVersion(path string) string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, dep := range bi.Deps {
		if dep.Path == path {
			return dep.Version
		}
	}
	return "unknown"
}

// ---- Log file helpers -------------------------------------------------------

// listLogFiles returns all daily log files in logsDir, newest first.
func listLogFiles() []LogFile {
	entries, err := os.ReadDir(dataPath(logsDir))
	if err != nil {
		return nil
	}
	var files []LogFile
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "sync-") && strings.HasSuffix(n, ".log") {
			display := strings.TrimSuffix(strings.TrimPrefix(n, "sync-"), ".log")
			files = append(files, LogFile{Name: n, Display: display})
		}
	}
	// Sort newest first (date-based names → reverse alphabetical).
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name > files[j].Name
	})
	return files
}

// ---- Page handlers ----------------------------------------------------------

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s := getAppState()
	cfg, _ := loadConfig()
	data := DashboardData{
		PageBase:       newBase("dashboard", r, w),
		IsRunning:      s.IsRunning,
		LastRunStr:     fmtTime(s.LastRun, "—"),
		NextRunStr:     fmtTime(s.NextRun, "Starting soon..."),
		LastRunStatus:  s.LastRunStatus,
		StatusDotClass: statusDotClass(s.LastRunStatus),
		LastRunChanges: s.LastRunChanges,
		Added:          s.LastRunStats.Added,
		Updated:        s.LastRunStats.Updated,
		Deleted:        s.LastRunStats.Deleted,
		SyncProgress:   s.SyncProgress,
		Config:         cfg,
		TheaterSuffix:  templateSuffix(cfg.MovieTheaterTemplate),
		DigitalSuffix:  templateSuffix(cfg.MovieDigitalTemplate),
	}
	render(w, "dashboard", data)
}

func handleUpcoming(w http.ResponseWriter, r *http.Request) {
	render(w, "upcoming", struct{ PageBase }{newBase("upcoming", r, w)})
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	h := loadHistory()
	// Reverse for newest-first.
	for i, j := 0, len(h)-1; i < j; i, j = i+1, j-1 {
		h[i], h[j] = h[j], h[i]
	}
	render(w, "history", HistoryData{
		PageBase: newBase("history", r, w),
		History:  h,
	})
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := parseSettingsRequest(r); err != nil {
			setFlash(w, "danger", "Settings could not be saved: "+err.Error())
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		var bindBlocked bool
		err := mutateConfig(func(c *Config) error {
			applySettingsForm(c, r)
			if c.WebBindAddress == "0.0.0.0" && c.WebUIPasswordHash == "" {
				bindBlocked = true
				return fmt.Errorf("Set a Web UI password before enabling Local network access.")
			}
			return nil
		})
		if err != nil {
			if bindBlocked {
				setFlash(w, "danger", err.Error())
			} else {
				setFlash(w, "danger", "Settings could not be saved: "+err.Error())
			}
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		logEvent("[UI] Settings saved")
		setFlash(w, "success", "Settings saved!")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	cfg, _ := loadConfig()
	render(w, "settings", SettingsData{
		PageBase: newBase("settings", r, w),
		Config:   cfg,
	})
}

func parseSettingsRequest(r *http.Request) error {
	// Settings forms carry small text fields; cap the body so a runaway upload
	// cannot exhaust memory before parsing rejects it.
	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		return r.ParseMultipartForm(maxFormBodyBytes)
	}
	return r.ParseForm()
}

func applySettingsForm(cfg *Config, r *http.Request) {
	cfg.UseRadarr = r.FormValue("use_radarr") != ""
	cfg.UseSonarr = r.FormValue("use_sonarr") != ""
	cfg.UsePushover = r.FormValue("use_pushover") != ""
	cfg.PushoverSound = r.FormValue("pushover_sound")
	cfg.PushoverOnAdded = r.FormValue("pushover_on_added") != ""
	cfg.PushoverOnUpdated = r.FormValue("pushover_on_updated") != ""
	cfg.PushoverOnDeleted = r.FormValue("pushover_on_deleted") != ""
	cfg.PushoverOnError = r.FormValue("pushover_on_error") != ""
	cfg.PushoverOnUpdate = r.FormValue("pushover_on_update_available") != ""
	cfg.SyncOnStart = r.FormValue("sync_on_start") != ""
	cfg.AutoCleanupPast = r.FormValue("auto_cleanup_past") != ""
	cfg.WebBindAddress = normalizeWebBindAddress(r.FormValue("web_bind_address"))
	cfg.RadarrURL = strings.TrimSpace(r.FormValue("radarr_url"))
	cfg.RadarrAPIKey = strings.TrimSpace(r.FormValue("radarr_api_key"))
	cfg.SonarrURL = strings.TrimSpace(r.FormValue("sonarr_url"))
	cfg.SonarrAPIKey = strings.TrimSpace(r.FormValue("sonarr_api_key"))
	cfg.RadarrTrackTheater = r.FormValue("radarr_track_theater") != ""
	cfg.RadarrTrackDigital = r.FormValue("radarr_track_digital") != ""
	cfg.PushoverToken = strings.TrimSpace(r.FormValue("pushover_app_token"))
	cfg.PushoverUser = strings.TrimSpace(r.FormValue("pushover_user_key"))
	cfg.ServiceName = strings.TrimSpace(r.FormValue("service_name"))
	if formHasPrefix(r, "calendar_target_id_") {
		cfg.CalendarTargets = cfg.CalendarTargets[:0]
		for i := 0; i < maxCalendarTargets; i++ {
			id := strings.TrimSpace(r.FormValue(fmt.Sprintf("calendar_target_id_%d", i)))
			if id == "" {
				continue
			}
			cfg.CalendarTargets = append(cfg.CalendarTargets, CalendarTarget{
				ID:            id,
				Name:          strings.TrimSpace(r.FormValue(fmt.Sprintf("calendar_target_name_%d", i))),
				RadarrEnabled: r.FormValue(fmt.Sprintf("calendar_target_radarr_%d", i)) != "",
				SonarrEnabled: r.FormValue(fmt.Sprintf("calendar_target_sonarr_%d", i)) != "",
				RadarrColorID: strings.TrimSpace(r.FormValue(fmt.Sprintf("calendar_target_radarr_color_%d", i))),
				SonarrColorID: strings.TrimSpace(r.FormValue(fmt.Sprintf("calendar_target_sonarr_color_%d", i))),
			})
		}
	}
	cfg.MovieTheaterTemplate = strings.TrimSpace(r.FormValue("movie_theater_template"))
	cfg.MovieDigitalTemplate = strings.TrimSpace(r.FormValue("movie_digital_template"))
	cfg.EpisodeTemplate = strings.TrimSpace(r.FormValue("episode_template"))
	fmt.Sscanf(r.FormValue("run_interval_hours"), "%f", &cfg.RunIntervalHours)
	fmt.Sscanf(r.FormValue("web_port"), "%d", &cfg.WebPort)
	fmt.Sscanf(r.FormValue("radarr_theater_day_offset"), "%d", &cfg.RadarrTheaterDayOffset)
	fmt.Sscanf(r.FormValue("radarr_digital_day_offset"), "%d", &cfg.RadarrDigitalDayOffset)
	fmt.Sscanf(r.FormValue("sonarr_day_offset"), "%d", &cfg.SonarrDayOffset)
	fmt.Sscanf(r.FormValue("max_log_files"), "%d", &cfg.MaxLogFiles)
	fmt.Sscanf(r.FormValue("max_history_entries"), "%d", &cfg.MaxHistoryEntries)
	if cfg.RunIntervalHours <= 0 {
		cfg.RunIntervalHours = 6
	}
	if cfg.WebPort <= 0 {
		cfg.WebPort = 5000
	}
	if cfg.MaxLogFiles <= 0 {
		cfg.MaxLogFiles = 30
	}
	if cfg.MaxHistoryEntries <= 0 {
		cfg.MaxHistoryEntries = 2000
	}
}

func formHasPrefix(r *http.Request, prefix string) bool {
	for key := range r.Form {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	if r.MultipartForm != nil {
		for key := range r.MultipartForm.Value {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
	}
	return false
}

func handleIgnored(w http.ResponseWriter, r *http.Request) {
	render(w, "ignored", IgnoredData{
		PageBase: newBase("ignored", r, w),
		Shows:    loadIgnoredList(),
	})
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	allFiles := listLogFiles()

	// Determine which file to display (default: most recent).
	selectedFile := filepath.Base(r.URL.Query().Get("file"))
	if selectedFile == "." || selectedFile == "" {
		if len(allFiles) > 0 {
			selectedFile = allFiles[0].Name
		}
	}

	logContent := ""
	if selectedFile != "" {
		path := filepath.Join(dataPath(logsDir), selectedFile)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			sep := "\n" + separator()
			var blocks []string
			for _, b := range strings.Split(content, sep) {
				b = strings.TrimSpace(b)
				if b != "" {
					blocks = append(blocks, b)
				}
			}
			// Reverse to show newest first; cap at 50 blocks.
			if len(blocks) > 50 {
				blocks = blocks[len(blocks)-50:]
			}
			for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
				blocks[i], blocks[j] = blocks[j], blocks[i]
			}
			logContent = strings.Join(blocks, "\n"+separator()+"\n")
		}
	}

	render(w, "logs", LogsData{
		PageBase:     newBase("logs", r, w),
		LogContent:   logContent,
		LogFiles:     allFiles,
		SelectedFile: selectedFile,
	})
}

func handleLogsDownload(w http.ResponseWriter, r *http.Request) {
	file := filepath.Base(r.URL.Query().Get("file"))
	if file == "" || file == "." {
		file = filepath.Base(currentLogFile())
	}
	path := filepath.Join(dataPath(logsDir), file)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		http.Error(w, "Log file not found.", 404)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Content-Type", "text/plain")
	http.ServeFile(w, r, path)
}

func handleLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/logs", http.StatusSeeOther)
		return
	}
	file := filepath.Base(r.FormValue("file"))
	if file == "" || file == "." {
		file = filepath.Base(currentLogFile())
	}
	path := filepath.Join(dataPath(logsDir), file)
	ts := time.Now().Format("2006-01-02 15:04:05")
	appendHistory([]HistoryEntry{{Timestamp: ts, Action: "system", Message: "[System] Sync log was cleared by user"}})
	content := fmt.Sprintf("%s\n%s [System] Log file cleared\n", separator(), ts)
	_ = os.WriteFile(path, []byte(content), 0644)
	setFlash(w, "success", "Log file cleared.")
	http.Redirect(w, r, "/logs?file="+url.QueryEscape(file), http.StatusSeeOther)
}

func handleAbout(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	render(w, "about", AboutData{
		PageBase:     newBase("about", r, w),
		AppAuthor:    appAuthor,
		AppCreated:   appCreated,
		GoVersion:    runtime.Version(),
		PlatformInfo: runtime.GOOS + "/" + runtime.GOARCH,
		UptimeStr:    formatUptime(time.Since(startTime)),
		Deps: AboutDeps{
			GoAPI:  depVersion("google.golang.org/api"),
			OAuth2: depVersion("golang.org/x/oauth2"),
		},
		Config: cfg,
		Update: getUpdateState(),
	})
}

func handleRunNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !runSyncJob() {
		setFlash(w, "warning", "A sync is already in progress.")
	} else {
		setFlash(w, "success", "Sync started!")
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleBackup(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	ignoredFile := sanitizedIgnoredShowsFile(cfg.IgnoredShowsFile)

	zipName := "calendarr-backup-" + time.Now().Format("2006-01-02") + ".zip"

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	configData, err := os.ReadFile(dataPath(configFile))
	if err != nil {
		http.Error(w, "Could not read config file.", http.StatusInternalServerError)
		return
	}
	cfgEntry, _ := zw.Create("config.json")
	cfgEntry.Write(configData)

	ignoredData, err := os.ReadFile(dataPath(ignoredFile))
	if err != nil {
		ignoredData = []byte("[]")
	}
	ignoredEntry, _ := zw.Create("ignored_shows.json")
	ignoredEntry.Write(ignoredData)

	zw.Close()

	logEvent("[UI] Settings backup downloaded")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+zipName+`"`)
	w.Write(buf.Bytes())
}

func handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	limitBody(w, r, maxRestoreBodyBytes)
	if err := r.ParseMultipartForm(maxRestoreBodyBytes); err != nil {
		if isBodyTooLarge(err) {
			setFlash(w, "danger", "Backup file is too large (5 MB max).")
		} else {
			setFlash(w, "danger", "Could not read uploaded file: "+err.Error())
		}
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	file, _, err := r.FormFile("config_file")
	if err != nil {
		setFlash(w, "danger", "No file selected.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	defer file.Close()

	rawBytes, err := io.ReadAll(file)
	if err != nil {
		setFlash(w, "danger", "Could not read uploaded file.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(rawBytes), int64(len(rawBytes)))
	if err != nil {
		setFlash(w, "danger", "Invalid backup file. Please upload a .zip backup.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	var configBytes []byte
	var ignoredBytes []byte
	for _, f := range zr.File {
		switch f.Name {
		case "config.json":
			rc, err := f.Open()
			if err != nil {
				setFlash(w, "danger", "Could not read config.json from backup.")
				http.Redirect(w, r, "/settings", http.StatusSeeOther)
				return
			}
			configBytes, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				setFlash(w, "danger", "Could not read config.json from backup.")
				http.Redirect(w, r, "/settings", http.StatusSeeOther)
				return
			}
		case "ignored_shows.json":
			rc, err := f.Open()
			if err == nil {
				ignoredBytes, _ = io.ReadAll(rc)
				rc.Close()
			}
		}
	}

	if configBytes == nil {
		setFlash(w, "danger", "Backup does not contain config.json.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		setFlash(w, "danger", "Invalid config.json in backup.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	normalizeLoadedConfig(&cfg)
	hasCalendar := strings.TrimSpace(cfg.CalendarID) != "" || len(cfg.CalendarTargets) > 0
	if cfg.RadarrAPIKey == "" && cfg.SonarrAPIKey == "" && !hasCalendar {
		setFlash(w, "danger", "Invalid backup — missing required fields.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if cfg.WebBindAddress == "0.0.0.0" && strings.TrimSpace(cfg.WebUIPasswordHash) == "" {
		setFlash(w, "danger", "Backup would enable LAN access without a Web UI password — refusing to restore.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	if ignoredBytes != nil {
		var arr []string
		if err := json.Unmarshal(ignoredBytes, &arr); err != nil {
			setFlash(w, "danger", "Backup contains an invalid ignored_shows.json.")
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		safe, err := json.MarshalIndent(arr, "", "  ")
		if err != nil {
			setFlash(w, "danger", "Could not encode ignored shows from backup: "+err.Error())
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
		if err := os.WriteFile(dataPath(cfg.IgnoredShowsFile), safe, 0644); err != nil {
			setFlash(w, "danger", "Could not write ignored shows from backup: "+err.Error())
			http.Redirect(w, r, "/settings", http.StatusSeeOther)
			return
		}
	}

	if err := saveConfig(cfg); err != nil {
		setFlash(w, "danger", "Could not save restored config: "+err.Error())
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	logEvent("[UI] Settings restored from backup")
	setFlash(w, "success", "Settings restored successfully!")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/history", http.StatusSeeOther)
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	if f, err := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		fmt.Fprintf(f, "\n%s [System] History log was cleared by user\n", ts)
		f.Close()
	}
	_ = saveHistory([]HistoryEntry{{Timestamp: ts, Action: "system", Message: "[System] History was cleared"}})
	setFlash(w, "success", "History cleared.")
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}

// renderLogin renders the standalone login template. setupMode is true when no
// password is configured yet (first-time setup flow on a LAN-bound install).
func renderLogin(w http.ResponseWriter, setupMode bool, errMsg string) {
	t, ok := pageTemplates["login"]
	if !ok {
		http.Error(w, "template not found: login", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "login", LoginData{
		AppVersion: appVersion,
		SetupMode:  setupMode,
		Error:      errMsg,
		CSRFToken:  csrfToken,
	}); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig()
	setupMode := cfg.WebUIPasswordHash == ""

	if r.Method != http.MethodPost {
		// Already logged in — bounce to dashboard.
		if !setupMode && sessionValid(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		renderLogin(w, setupMode, "")
		return
	}

	if err := r.ParseForm(); err != nil {
		renderLogin(w, setupMode, "Could not read form.")
		return
	}

	if setupMode {
		newPass := r.FormValue("new_password")
		confirm := r.FormValue("confirm_password")
		if newPass != confirm {
			renderLogin(w, true, "Passwords do not match.")
			return
		}
		if len(newPass) < minPasswordLen {
			renderLogin(w, true, fmt.Sprintf("Password must be at least %d characters.", minPasswordLen))
			return
		}
		if len(newPass) > maxPasswordLen {
			renderLogin(w, true, fmt.Sprintf("Password must be no more than %d characters.", maxPasswordLen))
			return
		}
		hash, err := hashPassword(newPass)
		if err != nil {
			renderLogin(w, true, "Could not hash password: "+err.Error())
			return
		}
		if err := mutateConfig(func(c *Config) error {
			c.WebUIPasswordHash = hash
			return nil
		}); err != nil {
			renderLogin(w, true, "Could not save password: "+err.Error())
			return
		}
		logEvent("[Auth] Web UI password set")
		createSession(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	pass := r.FormValue("password")
	if !checkPassword(cfg.WebUIPasswordHash, pass) {
		appLog("[Auth] Failed login from %s", r.RemoteAddr)
		renderLogin(w, false, "Incorrect password.")
		return
	}
	createSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	destroySession(r, w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Write(faviconData)
}

func handleSidebarLogo(w http.ResponseWriter, r *http.Request) {
	writePNG(w, sidebarLogoData)
}

func handleSidebarLogoV2(w http.ResponseWriter, r *http.Request) {
	writePNG(w, sidebarLogoV2Data)
}

func handleAboutBannerDark(w http.ResponseWriter, r *http.Request) {
	writePNG(w, aboutBannerDarkData)
}

func handleAboutBannerLight(w http.ResponseWriter, r *http.Request) {
	writePNG(w, aboutBannerLightData)
}

func writePNG(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(data)
}

// ---- /api/logs/content ------------------------------------------------------

func apiLogsContent(w http.ResponseWriter, r *http.Request) {
	selectedFile := filepath.Base(r.URL.Query().Get("file"))
	allFiles := listLogFiles()
	if selectedFile == "." || selectedFile == "" {
		if len(allFiles) > 0 {
			selectedFile = allFiles[0].Name
		}
	}
	logContent := ""
	if selectedFile != "" {
		path := filepath.Join(dataPath(logsDir), selectedFile)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			sep := "\n" + separator()
			var blocks []string
			for _, b := range strings.Split(content, sep) {
				b = strings.TrimSpace(b)
				if b != "" {
					blocks = append(blocks, b)
				}
			}
			if len(blocks) > 50 {
				blocks = blocks[len(blocks)-50:]
			}
			for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
				blocks[i], blocks[j] = blocks[j], blocks[i]
			}
			logContent = strings.Join(blocks, "\n"+separator()+"\n")
		}
	}
	jsonOK(w, map[string]interface{}{
		"content": logContent,
		"file":    selectedFile,
	})
}

// ---- Template loading -------------------------------------------------------

var pageTemplates map[string]*template.Template

func loadTemplates() {
	pages := []string{"dashboard", "upcoming", "history", "settings", "ignored", "logs", "about"}
	pageTemplates = make(map[string]*template.Template)
	for _, p := range pages {
		t := template.Must(template.New("").ParseFS(
			templateFS,
			"templates/layout.html",
			"templates/"+p+".html",
		))
		pageTemplates[p] = t
	}
	// Login is rendered standalone (no sidebar, no layout wrapper).
	pageTemplates["login"] = template.Must(template.New("").ParseFS(
		templateFS,
		"templates/login.html",
	))
}
