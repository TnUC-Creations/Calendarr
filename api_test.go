package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

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
