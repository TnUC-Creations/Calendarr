package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestApplySettingsFormPreservesCalendarTargetsWhenTargetsAbsent(t *testing.T) {
	cfg := defaultConfig()
	cfg.CalendarTargets = []CalendarTarget{{ID: "movies", Name: "Movies", RadarrEnabled: true}}

	form := url.Values{
		"use_radarr":                {"on"},
		"use_sonarr":                {"on"},
		"radarr_url":                {"http://radarr/api/v3"},
		"radarr_api_key":            {"radarr-key"},
		"sonarr_url":                {"http://sonarr/api/v3"},
		"sonarr_api_key":            {"sonarr-key"},
		"radarr_track_theater":      {"on"},
		"radarr_track_digital":      {"on"},
		"run_interval_hours":        {"6"},
		"web_port":                  {"5000"},
		"radarr_theater_day_offset": {"0"},
		"radarr_digital_day_offset": {"0"},
		"sonarr_day_offset":         {"0"},
		"max_log_files":             {"30"},
		"max_history_entries":       {"2000"},
		"movie_theater_template":    {"{title} Theater Release"},
		"movie_digital_template":    {"{title} Digital Release"},
		"episode_template":          {"{title} S{season:02d}E{episode:02d}"},
	}
	req := httptest.NewRequest("POST", "/api/settings/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := parseSettingsRequest(req); err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}

	applySettingsForm(&cfg, req)

	if len(cfg.CalendarTargets) != 1 || cfg.CalendarTargets[0].ID != "movies" {
		t.Fatalf("calendar targets = %#v, want original target preserved", cfg.CalendarTargets)
	}
}

func TestApplySettingsFormPersistsPushoverUpdateAvailable(t *testing.T) {
	cfg := defaultConfig()
	form := url.Values{
		"pushover_on_update_available": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/settings/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := parseSettingsRequest(req); err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}

	applySettingsForm(&cfg, req)

	if !cfg.PushoverOnUpdate {
		t.Fatal("PushoverOnUpdate = false, want true")
	}
}

func TestApplySettingsFormIgnoresLegacySteamAPIKey(t *testing.T) {
	cfg := defaultConfig()
	cfg.SteamAPIKey = "old-key"
	form := url.Values{
		"steam_id":      {"76561198000000001"},
		"steam_api_key": {"submitted-key"},
	}
	req := httptest.NewRequest("POST", "/api/settings/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := parseSettingsRequest(req); err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}

	applySettingsForm(&cfg, req)

	if cfg.SteamAPIKey != "" {
		t.Fatalf("SteamAPIKey = %q, want ignored and cleared", cfg.SteamAPIKey)
	}
	if cfg.SteamID != "76561198000000001" {
		t.Fatalf("SteamID = %q, want submitted Steam64 ID", cfg.SteamID)
	}
}

func TestParseSettingsRequestParsesMultipartAutosave(t *testing.T) {
	body := strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"radarr_url\"\r\n\r\nhttp://radarr/api/v3\r\n--x--\r\n")
	req := httptest.NewRequest("POST", "/api/settings/save", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")

	if err := parseSettingsRequest(req); err != nil {
		t.Fatalf("parseSettingsRequest: %v", err)
	}
	if got := req.FormValue("radarr_url"); got != "http://radarr/api/v3" {
		t.Fatalf("radarr_url = %q, want parsed multipart value", got)
	}
}
