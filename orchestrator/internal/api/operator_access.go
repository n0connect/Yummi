package api

import (
	"log/slog"
	"net"
	"net/http"

	"github.com/n0connect/yummi/orchestrator/internal/controlaccess"
)

// OperatorAccess is a network trust boundary for the single-operator local
// deployment. Only the loopback operator and the isolated UI proxy may reach
// this listener. The sandbox has a different, policy-read-only listener.
func OperatorAccess(rawPeers string) func(http.Handler) http.Handler {
	peers, err := controlaccess.ParsePeers(rawPeers)
	if err != nil {
		slog.Error("invalid operator peer policy; denying operator requests", "error", err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err != nil || !peers.Allows(r.RemoteAddr) || !localOperatorHost(r.Host) {
				writeError(w, http.StatusForbidden, "operator access denied", nil)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !isLocalhostOrigin(origin) {
				writeError(w, http.StatusForbidden, "operator origin denied", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func localOperatorHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}
