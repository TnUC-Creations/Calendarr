package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseSteamDate(t *testing.T) {
	tests := []struct {
		input  string
		want   string
		wantOk bool
	}{
		{"May 22, 2025", "2025-05-22", true},
		{"22 May, 2025", "2025-05-22", true},
		{"Jan 1, 2006", "2006-01-01", true},
		{"1 Jan, 2006", "2006-01-01", true},
		{"May 2025", "", false},
		{"2025", "", false},
		{"", "", false},
		{"coming soon", "", false},
		{"TBD", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseSteamDate(tt.input)
			if got != tt.want || ok != tt.wantOk {
				t.Fatalf("parseSteamDate(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.wantOk)
			}
		})
	}
}

func TestResolveSteamInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantID  string
		wantErr bool
	}{
		{"raw steam64", "76561198000000001", "76561198000000001", false},
		{"profiles https url", "https://steamcommunity.com/profiles/76561198000000001", "76561198000000001", false},
		{"profiles http url", "http://steamcommunity.com/profiles/76561198000000001", "76561198000000001", false},
		{"profiles hostless url", "steamcommunity.com/profiles/76561198000000001", "76561198000000001", false},
		{"profiles uppercase host", "https://STEAMCOMMUNITY.COM/profiles/76561198000000001", "76561198000000001", false},
		{"profiles trailing slash", "https://steamcommunity.com/profiles/76561198000000001/", "76561198000000001", false},
		{"profiles trailing path", "https://steamcommunity.com/profiles/76561198000000001/games/?tab=all", "76561198000000001", false},
		{"id vanity https rejected", "https://steamcommunity.com/id/myname", "", true},
		{"id vanity hostless rejected", "steamcommunity.com/id/myname", "", true},
		{"bare vanity rejected", "myname", "", true},
		{"profiles invalid id", "https://steamcommunity.com/profiles/not-a-steamid", "", true},
		{"invalid chars", "notvalid!@#", "", true},
		{"empty", "", "", true},
		{"single char", "x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := resolveSteamInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSteamInput(%q) err = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSteamInput(%q) unexpected err: %v", tt.input, err)
			}
			if id != tt.wantID {
				t.Errorf("steam64ID = %q, want %q", id, tt.wantID)
			}
		})
	}
}

func TestSteamIDRegex(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"76561198000000001", true},
		{"7656119800000000", false},
		{"765611980000000012", false},
		{"00000000000000000", false},
		{"7656abc1234567890", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := steamIDRe.MatchString(tt.input); got != tt.want {
				t.Fatalf("steamIDRe.Match(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAppIDRegex(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1234567", true},
		{"0", true},
		{"9999999999", true},
		{"12345678901", false},
		{"123abc", false},
		{"", false},
		{"12345,67890", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := appIDRe.MatchString(tt.input); got != tt.want {
				t.Fatalf("appIDRe.Match(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFetchSteamAppDetailsRetryAfter429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"12345":{"success":true,"data":{"release_date":{"coming_soon":false,"date":"May 22, 2025"},"short_description":"a game"}}}`)
	}))
	defer srv.Close()

	client := srv.Client()
	// First call should return rate limited error.
	_, err := fetchSteamAppDetailsAt(client, srv.URL, "12345")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("first call err = %v, want rate-limited error", err)
	}
	// Second call should succeed.
	details, err := fetchSteamAppDetailsAt(client, srv.URL, "12345")
	if err != nil {
		t.Fatalf("second call err = %v, want success", err)
	}
	if details.Data.ReleaseDate.Date != "May 22, 2025" {
		t.Fatalf("date = %q, want %q", details.Data.ReleaseDate.Date, "May 22, 2025")
	}
}

func TestFetchSteamAppDetailsRateLimitedPersists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := srv.Client()
	_, err := fetchSteamAppDetailsAt(client, srv.URL, "12345")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v, want rate-limited error", err)
	}
	_, err = fetchSteamAppDetailsAt(client, srv.URL, "12345")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("retry err = %v, want rate-limited error", err)
	}
}

