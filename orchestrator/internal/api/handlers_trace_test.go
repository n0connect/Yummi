package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

func TestHandleListTacticsAll(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	_ = ks.RecordTactic(context.Background(), "nmap -sV 10.0.0.5", "worked", nil, "")
	r := chi.NewRouter()
	r.Get("/tactics", HandleListTactics(ks))
	req := httptest.NewRequest(http.MethodGet, "/tactics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "TARGET") || !strings.Contains(rec.Body.String(), "nmap") {
		t.Fatalf("tactics: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleListContextAndActivityEmpty(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	r := chi.NewRouter()
	r.Get("/sessions/{sessionID}/context", HandleListContext(ks))
	req := httptest.NewRequest(http.MethodGet, "/sessions/s1/context", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty context should be []: %d %q", rec.Code, rec.Body.String())
	}
}

func TestHandleListRolesNilSafe(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/agent/roles", HandleListRoles(nil))
	req := httptest.NewRequest(http.MethodGet, "/agent/roles", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("nil runner roles should be 200, got %d", rec.Code)
	}
}
