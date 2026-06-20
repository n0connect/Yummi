package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// LoggingMiddleware logs each HTTP request with structured fields.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		// UI üzerindeki system/status, system/logs veya healthz gibi sürekli atılan polling isteklerini loglama
		path := r.URL.Path
		if path == "/healthz" || path == "/api/v1/system/status" || path == "/api/v1/system/logs" || path == "/ws/events" || path == "/ws/terminal" {
			return
		}

		slog.Info("http",
			"method", r.Method,
			"path", path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

// JSONContentType sets Content-Type to application/json for API routes.
func JSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// TimeoutUnlessWebSocket applies request timeouts to normal HTTP requests but
// skips WebSocket upgrade requests, which are long-lived by design.
func TimeoutUnlessWebSocket(timeout time.Duration) func(http.Handler) http.Handler {
	withTimeout := middleware.Timeout(timeout)

	return func(next http.Handler) http.Handler {
		timed := withTimeout(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isWebSocketRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			timed.ServeHTTP(w, r)
		})
	}
}

func isWebSocketRequest(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}