// fetchSteamAppDetailsAt is a test helper that targets a custom base URL.
// Mirrors fetchSteamAppDetails but allows redirecting to httptest server.
func fetchSteamAppDetailsAt(client *http.Client, baseURL, appID string) (*steamAppDetails, error) {
	u := baseURL + "/api/appdetails?appids=" + appID
	return fetchSteamAppDetailsURL(client, u, appID)
}

// fetchSteamWishlistAt mirrors fetchSteamWishlist but lets the test target
// an httptest server URL. The status-code classification (401/403/404 → config
// error) and io.LimitReader budgeting match the production function.
func fetchSteamWishlistAt(client *http.Client, baseURL, steamID string) (map[string]steamWishlistEntry, int, error) {
	params := url.Values{}
	params.Set("steamid", steamID)
	u := baseURL + "/IWishlistService/GetWishlist/v1/?" + params.Encode()
	return fetchSteamWishlistURL(client, u)
}

// ---- Additional gap coverage (Quinn Bellamy, QA) ----------------------------

// TestParseSteamDateRejectsAdditionalVagueValues covers values the spec
// explicitly lists as rejection cases that Kai's initial table did not include:
// "Q3 2025", "Coming Soon" (title case), and "TBA".
func TestParseSteamDateRejectsAdditionalVagueValues(t *testing.T) {
	for _, input := range []string{
		"Q3 2025",
		"Coming Soon",
		"TBA",
		"To be announced",
		"Winter 2025",
		"Early 2026",
	} {
		if got, ok := parseSteamDate(input); ok || got != "" {
			t.Errorf("parseSteamDate(%q) = (%q, %v), want (\"\", false)", input, got, ok)
		}
	}
}

// TestResolveSteamIDRejectsVanity confirms vanity URLs are unsupported because
// Calendarr no longer accepts or sends a Steam Web API key.
func TestResolveSteamIDRejectsVanity(t *testing.T) {
	_, err := resolveSteamID("https://steamcommunity.com/id/myname")
	if err == nil {
		t.Fatal("resolveSteamID with vanity URL returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "unsupported Steam profile URL") {
		t.Fatalf("error = %v, want unsupported vanity URL message", err)
	}
}

// TestResolveSteamIDPropagatesInputError confirms invalid inputs reach the
// caller as errors rather than producing an empty/zero Steam64 ID.
func TestResolveSteamIDPropagatesInputError(t *testing.T) {
	for _, input := range []string{"", "notvalid!@#", "7656"} {
		_, err := resolveSteamID(input)
		if err == nil {
			t.Errorf("resolveSteamID(%q) err = nil, want error", input)
		}
	}
}

// TestResolveSteamIDRawPassthrough confirms that a raw Steam64 ID is returned
// without any keyed Steam API dependency.
func TestResolveSteamIDRawPassthrough(t *testing.T) {
	got, err := resolveSteamID("76561198000000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "76561198000000001" {
		t.Fatalf("got = %q, want unchanged Steam64 ID", got)
	}
}

func TestResolveSteamIDProfileURLWithoutKey(t *testing.T) {
	got, err := resolveSteamID("https://steamcommunity.com/profiles/76561198000000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "76561198000000001" {
		t.Fatalf("got = %q, want Steam64 ID from /profiles/ URL", got)
	}
}

// TestFetchSteamWishlistHappyPath verifies a valid wishlist JSON decodes
// into the expected map and the returned status code is 200.
func TestFetchSteamWishlistHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("steamid"); got != "76561198000000001" {
			t.Fatalf("steamid query = %q, want requested Steam64 ID", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"response":{"items":[{"appid":12345,"priority":0,"date_added":1700000000},{"appid":67890,"priority":1,"date_added":1700000001}]}}`)
	}))
	defer srv.Close()

	wishlist, status, err := fetchSteamWishlistAt(srv.Client(), srv.URL, "76561198000000001")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if len(wishlist) != 2 {
		t.Fatalf("wishlist size = %d, want 2", len(wishlist))
	}
	if _, ok := wishlist["12345"]; !ok {
		t.Errorf("wishlist missing app ID 12345")
	}
	if _, ok := wishlist["67890"]; !ok {
		t.Errorf("wishlist missing app ID 67890")
	}
}

// TestFetchSteamWishlistEmptyIsNotAnError confirms an empty Web API response
// returns an empty map and no error — the spec is explicit that an empty
// wishlist (private profile, brand new account) is a normal condition.
func TestFetchSteamWishlistEmptyIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"response":{"items":[]}}`)
	}))
	defer srv.Close()

	wishlist, status, err := fetchSteamWishlistAt(srv.Client(), srv.URL, "76561198000000001")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if len(wishlist) != 0 {
		t.Fatalf("wishlist size = %d, want 0", len(wishlist))
	}
}

