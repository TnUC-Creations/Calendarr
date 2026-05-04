package main

import (
	"sync"
	"time"
)

// SyncStats holds per-run change counts used by the dashboard and API.
type SyncStats struct {
	Added   int
	Updated int
	Deleted int
}

// RunChange pairs an action type with a human-readable message so the
// dashboard can render the correct icon and color per entry.
type RunChange struct {
	Action  string `json:"action"`  // "added", "updated", "deleted"
	Message string `json:"message"`
}

// AppState is the live state of the scheduler, protected by stateMu.
type AppState struct {
	IsRunning      bool
	LastRun        *time.Time
	LastRunStatus  string
	LastRunChanges []RunChange
	LastRunStats   SyncStats
	NextRun        *time.Time
	SyncProgress   string // current activity shown while IsRunning == true
}

// CleanupState tracks the background cleanup worker, protected by cleanupMu.
type CleanupState struct {
	Running bool
	Done    bool
	Ok      *bool  // nil = not finished, true = success, false = error
	Deleted int
	Scanned int
	Message string
}

// PreviewState tracks a dry-run sync, protected by previewMu.
type PreviewState struct {
	Running  bool
	Done     bool
	Result   *SyncResult
	Error    string
	Progress string
}

var (
	appState AppState = AppState{LastRunStatus: "Never run"}
	stateMu  sync.RWMutex

	cleanupState CleanupState
	cleanupMu    sync.Mutex

	previewState PreviewState
	previewMu    sync.Mutex
)

// ---- App state helpers -------------------------------------------------------

func getAppState() AppState {
	stateMu.RLock()
	defer stateMu.RUnlock()
	s := appState
	return s
}

func setRunning(v bool) {
	stateMu.Lock()
	appState.IsRunning = v
	stateMu.Unlock()
}

func isRunning() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return appState.IsRunning
}

func setNextRun(t time.Time) {
	stateMu.Lock()
	appState.NextRun = &t
	stateMu.Unlock()
}

func setSyncProgress(msg string) {
	stateMu.Lock()
	appState.SyncProgress = msg
	stateMu.Unlock()
}

func finishRun(runTime time.Time, status string, changes []RunChange, stats SyncStats) {
	stateMu.Lock()
	appState.LastRun = &runTime
	appState.LastRunStatus = status
	appState.LastRunChanges = changes
	appState.LastRunStats = stats
	appState.IsRunning = false
	appState.SyncProgress = ""
	stateMu.Unlock()
}

// ---- Cleanup state helpers --------------------------------------------------

func getCleanupState() CleanupState {
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	return cleanupState
}

func setCleanupRunning() {
	cleanupMu.Lock()
	cleanupState = CleanupState{Running: true}
	cleanupMu.Unlock()
}

func updateCleanupProgress(scanned, deleted int) {
	cleanupMu.Lock()
	cleanupState.Scanned = scanned
	cleanupState.Deleted = deleted
	cleanupMu.Unlock()
}

// mergeCleanupIntoLastRun adds auto-cleanup deletions into the last sync run's
// dashboard stats and Last Run Changes list.
func mergeCleanupIntoLastRun(count int, messages []string) {
	stateMu.Lock()
	appState.LastRunStats.Deleted += count
	for _, m := range messages {
		appState.LastRunChanges = append(appState.LastRunChanges, RunChange{Action: "deleted", Message: m})
	}
	stateMu.Unlock()
}

// ---- Preview state helpers --------------------------------------------------

func getPreviewState() PreviewState {
	previewMu.Lock()
	defer previewMu.Unlock()
	return previewState
}

func setPreviewRunning() {
	previewMu.Lock()
	previewState = PreviewState{Running: true}
	previewMu.Unlock()
}

func setPreviewProgress(msg string) {
	previewMu.Lock()
	previewState.Progress = msg
	previewMu.Unlock()
}

func finishPreview(result *SyncResult, errMsg string) {
	previewMu.Lock()
	previewState.Running = false
	previewState.Done = true
	previewState.Result = result
	previewState.Error = errMsg
	previewMu.Unlock()
}

func finishCleanup(ok bool, scanned, deleted int, message string) {
	cleanupMu.Lock()
	cleanupState.Running = false
	cleanupState.Done = true
	cleanupState.Ok = &ok
	cleanupState.Scanned = scanned
	cleanupState.Deleted = deleted
	cleanupState.Message = message
	cleanupMu.Unlock()
}
