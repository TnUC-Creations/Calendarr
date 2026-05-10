package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ---- App metadata -----------------------------------------------------------

const (
	appName    = "Calendarr"
	appVersion = "1.10.0"
	appAuthor  = "TnUC Creations"
	appCreated = "April 2026"
)

// startTime is set once at launch and used to display uptime on the About page.
var startTime = time.Now()

// ---- File paths (relative to working directory set by NSSM) ----------------

const (
	configFile  = "config.json"
	logsDir     = "logs"
	historyFile = "history.json"
)

// currentLogFile returns the path to today's daily log file.
func currentLogFile() string {
	return filepath.Join(dataPath(logsDir), "sync-"+time.Now().Format("2006-01-02")+".log")
}

// pruneOldLogs deletes daily log files beyond the newest maxFiles, oldest first.
func pruneOldLogs(maxFiles int) {
	if maxFiles <= 0 {
		maxFiles = 30
	}
	entries, err := os.ReadDir(dataPath(logsDir))
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "sync-") && strings.HasSuffix(n, ".log") {
			names = append(names, n)
		}
	}
	sort.Strings(names) // oldest first (date-based names)
	for len(names) > maxFiles {
		_ = os.Remove(filepath.Join(dataPath(logsDir), names[0]))
		names = names[1:]
	}
}

// ---- Sync job ---------------------------------------------------------------

// runSyncJob starts a sync in a goroutine if one isn't already running.
// Returns false if a sync was already in progress. Uses tryStartRun so two
// callers racing in the same instant cannot both launch a worker.
func runSyncJob() bool {
	if !tryStartRun() {
		return false
	}
	safeGo(syncWorker)
	return true
}