func TestFetchSteamWishlistNonJSONIncludesSanitizedDiagnostics(t *testing.T) {
	secret := "GOCSPX-" + strings.Repeat("A", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html>\n<body>maintenance token=%s %s</body></html>", secret, strings.Repeat("x", 300))
	}))
	defer srv.Close()

	_, status, err := fetchSteamWishlistAt(srv.Client(), srv.URL, "76561198000000001")
	if err == nil {
		t.Fatal("err = nil, want non-JSON diagnostic error")
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	msg := err.Error()
	for _, want := range []string{"HTTP 200", "text/html", "non-JSON response prefix"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want %q", msg, want)
		}
	}
	if strings.Contains(msg, secret) || strings.Contains(msg, "\n") {
		t.Fatalf("error leaked secret or newline: %q", msg)
	}
	prefix := sanitizedBodyPrefix([]byte("<html>token="+secret+" "+strings.Repeat("x", 300)+"</html>"), 160)
	if len([]rune(prefix)) > 160 {
		t.Fatalf("prefix length = %d, want <= 160", len([]rune(prefix)))
	}
	if strings.Contains(prefix, secret) {
		t.Fatalf("prefix leaked secret: %q", prefix)
	}
}

func TestFetchSteamWishlistNonJSONSanitizesContentType(t *testing.T) {
	secret := "GOCSPX-" + strings.Repeat("B", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; token="+secret+"; "+strings.Repeat("x", 200))
		fmt.Fprint(w, "<html>maintenance</html>")
	}))
	defer srv.Close()

	_, _, err := fetchSteamWishlistAt(srv.Client(), srv.URL, "76561198000000001")
	if err == nil {
		t.Fatal("err = nil, want non-JSON diagnostic error")
	}

	msg := err.Error()
	if strings.Contains(msg, secret) {
		t.Fatalf("error leaked content-type secret: %q", msg)
	}
	if !strings.Contains(msg, "token=[redacted]") {
		t.Fatalf("error = %q, want redacted content-type token", msg)
	}
	if strings.Contains(msg, strings.Repeat("x", 121)) {
		t.Fatalf("error included uncapped content-type value: %q", msg)
	}

	header := sanitizedHeaderValue("text/html; token="+secret+"; "+strings.Repeat("x", 200), 120)
	if len([]rune(header)) > 120 {
		t.Fatalf("sanitized header length = %d, want <= 120", len([]rune(header)))
	}
	if strings.Contains(header, secret) {
		t.Fatalf("sanitized header leaked secret: %q", header)
	}
}

// TestFetchSteamWishlistAuthErrorsReturnStatusCode covers Zara Finding 12:
// the caller (syncSteam) must be able to classify 401/403/404 as a config
// error vs a transient data error. The function therefore returns the HTTP
// status code alongside the error.
func TestFetchSteamWishlistAuthErrorsReturnStatusCode(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			_, status, err := fetchSteamWishlistAt(srv.Client(), srv.URL, "76561198000000001")
			if err == nil {
				t.Fatalf("HTTP %d returned nil error", code)
			}
			if status != code {
				t.Fatalf("returned status = %d, want %d", status, code)
			}
		})
	}
}

