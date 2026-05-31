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

func TestSettingsTemplateRendersPublicHealthFeedToggle(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := SettingsData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "settings",
		},
		Config: Config{PublicHealthFeed: true, DetailedHealthFeed: true, HealthFeedToken: "feed-token"},
	}
	if err := pageTemplates["settings"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `name="public_health_feed"`) || !strings.Contains(html, `id="public_health_feed"`) {
		t.Fatal("settings template did not render public health feed toggle")
	}
	if !strings.Contains(html, `name="detailed_health_feed"`) || !strings.Contains(html, `id="detailed_health_feed"`) {
		t.Fatal("settings template did not render detailed health feed toggle")
	}
	if !strings.Contains(html, `Detailed feed entries can contain movie, show, and game titles`) {
		t.Fatal("settings template should explain the detailed health feed risk")
	}
	if !strings.Contains(html, `value="feed-token"`) {
		t.Fatal("settings template should render the authenticated detailed feed token")
	}
}

func TestSettingsTemplateRendersHealthFeedCopyAndTestControls(t *testing.T) {
	src, err := os.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`id="health-feed-copy-btn"`,
		`onclick="copyHealthFeedToken()"`,
		`id="health-feed-test-btn"`,
		`onclick="testHealthFeed()"`,
		`id="health-feed-status"`,
		`headers: {'X-Calendarr-Feed-Token': token}`,
		`fetch('/health/feed'`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings template missing health feed control wiring %q", want)
		}
	}
	if strings.Contains(html, `/health/feed?token`) {
		t.Fatal("settings feed test should not put the token in the URL")
	}
}

func TestSettingsTemplateAutosavesPublicHealthFeedToggle(t *testing.T) {
	src, err := os.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings template: %v", err)
	}
	html := string(src)
	if !strings.Contains(html, `!['public_health_feed', 'detailed_health_feed'].includes(target.id)`) {
		t.Fatal("Security & Backup autosave exception should allow health feed changes")
	}
}

func TestSettingsTemplateUnloadAutosaveIncludesCSRFFormField(t *testing.T) {
	src, err := os.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`<form method="post" action="/settings" id="settings-form" novalidate>`,
		`<input type="hidden" name="_csrf" value="{{.CSRFToken}}">`,
		`const body = new URLSearchParams(new FormData(settingsForm)).toString();`,
		`navigator.sendBeacon('/api/settings/save', blob);`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings unload autosave missing CSRF-safe form submission wiring %q", want)
		}
	}
}

func TestSettingsTemplateGeneralFieldsUseTopAlignment(t *testing.T) {
	src, err := os.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings template: %v", err)
	}
	html := string(src)
	if !strings.Contains(html, `<div class="row g-3 align-items-start">`) {
		t.Fatal("General settings fields should use top alignment")
	}
	if !strings.Contains(html, `<div class="col-md-3 pb-1 pt-md-4">`) {
		t.Fatal("General settings switches should use a desktop offset")
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

func TestSettingsTemplateDoesNotRenderSteamAPIKey(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := SettingsData{
		PageBase: PageBase{
			CSRFToken:   "test-token",
			CurrentPage: "settings",
		},
		Config: Config{SteamAPIKey: "legacy-key"},
	}
	if err := pageTemplates["settings"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, blocked := range []string{`name="steam_api_key"`, `Steam API Key`, `legacy-key`, `steamcommunity.com/id/`} {
		if strings.Contains(html, blocked) {
			t.Fatalf("settings template exposed unsupported Steam API key or vanity URL text %q", blocked)
		}
	}
	if !strings.Contains(html, `steamcommunity.com/profiles/`) {
		t.Fatal("settings template should point users to /profiles/<Steam64> URLs")
	}
}

func TestSettingsTemplateRendersSteamColorControl(t *testing.T) {
	src, err := os.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`data-target-{{$i}}-steam-color="{{$t.SteamColorID}}"`,
		`calendar_target_steam_color_${i}`,
		`steam_color_id: row.steamColor`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings template missing Steam color wiring %q", want)
		}
	}
}