func syncWorker() {
	runTime := time.Now()

	cfg, err := loadConfig()
	if err != nil {
		finishRun(runTime, fmt.Sprintf("Error: %v", err), nil, SyncStats{})
		return
	}

	// Open the log file immediately and write the run header so that
	// auto-refresh shows progress live as the sync runs rather than waiting
	// for the entire sync to complete before anything appears.
	var w io.Writer = io.Discard
	logFile, fileErr := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if fileErr == nil {
		fmt.Fprintf(logFile, "\n%s\nRun at %s  [Calendarr v%s | uptime: %s]\n",
			separator(), runTime.Format("2006-01-02 15:04:05"), appVersion, formatUptime(time.Since(startTime)))
		w = logFile
	}

	result, syncErr := runSync(cfg, w, false)
	fmt.Fprintf(w, "[Sync] Total time: %s\n", time.Since(runTime).Round(time.Millisecond))

	status := "Success"
	if syncErr != nil {
		status = fmt.Sprintf("Error: %v", syncErr)
	}

	// Write to history early (crash-safe — before Pushover or log file write).
	ts := runTime.Format("2006-01-02 15:04:05")
	var entries []HistoryEntry
	for _, m := range result.Added {
		entries = append(entries, HistoryEntry{Timestamp: ts, Action: "added", Message: m})
	}
	for _, m := range result.Updated {
		entries = append(entries, HistoryEntry{Timestamp: ts, Action: "updated", Message: m})
	}
	for _, m := range result.Deleted {
		entries = append(entries, HistoryEntry{Timestamp: ts, Action: "deleted", Message: m})
	}
	if len(entries) > 0 {
		appendHistory(entries)
	}

	// Prune old log files per config.
	if cfg2, err2 := loadConfig(); err2 == nil {
		pruneOldLogs(cfg2.MaxLogFiles)
	}

	var changes []RunChange
	for _, m := range result.Added {
		changes = append(changes, RunChange{Action: "added", Message: m})
	}
	for _, m := range result.Updated {
		changes = append(changes, RunChange{Action: "updated", Message: m})
	}
	for _, m := range result.Deleted {
		changes = append(changes, RunChange{Action: "deleted", Message: m})
	}
	stats := SyncStats{
		Added:   len(result.Added),
		Updated: len(result.Updated),
		Deleted: len(result.Deleted),
	}

	// Auto-cleanup: remove past Calendarr events after a successful sync.
	var cleanupDeleted []string
	if cfg.AutoCleanupPast && !strings.Contains(status, "Error") {
		var cleanupErr error
		cleanupDeleted, cleanupErr = runCleanup(cfg, "past")
		if len(cleanupDeleted) > 0 {
			stats.Deleted += len(cleanupDeleted)
			for _, m := range cleanupDeleted {
				changes = append(changes, RunChange{Action: "deleted", Message: m})
			}
		}
		if cleanupErr != nil {
			status = fmt.Sprintf("Error: %v", cleanupErr)
			fmt.Fprintf(w, "[ERROR] Auto-cleanup failed: %v\n", cleanupErr)
		}
	}
	finishRun(runTime, status, changes, stats)

	// Log Pushover decisions and send — written to w so they appear in the log file.
	if syncErr != nil {
		if cfg.UsePushover && cfg.PushoverOnError && cfg.PushoverToken != "" && cfg.PushoverUser != "" {
			msg := fmt.Sprintf("Sync failed: %v", syncErr)
			fmt.Fprintf(w, "[Pushover] Error notification: ENABLED — sending failure reason\n")
			sendPushover(cfg.PushoverToken, cfg.PushoverUser, msg, cfg.PushoverSound)
		} else if cfg.UsePushover && cfg.PushoverOnError {
			fmt.Fprintf(w, "[Pushover] Error notification: ENABLED but credentials missing — skipped\n")
		} else {
			fmt.Fprintf(w, "[Pushover] Error notification: DISABLED\n")
		}
	} else if cfg.UsePushover && cfg.PushoverToken != "" && cfg.PushoverUser != "" {
		totalDeleted := len(result.Deleted) + len(cleanupDeleted)
		fmt.Fprintf(w, "[Pushover] Sync result: %d added, %d updated, %d deleted (%d from auto-cleanup)\n",
			len(result.Added), len(result.Updated), len(result.Deleted), len(cleanupDeleted))

		var notifyAdded, notifyUpdated, notifyDeleted []string
		if cfg.PushoverOnAdded {
			notifyAdded = result.Added
			fmt.Fprintf(w, "[Pushover] Add notifications: ENABLED — %d item(s)\n", len(notifyAdded))
		} else {
			fmt.Fprintf(w, "[Pushover] Add notifications: DISABLED\n")
		}
		if cfg.PushoverOnUpdated {
			notifyUpdated = result.Updated
			fmt.Fprintf(w, "[Pushover] Update notifications: ENABLED — %d item(s)\n", len(notifyUpdated))
		} else {
			fmt.Fprintf(w, "[Pushover] Update notifications: DISABLED\n")
		}
		if cfg.PushoverOnDeleted {
			notifyDeleted = append(result.Deleted, cleanupDeleted...)
			fmt.Fprintf(w, "[Pushover] Delete notifications: ENABLED — %d item(s)\n", len(notifyDeleted))
		} else {
			fmt.Fprintf(w, "[Pushover] Delete notifications: DISABLED (%d item(s) suppressed)\n", totalDeleted)
		}

		if msg := buildPushoverMessage(notifyAdded, notifyUpdated, notifyDeleted); msg != "" {
			fmt.Fprintf(w, "[Pushover] Sending notification\n")
			sendPushover(cfg.PushoverToken, cfg.PushoverUser, msg, cfg.PushoverSound)
		} else {
			fmt.Fprintf(w, "[Pushover] Nothing to send — no items in enabled categories\n")
		}
	} else if cfg.UsePushover {
		fmt.Fprintf(w, "[Pushover] Skipped — token or user key not configured\n")
	}

	fmt.Fprintf(w, "Status: %s\nAdded: %d  Updated: %d  Deleted: %d\n",
		status, len(result.Added), len(result.Updated), len(result.Deleted))
	if logFile != nil {
		logFile.Close()
	}
}

// ---- Background scheduler ---------------------------------------------------

