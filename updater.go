package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	githubOwner         = "TnUC-Creations"
	githubRepo          = "Calendarr"
	updateCheckInterval = 24 * time.Hour
)

// UpdateState holds the result of the most recent update check.
type UpdateState struct {
	Available   bool
	LatestTag   string
	LatestVer   string
	DownloadURL string
	ChecksumURL string
	ReleaseURL  string
	Checking    bool
	LastChecked time.Time
	Error       string
}

var (
	updateState UpdateState
	updateMu    sync.RWMutex
)

type updateCheckMode int

const (
	updateCheckBackground updateCheckMode = iota
	updateCheckManual
)

func getUpdateState() UpdateState {
	updateMu.RLock()
	defer updateMu.RUnlock()
	return updateState
}

// parseVersion converts "v1.6.0" or "1.6.0" to [3]int{1, 6, 0}.
func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var r [3]int
	for i, p := range parts {
		n, _ := strconv.Atoi(p)
		r[i] = n
	}
	return r
}

// versionNewer returns true if a is strictly newer than b.
func versionNewer(a, b string) bool {
	va, vb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if va[i] > vb[i] {
			return true
		}
		if va[i] < vb[i] {
			return false
		}
	}
	return false
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// checkForUpdates queries the GitHub Releases API and updates updateState.
func checkForUpdates(mode updateCheckMode) {
	updateMu.Lock()
	updateState.Checking = true
	updateState.Error = ""
	updateMu.Unlock()

	logEvent("[Updater] Checking for updates...")

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		newState := UpdateState{Checking: false, LastChecked: time.Now(), Error: "Could not build update request: " + err.Error()}
		logEvent("[Updater] Check failed: " + newState.Error)
		updateMu.Lock()
		updateState = newState
		updateMu.Unlock()
		return
	}
	req.Header.Set("User-Agent", "Calendarr/"+appVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)

	newState := UpdateState{Checking: false, LastChecked: time.Now()}

	if err != nil {
		newState.Error = "Could not reach GitHub: " + err.Error()
		logEvent("[Updater] Check failed: " + newState.Error)
		updateMu.Lock()
		updateState = newState
		updateMu.Unlock()
		return
	}
	defer resp.Body.Close()

	// 404 usually means the repo is private to unauthenticated GitHub API calls,
	// but it can also mean the repo or release does not exist.
	if resp.StatusCode == 404 {
		newState.Error = "GitHub release check returned HTTP 404. The repository may be private, missing, or have no releases."
		logEvent("[Updater] Check failed: " + newState.Error)
		updateMu.Lock()
		updateState = newState
		updateMu.Unlock()
		return
	}
	if resp.StatusCode != 200 {
		newState.Error = fmt.Sprintf("GitHub API returned HTTP %d", resp.StatusCode)
		logEvent("[Updater] Check failed: " + newState.Error)
		updateMu.Lock()
		updateState = newState
		updateMu.Unlock()
		return
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		newState.Error = "Failed to parse release info"
		logEvent("[Updater] Check failed: " + newState.Error)
		updateMu.Lock()
		updateState = newState
		updateMu.Unlock()
		return
	}

	release.TagName = strings.TrimSpace(release.TagName)
	newState.LatestTag = release.TagName
	newState.LatestVer = strings.TrimPrefix(release.TagName, "v")
	newState.ReleaseURL = release.HTMLURL
	newState.Available = versionNewer(release.TagName, appVersion)

	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, "calendarr.exe") {
			newState.DownloadURL = asset.BrowserDownloadURL
		}
		if strings.EqualFold(asset.Name, "calendarr.exe.sha256") {
			newState.ChecksumURL = asset.BrowserDownloadURL
		}
	}
	if newState.Available && (newState.DownloadURL == "" || newState.ChecksumURL == "") {
		switch {
		case newState.DownloadURL == "" && newState.ChecksumURL == "":
			newState.Error = "Release is missing calendarr.exe and calendarr.exe.sha256. In-app update is blocked until both assets are published."
		case newState.DownloadURL == "":
			newState.Error = "Release is missing calendarr.exe. In-app update is blocked until the executable asset is published."
		default:
			newState.Error = "Release is missing calendarr.exe.sha256. In-app update is blocked until a checksum asset is published."
		}
		logEvent("[Updater] Check warning: " + newState.Error)
	}

	if newState.Available {
		logEvent(fmt.Sprintf("[Updater] Update available: v%s → v%s", appVersion, newState.LatestVer))
	} else {
		logEvent(fmt.Sprintf("[Updater] Up to date (v%s is current)", appVersion))
	}

	updateMu.Lock()
	updateState = newState
	updateMu.Unlock()

	if newState.Available {
		notifyUpdateAvailable(newState, mode)
	}
}

