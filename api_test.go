package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAPIHealthAllowsUnauthenticatedLivenessCheck(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = oldDataDir })
	cfg := defaultConfig()
	cfg.PublicHealthFeed = true
	cfg.WebUIPasswordHash = "configured"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	if err := saveHistory([]HistoryEntry{
		{Timestamp: "2026-05-30 08:00:00", Action: "added", Message: "First"},
		{Timestamp: "2026-05-30 08:01:00", Action: "system", Message: "Do not expose"},
		{Timestamp: "2026-05-30 08:02:00", Action: "deleted", Message: "Second"},
	}); err != nil {
		t.Fatalf("saveHistory: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", apiHealth)
	handler := authMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want no redirect", got)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || body["ok"] != true || body["version"] != appVersion {
		t.Fatalf("response = %#v, want public liveness fields only", body)
	}
	if _, exposed := body["history"]; exposed {
		t.Fatalf("response = %#v, public health endpoint must not expose history", body)
	}
}

func TestAPIHealthReturnsNotFoundWhenPublicFeedDisabled(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = oldDataDir })
	if err := saveConfig(defaultConfig()); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	apiHealth(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAPIHealthFeedRequiresToken(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = oldDataDir })
	cfg := defaultConfig()
	cfg.DetailedHealthFeed = true
	cfg.HealthFeedToken = "feed-secret"
	cfg.WebUIPasswordHash = "configured"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health/feed", nil)
	rec := httptest.NewRecorder()
	apiHealthFeed(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAPIHealthFeedReturnsRecentChangesWithHeaderToken(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = oldDataDir })
	cfg := defaultConfig()
	cfg.DetailedHealthFeed = true
	cfg.HealthFeedToken = "feed-secret"
	cfg.WebUIPasswordHash = "configured"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	if err := saveHistory([]HistoryEntry{
		{Timestamp: "2026-05-30 08:00:00", Action: "added", Message: "First"},
		{Timestamp: "2026-05-30 08:01:00", Action: "system", Message: "Do not expose"},
		{Timestamp: "2026-05-30 08:02:00", Action: "deleted", Message: "Second"},
	}); err != nil {
		t.Fatalf("saveHistory: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health/feed", apiHealthFeed)
	handler := authMiddleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/health/feed", nil)
	req.Header.Set("X-Calendarr-Feed-Token", "feed-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	history, ok := body["history"].([]interface{})
	if !ok || len(history) != 2 {
		t.Fatalf("history = %#v, want two detailed entries", body["history"])
	}
	first, ok := history[0].(map[string]interface{})
	if !ok || first["action"] != "deleted" || first["message"] != "Second" {
		t.Fatalf("first history entry = %#v, want newest deleted entry", history[0])
	}
}

func TestValidHealthFeedTokenAcceptsQueryFallback(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/feed?token=feed-secret", nil)
	if !validHealthFeedToken(req, "feed-secret") {
		t.Fatal("expected query token fallback to be accepted")
	}
}

func TestDetailedHealthHistoryReturnsNewestTenChanges(t *testing.T) {
	entries := []HistoryEntry{{Action: "system", Message: "ignore"}}
	for i := 0; i < 12; i++ {
		entries = append(entries, HistoryEntry{Action: "updated", Message: fmt.Sprintf("change-%d", i)})
	}

	got := detailedHealthHistory(entries)

	if len(got) != 10 {
		t.Fatalf("history length = %d, want 10", len(got))
	}
	if got[0].Message != "change-11" || got[9].Message != "change-2" {
		t.Fatalf("history = %#v, want newest ten changes in reverse chronological order", got)
	}
}

func TestAPITestRadarrRejectsMalformedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/test/radarr", strings.NewReader("{"))
	rec := httptest.NewRecorder()

	apiTestRadarr(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertJSONFailure(t, rec.Body.String())
}

func TestAPITestRadarrRejectsOversizedJSON(t *testing.T) {
	body := `{"radarr_url":"` + strings.Repeat("x", int(maxJSONBodyBytes)+1) + `","radarr_api_key":"abc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/test/radarr", strings.NewReader(body))
	rec := httptest.NewRecorder()

	apiTestRadarr(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	assertJSONFailure(t, rec.Body.String())
}

func TestAPITestRadarrSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/status" {
			t.Fatalf("path = %s, want /system/status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	defer upstream.Close()

	body := `{"radarr_url":"` + upstream.URL + `","radarr_api_key":"abc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/test/radarr", strings.NewReader(body))
	rec := httptest.NewRecorder()

	apiTestRadarr(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true {
		t.Fatalf("response = %#v, want ok true", got)
	}
}

func TestAPITestPushoverRejectsMissingFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/test/pushover", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	apiTestPushover(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertJSONFailure(t, rec.Body.String())
}

func TestSettingsTestFailureLogRedactsSecretsAndURLs(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = oldDataDir })
	if err := os.MkdirAll(dataPath(logsDir), 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}

	secret := "GOCSPX-" + strings.Repeat("A", 32)
	logSettingsTestFailure("Steam", "wishlist parse failed at https://example.com/path?token="+secret+" body token="+secret)

	data, err := os.ReadFile(currentLogFile())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(data)
	if !strings.Contains(logText, "[Settings Test] Steam failure") {
		t.Fatalf("log = %q, want settings test failure", logText)
	}
	if strings.Contains(logText, secret) || strings.Contains(logText, "https://example.com/path") {
		t.Fatalf("log leaked secret or full URL: %q", logText)
	}
	if !strings.Contains(logText, "[redacted]") || !strings.Contains(logText, "[url]") {
		t.Fatalf("log = %q, want redaction markers", logText)
	}
}

func TestUpcomingEventKindIncludesSteamEvents(t *testing.T) {
	cfg := defaultConfig()

	tests := []struct {
		name        string
		summary     string
		description string
		want        string
	}{
		{name: "theater", summary: "Movie Theater Release", want: "theater"},
		{name: "digital", summary: "Movie Digital Release", want: "digital"},
		{name: "episode", summary: "Show S01E02", want: "episode"},
		{name: "steam", summary: "Game - Steam Release", description: "Steam App ID: 12345", want: "steam"},
		{name: "steam shaped personal event", summary: "Game - Steam Release", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upcomingEventKind(tt.summary, tt.description, cfg); got != tt.want {
				t.Fatalf("upcomingEventKind(%q, %q) = %q, want %q", tt.summary, tt.description, got, tt.want)
			}
		})
	}
}

func TestTargetCleanupModeDefaultsSourceCleanupToAll(t *testing.T) {
	if got := targetCleanupMode("", []string{"steam"}); got != "all" {
		t.Fatalf("targetCleanupMode blank with source = %q, want all", got)
	}
	if got := targetCleanupMode("future", []string{"steam"}); got != "future" {
		t.Fatalf("targetCleanupMode explicit future = %q, want future", got)
	}
	if got := targetCleanupMode("", nil); got != "" {
		t.Fatalf("targetCleanupMode blank without source = %q, want blank", got)
	}
}

func assertJSONFailure(t *testing.T, body string) {
	t.Helper()
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("response is not JSON: %v; body=%s", err, body)
	}
	if got["ok"] != false {
		t.Fatalf("response = %#v, want ok false", got)
	}
	if got["message"] == "" {
		t.Fatalf("response = %#v, want message", got)
	}
}
