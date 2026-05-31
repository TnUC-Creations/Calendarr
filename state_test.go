package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetRuntimeStateForTest(t *testing.T) {
	t.Helper()
	stateMu.Lock()
	appState = AppState{LastRunStatus: "Never run"}
	stateMu.Unlock()

	cleanupMu.Lock()
	cleanupState = CleanupState{}
	cleanupMu.Unlock()

	previewMu.Lock()
	previewState = PreviewState{}
	previewMu.Unlock()

	operationMu.Lock()
	operationState = OperationState{}
	nextOperationID = 0
	operationMu.Unlock()
}

func TestOperationGateBlocksConcurrentSync(t *testing.T) {
	resetRuntimeStateForTest(t)

	op, ok := tryBeginOperation("cleanup", "Cleanup")
	if !ok {
		t.Fatal("tryBeginOperation cleanup = false, want true")
	}
	defer finishOperation(op.ID)

	if runSyncJob() {
		t.Fatal("runSyncJob = true while cleanup operation is active, want false")
	}
	if isRunning() {
		t.Fatal("sync running flag was set even though operation gate rejected sync")
	}
}

func TestFinishRunReleasesSyncOperation(t *testing.T) {
	resetRuntimeStateForTest(t)

	if !tryStartRun() {
		t.Fatal("tryStartRun = false, want true")
	}
	op := getOperationState()
	if !op.Active || op.Kind != "sync" {
		t.Fatalf("operation = %#v, want active sync operation", op)
	}

	finishRun(time.Now(), "Success", nil, SyncStats{})

	if got := getOperationState(); got.Active {
		t.Fatalf("operation = %#v, want inactive after finishRun", got)
	}
	if isRunning() {
		t.Fatal("sync running flag still true after finishRun")
	}
}

func TestAPIPreviewRejectsActiveOperation(t *testing.T) {
	resetRuntimeStateForTest(t)
	op, ok := tryBeginOperation("cleanup", "Cleanup")
	if !ok {
		t.Fatal("tryBeginOperation cleanup = false, want true")
	}
	defer finishOperation(op.ID)

	req := httptest.NewRequest(http.MethodPost, "/api/preview", nil)
	rec := httptest.NewRecorder()

	apiPreview(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	assertJSONFailure(t, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "Cleanup is already in progress") {
		t.Fatalf("body = %s, want active operation label", rec.Body.String())
	}
}

func TestAPICleanupRejectsActiveOperation(t *testing.T) {
	resetRuntimeStateForTest(t)
	op, ok := tryBeginOperation("sync", "Sync")
	if !ok {
		t.Fatal("tryBeginOperation sync = false, want true")
	}
	defer finishOperation(op.ID)

	req := httptest.NewRequest(http.MethodPost, "/api/cleanup", strings.NewReader(`{"mode":"past"}`))
	rec := httptest.NewRecorder()

	apiCleanup(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	assertJSONFailure(t, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "Sync is already in progress") {
		t.Fatalf("body = %s, want active operation label", rec.Body.String())
	}
}

func TestFinishRunOnPanicReleasesSyncOperation(t *testing.T) {
	resetRuntimeStateForTest(t)
	if !tryStartRun() {
		t.Fatal("tryStartRun = false, want true")
	}

	func() {
		defer func() { _ = recover() }()
		defer finishRunOnPanic(time.Now())
		panic("boom")
	}()

	if got := getOperationState(); got.Active {
		t.Fatalf("operation = %#v, want inactive after sync panic", got)
	}
	if s := getAppState(); s.IsRunning || !strings.Contains(s.LastRunStatus, "failed unexpectedly") {
		t.Fatalf("app state = %#v, want finished sync error", s)
	}
}

func TestFinishPreviewOnPanicMarksPreviewFailed(t *testing.T) {
	resetRuntimeStateForTest(t)
	setPreviewRunning()

	func() {
		defer func() { _ = recover() }()
		defer finishPreviewOnPanic()
		panic("boom")
	}()

	s := getPreviewState()
	if s.Running || !s.Done || !strings.Contains(s.Error, "failed unexpectedly") {
		t.Fatalf("preview state = %#v, want finished preview error", s)
	}
}

func TestFinishCleanupOnPanicMarksCleanupFailed(t *testing.T) {
	resetRuntimeStateForTest(t)
	setCleanupRunning()
	updateCleanupProgress(7, 2)

	func() {
		defer func() { _ = recover() }()
		defer finishCleanupOnPanic()
		panic("boom")
	}()

	s := getCleanupState()
	if s.Running || !s.Done || s.Ok == nil || *s.Ok || s.Scanned != 7 || s.Deleted != 2 {
		t.Fatalf("cleanup state = %#v, want finished cleanup error preserving counts", s)
	}
}