// backgroundUpdateChecker runs checkForUpdates after a short startup delay,
// then repeats every 24 hours.
func backgroundUpdateChecker() {
	time.Sleep(30 * time.Second) // let the app finish starting up first
	checkForUpdates(updateCheckBackground)
	for {
		time.Sleep(updateCheckInterval)
		checkForUpdates(updateCheckBackground)
	}
}

type updatePushoverDecision struct {
	Send    bool
	Reason  string
	Message string
}

func decideUpdatePushover(cfg Config, state UpdateState, mode updateCheckMode) updatePushoverDecision {
	if !state.Available {
		return updatePushoverDecision{Reason: "no update available"}
	}
	if mode != updateCheckBackground {
		return updatePushoverDecision{Reason: "manual check"}
	}
	if !cfg.UsePushover || !cfg.PushoverOnUpdate {
		return updatePushoverDecision{Reason: "disabled"}
	}
	if strings.TrimSpace(cfg.PushoverToken) == "" || strings.TrimSpace(cfg.PushoverUser) == "" {
		return updatePushoverDecision{Reason: "credentials missing"}
	}
	if state.DownloadURL == "" || state.ChecksumURL == "" {
		return updatePushoverDecision{Reason: "not installable"}
	}
	if state.LatestTag != "" && state.LatestTag == cfg.LastUpdatePushoverTag {
		return updatePushoverDecision{Reason: "already notified"}
	}

	latest := state.LatestVer
	if latest == "" {
		latest = strings.TrimPrefix(state.LatestTag, "v")
	}
	msg := fmt.Sprintf("Calendarr update available: v%s -> v%s", appVersion, latest)
	if state.ReleaseURL != "" {
		msg += "\n" + state.ReleaseURL
	}
	return updatePushoverDecision{Send: true, Reason: "enabled", Message: msg}
}

func notifyUpdateAvailable(state UpdateState, mode updateCheckMode) {
	cfg, err := loadConfig()
	if err != nil {
		logEvent("[Pushover] Update notification: could not load settings - " + err.Error())
		return
	}

	decision := decideUpdatePushover(cfg, state, mode)
	switch decision.Reason {
	case "manual check":
		logEvent("[Pushover] Update notification: manual check - skipped")
	case "disabled":
		logEvent("[Pushover] Update notification: DISABLED")
	case "credentials missing":
		logEvent("[Pushover] Update notification: ENABLED but credentials missing - skipped")
	case "not installable":
		logEvent("[Pushover] Update notification: update exists but is not installable through the in-app updater - skipped")
	case "already notified":
		logEvent("[Pushover] Update notification: already notified for " + state.LatestTag)
	case "enabled":
		logEvent("[Pushover] Update notification: ENABLED - sending")
		sendPushover(cfg.PushoverToken, cfg.PushoverUser, decision.Message, cfg.PushoverSound)
		tag := state.LatestTag
		if tag == "" {
			tag = "v" + state.LatestVer
		}
		if err := mutateConfig(func(c *Config) error {
			c.LastUpdatePushoverTag = tag
			return nil
		}); err != nil {
			logEvent("[Pushover] Update notification: could not save notified version - " + err.Error())
		}
	}
}

