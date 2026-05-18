package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDefaultConfigReleaseOffsetsAndRadarrTracking(t *testing.T) {
	cfg := defaultConfig()

	if cfg.RadarrTheaterDayOffset != 0 {
		t.Fatalf("radarr theater offset = %d, want 0", cfg.RadarrTheaterDayOffset)
	}
	if cfg.RadarrDigitalDayOffset != 0 {
		t.Fatalf("radarr digital offset = %d, want 0", cfg.RadarrDigitalDayOffset)
	}
	if cfg.SonarrDayOffset != 0 {
		t.Fatalf("sonarr offset = %d, want 0", cfg.SonarrDayOffset)
	}
	if !cfg.RadarrTrackTheater || !cfg.RadarrTrackDigital {
		t.Fatalf("radarr tracking = theater:%v digital:%v, want both true", cfg.RadarrTrackTheater, cfg.RadarrTrackDigital)
	}
	if cfg.WebBindAddress != "127.0.0.1" {
		t.Fatalf("web bind address = %q, want 127.0.0.1", cfg.WebBindAddress)
	}
	if cfg.PushoverOnUpdate {
		t.Fatal("PushoverOnUpdate = true, want false by default")
	}
}

func TestNormalizeWebBindAddress(t *testing.T) {
	tests := map[string]string{
		"":          "127.0.0.1",
		"localhost": "127.0.0.1",
		"127.0.0.1": "127.0.0.1",
		"0.0.0.0":   "0.0.0.0",
		"::":        "0.0.0.0",
		"1.2.3.4":   "127.0.0.1",
	}
	for input, want := range tests {
		if got := normalizeWebBindAddress(input); got != want {
			t.Fatalf("normalizeWebBindAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizedIgnoredShowsFile(t *testing.T) {
	tests := map[string]string{
		"":                         "ignored_shows.json",
		"  ":                       "ignored_shows.json",
		"ignored_shows.json":       "ignored_shows.json",
		"my_list.json":             "my_list.json",
		`C:\Windows\Temp\evil.txt`: "ignored_shows.json",
		"/etc/passwd":              "ignored_shows.json",
		`..\..\evil.txt`:           "ignored_shows.json",
		"../etc/passwd":            "ignored_shows.json",
		"sub/dir/file.json":        "ignored_shows.json",
		`sub\dir\file.json`:        "ignored_shows.json",
		".":                        "ignored_shows.json",
		"..":                       "ignored_shows.json",
	}
	for input, want := range tests {
		if got := sanitizedIgnoredShowsFile(input); got != want {
			t.Fatalf("sanitizedIgnoredShowsFile(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeCalendarTargetsMigratesLegacyCalendarID(t *testing.T) {
	cfg := Config{CalendarID: "movies", UseRadarr: true, UseSonarr: false}

	normalizeCalendarTargets(&cfg)

	if len(cfg.CalendarTargets) != 1 {
		t.Fatalf("target count = %d, want 1", len(cfg.CalendarTargets))
	}
	target := cfg.CalendarTargets[0]
	if target.ID != "movies" {
		t.Fatalf("target ID = %q, want movies", target.ID)
	}
	if !target.RadarrEnabled || target.SonarrEnabled {
		t.Fatalf("target toggles = radarr:%v sonarr:%v, want radarr:true sonarr:false", target.RadarrEnabled, target.SonarrEnabled)
	}
	if cfg.CalendarID != "movies" {
		t.Fatalf("legacy calendar ID = %q, want movies", cfg.CalendarID)
	}
}

func TestNormalizeCalendarTargetsDefaultsBlankConfigToPrimary(t *testing.T) {
	cfg := Config{UseRadarr: true, UseSonarr: true}

	normalizeCalendarTargets(&cfg)

	if len(cfg.CalendarTargets) != 1 || cfg.CalendarTargets[0].ID != "primary" {
		t.Fatalf("targets = %#v, want one primary target", cfg.CalendarTargets)
	}
}

func TestNormalizeCalendarTargetsRemovesBlankDuplicateAndCapsAtFive(t *testing.T) {
	cfg := Config{
		CalendarTargets: []CalendarTarget{
			{ID: "one"},
			{ID: " "},
			{ID: "ONE"},
			{ID: "two"},
			{ID: "three"},
			{ID: "four"},
			{ID: "five"},
			{ID: "six"},
		},
	}

	normalizeCalendarTargets(&cfg)

	got := make([]string, len(cfg.CalendarTargets))
	for i, target := range cfg.CalendarTargets {
		got[i] = target.ID
	}
	want := []string{"one", "two", "three", "four", "five"}
	if len(got) != len(want) {
		t.Fatalf("targets = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %#v, want %#v", got, want)
		}
	}
}

func TestNormalizeCalendarTargetsKeepsValidColorsAndDropsInvalidColors(t *testing.T) {
	cfg := Config{
		CalendarTargets: []CalendarTarget{
			{ID: "movies", RadarrColorID: "9", SonarrColorID: "99", SteamColorID: "10"},
		},
	}

	normalizeCalendarTargets(&cfg)

	target := cfg.CalendarTargets[0]
	if target.RadarrColorID != "9" {
		t.Fatalf("radarr color = %q, want 9", target.RadarrColorID)
	}
	if target.SonarrColorID != "" {
		t.Fatalf("sonarr color = %q, want empty", target.SonarrColorID)
	}
	if target.SteamColorID != "10" {
		t.Fatalf("steam color = %q, want 10", target.SteamColorID)
	}
}

func TestCalendarTargetsForSourceRoutesByToggle(t *testing.T) {
	targets := []CalendarTarget{
		{ID: "radarr", RadarrEnabled: true},
		{ID: "sonarr", SonarrEnabled: true},
		{ID: "both", RadarrEnabled: true, SonarrEnabled: true},
	}

	radarr := calendarTargetsForSource(targets, "radarr")
	sonarr := calendarTargetsForSource(targets, "sonarr")

	if got := targetIDs(radarr); len(got) != 2 || got[0] != "radarr" || got[1] != "both" {
		t.Fatalf("radarr targets = %#v, want radarr and both", got)
	}
	if got := targetIDs(sonarr); len(got) != 2 || got[0] != "sonarr" || got[1] != "both" {
		t.Fatalf("sonarr targets = %#v, want sonarr and both", got)
	}
}

func TestNormalizeCalendarTargetsEnablesFirstExistingTargetWhenSteamGloballyEnabled(t *testing.T) {
	cfg := Config{
		UseSteam: true,
		CalendarTargets: []CalendarTarget{
			{ID: "movies", RadarrEnabled: true},
			{ID: "shows", SonarrEnabled: true},
		},
	}

	normalizeCalendarTargets(&cfg)

	if !cfg.CalendarTargets[0].SteamEnabled {
		t.Fatalf("first target SteamEnabled = false, want true")
	}
	if cfg.CalendarTargets[1].SteamEnabled {
		t.Fatalf("second target SteamEnabled = true, want unchanged false")
	}
}

func TestNormalizeCalendarTargetsKeepsExistingSteamTarget(t *testing.T) {
	cfg := Config{
		UseSteam: true,
		CalendarTargets: []CalendarTarget{
			{ID: "movies"},
			{ID: "steam", SteamEnabled: true},
		},
	}

	normalizeCalendarTargets(&cfg)

	if cfg.CalendarTargets[0].SteamEnabled {
		t.Fatalf("first target SteamEnabled = true, want unchanged false")
	}
	if !cfg.CalendarTargets[1].SteamEnabled {
		t.Fatalf("second target SteamEnabled = false, want preserved true")
	}
}

func TestSourceSetDefaultsToAllCleanupSources(t *testing.T) {
	set := sourceSet(nil)

	if _, ok := set["radarr"]; !ok {
		t.Fatal("expected radarr in default source set")
	}
	if _, ok := set["sonarr"]; !ok {
		t.Fatal("expected sonarr in default source set")
	}
	if _, ok := set["steam"]; !ok {
		t.Fatal("expected steam in default source set")
	}
}

func targetIDs(targets []CalendarTarget) []string {
	ids := make([]string, len(targets))
	for i, target := range targets {
		ids[i] = target.ID
	}
	return ids
}

// Restoring a backup that omits default-true booleans must preserve those
// defaults, matching loadConfig() behavior. handleRestore unmarshals onto a
// defaultConfig() base for this reason.
func TestRestoreBaseUnmarshalPreservesDefaultTrueBooleans(t *testing.T) {
	partial := []byte(`{"radarr_api_key":"k","sonarr_api_key":"k","calendar_id":"primary"}`)

	cfg := defaultConfig()
	if err := json.Unmarshal(partial, &cfg); err != nil {
		t.Fatalf("unmarshal onto defaults failed: %v", err)
	}
	normalizeLoadedConfig(&cfg)

	checks := map[string]bool{
		"UseRadarr":          cfg.UseRadarr,
		"UseSonarr":          cfg.UseSonarr,
		"RadarrTrackTheater": cfg.RadarrTrackTheater,
		"RadarrTrackDigital": cfg.RadarrTrackDigital,
		"UsePushover":        cfg.UsePushover,
		"PushoverOnAdded":    cfg.PushoverOnAdded,
		"PushoverOnUpdated":  cfg.PushoverOnUpdated,
	}
	for name, got := range checks {
		if !got {
			t.Errorf("%s = false after restore-style unmarshal, want true", name)
		}
	}
}

func TestNormalizeLoadedConfigClearsLegacySteamAPIKey(t *testing.T) {
	cfg := defaultConfig()
	cfg.SteamAPIKey = "legacy-key"

	normalizeLoadedConfig(&cfg)

	if cfg.SteamAPIKey != "" {
		t.Fatalf("SteamAPIKey = %q, want cleared during config normalization", cfg.SteamAPIKey)
	}
}

func TestSaveConfigDoesNotPersistLegacySteamAPIKey(t *testing.T) {
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = oldDataDir })

	cfg := defaultConfig()
	cfg.SteamAPIKey = "legacy-key"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	data, err := os.ReadFile(dataPath(configFile))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved map[string]interface{}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if _, ok := saved["steam_api_key"]; ok {
		t.Fatalf("saved config contains legacy steam_api_key: %s", data)
	}
}