// TestFetchSteamWishlistHTTPTimeoutReturnsError exercises the network-failure
// path. The server stalls long enough that a 100ms client timeout fires.
func TestFetchSteamWishlistHTTPTimeoutReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, _, err := fetchSteamWishlistAt(client, srv.URL, "76561198000000001")
	if err == nil {
		t.Fatal("err = nil, want timeout-related error")
	}
}

// TestFetchSteamAppDetailsNonJSONReturnsError covers the malformed-response
// path: Steam returns HTTP 200 but the body is HTML or garbage. The function
// must return an error, not panic.
func TestFetchSteamAppDetailsNonJSONReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>maintenance</body></html>`)
	}))
	defer srv.Close()

	_, err := fetchSteamAppDetailsAt(srv.Client(), srv.URL, "12345")
	if err == nil {
		t.Fatal("err = nil, want JSON parse error")
	}
}

// TestFetchSteamAppDetailsSuccessFalseDecodesGracefully confirms that a
// well-formed Steam response with `success: false` decodes cleanly. The
// caller is responsible for skipping the game; the fetch itself must not
// crash or return an error for this shape.
func TestFetchSteamAppDetailsSuccessFalseDecodesGracefully(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"12345":{"success":false}}`)
	}))
	defer srv.Close()

	details, err := fetchSteamAppDetailsAt(srv.Client(), srv.URL, "12345")
	if err != nil {
		t.Fatalf("err = %v, want nil (decode should succeed)", err)
	}
	if details == nil {
		t.Fatal("details = nil, want non-nil struct so caller can inspect Success")
	}
	if details.Success {
		t.Errorf("Success = true, want false")
	}
}

func TestFetchSteamAppDetailsComingSoonVagueDateIsStillRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"12345":{"success":true,"data":{"release_date":{"coming_soon":true,"date":"Q3 2025"},"short_description":"upcoming"}}}`)
	}))
	defer srv.Close()

	details, err := fetchSteamAppDetailsAt(srv.Client(), srv.URL, "12345")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !details.Data.ReleaseDate.ComingSoon {
		t.Errorf("ComingSoon = false, want true")
	}
	if dateStr, skippedComingSoon, ok := steamReleaseDate(details); ok || dateStr != "" || !skippedComingSoon {
		t.Errorf("steamReleaseDate = (%q, %v, %v), want empty date, coming-soon skip, not ok",
			dateStr, skippedComingSoon, ok)
	}
}

func TestSteamReleaseDateAcceptsComingSoonExactDate(t *testing.T) {
	details := &steamAppDetails{Success: true}
	details.Data.Name = "Moonshiner Simulator"
	details.Data.ReleaseDate.ComingSoon = true
	details.Data.ReleaseDate.Date = "May 20, 2026"

	dateStr, skippedComingSoon, ok := steamReleaseDate(details)
	if !ok {
		t.Fatalf("steamReleaseDate rejected exact coming-soon date")
	}
	if skippedComingSoon {
		t.Fatal("steamReleaseDate marked exact coming-soon date as skipped")
	}
	if dateStr != "2026-05-20" {
		t.Fatalf("dateStr = %q, want 2026-05-20", dateStr)
	}
}

func TestSteamAppDetailsCacheHitMissAndTTL(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"12345":{"success":true,"data":{"name":"Game A","release_date":{"coming_soon":false,"date":"May 22, 2025"},"short_description":"a game"}}}`)),
		}, nil
	})}
	cache := steamAppDetailsCache{Entries: map[string]steamAppDetailsCacheEntry{}}

	details, hit, err := fetchSteamAppDetailsCached(client, &cache, "12345", now)
	if err != nil {
		t.Fatalf("miss err = %v, want nil", err)
	}
	if hit {
		t.Fatal("first fetch hit cache, want miss")
	}
	if calls != 1 {
		t.Fatalf("calls after miss = %d, want 1", calls)
	}
	if details.Data.Name != "Game A" {
		t.Fatalf("cached detail title = %q, want Game A", details.Data.Name)
	}

	details, hit, err = fetchSteamAppDetailsCached(client, &cache, "12345", now.Add(29*24*time.Hour))
	if err != nil {
		t.Fatalf("fresh cache err = %v, want nil", err)
	}
	if !hit {
		t.Fatal("second fetch missed cache, want hit")
	}
	if calls != 1 {
		t.Fatalf("calls after cache hit = %d, want 1", calls)
	}
	if details.Data.ReleaseDate.Date != "May 22, 2025" {
		t.Fatalf("cached date = %q, want May 22, 2025", details.Data.ReleaseDate.Date)
	}

	_, hit, err = fetchSteamAppDetailsCached(client, &cache, "12345", now.Add(31*24*time.Hour))
	if err != nil {
		t.Fatalf("expired cache err = %v, want nil", err)
	}
	if hit {
		t.Fatal("expired cache returned hit, want miss")
	}
	if calls != 2 {
		t.Fatalf("calls after expired cache = %d, want 2", calls)
	}
}

