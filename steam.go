package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	calendar "google.golang.org/api/calendar/v3"
)

const (
	steamRequestDelay        = 200 * time.Millisecond
	steamRateLimitSleep      = 60 * time.Second
	steamWishlistMaxBytes    = 5 << 20   // 5 MB
	steamAppDetailMaxBytes   = 512 << 10 // 512 KB
	steamWishlistTimeout     = 30 * time.Second
	steamDetailTimeout       = 15 * time.Second
	steamAppDetailsCacheFile = "steam_appdetails_cache.json"
	steamReleasedCacheTTL    = 30 * 24 * time.Hour
	steamComingSoonCacheTTL  = 24 * time.Hour
	steamFailureCacheTTL     = 6 * time.Hour
)

var (
	steamIDRe = regexp.MustCompile(`^7656\d{13}$`)
	appIDRe   = regexp.MustCompile(`^\d{1,10}$`)

	secretAssignmentRe         = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|session|csrf|oauth|refresh[_-]?token|password|user[_-]?key)=['"]?[^'"&\s<>]+`)
	longSecretRe               = regexp.MustCompile(`\b[A-Za-z0-9_-]{24,}\b`)
	gocspxRe                   = regexp.MustCompile(`GOCSPX-[A-Za-z0-9_-]+`)
	newSteamWishlistHTTPClient = func() *http.Client { return &http.Client{Timeout: steamWishlistTimeout} }
	newSteamDetailHTTPClient   = func() *http.Client { return &http.Client{Timeout: steamDetailTimeout} }
)

type steamWishlistEntry struct {
	Name string `json:"name"`
}

type steamAppDetails struct {
	Success bool `json:"success"`
	Data    struct {
		Name        string `json:"name"`
		ReleaseDate struct {
			ComingSoon bool   `json:"coming_soon"`
			Date       string `json:"date"`
		} `json:"release_date"`
		ShortDescription string `json:"short_description"`
	} `json:"data"`
}

type steamAppDetailsCache struct {
	Entries map[string]steamAppDetailsCacheEntry `json:"entries"`
}

type steamAppDetailsCacheEntry struct {
	Title            string    `json:"title,omitempty"`
	ShortDescription string    `json:"short_description,omitempty"`
	ReleaseDate      string    `json:"release_date,omitempty"`
	ComingSoon       bool      `json:"coming_soon,omitempty"`
	Success          bool      `json:"success,omitempty"`
	FetchedAt        time.Time `json:"fetched_at,omitempty"`
	FailedAt         time.Time `json:"failed_at,omitempty"`
	Status           string    `json:"status,omitempty"`
}

// resolveSteamInput normalises a raw user-provided Steam ID or profile URL.
// Only raw Steam64 IDs and steamcommunity.com/profiles/<Steam64> URLs are
// supported. Vanity /id/ URLs require Steam's keyed API and are intentionally
// rejected.
func resolveSteamInput(raw string) (string, error) {
	s := strings.TrimSpace(raw)

	if steamIDRe.MatchString(s) {
		return s, nil
	}

	lower := strings.ToLower(s)
	for _, prefix := range []string{
		"https://steamcommunity.com/profiles/",
		"http://steamcommunity.com/profiles/",
		"steamcommunity.com/profiles/",
	} {
		if strings.HasPrefix(lower, prefix) {
			id := s[len(prefix):]
			id = strings.TrimRight(id, "/")
			if idx := strings.IndexByte(id, '/'); idx >= 0 {
				id = id[:idx]
			}
			if steamIDRe.MatchString(id) {
				return id, nil
			}
			return "", fmt.Errorf("invalid Steam profile URL: /profiles/ must contain a 17-digit Steam64 ID starting with 7656")
		}
	}
	for _, prefix := range []string{
		"https://steamcommunity.com/id/",
		"http://steamcommunity.com/id/",
		"steamcommunity.com/id/",
	} {
		if strings.HasPrefix(lower, prefix) {
			return "", fmt.Errorf("unsupported Steam profile URL: enter your public 17-digit Steam64 ID or a steamcommunity.com/profiles/<Steam64> URL")
		}
	}

	return "", fmt.Errorf("invalid Steam ID or profile URL: enter a 17-digit Steam64 ID starting with 7656 or a steamcommunity.com/profiles/<Steam64> URL")
}

