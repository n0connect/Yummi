package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// The operator briefing is the first brick of Yummi's "operator drives, AI
// assists" direction: the human working by hand asks Yummi what it already
// knows about a tool/goal — matching skills, tactics that worked before,
// findings already on record — and gets it back FOR THEIR OWN USE, not
// folded into an agent prompt. Everything the AI conductor is fed, the
// operator can now pull for themselves.
func TestHandleOperatorBriefingSurfacesAccumulatedKnowledge(t *testing.T) {
	reg := skill.New([]skill.Skill{
		{Name: "recon", Description: "port scanning", Triggers: []string{"nmap", "scan", "recon"}, Knowledge: "scan first"},
		{Name: "web", Description: "web assessment", Triggers: []string{"whatweb", "http"}, Knowledge: "fingerprint"},
	})
	ks, _ := knowledge.NewStore(nil)
	_ = ks.RecordTactic(context.Background(), "nmap -sV 10.0.0.5", "worked", nil, "top ports first")
	_ = ks.RecordFinding(context.Background(), knowledge.Finding{
		SessionID: "s1", Severity: "info", Title: "ssh open", Description: "port 22", Source: "operator",
	})

	r := chi.NewRouter()
	r.Get("/operator/briefing", HandleOperatorBriefing(reg, ks))

	req := httptest.NewRequest(http.MethodGet, "/operator/briefing?goal=recon+scan&tool=nmap&session=s1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("briefing: %d %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Skills   []skill.Skill       `json:"skills"`
		Tactics  []knowledge.Tactic  `json:"tactics"`
		Findings []knowledge.Finding `json:"findings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}

	// The nmap/recon skill must be surfaced (matched on triggers); the
	// unrelated "web" skill must not.
	if len(got.Skills) != 1 || got.Skills[0].Name != "recon" {
		t.Fatalf("expected only the recon skill surfaced, got %+v", got.Skills)
	}
	// The recorded nmap tactic must come back for the operator to reuse.
	if len(got.Tactics) != 1 || got.Tactics[0].Tool != "nmap" {
		t.Fatalf("expected the nmap tactic surfaced, got %+v", got.Tactics)
	}
	// Findings already on record for this session are part of the operator's
	// briefing — what's already been established, so they don't redo it.
	if len(got.Findings) != 1 || got.Findings[0].Title != "ssh open" {
		t.Fatalf("expected the session finding surfaced, got %+v", got.Findings)
	}
}

func TestHandleOperatorBriefingNoSessionOmitsFindings(t *testing.T) {
	reg := skill.New(nil)
	ks, _ := knowledge.NewStore(nil)
	r := chi.NewRouter()
	r.Get("/operator/briefing", HandleOperatorBriefing(reg, ks))

	// No session param — findings require a session; must degrade to an
	// empty list, not error, so the operator can still pull skills/tactics
	// before a session is even chosen.
	req := httptest.NewRequest(http.MethodGet, "/operator/briefing?tool=nmap", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("briefing without session should be OK: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Findings []knowledge.Finding `json:"findings"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Findings) != 0 {
		t.Fatalf("no session should mean no findings, got %+v", got.Findings)
	}
}

func TestHandleOperatorBriefingRejectsBadSession(t *testing.T) {
	reg := skill.New(nil)
	ks, _ := knowledge.NewStore(nil)
	r := chi.NewRouter()
	r.Get("/operator/briefing", HandleOperatorBriefing(reg, ks))

	req := httptest.NewRequest(http.MethodGet, "/operator/briefing?session=../etc/passwd", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed session id must be rejected, got %d %s", rec.Code, rec.Body.String())
	}
}