func backgroundScheduler() {
	time.Sleep(3 * time.Second)

	cfg, _ := loadConfig()
	if cfg.SyncOnStart {
		logEvent("[Scheduler] Sync on start enabled — triggering immediate sync")
		runSyncJob()
	} else {
		next := time.Now().Add(time.Duration(cfg.RunIntervalHours * float64(time.Hour)))
		setNextRun(next)
		logEvent(fmt.Sprintf("[Scheduler] Next sync scheduled for %s", next.Format("2006-01-02 15:04:05")))
	}

	for {
		cfg, _ = loadConfig()
		interval := time.Duration(cfg.RunIntervalHours * float64(time.Hour))
		waitStart := time.Now()
		next := waitStart.Add(interval)
		setNextRun(next)
		logEvent(fmt.Sprintf("[Scheduler] Next sync scheduled for %s", next.Format("2006-01-02 15:04:05")))

		// Sleep in small increments so a shortened interval can take effect mid-wait.
		for time.Now().Before(next) {
			time.Sleep(30 * time.Second)
			cur, _ := loadConfig()
			curInterval := time.Duration(cur.RunIntervalHours * float64(time.Hour))
			if curInterval > 0 && curInterval < interval {
				interval = curInterval
				next = waitStart.Add(curInterval)
				setNextRun(next)
				if time.Now().After(next) {
					logEvent("[Scheduler] Interval shortened — running sync now")
					break
				}
				logEvent(fmt.Sprintf("[Scheduler] Interval shortened — next sync now %s", next.Format("2006-01-02 15:04:05")))
			}
		}

		runSyncJob()
	}
}

// ---- Windows: launch a detached grandchild process --------------------------

// startDetached launches a command in a new independent window so NSSM's
// child-process cleanup doesn't kill it when the service stops.
func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = detachedSysProcAttr()
	return cmd.Start()
}

// ---- HTTP routes ------------------------------------------------------------

func registerRoutes(mux *http.ServeMux) {
	// Pages
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/upcoming", handleUpcoming)
	mux.HandleFunc("/history", handleHistory)
	mux.HandleFunc("/history/clear", handleHistoryClear)
	mux.HandleFunc("/settings", handleSettings)
	mux.HandleFunc("/ignored", handleIgnored)
	mux.HandleFunc("/logs", handleLogs)
	mux.HandleFunc("/logs/download", handleLogsDownload)
	mux.HandleFunc("/logs/clear", handleLogsClear)
	mux.HandleFunc("/about", handleAbout)
	mux.HandleFunc("/run", handleRunNow)
	mux.HandleFunc("/backup", handleBackup)
	mux.HandleFunc("/restore", handleRestore)
	mux.HandleFunc("/login", handleLogin)
	mux.HandleFunc("/logout", handleLogout)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.HandleFunc("/assets/sidebar-logo.png", handleSidebarLogo)
	mux.HandleFunc("/assets/sidebar-logo-v2.png", handleSidebarLogoV2)
	mux.HandleFunc("/assets/about-banner-dark.png", handleAboutBannerDark)
	mux.HandleFunc("/assets/about-banner-light.png", handleAboutBannerLight)

	// JSON API
	mux.HandleFunc("/api/status", apiStatus)
	mux.HandleFunc("/api/run", apiRun)
	mux.HandleFunc("/api/cleanup", apiCleanup)
	mux.HandleFunc("/api/cleanup/target", apiCleanupTarget)
	mux.HandleFunc("/api/cleanup/status", apiCleanupStatus)
	mux.HandleFunc("/api/settings/save", apiSettingsSave)
	mux.HandleFunc("/api/settings/calendar-targets", apiCalendarTargetsSave)
	mux.HandleFunc("/api/restart", apiRestart)
	mux.HandleFunc("/api/ignored/save", apiIgnoredSave)
	mux.HandleFunc("/api/sonarr-shows", apiSonarrShows)
	mux.HandleFunc("/api/upcoming", apiUpcoming)
	mux.HandleFunc("/api/test/radarr", apiTestRadarr)
	mux.HandleFunc("/api/test/sonarr", apiTestSonarr)
	mux.HandleFunc("/api/test/pushover", apiTestPushover)
	mux.HandleFunc("/api/test/calendar", apiTestCalendar)
	mux.HandleFunc("/api/stop", apiStop)
	mux.HandleFunc("/api/update/status", apiUpdateStatus)
	mux.HandleFunc("/api/update/check", apiUpdateCheck)
	mux.HandleFunc("/api/update/apply", apiUpdateApply)
	mux.HandleFunc("/api/logs/content", apiLogsContent)
	mux.HandleFunc("/api/preview", apiPreview)
	mux.HandleFunc("/api/preview/status", apiPreviewStatus)
	mux.HandleFunc("/oauth/start", handleOAuthStart)
	mux.HandleFunc("/oauth/callback", handleOAuthCallback)
	mux.HandleFunc("/api/auth/google/status", apiGoogleStatus)
	mux.HandleFunc("/api/auth/google/disconnect", apiGoogleDisconnect)
	mux.HandleFunc("/api/auth/password", apiSetPassword)
	mux.HandleFunc("/api/auth/password/clear", apiClearPassword)
	mux.HandleFunc("/api/calendars", apiCalendars)
}

