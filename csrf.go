package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

const csrfFieldName = "_csrf"

var csrfToken = newCSRFToken()

func newCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func validCSRFToken(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(csrfToken)) == 1
}

func csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		if validCSRFToken(r.Header.Get("X-CSRF-Token")) {
			next.ServeHTTP(w, r)
			return
		}
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
			if err := r.ParseForm(); err == nil && validCSRFToken(r.FormValue(csrfFieldName)) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
	})
}