func TestSteamAppDetailsCacheFailureTTL(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`error`)),
		}, nil
	})}
	cache := steamAppDetailsCache{Entries: map[string]steamAppDetailsCacheEntry{}}

	_, hit, err := fetchSteamAppDetailsCached(client, &cache, "12345", now)
	if err == nil || hit {
		t.Fatalf("first failure hit=%v err=%v, want miss with error", hit, err)
	}
	_, hit, err = fetchSteamAppDetailsCached(client, &cache, "12345", now.Add(5*time.Hour))
	if err == nil || !hit {
		t.Fatalf("cached failure hit=%v err=%v, want hit with error", hit, err)
	}
	if calls != 1 {
		t.Fatalf("calls before failure TTL = %d, want 1", calls)
	}
	_, hit, err = fetchSteamAppDetailsCached(client, &cache, "12345", now.Add(7*time.Hour))
	if err == nil || hit {
		t.Fatalf("expired failure hit=%v err=%v, want miss with error", hit, err)
	}
	if calls != 2 {
		t.Fatalf("calls after failure TTL = %d, want 2", calls)
	}
}

func TestSteamAppDetailsCacheDoesNotStoreRateLimitFailures(t *testing.T) {
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`rate limit`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"12345":{"success":true,"data":{"name":"Recovered","release_date":{"date":"May 22, 2025"}}}}`,
			)),
		}, nil
	})}
	cache := steamAppDetailsCache{Entries: map[string]steamAppDetailsCacheEntry{}}

	_, hit, err := fetchSteamAppDetailsCached(client, &cache, "12345", now)
	if err == nil || hit {
		t.Fatalf("first rate limit hit=%v err=%v, want miss with error", hit, err)
	}
	if _, ok := cache.Entries["12345"]; ok {
		t.Fatal("rate limit failure was cached")
	}

	details, hit, err := fetchSteamAppDetailsCached(client, &cache, "12345", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("retry after rate limit err = %v, want nil", err)
	}
	if hit {
		t.Fatal("retry after rate limit hit cache, want network fetch")
	}
	if calls != 2 {
		t.Fatalf("calls after retry = %d, want 2", calls)
	}
	if details.Data.Name != "Recovered" {
		t.Fatalf("name = %q, want Recovered", details.Data.Name)
	}
}

func TestCheckSteamConnectivitySamplesAtMostThreeAppDetails(t *testing.T) {
	oldWishlistClient := newSteamWishlistHTTPClient
	oldDetailClient := newSteamDetailHTTPClient
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() {
		newSteamWishlistHTTPClient = oldWishlistClient
		newSteamDetailHTTPClient = oldDetailClient
		dataDir = oldDataDir
	})

	wishlistCalls := 0
	detailCalls := 0
	newSteamWishlistHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			wishlistCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"response":{"items":[{"appid":1001},{"appid":1002},{"appid":1003},{"appid":1004},{"appid":1005}]}}`)),
			}, nil
		})}
	}
	newSteamDetailHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			detailCalls++
			appID := req.URL.Query().Get("appids")
			body := fmt.Sprintf(`{"%s":{"success":true,"data":{"name":"Game %s","release_date":{"coming_soon":false,"date":"May 22, 2025"},"short_description":"sample"}}}`, appID, appID)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})}
	}

	var out strings.Builder
	err := checkSteamConnectivity(Config{SteamID: "76561198000000001"}, &out)
	if err != nil {
		t.Fatalf("checkSteamConnectivity err = %v, want nil\noutput:\n%s", err, out.String())
	}
	if wishlistCalls != 1 {
		t.Fatalf("wishlist calls = %d, want 1", wishlistCalls)
	}
	if detailCalls != 3 {
		t.Fatalf("detail calls = %d, want 3 sample fetches", detailCalls)
	}
}

