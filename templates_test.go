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

func TestSettingsTemplateRendersAllFiveTabs(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := SettingsData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "settings",
		},
		Config: Config{},
	}
	if err := pageTemplates["settings"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()

	wantPanes := []string{"tab-general", "tab-media", "tab-calendar", "tab-notifications", "tab-security-backup"}
	for _, id := range wantPanes {
		if !strings.Contains(html, `data-bs-target="#`+id+`"`) {
			t.Errorf("missing tab button targeting #%s", id)
		}
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing tab pane id %s", id)
		}
	}

	musts := []string{
		`id="settings-form"`,
		`id="settings-save-status"`,
		`name="run_interval_hours"`,
		`name="radarr_url"`,
		`name="sonarr_url"`,
		`name="pushover_on_update_available"`,
		`id="pushover_on_update_available"`,
		`name="movie_theater_template"`,
		`id="restore-form"`,
		`id="webui-set-password-btn"`,
	}
	for _, want := range musts {
		if !strings.Contains(html, want) {
			t.Errorf("settings template missing %q after tab refactor", want)
		}
	}

	// All five tab panes must share one tab-content group so Bootstrap toggles
	// them as a single set (otherwise switching to Security & Backup can leave
	// the previous pane visible).
	if got := strings.Count(html, `class="tab-content`); got != 1 {
		t.Errorf("expected exactly 1 tab-content group, found %d", got)
	}

	// All five panes must appear between the single tab-content opener and the
	// matching </form> close so Bootstrap walks them as siblings.
	tabContentIdx := strings.Index(html, `class="tab-content`)
	formCloseIdx := strings.Index(html[tabContentIdx:], `</form>`)
	if tabContentIdx < 0 || formCloseIdx < 0 {
		t.Fatal("could not locate tab-content / </form> markers in rendered settings")
	}
	group := html[tabContentIdx : tabContentIdx+formCloseIdx]
	for _, id := range wantPanes {
		if !strings.Contains(group, `id="`+id+`"`) {
			t.Errorf("pane %s is not inside the shared tab-content group", id)
		}
	}

	// Restore form must live outside settings-form (after </form>) so we don't
	// nest one form inside another.
	settingsFormCloseIdx := strings.Index(html, `</form>`)
	if !strings.Contains(html[settingsFormCloseIdx:], `id="restore-form"`) {
		t.Error("restore-form should live outside settings-form (after first </form>)")
	}
	if strings.Contains(html[:settingsFormCloseIdx], `id="restore-form"`) {
		t.Error("restore-form should not appear before the first </form> (would be a nested form)")
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
