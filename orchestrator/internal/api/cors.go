package api

import "net/http"

// CORSMiddleware sets CORS headers, but ONLY for loopback origins. The previous
// implementation reflected ANY Origin back in Access-Control-Allow-Origin while
// also sending Access-Control-Allow-Credentials: true — that let any website the
// operator visited make credentialed cross-origin calls to the localhost API and
// READ the responses (enumerate sessions/findings, drive /chat and /sandbox/exec).
// We now reflect the origin only when it is localhost, matching the WebSocket
// upgrade check (isLocalhostOrigin is the shared source of truth). In production
// the browser talks same-origin through nginx; in dev Vite runs on localhost — so
// legitimate flows are unaffected while cross-site origins get no CORS grant.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isLocalhostOrigin(origin) {
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
