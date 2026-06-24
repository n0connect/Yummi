package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// mounts the finding handlers on a real chi router so URL params resolve.
func knowledgeRouter(ks *knowledge.Store) http.Handler {
	r := chi.NewRouter()
	r.Post("/sessions/{sessionID}/findings", HandleCreateFinding(ks))
	r.Get("/sessions/{sessionID}/findings", HandleListFindings(ks))
	r.Post("/sessions/{sessionID}/targets", HandleCreateTarget(ks))
	r.Get("/sessions/{sessionID}/targets", HandleListTargets(ks))
	return r
}

func TestHandleCreateFindingPersistsToKnowledge(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	srv := knowledgeRouter(ks)

	body, _ := json.Marshal(map[string]string{"severity": "high", "title": "open redis"})
	req := httptest.NewRequest(http.MethodPost, "/sessions/s1/findings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	got := ks.ListFindings("s1")
	if len(got) != 1 || got[0].Source != "manual" || got[0].Title != "open redis" {
		t.Fatalf("finding not persisted with manual source: %+v", got)
	}
}

func TestHandleCreateTargetPersistsToKnowledge(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	srv := knowledgeRouter(ks)

	body, _ := json.Marshal(map[string]any{"value": "10.0.0.5", "in_scope": true})
	req := httptest.NewRequest(http.MethodPost, "/sessions/s1/targets", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	got := ks.ListTargets("s1")
	if len(got) != 1 || got[0].Value != "10.0.0.5" || !got[0].InScope {
		t.Fatalf("target not persisted: %+v", got)
	}
}
