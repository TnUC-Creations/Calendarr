package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallbackURIUsesActiveRunningPort(t *testing.T) {
	oldDataDir := dataDir
	oldActivePort := activeWebPort
	dataDir = t.TempDir()
	activeWebPort = 5000
	t.Cleanup(func() {
		dataDir = oldDataDir
		activeWebPort = oldActivePort
	})

	cfg := defaultConfig()
	cfg.WebPort = 6123
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if got, want := callbackURI(), "http://localhost:5000/oauth/callback"; got != want {
		t.Fatalf("callbackURI = %q, want %q", got, want)
	}
}

func TestRequestFromLocalhost(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:5000":  true,
		"[::1]:5000":      true,
		"192.168.1.5:123": false,
	}

	for remote, want := range tests {
		t.Run(remote, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/oauth/start", nil)
			req.RemoteAddr = remote
			if got := requestFromLocalhost(req); got != want {
				t.Fatalf("requestFromLocalhost = %v, want %v", got, want)
			}
		})
	}
}

func TestCallbackBaseURLFallsBackToSavedPortWhenNoActivePort(t *testing.T) {
	oldDataDir := dataDir
	oldActivePort := activeWebPort
	dataDir = t.TempDir()
	activeWebPort = 0
	t.Cleanup(func() {
		dataDir = oldDataDir
		activeWebPort = oldActivePort
	})

	cfg := defaultConfig()
	cfg.WebPort = 6123
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got, want := callbackBaseURL(), "http://localhost:6123"; got != want {
		t.Fatalf("callbackBaseURL = %q, want %q", got, want)
	}
	if got := filepath.Base(dataPath(configFile)); got != configFile {
		t.Fatalf("dataPath sanity check = %q, want %q", got, configFile)
	}
}

func TestOAuthSuccessPageRefreshesOpenerAndProvidesFallback(t *testing.T) {
	rr := httptest.NewRecorder()
	renderOAuthSuccessPage(rr)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q, want text/html; charset=utf-8", got)
	}
	html := rr.Body.String()
	for _, want := range []string{
		`window.opener.location.href = target`,
		`window.opener.focus()`,
		`window.close()`,
		`localStorage.setItem('calendarr-oauth-complete'`,
		`href="/settings#calendar"`,
		`Google Calendar connected`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("OAuth success page missing %q", want)
		}
	}
}
