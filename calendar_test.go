package main

import "testing"

func TestCleanupEventSourceClassifiesConfiguredEvents(t *testing.T) {
	cfg := defaultConfig()

	tests := []struct {
		name        string
		summary     string
		description string
		want        string
	}{
		{name: "theater movie", summary: "Mortal Kombat Theater Release", want: "radarr"},
		{name: "digital movie", summary: "Mortal Kombat Digital Release", want: "radarr"},
		{name: "episode", summary: "FROM S04E03", want: "sonarr"},
		{name: "steam game", summary: "Hollow Knight - Steam Release", description: "Steam App ID: 367520", want: "steam"},
		{name: "steam-shaped personal event without app id", summary: "Hollow Knight - Steam Release", want: ""},
		{name: "personal", summary: "Dentist", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanupEventSource(tt.summary, tt.description, cfg); got != tt.want {
				t.Fatalf("cleanupEventSource(%q, %q) = %q, want %q", tt.summary, tt.description, got, tt.want)
			}
		})
	}
}

func TestSourceSetIncludesSteamWithoutMatchingOtherSources(t *testing.T) {
	set := sourceSet([]string{"steam"})

	if _, ok := set["steam"]; !ok {
		t.Fatal("expected steam in source set")
	}
	if _, ok := set["radarr"]; ok {
		t.Fatal("did not expect radarr in steam-only source set")
	}
	if _, ok := set["sonarr"]; ok {
		t.Fatal("did not expect sonarr in steam-only source set")
	}
}

func TestNormalizeCleanupMode(t *testing.T) {
	tests := map[string]string{
		"":       "past",
		"past":   "past",
		"future": "future",
		"all":    "all",
		"bad":    "past",
		" ALL ":  "all",
	}

	for input, want := range tests {
		if got := normalizeCleanupMode(input); got != want {
			t.Fatalf("normalizeCleanupMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCleanupEventKindClassifiesRadarrSubtypes(t *testing.T) {
	cfg := defaultConfig()

	tests := []struct {
		summary string
		want    string
	}{
		{summary: "Mortal Kombat Theater Release", want: "radarr_theater"},
		{summary: "Mortal Kombat Digital Release", want: "radarr_digital"},
		{summary: "FROM S04E03", want: "sonarr"},
		{summary: "Hollow Knight - Steam Release", want: ""},
	}

	for _, tt := range tests {
		if got := cleanupEventKind(tt.summary, cfg); got != tt.want {
			t.Fatalf("cleanupEventKind(%q) = %q, want %q", tt.summary, got, tt.want)
		}
	}
}
