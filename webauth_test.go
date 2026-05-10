package main

import (
	"testing"
	"time"
)

func TestLoginFailuresThrottleAndSuccessResetsIP(t *testing.T) {
	resetAuthAttemptsForTest()
	remote := "192.0.2.10:54321"

	for i := 0; i < authLockoutThreshold-1; i++ {
		if delay := recordLoginFailure(remote); delay != 0 {
			t.Fatalf("failure %d delay = %s, want no delay before threshold", i+1, delay)
		}
	}
	if delay := recordLoginFailure(remote); delay <= 0 {
		t.Fatalf("delay = %s, want throttle at threshold", delay)
	}
	if delay := authThrottleDelay(remote); delay <= 0 {
		t.Fatalf("authThrottleDelay = %s, want active throttle", delay)
	}

	recordLoginSuccess(remote)
	if delay := authThrottleDelay(remote); delay != 0 {
		t.Fatalf("authThrottleDelay after success = %s, want 0", delay)
	}
}

func TestClearSessionsRevokesAllSessions(t *testing.T) {
	sessionStoreMu.Lock()
	sessionStore = map[string]time.Time{
		"a": time.Now().Add(time.Hour),
		"b": time.Now().Add(time.Hour),
	}
	sessionStoreMu.Unlock()

	clearSessions()

	sessionStoreMu.Lock()
	defer sessionStoreMu.Unlock()
	if len(sessionStore) != 0 {
		t.Fatalf("sessionStore length = %d, want 0", len(sessionStore))
	}
}

func resetAuthAttemptsForTest() {
	authAttemptMu.Lock()
	defer authAttemptMu.Unlock()
	authAttemptsByIP = map[string]authAttemptState{}
	authGlobalFailures = nil
}