// ---- Entry point ------------------------------------------------------------

func main() {
	// Parse CLI flags before any file I/O so dataDir is available immediately.
	installFlag := flag.Bool("install", false, "Install Calendarr as a Windows service (requires Administrator)")
	uninstallFlag := flag.Bool("uninstall", false, "Uninstall the Calendarr Windows service (requires Administrator)")
	dataDirFlag := flag.String("data", "", "Data directory for config, logs, and history (default: current directory)")
	flag.Parse()

	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	}

	// Service install / uninstall — run and exit, no web server.
	if *installFlag {
		exePath, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		if err := installService(exePath, *dataDirFlag); err != nil {
			fmt.Fprintln(os.Stderr, "Install failed:", err)
			os.Exit(1)
		}
		fmt.Printf("Service %q installed successfully.\n", serviceName)
		return
	}

	if *uninstallFlag {
		if err := uninstallService(); err != nil {
			fmt.Fprintln(os.Stderr, "Uninstall failed:", err)
			os.Exit(1)
		}
		fmt.Printf("Service %q uninstalled successfully.\n", serviceName)
		return
	}

	// Check if launched by Windows Service Control Manager.
	if runIfWindowsService() {
		return
	}

	// Running from the command line.
	startApp()
}

// startApp contains all normal startup logic. Called directly from main()
// when running from the command line, or from the Windows service Execute
// handler when running as a service.
func startApp() {
	if dataDir != "" {
		_ = os.MkdirAll(dataDir, 0755)
	}
	_ = os.MkdirAll(dataPath(logsDir), 0755)

	log.SetOutput(logFileWriter{})
	log.SetFlags(log.Ldate | log.Ltime)

	loadTemplates()

	cleanupConfigTmp()
	cleanupHistoryTmp()

	cfg, err := loadConfig()
	if err != nil {
		if os.IsNotExist(err) {
			// First run — write a default config.json so the UI is usable immediately.
			_ = saveConfig(cfg)
			logEvent("[Startup] First run — default config.json created")
		} else {
			fmt.Fprintf(os.Stderr, "WARNING: could not load config.json: %v\n", err)
		}
	}

	// If a previous auto-update ran, its batch left a result log. Read, log, delete.
	if content, err := os.ReadFile(dataPath("calendarr-update.log")); err == nil {
		logEvent("[Updater] Previous update result:\n" + strings.TrimSpace(string(content)))
		_ = os.Remove(dataPath("calendarr-update.log"))
	}

	// Log startup details — useful for debugging service/path issues.
	dataDirLabel := "(current directory)"
	if dataDir != "" {
		dataDirLabel = dataDir
	}
	logEvent(fmt.Sprintf("[Startup] Calendarr v%s | %s/%s | bind %s:%d | data: %s",
		appVersion, runtime.GOOS, runtime.GOARCH, cfg.WebBindAddress, cfg.WebPort, dataDirLabel))
	activeWebPort = cfg.WebPort

	safeGo(backgroundScheduler)
	safeGo(backgroundUpdateChecker)
	safeGo(sessionSweeper)

	mux := http.NewServeMux()
	registerRoutes(mux)

	addr := fmt.Sprintf("%s:%d", cfg.WebBindAddress, cfg.WebPort)
	fmt.Printf("%s v%s listening on http://%s\n", appName, appVersion, addr)
	if err := http.ListenAndServe(addr, httpMiddleware(authMiddleware(csrfMiddleware(mux)))); err != nil {
		fmt.Fprintln(os.Stderr, "Server error:", err)
		os.Exit(1)
	}
}
