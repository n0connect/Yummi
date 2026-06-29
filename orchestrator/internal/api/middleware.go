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

// maxRequestBody bounds the size of a REST request body. It is generous enough
// not to restrict legitimate use (a chat turn that feeds a large tool output
// back for analysis) while capping unbounded-body memory exhaustion: every JSON
// handler decodes straight from r.Body, so without this a single oversized
// request could force an arbitrarily large allocation. The orchestrator binds to
// localhost, so this is defense-in-depth, not the primary boundary.
const maxRequestBody = 8 << 20 // 8 MiB

// BodyLimit caps the request body via http.MaxBytesReader, so a handler that
// reads past the limit gets a clean error (decoded as a 400) instead of
// allocating without bound. Applied to REST routes only; WebSocket upgrades read
// frames, not a JSON body, and must not be wrapped.
func BodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !isWebSocketRequest(r) {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
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
