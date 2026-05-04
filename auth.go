package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// ---- Embedded OAuth credentials ---------------------------------------------
// Web Application type credentials. The redirect URI is registered in Google
// Cloud Console as http://localhost:5000/oauth/callback.
// Client secrets for installed/web apps are not truly secret when distributed,
// but should never be committed to a public repo directly.

const (
	googleClientID              = "426279011260-dme1qg2dme1om8109dua1scmdn5si5dq.apps.googleusercontent.com"
	googleClientSecret          = "REPLACE_WITH_RELEASE_GOOGLE_CLIENT_SECRET"
	googleCalendarScope         = "https://www.googleapis.com/auth/calendar.events"
	googleCalendarReadonlyScope = "https://www.googleapis.com/auth/calendar.readonly"
)

func oauthConfig(redirectURL string) *oauth2.Config {
	clientID, clientSecret := googleOAuthCredentials()
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{googleCalendarScope, googleCalendarReadonlyScope},
		RedirectURL:  redirectURL,
	}
}

func googleOAuthCredentials() (string, string) {
	clientID := strings.TrimSpace(os.Getenv("CALENDARR_GOOGLE_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("CALENDARR_GOOGLE_CLIENT_SECRET"))
	if clientID != "" && clientSecret != "" {
		return clientID, clientSecret
	}
	if fileID, fileSecret, ok := googleOAuthCredentialsFromFile(dataPath("google_oauth_client.json")); ok {
		if clientID == "" {
			clientID = fileID
		}
		if clientSecret == "" {
			clientSecret = fileSecret
		}
	}
	if clientID == "" {
		clientID = googleClientID
	}
	if clientSecret == "" {
		clientSecret = googleClientSecret
	}
	return clientID, clientSecret
}

func googleOAuthCredentialsFromFile(path string) (string, string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var raw struct {
		Installed *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"installed"`
		Web *struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		} `json:"web"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", "", false
	}
	switch {
	case raw.Installed != nil:
		id := strings.TrimSpace(raw.Installed.ClientID)
		secret := strings.TrimSpace(raw.Installed.ClientSecret)
		return id, secret, id != "" && secret != ""
	case raw.Web != nil:
		id := strings.TrimSpace(raw.Web.ClientID)
		secret := strings.TrimSpace(raw.Web.ClientSecret)
		return id, secret, id != "" && secret != ""
	default:
		id := strings.TrimSpace(raw.ClientID)
		secret := strings.TrimSpace(raw.ClientSecret)
		return id, secret, id != "" && secret != ""
	}
}

// ---- CSRF state -------------------------------------------------------------

var (
	oauthStateToken string
	oauthStateMu    sync.Mutex
)

func newOAuthState() string {
	b := make([]byte, 16)
	rand.Read(b)
	state := hex.EncodeToString(b)
	oauthStateMu.Lock()
	oauthStateToken = state
	oauthStateMu.Unlock()
	return state
}

func checkOAuthState(state string) bool {
	oauthStateMu.Lock()
	defer oauthStateMu.Unlock()
	return state != "" && state == oauthStateToken
}

// ---- Handlers ---------------------------------------------------------------

// callbackURI builds the redirect URI using localhost and the configured port.
// Google OAuth requires this to be done from a browser on the Calendarr server
// machine itself — this is a one-time setup step.
func callbackURI() string {
	cfg, _ := loadConfig()
	port := cfg.WebPort
	if port <= 0 {
		port = 5000
	}
	return fmt.Sprintf("http://localhost:%d/oauth/callback", port)
}

// handleOAuthStart redirects the browser to Google's consent screen.
func handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	state := newOAuthState()
	authURL := oauthConfig(callbackURI()).AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
	)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// handleOAuthCallback receives the redirect from Google after the user
// authorizes, exchanges the code for tokens, and saves the refresh token.
func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		setFlash(w, "danger", "Google authorization was denied: "+errParam)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	if !checkOAuthState(r.URL.Query().Get("state")) {
		setFlash(w, "danger", "Invalid OAuth state — please try connecting again.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		setFlash(w, "danger", "No authorization code received from Google.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	token, err := oauthConfig(callbackURI()).Exchange(context.Background(), code)
	if err != nil {
		setFlash(w, "danger", "Failed to complete Google authorization: "+err.Error())
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	if token.RefreshToken == "" {
		setFlash(w, "danger", "Google did not return a refresh token. Try disconnecting and reconnecting.")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	cfg, err := loadConfig()
	if err != nil {
		setFlash(w, "danger", "Could not load config: "+err.Error())
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	cfg.GoogleRefreshToken = token.RefreshToken
	if err := saveConfig(cfg); err != nil {
		setFlash(w, "danger", "Could not save token: "+err.Error())
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	setFlash(w, "success", "Google Calendar connected successfully!")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