// resolveSteamID returns a validated Steam64 ID string.
func resolveSteamID(input string) (string, error) {
	return resolveSteamInput(input)
}

// fetchSteamWishlist returns a map from App ID string to entry. The second
// return value is the HTTP status code (used by callers to distinguish
// config-level failures from data-level failures).
func fetchSteamWishlist(client *http.Client, steamID string) (map[string]steamWishlistEntry, int, error) {
	params := url.Values{}
	params.Set("steamid", steamID)
	u := "https://api.steampowered.com/IWishlistService/GetWishlist/v1/?" + params.Encode()
	return fetchSteamWishlistURL(client, u)
}

func fetchSteamWishlistURL(client *http.Client, u string) (map[string]steamWishlistEntry, int, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("wishlist request build failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("wishlist fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode, fmt.Errorf("wishlist fetch failed (HTTP %d) — check Steam ID", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("wishlist fetch failed (HTTP %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, steamWishlistMaxBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("wishlist read failed (HTTP %d): %w", resp.StatusCode, err)
	}
	var result struct {
		Response struct {
			Items []struct {
				AppID int64 `json:"appid"`
			} `json:"items"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("wishlist parse failed (HTTP %d, content-type %q): non-JSON response prefix %q: %w",
			resp.StatusCode, sanitizedHeaderValue(resp.Header.Get("Content-Type"), 120), sanitizedBodyPrefix(body, 160), err)
	}
	wishlist := make(map[string]steamWishlistEntry, len(result.Response.Items))
	for _, item := range result.Response.Items {
		if item.AppID <= 0 {
			continue
		}
		wishlist[fmt.Sprintf("%d", item.AppID)] = steamWishlistEntry{}
	}
	return wishlist, resp.StatusCode, nil
}

func sanitizedHeaderValue(value string, max int) string {
	clean := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
	clean = strings.Join(strings.Fields(clean), " ")
	clean = redactSecretLikeText(clean)
	if clean == "" {
		return ""
	}
	runes := []rune(clean)
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

func sanitizedBodyPrefix(body []byte, max int) string {
	clean := strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, string(body))
	clean = strings.Join(strings.Fields(clean), " ")
	clean = redactSecretLikeText(clean)
	if clean == "" {
		return ""
	}
	runes := []rune(clean)
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

func redactSecretLikeText(s string) string {
	s = gocspxRe.ReplaceAllString(s, "[redacted]")
	s = secretAssignmentRe.ReplaceAllString(s, "$1=[redacted]")
	s = longSecretRe.ReplaceAllString(s, "[redacted]")
	return s
}

// fetchSteamAppDetails fetches release metadata for a single App ID.
func fetchSteamAppDetails(client *http.Client, appID string) (*steamAppDetails, error) {
	u := "https://store.steampowered.com/api/appdetails?appids=" + appID
	return fetchSteamAppDetailsURL(client, u, appID)
}

func fetchSteamAppDetailsURL(client *http.Client, u, appID string) (*steamAppDetails, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("app detail request build failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("app detail fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("app detail fetch failed (HTTP %d)", resp.StatusCode)
	}

	body := io.LimitReader(resp.Body, steamAppDetailMaxBytes)
	var wrapper map[string]steamAppDetails
	if err := json.NewDecoder(body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("app detail parse failed: %w", err)
	}
	details, ok := wrapper[appID]
	if !ok {
		return nil, fmt.Errorf("app ID %s not found in response", appID)
	}
	return &details, nil
}

func loadSteamAppDetailsCache() steamAppDetailsCache {
	cache := steamAppDetailsCache{Entries: map[string]steamAppDetailsCacheEntry{}}
	data, err := os.ReadFile(dataPath(steamAppDetailsCacheFile))
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil || cache.Entries == nil {
		return steamAppDetailsCache{Entries: map[string]steamAppDetailsCacheEntry{}}
	}
	return cache
}

func saveSteamAppDetailsCache(cache steamAppDetailsCache) error {
	if cache.Entries == nil {
		cache.Entries = map[string]steamAppDetailsCacheEntry{}
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	target := dataPath(steamAppDetailsCacheFile)
	if dir := filepath.Dir(target); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(target, 0600)
	return nil
}

func cachedSteamAppDetails(entry steamAppDetailsCacheEntry) *steamAppDetails {
	details := &steamAppDetails{Success: entry.Success}
	details.Data.Name = entry.Title
	details.Data.ShortDescription = entry.ShortDescription
	details.Data.ReleaseDate.Date = entry.ReleaseDate
	details.Data.ReleaseDate.ComingSoon = entry.ComingSoon
	return details
}

func steamAppDetailsCacheEntryFrom(details *steamAppDetails, fetchedAt time.Time) steamAppDetailsCacheEntry {
	return steamAppDetailsCacheEntry{
		Title:            details.Data.Name,
		ShortDescription: details.Data.ShortDescription,
		ReleaseDate:      details.Data.ReleaseDate.Date,
		ComingSoon:       details.Data.ReleaseDate.ComingSoon,
		Success:          details.Success,
		FetchedAt:        fetchedAt,
		Status:           "ok",
	}
}

func steamAppDetailsCacheTTL(entry steamAppDetailsCacheEntry) time.Duration {
	if !entry.FailedAt.IsZero() {
		return steamFailureCacheTTL
	}
	if entry.ComingSoon {
		return steamComingSoonCacheTTL
	}
	if _, ok := parseSteamDate(entry.ReleaseDate); !ok {
		return steamComingSoonCacheTTL
	}
	return steamReleasedCacheTTL
}

func validSteamAppDetailsCacheEntry(entry steamAppDetailsCacheEntry, now time.Time) bool {
	timestamp := entry.FetchedAt
	if !entry.FailedAt.IsZero() {
		timestamp = entry.FailedAt
	}
	if timestamp.IsZero() {
		return false
	}
	return now.Sub(timestamp) < steamAppDetailsCacheTTL(entry)
}

func fetchSteamAppDetailsCached(client *http.Client, cache *steamAppDetailsCache, appID string, now time.Time) (*steamAppDetails, bool, error) {
	if cache.Entries == nil {
		cache.Entries = map[string]steamAppDetailsCacheEntry{}
	}
	if entry, ok := cache.Entries[appID]; ok && validSteamAppDetailsCacheEntry(entry, now) {
		if !entry.FailedAt.IsZero() {
			return nil, true, fmt.Errorf("cached app detail failure: %s", entry.Status)
		}
		return cachedSteamAppDetails(entry), true, nil
	}

	details, err := fetchSteamAppDetails(client, appID)
	if err != nil {
		if isSteamRateLimitError(err) {
			return nil, false, err
		}
		cache.Entries[appID] = steamAppDetailsCacheEntry{
			FailedAt: now,
			Status:   err.Error(),
		}
		return nil, false, err
	}
	cache.Entries[appID] = steamAppDetailsCacheEntryFrom(details, now)
	return details, false, nil
}

func isSteamRateLimitError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "rate limited")
}

func steamReleaseDate(details *steamAppDetails) (dateStr string, skippedComingSoon bool, ok bool) {
	dateStr, ok = parseSteamDate(details.Data.ReleaseDate.Date)
	if ok {
		return dateStr, false, true
	}
	return "", details.Data.ReleaseDate.ComingSoon, false
}

// parseSteamDate attempts to parse Steam's free-form release date string into
// a YYYY-MM-DD string. Returns ("", false) for any vague or unparseable value.
func parseSteamDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, layout := range []string{
		"Jan 2, 2006",
		"2 Jan, 2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}

// checkSteamConnectivity resolves the Steam ID, fetches the wishlist, and
// writes human-readable [Check] lines to w. Used by checkConnectivity() and
// the /api/test/steam endpoint.
func checkSteamConnectivity(cfg Config, w io.Writer) error {
	fmt.Fprintf(w, "[Check] Resolving Steam ID...\n")
	steamID, err := resolveSteamID(cfg.SteamID)
	if err != nil {
		return fmt.Errorf("Steam ID resolution: %w", err)
	}
	fmt.Fprintf(w, "[OK] Steam ID resolved: %s\n", steamID)

	fmt.Fprintf(w, "[Check] Fetching Steam wishlist...\n")
	client := newSteamWishlistHTTPClient()
	wishlist, _, err := fetchSteamWishlist(client, steamID)
	if err != nil {
		return fmt.Errorf("Steam wishlist: %w", err)
	}
	if len(wishlist) == 0 {
		fmt.Fprintf(w, "[WARN] Wishlist is empty or profile is private — sync will process 0 games\n")
	} else {
		fmt.Fprintf(w, "[OK] Wishlist reachable — %d game(s) found\n", len(wishlist))
	}
	appIDs := sortedSteamWishlistAppIDs(wishlist)
	if len(appIDs) > 3 {
		appIDs = appIDs[:3]
	}
	if len(appIDs) > 0 {
		fmt.Fprintf(w, "[Check] Fetching Steam metadata sample...\n")
		detailClient := newSteamDetailHTTPClient()
		cache := loadSteamAppDetailsCache()
		for _, appID := range appIDs {
			details, _, err := fetchSteamAppDetailsCached(detailClient, &cache, appID, time.Now())
			if err != nil {
				delete(cache.Entries, appID)
				fmt.Fprintf(w, "[WARN] App %s metadata sample failed: %v\n", appID, err)
				continue
			}
			title := strings.TrimSpace(details.Data.Name)
			if title == "" {
				title = "Steam App " + appID
			}
			fmt.Fprintf(w, "[OK] App %s metadata reachable: %s\n", appID, title)
		}
		if err := saveSteamAppDetailsCache(cache); err != nil {
			fmt.Fprintf(w, "[WARN] Steam metadata cache save failed: %v\n", err)
		}
	}
	return nil
}

func sortedSteamWishlistAppIDs(wishlist map[string]steamWishlistEntry) []string {
	appIDs := make([]string, 0, len(wishlist))
	for key := range wishlist {
		if appIDRe.MatchString(key) {
			appIDs = append(appIDs, key)
		}
	}
	sort.Strings(appIDs)
	return appIDs
}

// syncSteam runs the Steam phase of a sync. configErr is true when the failure
// is a configuration problem (bad ID, bad key, profile not found) that should
// surface on the dashboard. Data-level failures (a single game's details fail)
// are logged and skipped silently.
func syncSteam(cfg Config, calSvc *calendar.Service, targets []CalendarTarget,
	result *SyncResult, w io.Writer, dryRun bool, progress func(string)) (configErr bool, err error) {

	steamID, err := resolveSteamID(cfg.SteamID)
	if err != nil {
		return true, err
	}

	wishlistClient := newSteamWishlistHTTPClient()
	wishlist, status, err := fetchSteamWishlist(wishlistClient, steamID)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound {
			return true, err
		}
		return false, err
	}

	if len(wishlist) == 0 {
		fmt.Fprintf(w, "[Steam] Wishlist is empty or profile is private — nothing to sync\n")
		return false, nil
	}

	fmt.Fprintf(w, "[Steam] %d game(s) on wishlist\n", len(wishlist))

	appIDs := make([]string, 0, len(wishlist))
	for key := range wishlist {
		if !appIDRe.MatchString(key) {
			fmt.Fprintf(w, "[Steam] Skipping non-numeric App ID\n")
			continue
		}
		appIDs = append(appIDs, key)
	}
	sort.Strings(appIDs)

	if len(appIDs) > cfg.SteamMaxGames {
		fmt.Fprintf(w, "[Steam] Wishlist has %d games; processing first %d per SteamMaxGames limit\n", len(appIDs), cfg.SteamMaxGames)
		appIDs = appIDs[:cfg.SteamMaxGames]
	}

	multi := len(calendarTargets(cfg)) > 1
	detailClient := newSteamDetailHTTPClient()
	cache := loadSteamAppDetailsCache()
	rateLimitAborted := false

	for _, target := range targets {
		if rateLimitAborted {
			fmt.Fprintf(w, "[Steam] Rate limit already hit — skipping %s this run\n", target.ID)
			continue
		}
		existingEvents, listErr := listCalendarEvents(calSvc, target.ID)
		if listErr != nil {
			fmt.Fprintf(w, "[Steam] [ERROR] Failed to load existing events for %s: %v\n", target.ID, listErr)
			continue
		}
		existingIndex := indexEventsBySummary(existingEvents)

		added, updated, skippedComing, skippedVague, perGameErr := 0, 0, 0, 0, 0

		for i, appID := range appIDs {
			if rateLimitAborted {
				break
			}
			progress(fmt.Sprintf("Steam: fetching details for game %d/%d...", i+1, len(appIDs)))

			details, _, dErr := fetchSteamAppDetailsCached(detailClient, &cache, appID, time.Now())
			if isSteamRateLimitError(dErr) {
				fmt.Fprintf(w, "[Steam] Rate limited — waiting %s before retry\n", steamRateLimitSleep)
				time.Sleep(steamRateLimitSleep)
				details, _, dErr = fetchSteamAppDetailsCached(detailClient, &cache, appID, time.Now())
				if isSteamRateLimitError(dErr) {
					remaining := len(appIDs) - i
					fmt.Fprintf(w, "[Steam] Rate limit persists — skipping remaining %d games this run\n", remaining)
					rateLimitAborted = true
					break
				}
			}
			if dErr != nil {
				perGameErr++
				fmt.Fprintf(w, "[Steam] App %s: %v\n", appID, dErr)
				time.Sleep(steamRequestDelay)
				continue
			}

			dateStr, comingSoonWithoutDate, ok := steamReleaseDate(details)
			if !ok {
				if comingSoonWithoutDate {
					skippedComing++
				} else {
					skippedVague++
				}
				time.Sleep(steamRequestDelay)
				continue
			}

			entry := wishlist[appID]
			gameTitle := strings.TrimSpace(details.Data.Name)
			if gameTitle == "" {
				gameTitle = entry.Name
			}
			if gameTitle == "" {
				gameTitle = "Steam App " + appID
			}
			summary := fmtMovieTitle(cfg.SteamTemplate, gameTitle)
			description := strings.TrimSpace(details.Data.ShortDescription)
			if description != "" {
				description += "\n\n"
			}
			description += "Steam App ID: " + appID

			ev := allDayCalendarEvent(summary, description, dateStr, target.SteamColorID)
			existing := existingIndex[summary]
			if existing == nil {
				msg := fmt.Sprintf("%s on %s%s", summary, dateStr, targetLabel(target, multi))
				okWrite := dryRun
				if !dryRun {
					inserted, insErr := calSvc.Events.Insert(target.ID, ev).Do()
					if insErr != nil {
						perGameErr++
						fmt.Fprintf(w, "[Steam] [ERROR] Failed adding %s: %v\n", msg, insErr)
						time.Sleep(steamRequestDelay)
						continue
					}
					existingIndex[summary] = inserted
					okWrite = true
				} else {
					existingIndex[summary] = ev
				}
				if okWrite {
					added++
					result.Added = append(result.Added, msg)
				}
			} else if allDayEventNeedsUpdate(existing, dateStr, target.SteamColorID) {
				msg := fmt.Sprintf("%s date changed to %s%s", summary, dateStr, targetLabel(target, multi))
				okWrite := dryRun
				if !dryRun {
					updatedEv, upErr := calSvc.Events.Update(target.ID, existing.Id, ev).Do()
					if upErr != nil {
						perGameErr++
						fmt.Fprintf(w, "[Steam] [ERROR] Failed updating %s: %v\n", msg, upErr)
						time.Sleep(steamRequestDelay)
						continue
					}
					existingIndex[summary] = updatedEv
					okWrite = true
				} else {
					existingIndex[summary] = ev
				}
				if okWrite {
					updated++
					result.Updated = append(result.Updated, msg)
				}
			}
			time.Sleep(steamRequestDelay)
		}

		fmt.Fprintf(w, "[Steam] %s — added %d, updated %d, skipped (coming soon without exact date) %d, skipped (vague date) %d, errors %d\n",
			target.ID, added, updated, skippedComing, skippedVague, perGameErr)
	}
	if err := saveSteamAppDetailsCache(cache); err != nil {
		fmt.Fprintf(w, "[Steam] [WARN] Failed saving Steam metadata cache: %v\n", err)
	}

	return false, nil
}
