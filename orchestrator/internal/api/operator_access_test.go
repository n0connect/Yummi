package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/config"
)

func TestOperatorRouterRejectsUntrustedPeersAndOrigins(t *testing.T) {
	t.Setenv("YUMMI_OPERATOR_ALLOWED_PEERS", "127.0.0.1/32,::1/128,172.21.0.4/32")
	router := NewRouter(nil, nil, nil, config.Load(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	for _, tc := range []struct {
		name, peer, origin, forwarded, method, body string
	}{
		{"sandbox read", "172.20.0.10:41000", "", "", "GET", ""},
		{"sandbox mutation", "172.20.0.10:41000", "", "", "POST", `{}`},
		{"forwarded peer spoof", "172.20.0.10:41000", "", "127.0.0.1", "GET", ""},
		{"foreign origin JSON", "127.0.0.1:41000", "https://untrusted.invalid", "", "POST", `{}`},
		{"foreign origin simple POST", "172.21.0.4:41000", "https://untrusted.invalid", "", "POST", `{}`},
		{"opaque origin", "127.0.0.1:41000", "null", "", "GET", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/api/v1/gateway/egress-mode", strings.NewReader(tc.body))
			r.RemoteAddr = tc.peer
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			r.Header.Set("X-Real-IP", tc.forwarded)
			r.Header.Set("Content-Type", "text/plain")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("untrusted request reached operator handler: HTTP %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestOperatorAccessAcceptsOnlyLocalHostAndTrustedSocket(t *testing.T) {
	handler := OperatorAccess("127.0.0.1/32,::1/128,172.21.0.4/32")(http.HandlerFunc(HandleHealthz))
	for _, tc := range []struct {
		peer, host, origin string
		want               int
	}{
		{"127.0.0.1:80", "localhost:9080", "", 200},
		{"172.21.0.4:80", "localhost:3000", "http://localhost:3000", 200},
		{"[::1]:80", "[::1]:9080", "http://[::1]:9080", 200},
		{"172.21.0.4:80", "rebinding.invalid", "", 403},
		{"172.20.0.10:80", "localhost:9080", "http://localhost:3000", 403},
		{"127.0.0.1:80", "localhost", "javascript://localhost", 403},
		{"127.0.0.1:80", "localhost", "http://user@localhost", 403},
		{"127.0.0.1:80", "localhost", "http://localhost/path", 403},
	} {
		r := httptest.NewRequest("GET", "/healthz", nil)
		r.RemoteAddr, r.Host = tc.peer, tc.host
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("peer=%s host=%s origin=%s: got %d want %d", tc.peer, tc.host, tc.origin, w.Code, tc.want)
		}
	}
	deny := OperatorAccess("0.0.0.0/0")(http.HandlerFunc(HandleHealthz))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://localhost/healthz", nil)
	r.RemoteAddr = "127.0.0.1:80"
	deny.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("invalid policy did not fail closed: %d", w.Code)
	}
}