// TestSteamIDRegexZaraBoundaries covers Zara Finding 1 with the exact
// adjacent-length cases that an attacker would try: one short, one long,
// wrong prefix, and a non-digit character mid-string.
func TestSteamIDRegexZaraBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"16 digits (too short)", "7656119800000000", false},
		{"18 digits (too long)", "765611980000000012", false},
		{"wrong prefix 1234", "1234567890123456", false},
		{"wrong prefix 17 digits", "12345678901234567", false},
		{"non-digit at position 6", "7656abc1234567890", false},
		{"valid 17 digit", "76561198000000001", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := steamIDRe.MatchString(c.input); got != c.want {
				t.Fatalf("steamIDRe(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// TestAppIDRegexZaraBoundaries covers Zara Finding 3: any wishlist key with
// non-numeric content must be rejected before being substituted into the
// app-detail URL. Empty is rejected (would produce a no-arg URL).
func TestAppIDRegexZaraBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"alpha mixed", "123abc", false},
		{"comma injection", "12345,67890", false},
		{"newline injection", "12345\n67890", false},
		{"space injection", "12345 67890", false},
		{"sql-style", "0; SELECT", false},
		{"valid short", "1", true},
		{"valid long 10 digits", "9999999999", true},
		{"too long 11 digits", "12345678901", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appIDRe.MatchString(c.input); got != c.want {
				t.Fatalf("appIDRe(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// TestNormalizeLoadedConfigSteamMaxGames covers the hard cap Zara required
// in normalizeLoadedConfig (Finding 7). Out-of-range values must collapse
// to the 500 default.
func TestNormalizeLoadedConfigSteamMaxGames(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults to 500", 0, 500},
		{"negative defaults to 500", -1, 500},
		{"large negative defaults to 500", -9999, 500},
		{"over cap collapses to 500", 999, 500},
		{"way over cap collapses to 500", 100000, 500},
		{"at cap is preserved", 500, 500},
		{"under cap is preserved", 250, 250},
		{"one is preserved", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.SteamMaxGames = c.in
			normalizeLoadedConfig(&cfg)
			if cfg.SteamMaxGames != c.want {
				t.Fatalf("SteamMaxGames in=%d out=%d, want %d", c.in, cfg.SteamMaxGames, c.want)
			}
		})
	}
}

// TestNormalizeLoadedConfigSteamTemplateDefault covers Zara Finding 11: an
// empty SteamTemplate must be replaced with the documented default so that
// fmtMovieTitle produces a sensible summary.
func TestNormalizeLoadedConfigSteamTemplateDefault(t *testing.T) {
	cfg := defaultConfig()
	cfg.SteamTemplate = ""
	normalizeLoadedConfig(&cfg)
	if cfg.SteamTemplate != "{title} - Steam Release" {
		t.Fatalf("SteamTemplate = %q, want default %q", cfg.SteamTemplate, "{title} - Steam Release")
	}
}

// TestNormalizeLoadedConfigSteamTemplatePreservesCustom confirms a user's
// custom non-empty template survives normalization unchanged.
func TestNormalizeLoadedConfigSteamTemplatePreservesCustom(t *testing.T) {
	cfg := defaultConfig()
	cfg.SteamTemplate = "Game: {title}"
	normalizeLoadedConfig(&cfg)
	if cfg.SteamTemplate != "Game: {title}" {
		t.Fatalf("SteamTemplate = %q, want preserved custom value", cfg.SteamTemplate)
	}
}

// TestNormalizeLoadedConfigTrimsSteamID confirms the spec's whitespace-
// trimming requirement that prevents accidental copy/paste leading/trailing
// spaces from breaking the steamIDRe match.
func TestNormalizeLoadedConfigTrimsSteamID(t *testing.T) {
	cfg := defaultConfig()
	cfg.SteamID = "   76561198000000001\t\n  "
	normalizeLoadedConfig(&cfg)
	if cfg.SteamID != "76561198000000001" {
		t.Fatalf("SteamID = %q, want trimmed Steam64 ID", cfg.SteamID)
	}
}

// TestDefaultConfigSteamDefaults locks in the Steam-related defaults the
// spec promised: UseSteam=false, SteamMaxGames=500, SteamTemplate set.
func TestDefaultConfigSteamDefaults(t *testing.T) {
	cfg := defaultConfig()
	if cfg.UseSteam {
		t.Errorf("UseSteam default = true, want false")
	}
	if cfg.SteamMaxGames != 500 {
		t.Errorf("SteamMaxGames default = %d, want 500", cfg.SteamMaxGames)
	}
	if cfg.SteamTemplate != "{title} - Steam Release" {
		t.Errorf("SteamTemplate default = %q, want %q", cfg.SteamTemplate, "{title} - Steam Release")
	}
}

// TestCalendarTargetsForSourceSteam confirms the new "steam" case in the
// per-source target filter Kai added to config.go.
func TestCalendarTargetsForSourceSteam(t *testing.T) {
	targets := []CalendarTarget{
		{ID: "radarr-only", RadarrEnabled: true},
		{ID: "steam-only", SteamEnabled: true},
		{ID: "all", RadarrEnabled: true, SonarrEnabled: true, SteamEnabled: true},
	}
	got := calendarTargetsForSource(targets, "steam")
	if len(got) != 2 {
		t.Fatalf("steam targets = %d, want 2", len(got))
	}
	if got[0].ID != "steam-only" || got[1].ID != "all" {
		t.Fatalf("steam targets = [%s,%s], want [steam-only,all]", got[0].ID, got[1].ID)
	}
}

// TestNormalizeCalendarTargetsFallbackSetsSteamEnabled covers the fallback
// single-target branch when no calendar targets are configured — it must
// reflect UseSteam onto the auto-generated primary target so a fresh user
// who toggles Steam on actually gets a target.
func TestNormalizeCalendarTargetsFallbackSetsSteamEnabled(t *testing.T) {
	cfg := Config{UseSteam: true}
	normalizeCalendarTargets(&cfg)
	if len(cfg.CalendarTargets) != 1 {
		t.Fatalf("target count = %d, want 1", len(cfg.CalendarTargets))
	}
	if !cfg.CalendarTargets[0].SteamEnabled {
		t.Fatalf("fallback target SteamEnabled = false, want true (mirrors cfg.UseSteam)")
	}
}

func TestCheckSteamConnectivityDoesNotPersistMetadataSampleFailures(t *testing.T) {
	oldWishlistClient := newSteamWishlistHTTPClient
	oldDetailClient := newSteamDetailHTTPClient
	oldDataDir := dataDir
	dataDir = t.TempDir()
	t.Cleanup(func() {
		newSteamWishlistHTTPClient = oldWishlistClient
		newSteamDetailHTTPClient = oldDetailClient
		dataDir = oldDataDir
	})

	newSteamWishlistHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"response":{"items":[{"appid":12345}]}}`)),
			}, nil
		})}
	}
	newSteamDetailHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`temporary error`)),
			}, nil
		})}
	}

	var out strings.Builder
	err := checkSteamConnectivity(Config{SteamID: "76561198000000001"}, &out)
	if err != nil {
		t.Fatalf("checkSteamConnectivity err = %v, want nil\noutput:\n%s", err, out.String())
	}
	cache := loadSteamAppDetailsCache()
	if _, ok := cache.Entries["12345"]; ok {
		t.Fatalf("metadata sample failure was persisted in cache: %#v", cache.Entries["12345"])
	}
}

func TestSyncSteamRateLimitAbortIsPhaseWide(t *testing.T) {
	src, err := os.ReadFile("steam.go")
	if err != nil {
		t.Fatalf("read steam.go: %v", err)
	}
	body := string(src)
	init := strings.Index(body, "rateLimitAborted := false")
	loop := strings.Index(body, "for _, target := range targets")
	if init < 0 {
		t.Fatal("syncSteam missing rateLimitAborted initialization")
	}
	if loop < 0 {
		t.Fatal("syncSteam target loop not found")
	}
	if init > loop {
		t.Fatal("rateLimitAborted is initialized inside the target loop; it must be phase-wide")
	}
	if strings.Count(body[loop:], "rateLimitAborted := false") > 0 {
		t.Fatal("rateLimitAborted is reset inside or after the target loop")
	}
	if !strings.Contains(body[loop:], "Rate limit already hit") {
		t.Fatal("syncSteam should skip later calendar targets after a phase-wide rate limit abort")
	}
}

func TestSyncSteamUsesSteamTargetColor(t *testing.T) {
	src, err := os.ReadFile("steam.go")
	if err != nil {
		t.Fatalf("read steam.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "allDayCalendarEvent(summary, description, dateStr, target.SteamColorID)") {
		t.Fatal("syncSteam should create Steam events with target.SteamColorID")
	}
	if !strings.Contains(body, "allDayEventNeedsUpdate(existing, dateStr, target.SteamColorID)") {
		t.Fatal("syncSteam should update existing Steam events when target.SteamColorID changes")
	}
}

// ---- Steam preview progress routing -----------------------------------------

// TestSyncSteamAvoidsDirectProgressStateWrites guards the preview path:
// syncSteam must route progress through its callback instead of writing
// directly to live sync state.
func TestSyncSteamAvoidsDirectProgressStateWrites(t *testing.T) {
	src, err := os.ReadFile("steam.go")
	if err != nil {
		t.Fatalf("read steam.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "func syncSteam(")
	if start < 0 {
		t.Fatal("syncSteam function not found in steam.go")
	}
	tail := body[start:]
	if strings.Contains(tail, "setSyncProgress(") {
		t.Errorf(
			"syncSteam calls setSyncProgress() directly; route progress through callback. Preview/dry-run runs " +
				"will clobber appState.SyncProgress instead of writing to " +
				"previewState.Progress. Route the progress update through a " +
				"function parameter (matching runSync's `progress := setSyncProgress; " +
				"if dryRun { progress = setPreviewProgress }` pattern) or accept " +
				"a progress func argument in syncSteam's signature.")
	}
}

// TestSyncSteamHandlesDryRunFlag confirms syncSteam keeps the dry-run parameter
// used by preview-aware callers.
func TestSyncSteamHandlesDryRunFlag(t *testing.T) {
	src, err := os.ReadFile("steam.go")
	if err != nil {
		t.Fatalf("read steam.go: %v", err)
	}
	if !strings.Contains(string(src), "dryRun bool") {
		t.Error("syncSteam no longer takes dryRun bool — the Preview routing fix relies on this parameter")
	}
}
