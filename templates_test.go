package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestLoadTemplates(t *testing.T) {
	loadTemplates()
	if len(pageTemplates) == 0 {
		t.Fatal("expected templates to load")
	}
}

func TestSettingsTemplateRendersPushoverUpdateCheckbox(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := SettingsData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "settings",
		},
		Config: Config{PushoverOnUpdate: true},
	}
	if err := pageTemplates["settings"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `name="pushover_on_update_available"`) {
		t.Fatal("settings template did not render pushover_on_update_available checkbox")
	}
	if !strings.Contains(html, `id="pushover_on_update_available"`) {
		t.Fatal("settings template did not render pushover_on_update_available id")
	}
}

func TestAboutChangelogShowsFiveRecentVersions(t *testing.T) {
	data, err := os.ReadFile("templates/about.html")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), `id="older-changelog"`)
	if len(parts) < 2 {
		t.Fatal("older changelog collapse not found")
	}
	re := regexp.MustCompile(`>v\d+\.\d+\.\d+<`)
	if got := len(re.FindAllString(parts[0], -1)); got != 5 {
		t.Fatalf("visible changelog versions = %d, want 5", got)
	}
}

func TestDashboardCalendarTargetsRenderAsJavascriptStrings(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := map[string]interface{}{
		"CSRFToken":      "test-token",
		"CurrentPage":    "dashboard",
		"LastRunChanges": []string{},
		"Config": Config{CalendarTargets: []CalendarTarget{
			{ID: "movies@example.com", Name: "Movies"},
		}},
	}
	if err := pageTemplates["dashboard"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `id: "movies@example.com"`) {
		t.Fatalf("calendar ID was not rendered as a JavaScript string: %s", html)
	}
}
