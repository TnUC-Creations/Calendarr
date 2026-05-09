package main

import (
	"errors"
	"net/http"
)

// isBodyTooLarge reports whether err came from a MaxBytesReader cap being hit
// during read/decode/parse. Wraps both *http.MaxBytesError and the legacy
// "http: request body too large" string for older Go behaviour.
func isBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return true
	}
	return err.Error() == "http: request body too large"
}

// Per-endpoint body size caps. JSON endpoints take small structured input;
// only the restore upload (a backup zip) needs more headroom.
const (
	maxJSONBodyBytes    int64 = 256 << 10  // 256 KB
	maxRestoreBodyBytes int64 = 5 << 20    // 5 MB
	maxFormBodyBytes    int64 = 256 << 10  // 256 KB — settings forms are small
)

// limitBody wraps the request body in MaxBytesReader so any read past n bytes
// returns an error instead of silently consuming memory. Safe to call once per
// handler before parsing/decoding.
func limitBody(w http.ResponseWriter, r *http.Request, n int64) {
	r.Body = http.MaxBytesReader(w, r.Body, n)
}

// writeTooLarge sends a 413 JSON envelope. Use after a Decode/ParseForm error
// when MaxBytesReader was the likely cause.
func writeTooLarge(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	if msg == "" {
		msg = "Request body too large."
	}
	_, _ = w.Write([]byte(`{"ok":false,"message":"` + msg + `"}`))
}