// downloadUpdate downloads the new exe and sets up a self-deleting batch file
// that stops the service, kills the tray app, swaps the exe, and restarts.
func downloadUpdate() error {
	state := getUpdateState()
	if !state.Available {
		return fmt.Errorf("no update available")
	}
	if state.DownloadURL == "" {
		return fmt.Errorf("no download URL in release — visit the release page to update manually")
	}
	if state.ChecksumURL == "" {
		return fmt.Errorf("release is missing calendarr.exe.sha256 — in-app update is blocked for safety")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine exe path: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	updatePath := filepath.Join(exeDir, "calendarr-update.exe")
	batPath := filepath.Join(exeDir, "_update.bat")
	logPath := dataPath("calendarr-update.log")

	logEvent(fmt.Sprintf("[Updater] Starting update: v%s → v%s", appVersion, state.LatestVer))
	appLog("[Updater] Downloading: %s", state.DownloadURL)
	expectedSHA, err := downloadExpectedSHA256(state.ChecksumURL)
	if err != nil {
		return err
	}

	// Download the new exe.
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(state.DownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(updatePath)
	if err != nil {
		return fmt.Errorf("cannot create update file: %w", err)
	}
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		os.Remove(updatePath)
		return fmt.Errorf("download incomplete: %w", err)
	}
	f.Close()
	appLog("[Updater] Download complete: %.2f MB", float64(n)/(1024*1024))
	if err := verifySHA256(updatePath, expectedSHA); err != nil {
		os.Remove(updatePath)
		return err
	}

	// Validate the downloaded file is a Windows PE executable.
	// If the repo is private and auth is missing, GitHub returns an HTML login
	// page instead of the binary. Copying that over calendarr.exe breaks the
	// service. The MZ header (0x4D 0x5A) is present in every valid Windows exe.
	peCheck, err := os.Open(updatePath)
	if err == nil {
		magic := make([]byte, 2)
		peCheck.Read(magic)
		peCheck.Close()
		if magic[0] != 0x4D || magic[1] != 0x5A {
			os.Remove(updatePath)
			return fmt.Errorf("downloaded file is not a valid Windows executable — the release asset may require authentication to download")
		}
	}

	// Write the swap batch file.
	// ping replaces timeout: timeout hangs when cmd has no console (CREATE_NO_WINDOW).
	// Each step is logged to logPath; the result appears in the daily log on restart.
	bat := fmt.Sprintf(
		"@echo off\r\n"+
			"echo [%%TIME%%] Update started. >> \"%s\"\r\n"+
			"ping 127.0.0.1 -n 6 >nul 2>&1\r\n"+
			"echo [%%TIME%%] Stopping service: %s >> \"%s\"\r\n"+
			"net stop \"%s\" >> \"%s\" 2>&1\r\n"+
			"ping 127.0.0.1 -n 3 >nul 2>&1\r\n"+
			"echo [%%TIME%%] Copying new exe... >> \"%s\"\r\n"+
			"copy /y \"%s\" \"%s\" >> \"%s\" 2>&1\r\n"+
			"del /f /q \"%s\"\r\n"+
			"echo [%%TIME%%] Starting service: %s >> \"%s\"\r\n"+
			"net start \"%s\" >> \"%s\" 2>&1\r\n"+
			"echo [%%TIME%%] Complete. >> \"%s\"\r\n"+
			"del /f /q \"%%~f0\"\r\n",
		logPath,
		serviceName, logPath,
		serviceName, logPath,
		logPath,
		updatePath, exePath, logPath,
		updatePath,
		serviceName, logPath,
		serviceName, logPath,
		logPath,
	)
	if err := os.WriteFile(batPath, []byte(bat), 0644); err != nil {
		os.Remove(updatePath)
		return fmt.Errorf("cannot write update script: %w", err)
	}

	// Launch outside the service's Job Object (BREAKAWAY_FROM_JOB) so the batch
	// survives the service stopping itself.
	appLog("[Updater] Launching update script — service will stop in ~5 seconds")
	if err := startDetached("cmd", "/c", batPath); err != nil {
		os.Remove(updatePath)
		os.Remove(batPath)
		return fmt.Errorf("cannot launch update script: %w", err)
	}
	logEvent(fmt.Sprintf("[Updater] Script launched — stopping %s, swapping exe, restarting", serviceName))

	return nil
}

func downloadExpectedSHA256(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("checksum download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("checksum read failed: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("checksum asset is invalid")
	}
	for _, ch := range fields[0] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return "", fmt.Errorf("checksum asset is invalid")
		}
	}
	return strings.ToLower(fields[0]), nil
}

func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot verify update checksum: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("cannot verify update checksum: %w", err)
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != strings.ToLower(expected) {
		return fmt.Errorf("update checksum mismatch — downloaded file was not installed")
	}
	appLog("[Updater] SHA-256 verified: %s", got)
	return nil
}
