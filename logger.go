package main

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"
)

// appLog writes a plain timestamped line to the daily log file.
// Use this for application-level events (startup errors, panics, HTTP errors)
// that aren't part of a sync run and don't need a separator block.
func appLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("%s %s\n", ts, msg)
	if f, err := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		_, _ = f.WriteString(line)
		f.Close()
	}
}

// logFileWriter is an io.Writer that appends to the current daily log file.
// Passed to log.SetOutput so the standard log package lands in the same log.
type logFileWriter struct{}

func (logFileWriter) Write(p []byte) (n int, err error) {
	f, err := os.OpenFile(currentLogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Write(p)
}

// statusRecorder wraps ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// httpMiddleware recovers from handler panics and logs 5xx responses.
func httpMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if p := recover(); p != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				appLog("[Panic] %s %s — %v\n%s", r.Method, r.URL.Path, p, buf[:n])
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(rec, r)
		if rec.status >= 500 {
			appLog("[HTTP %d] %s %s", rec.status, r.Method, r.URL.Path)
		}
	})
}

// safeGo runs fn in a new goroutine and logs any panic it produces.
func safeGo(fn func()) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				appLog("[Panic] goroutine: %v\n%s", p, buf[:n])
			}
		}()
		fn()
	}()
}
