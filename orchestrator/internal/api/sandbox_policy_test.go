package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxPolicyHasNoOperatorAuthority(t *testing.T) {
	router := NewSandboxPolicyRouter(nil)
	for _, path := range []string{"/api/v1/gateway/egress-mode", "/api/v1/sessions", "/api/v1/sandbox/exec", "/api/v1/agent/mode", "/ws/events", "/v1/egress/check/extra"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			r := httptest.NewRequest(method, path, strings.NewReader(`{"mode":"allow_all"}`))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Errorf("unexpected policy authority: %s %s -> %d", method, path, w.Code)
			}
		}
	}
	for _, input := range []string{`{}`, `{"ip":"example.com"}`, `{"ip":"1.1.1.1","mode":"allow_all"}`, `{"ip":"1.1.1.1"}{}`, strings.Repeat(" ", 1025) + `{"ip":"1.1.1.1"}`} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/v1/egress/check", strings.NewReader(input)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid policy input accepted (%d): %.80s", w.Code, input)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/v1/egress/check", strings.NewReader(`{"ip":"1.1.1.1"}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("missing gateway did not fail closed: %d", w.Code)
	}
}
