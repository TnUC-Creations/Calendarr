package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// dataDir is the root directory for all data files (config, logs, history, etc.).
// Set by the --data CLI flag; empty means use the current working directory.
var dataDir string

// dataPath joins rel to dataDir. Absolute paths are returned unchanged.
func dataPath(rel string) string {
	if filepath.IsAbs(rel) || dataDir == "" {
		return rel
	}
	return filepath.Join(dataDir, rel)
}

// sanitizedIgnoredShowsFile constrains the ignored shows filename to a single
// safe basename inside the data directory. It rejects absolute paths, traversal
// segments, and any embedded path separator. Anything suspicious falls back to
// the default name.
func sanitizedIgnoredShowsFile(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "ignored_shows.json"
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "ignored_shows.json"
	}
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "..") {
		return "ignored_shows.json"
	}
	return cleaned
}

// Config mirrors config.json exactly. Bool fields default to true via defaultConfig()
// so that missing keys in an existing config.json are handled gracefully.
type Config struct {
	UseRadarr    bool   `json:"use_radarr"`
	RadarrURL    string `json:"radarr_url"`
	RadarrAPIKey string `json:"radarr_api_key"`

	UseSonarr    bool   `json:"use_sonarr"`
	SonarrURL    string `json:"sonarr_url"`
	SonarrAPIKey string `json:"sonarr_api_key"`

	RadarrTheaterDayOffset int  `json:"radarr_theater_day_offset"`
	RadarrDigitalDayOffset int  `json:"radarr_digital_day_offset"`
	SonarrDayOffset        int  `json:"sonarr_day_offset"`
	RadarrTrackTheater     bool `json:"radarr_track_theater"`
	RadarrTrackDigital     bool `json:"radarr_track_digital"`

	CalendarID         string           `json:"calendar_id"`
	CalendarTargets    []CalendarTarget `json:"calendar_targets"`
	GoogleRefreshToken string           `json:"google_refresh_token"`

	PushoverToken     string `json:"pushover_app_token"`
	PushoverUser      string `json:"pushover_user_key"`
	PushoverSound     string `json:"pushover_sound"`
	UsePushover       bool   `json:"use_pushover"`
	PushoverOnAdded   bool   `json:"pushover_on_added"`
	PushoverOnUpdated bool   `json:"pushover_on_updated"`
	PushoverOnDeleted bool   `json:"pushover_on_deleted"`
	PushoverOnError   bool   `json:"pushover_on_error"`

	RunIntervalHours float64 `json:"run_interval_hours"`
	WebPort          int     `json:"web_port"`
	WebBindAddress   string  `json:"web_bind_address"`
	SyncOnStart      bool    `json:"sync_on_start"`
	ServiceName      string  `json:"service_name"`

	IgnoredShowsFile     string `json:"ignored_shows_file"`
	MovieTheaterTemplate string `json:"movie_theater_template"`
	MovieDigitalTemplate string `json:"movie_digital_template"`
	EpisodeTemplate      string `json:"episode_template"`

	MaxLogFiles       int  `json:"max_log_files"`
	MaxHistoryEntries int  `json:"max_history_entries"`
	AutoCleanupPast   bool `json:"auto_cleanup_past"`

	WebUIPasswordHash string `json:"web_ui_password_hash"`
}

const maxCalendarTargets = 5

type CalendarTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RadarrEnabled bool   `json:"radarr_enabled"`
	SonarrEnabled bool   `json:"sonarr_enabled"`
	RadarrColorID string `json:"radarr_color_id"`
	SonarrColorID string `json:"sonarr_color_id"`
}

func defaultConfig() Config {
	return Config{
		UseRadarr:            true,
		RadarrURL:            "http://localhost:7878/api/v3",
		UseSonarr:            true,
		SonarrURL:            "http://localhost:8989/api/v3",
		RadarrTrackTheater:   true,
		RadarrTrackDigital:   true,
		UsePushover:          true,
		PushoverSound:        "intermission",
		PushoverOnAdded:      true,
		PushoverOnUpdated:    true,
		PushoverOnDeleted:    false,
		SyncOnStart:          false,
		RunIntervalHours:     6,
		WebPort:              5000,
		WebBindAddress:       "127.0.0.1",
		IgnoredShowsFile:     "ignored_shows.json",
		MovieTheaterTemplate: "{title} Theater Release",
		MovieDigitalTemplate: "{title} Digital Release",
		EpisodeTemplate:      "{title} S{season:02d}E{episode:02d}",
		MaxLogFiles:          30,
		MaxHistoryEntries:    2000,
	}
}

