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

func TestSettingsTemplateDisablesLANAccessWithoutPassword(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := SettingsData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "settings",
		},
		Config: Config{WebBindAddress: "127.0.0.1"},
	}
	if err := pageTemplates["settings"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `value="0.0.0.0"  disabled`) {
		t.Fatal("Local network option should be disabled when no Web UI password is set")
	}
	if !strings.Contains(html, `Set a Web UI password before enabling Local network access.`) {
		t.Fatal("settings template should explain that a password is required before Local network access")
	}
	if !strings.Contains(html, `Restart the Calendarr service after saving`) {
		t.Fatal("settings template should show restart warning copy for Web UI access changes")
	}
}

func TestSettingsTemplateAllowsLANAccessWithPassword(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := SettingsData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "settings",
		},
		Config: Config{WebBindAddress: "127.0.0.1", WebUIPasswordHash: "hash"},
	}
	if err := pageTemplates["settings"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, `Local network - set password first`) {
		t.Fatal("Local network option should not show password-required suffix when a password is set")
	}
}

func TestLayoutGoogleCalendarBannerLinksToCalendarTab(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := DashboardData{
		PageBase: PageBase{
			CSRFToken:         "test-token",
			CurrentPage:       "dashboard",
			CalendarConnected: false,
		},
		Config: Config{},
	}
	if err := pageTemplates["dashboard"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `href="/settings#calendar"`) {
		t.Fatal("Google Calendar banner should link to /settings#calendar")
	}
	if strings.Contains(html, `href="/settings#google-calendar-card"`) {
		t.Fatal("Google Calendar banner should not link to the old card anchor")
	}
}

func TestSettingsTemplateSupportsCalendarHashTab(t *testing.T) {
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
	for _, want := range []string{
		`'#calendar':        '#tab-calendar'`,
		`bootstrap.Tab.getOrCreateInstance(btn).show()`,
		`window.location.hash = 'calendar'`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings template missing hash-tab support %q", want)
		}
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
		`Start this connection from a browser on the Calendarr server.`,
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
	data := DashboardData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "dashboard",
		},
		Config: Config{CalendarTargets: []CalendarTarget{
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

func TestDashboardServiceBadgesRenderExternalLinksForValidURLs(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	cfg := Config{
		UseRadarr: true,
		RadarrURL: "http://radarr.local:7878/api/v3",
		UseSonarr: true,
		SonarrURL: "https://media.example.com/sonarr/api/v3",
	}
	data := DashboardData{
		PageBase:     PageBase{CSRFToken: "test-token", CurrentPage: "dashboard"},
		Config:       cfg,
		RadarrAppURL: serviceAppURL(cfg.RadarrURL),
		SonarrAppURL: serviceAppURL(cfg.SonarrURL),
	}
	if err := pageTemplates["dashboard"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`href="http://radarr.local:7878/" target="_blank" rel="noopener"`,
		`href="https://media.example.com/sonarr/" target="_blank" rel="noopener"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard missing service app link %q", want)
		}
	}
}

func TestDashboardServiceBadgesStayNonClickableForInvalidURLs(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	cfg := Config{
		UseRadarr: true,
		RadarrURL: "javascript:alert(1)",
	}
	data := DashboardData{
		PageBase:     PageBase{CSRFToken: "test-token", CurrentPage: "dashboard"},
		Config:       cfg,
		RadarrAppURL: serviceAppURL(cfg.RadarrURL),
	}
	if err := pageTemplates["dashboard"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, `href="javascript:alert`) {
		t.Fatal("dashboard rendered an unsafe Radarr link")
	}
	if !strings.Contains(html, `<span class="badge bg-warning text-dark">Radarr</span>`) {
		t.Fatal("dashboard should render invalid Radarr URLs as a non-clickable badge")
	}
}

func TestServiceAppURLStripsAPIPath(t *testing.T) {
	tests := map[string]string{
		"http://localhost:7878/api/v3":            "http://localhost:7878/",
		"https://media.example.com/sonarr/api/v3": "https://media.example.com/sonarr/",
		"http://localhost:8989/":                  "http://localhost:8989/",
		"javascript:alert(1)":                     "",
		"http://":                                 "",
		"not a url":                               "",
	}
	for raw, want := range tests {
		if got := serviceAppURL(raw); got != want {
			t.Fatalf("serviceAppURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