func TestSettingsTemplateTargetSourceCleanupUsesAllMode(t *testing.T) {
	src, err := os.ReadFile("templates/settings.html")
	if err != nil {
		t.Fatalf("read settings template: %v", err)
	}
	html := string(src)
	if !strings.Contains(html, `body: JSON.stringify({ calendar_id: calendarID, mode: 'all', sources })`) {
		t.Fatal("calendar target source cleanup should request all-mode cleanup")
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

func TestDashboardScheduleShowsSteamBadge(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := DashboardData{
		PageBase: PageBase{CSRFToken: "test-token", CurrentPage: "dashboard"},
		Config:   Config{UseSteam: true},
	}
	if err := pageTemplates["dashboard"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if !strings.Contains(html, `<span class="badge bg-success">Steam</span>`) {
		t.Fatal("dashboard schedule should show a Steam badge when Steam is enabled")
	}
}

func TestDashboardTemplateRendersSourceHealthCards(t *testing.T) {
	src, err := os.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatalf("read dashboard template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`id="source-health-body"`,
		`id="source-health-refresh"`,
		`fetch('/api/source-health')`,
		`function renderSourceHealth`,
		`${escHtml(source.name)}`,
		`${escHtml(source.message)}`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard template missing source health wiring %q", want)
		}
	}
}

func TestSetupChecklistTracksRequiredItems(t *testing.T) {
	cfg := defaultConfig()
	cfg.GoogleRefreshToken = ""
	cfg.UseRadarr = true
	cfg.RadarrURL = "http://radarr/api/v3"
	cfg.RadarrAPIKey = "radarr-key"
	cfg.UseSonarr = false
	cfg.UseSteam = false
	cfg.CalendarTargets = []CalendarTarget{{ID: "primary", RadarrEnabled: true}}

	items, complete, done, required := setupChecklist(cfg)

	if len(items) != 6 {
		t.Fatalf("items = %#v, want six checklist items", items)
	}
	if complete {
		t.Fatal("checklist complete = true, want false without Google connection")
	}
	if done != 2 || required != 3 {
		t.Fatalf("done/required = %d/%d, want 2/3", done, required)
	}
}

func TestDashboardTemplateRendersSetupChecklist(t *testing.T) {
	loadTemplates()
	var out bytes.Buffer
	data := DashboardData{
		PageBase: PageBase{CSRFToken: "test-token", CurrentPage: "dashboard"},
		SetupChecklist: []SetupChecklistItem{
			{Label: "Connect Google Calendar", Detail: "Required for calendar writes.", Href: "/settings#calendar"},
			{Label: "Set up Pushover", Detail: "Optional sync notifications.", Optional: true, Href: "/settings#pushover"},
		},
		SetupChecklistDone:     0,
		SetupChecklistRequired: 1,
	}
	if err := pageTemplates["dashboard"].ExecuteTemplate(&out, "layout", data); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{
		`id="setup-checklist-card"`,
		`Setup Checklist`,
		`0/1 required`,
		`Connect Google Calendar`,
		`Set up Pushover`,
		`calendarr-setup-checklist-dismissed`,
		`function dismissSetupChecklist()`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard template missing setup checklist rendering %q", want)
		}
	}
}

func TestLogsTemplateRendersSearchAndFilters(t *testing.T) {
	src, err := os.ReadFile("templates/logs.html")
	if err != nil {
		t.Fatalf("read logs template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`id="log-search"`,
		`id="log-severity-filter"`,
		`id="log-source-filter"`,
		`id="log-filter-count"`,
		`function applyLogFilters()`,
		`function logSeverity(block)`,
		`function logSource(block)`,
		`No log entries match the current filters.`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("logs template missing search/filter wiring %q", want)
		}
	}
}

func TestLogsTemplateKeepsFilteredLogsAsText(t *testing.T) {
	src, err := os.ReadFile("templates/logs.html")
	if err != nil {
		t.Fatalf("read logs template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`el.textContent = filtered.length ?`,
		`rawLogContent = data.content ||`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("logs template should render filtered log content as text, missing %q", want)
		}
	}
	if strings.Contains(html, `el.innerHTML`) {
		t.Fatal("logs template must not render log content with innerHTML")
	}
}

func TestUpcomingTemplateSupportsSteamAndEscapesAPIText(t *testing.T) {
	src, err := os.ReadFile("templates/upcoming.html")
	if err != nil {
		t.Fatalf("read upcoming template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`id="filter-steam"`,
		`onclick="setFilter('steam')"`,
		`if (kind === 'steam')`,
		`${escapeHTML(e.title)}`,
		`${escapeHTML(e.calendar)}`,
		`${escapeHTML(data.error)}`,
		`${escapeHTML(err)}`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("upcoming template missing safe Steam rendering %q", want)
		}
	}
	for _, blocked := range []string{
		`${e.title}`,
		`${e.calendar}`,
		`${data.error}`,
		`Failed to load: ${err}`,
	} {
		if strings.Contains(html, blocked) {
			t.Fatalf("upcoming template renders API text without escaping: %q", blocked)
		}
	}
}

func TestUpcomingTemplateSupportsSavedFilterAndExports(t *testing.T) {
	src, err := os.ReadFile("templates/upcoming.html")
	if err != nil {
		t.Fatalf("read upcoming template: %v", err)
	}
	html := string(src)
	for _, want := range []string{
		`const UPCOMING_FILTER_KEY = 'calendarr-upcoming-filter'`,
		`localStorage.setItem(UPCOMING_FILTER_KEY, f)`,
		`function filteredUpcomingEvents()`,
		`function exportUpcoming(format)`,
		`function csvCell(value)`,
		`downloadText(`,
		`onclick="exportUpcoming('csv')"`,
		`onclick="exportUpcoming('json')"`,
		`JSON.stringify(rows, null, 2)`,
		`header.map(key => csvCell(row[key])).join(',')`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("upcoming template missing saved filter/export wiring %q", want)
		}
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