func normalizeCalendarTargets(cfg *Config) {
	seen := make(map[string]struct{})
	targets := make([]CalendarTarget, 0, maxCalendarTargets)
	for _, t := range cfg.CalendarTargets {
		t.ID = strings.TrimSpace(t.ID)
		t.Name = strings.TrimSpace(t.Name)
		t.RadarrColorID = normalizeCalendarColorID(t.RadarrColorID)
		t.SonarrColorID = normalizeCalendarColorID(t.SonarrColorID)
		if t.ID == "" {
			continue
		}
		key := strings.ToLower(t.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, t)
		if len(targets) == maxCalendarTargets {
			break
		}
	}
	if len(targets) == 0 {
		id := strings.TrimSpace(cfg.CalendarID)
		if id == "" {
			id = "primary"
		}
		targets = append(targets, CalendarTarget{
			ID:            id,
			RadarrEnabled: cfg.UseRadarr,
			SonarrEnabled: cfg.UseSonarr,
		})
	}
	cfg.CalendarTargets = targets
	cfg.CalendarID = targets[0].ID
}

func normalizeCalendarColorID(id string) string {
	id = strings.TrimSpace(id)
	switch id {
	case "", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11":
		return id
	default:
		return ""
	}
}

func calendarTargets(cfg Config) []CalendarTarget {
	normalizeCalendarTargets(&cfg)
	return cfg.CalendarTargets
}

func calendarTargetsForSource(targets []CalendarTarget, source string) []CalendarTarget {
	filtered := make([]CalendarTarget, 0, len(targets))
	for _, target := range targets {
		switch source {
		case "radarr":
			if target.RadarrEnabled {
				filtered = append(filtered, target)
			}
		case "sonarr":
			if target.SonarrEnabled {
				filtered = append(filtered, target)
			}
		}
	}
	return filtered
}

// loadConfig reads config.json on top of defaults so missing bool keys stay true.
func loadConfig() (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(dataPath(configFile))
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	// Guard against zero values that would break the scheduler or web server.
	if cfg.RunIntervalHours <= 0 {
		cfg.RunIntervalHours = 6
	}
	if cfg.WebPort <= 0 {
		cfg.WebPort = 5000
	}
	cfg.WebBindAddress = normalizeWebBindAddress(cfg.WebBindAddress)
	cfg.IgnoredShowsFile = sanitizedIgnoredShowsFile(cfg.IgnoredShowsFile)
	if cfg.MovieTheaterTemplate == "" {
		cfg.MovieTheaterTemplate = "{title} Theater Release"
	}
	if cfg.MovieDigitalTemplate == "" {
		cfg.MovieDigitalTemplate = "{title} Digital Release"
	}
	if cfg.EpisodeTemplate == "" {
		cfg.EpisodeTemplate = "{title} S{season:02d}E{episode:02d}"
	}
	if cfg.MaxLogFiles <= 0 {
		cfg.MaxLogFiles = 30
	}
	if cfg.MaxHistoryEntries <= 0 {
		cfg.MaxHistoryEntries = 2000
	}
	normalizeCalendarTargets(&cfg)
	return cfg, nil
}

func saveConfig(cfg Config) error {
	cfg.WebBindAddress = normalizeWebBindAddress(cfg.WebBindAddress)
	cfg.IgnoredShowsFile = sanitizedIgnoredShowsFile(cfg.IgnoredShowsFile)
	normalizeCalendarTargets(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dataPath(configFile), data, 0644)
}

func normalizeWebBindAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	switch addr {
	case "0.0.0.0", "::":
		return "0.0.0.0"
	case "", "localhost", "127.0.0.1":
		return "127.0.0.1"
	default:
		return "127.0.0.1"
	}
}
