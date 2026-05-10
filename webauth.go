package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Web UI authentication: optional password + in-memory session cookies.
//
// Policy:
//   - When no password is set AND bind is localhost-only, every route is
//     allowed (back-compat for existing localhost users).
//   - When no password is set AND bind is 0.0.0.0, all routes except the
//     allowlist redirect to /login so the operator must finish setup before
//     LAN access is granted.
//   - When a password is set, every non-allowlisted route requires a valid
//     session cookie.

const (
	sessionCookieName = "calendarr_session"
	sessionTTL        = 30 * 24 * time.Hour
	bcryptCost        = 12
	minPasswordLen    = 8
	maxPasswordLen    = 72 // bcrypt hard limit

	authAttemptWindow    = 15 * time.Minute
	authLockoutThreshold = 5
	authLockoutDuration  = 2 * time.Minute
	authGlobalThreshold  = 25
	authGlobalBackoff    = 30 * time.Second
)

// sessionStore holds active session IDs. Sessions are kept in memory only; a
// service restart logs everyone out.
var (
	sessionStore   = map[string]time.Time{} // sessionID -> expiresAt
	sessionStoreMu sync.Mutex

	authAttemptMu      sync.Mutex
	authAttemptsByIP   = map[string]authAttemptState{}
	authGlobalFailures []time.Time
)

type authAttemptState struct {
	Failures     int
	FirstFailure time.Time
	LockedUntil  time.Time
}

// hashPassword returns a bcrypt hash for the given plain-text password.
func hashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// checkPassword reports whether plain matches the stored hash.
func checkPassword(hash, plain string) bool {
	if hash == "" || plain == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

func newSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// createSession stores a new session and writes the cookie to the response.
func createSession(w http.ResponseWriter) {
	id := newSessionID()
	if id == "" {
		return
	}
	sessionStoreMu.Lock()
	sessionStore[id] = time.Now().Add(sessionTTL)
	sessionStoreMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// destroySession deletes the caller's session and clears the cookie.
func destroySession(r *http.Request, w http.ResponseWriter) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		sessionStoreMu.Lock()
		delete(sessionStore, c.Value)
		sessionStoreMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessions revokes every active browser session. Use after password
// changes so old browsers cannot keep using the previous access grant.
func clearSessions() {
	sessionStoreMu.Lock()
	sessionStore = map[string]time.Time{}
	sessionStoreMu.Unlock()
}

// sessionValid reports whether the request carries a non-expired session.
func sessionValid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	sessionStoreMu.Lock()
	defer sessionStoreMu.Unlock()
	exp, ok := sessionStore[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(sessionStore, c.Value)
		return false
	}
	return true
}

// sessionSweeper periodically purges expired sessions so the map cannot grow
// unbounded across long uptimes.
func sessionSweeper() {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		sessionStoreMu.Lock()
		for id, exp := range sessionStore {
			if now.After(exp) {
				delete(sessionStore, id)
			}
		}
		sessionStoreMu.Unlock()
		pruneAuthAttempts(time.Now())
	}
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

func authThrottleDelay(remoteAddr string) time.Duration {
	now := time.Now()
	ip := clientIP(remoteAddr)

	authAttemptMu.Lock()
	defer authAttemptMu.Unlock()
	pruneAuthAttemptsLocked(now)

	if len(authGlobalFailures) >= authGlobalThreshold {
		return authGlobalBackoff
	}
	if st, ok := authAttemptsByIP[ip]; ok && now.Before(st.LockedUntil) {
		return time.Until(st.LockedUntil).Round(time.Second)
	}
	return 0
}

func recordLoginFailure(remoteAddr string) time.Duration {
	now := time.Now()
	ip := clientIP(remoteAddr)

	authAttemptMu.Lock()
	defer authAttemptMu.Unlock()
	pruneAuthAttemptsLocked(now)

	st := authAttemptsByIP[ip]
	if st.FirstFailure.IsZero() || now.Sub(st.FirstFailure) > authAttemptWindow {
		st = authAttemptState{FirstFailure: now}
	}
	st.Failures++
	if st.Failures >= authLockoutThreshold {
		extra := st.Failures - authLockoutThreshold
		if extra > 4 {
			extra = 4
		}
		st.LockedUntil = now.Add(authLockoutDuration + time.Duration(extra)*authLockoutDuration)
	}
	authAttemptsByIP[ip] = st
	authGlobalFailures = append(authGlobalFailures, now)

	if now.Before(st.LockedUntil) {
		return time.Until(st.LockedUntil).Round(time.Second)
	}
	return 0
}

func recordLoginSuccess(remoteAddr string) {
	ip := clientIP(remoteAddr)
	authAttemptMu.Lock()
	delete(authAttemptsByIP, ip)
	authAttemptMu.Unlock()
}

func pruneAuthAttempts(now time.Time) {
	authAttemptMu.Lock()
	defer authAttemptMu.Unlock()
	pruneAuthAttemptsLocked(now)
}

func pruneAuthAttemptsLocked(now time.Time) {
	cutoff := now.Add(-authAttemptWindow)
	for ip, st := range authAttemptsByIP {
		if st.LockedUntil.IsZero() || now.After(st.LockedUntil) {
			if st.FirstFailure.IsZero() || st.FirstFailure.Before(cutoff) {
				delete(authAttemptsByIP, ip)
			}
		}
	}
	kept := authGlobalFailures[:0]
	for _, ts := range authGlobalFailures {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	authGlobalFailures = kept
}

// authAllowlistedPath returns true for routes that bypass auth entirely.
func authAllowlistedPath(p string) bool {
	switch p {
	case "/login", "/logout", "/favicon.ico":
		return true
	}
	return strings.HasPrefix(p, "/assets/")
}

// authMiddleware enforces the policy described at the top of this file.
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authAllowlistedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		cfg, _ := loadConfig()
		passwordSet := cfg.WebUIPasswordHash != ""

		if !passwordSet {
			// Back-compat: localhost-only without a password is allowed.
			if cfg.WebBindAddress != "0.0.0.0" {
				next.ServeHTTP(w, r)
				return
			}
			// Bound to LAN with no password — force operator to set one.
			redirectOrJSON(w, r, "/login", "Set a Web UI password before enabling LAN access.")
			return
		}

		if sessionValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		redirectOrJSON(w, r, "/login", "Authentication required.")
	})
}

// redirectOrJSON sends a 302 to the login page for browser navigation, or a
// 401 JSON response for AJAX/JSON requests so callers can react sensibly.
func redirectOrJSON(w http.ResponseWriter, r *http.Request, location, msg string) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"message":"` + msg + `"}`))
		return
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func wantsJSON(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		return true
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "application/json")
}
