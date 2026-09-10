package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

func setupTestRouter(ks *knowledge.Store, sm *session.Manager) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/api/v1/sessions/{sessionID}/assets", HandleListAssets(ks))
	r.Post("/api/v1/sessions/{sessionID}/assets", HandleCreateAsset(ks))
	r.Get("/api/v1/sessions/{sessionID}/report", HandleGenerateReport(ks, sm))
	r.Get("/api/v1/proxy/status", HandleProxyStatus())
	r.Post("/api/v1/proxy/inject", HandleProxyInject(ks, sm))
	return r
}

func TestAssetsHandlers(t *testing.T) {
	ks, err := knowledge.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := setupTestRouter(ks, nil)

	// 1. Create asset
	body := strings.NewReader(`{"type":"domain","value":"test.example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/sess-1/assets", body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "test.example.com") {
		t.Fatalf("expected asset value in body, got: %s", w.Body.String())
	}

	// 2. List flat assets
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/assets", nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wList.Code, wList.Body.String())
	}
	if !strings.Contains(wList.Body.String(), "test.example.com") {
		t.Fatalf("expected asset in list, got: %s", wList.Body.String())
	}

	// 3. List tree assets
	reqTree := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-1/assets?format=tree", nil)
	wTree := httptest.NewRecorder()
	r.ServeHTTP(wTree, reqTree)

	if wTree.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for tree, got %d: %s", wTree.Code, wTree.Body.String())
	}
}

func TestReportHandler(t *testing.T) {
	ks, err := knowledge.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	_ = ks.CreateTarget(context.Background(), knowledge.Target{SessionID: "sess-rep-1", Value: "api.target.com", InScope: true})
	_ = ks.RecordFinding(context.Background(), knowledge.Finding{
		SessionID:   "sess-rep-1",
		Title:       "IDOR in Account API",
		Severity:    "high",
		Description: "Horizontal privilege escalation.",
		Source:      "agent",
	})
	_, _ = ks.SaveAsset(context.Background(), knowledge.AssetNode{
		SessionID: "sess-rep-1",
		Type:      knowledge.AssetSubdomain,
		Value:     "api.target.com",
	})

	r := setupTestRouter(ks, nil)

	// HackerOne format
	reqH1 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-rep-1/report?format=hackerone", nil)
	wH1 := httptest.NewRecorder()
	r.ServeHTTP(wH1, reqH1)

	if wH1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wH1.Code, wH1.Body.String())
	}
	if !strings.Contains(wH1.Body.String(), "IDOR in Account API") {
		t.Fatalf("expected finding title in report, got: %s", wH1.Body.String())
	}

	// Executive format
	reqExec := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-rep-1/report?format=executive", nil)
	wExec := httptest.NewRecorder()
	r.ServeHTTP(wExec, reqExec)

	if wExec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wExec.Code, wExec.Body.String())
	}
	if !strings.Contains(wExec.Body.String(), "Executive Report") {
		t.Fatalf("expected executive title, got: %s", wExec.Body.String())
	}
}

func TestProxyHandlers(t *testing.T) {
	ks, err := knowledge.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := setupTestRouter(ks, nil)

	// Proxy status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/proxy/status", nil)
	wStatus := httptest.NewRecorder()
	r.ServeHTTP(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wStatus.Code, wStatus.Body.String())
	}
	if !strings.Contains(wStatus.Body.String(), `"status":"ready"`) {
		t.Fatalf("expected status ready, got: %s", wStatus.Body.String())
	}

	// Proxy inject
	injectBody := strings.NewReader(`{
		"session_id": "sess-proxy-1",
		"method": "GET",
		"url": "https://vault.target.com/v2/tokens?app=web&mode=debug",
		"status_code": 200
	}`)
	reqInject := httptest.NewRequest(http.MethodPost, "/api/v1/proxy/inject", injectBody)
	wInject := httptest.NewRecorder()
	r.ServeHTTP(wInject, reqInject)

	if wInject.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", wInject.Code, wInject.Body.String())
	}
	if !strings.Contains(wInject.Body.String(), `"status":"ingested"`) {
		t.Fatalf("expected ingested status, got: %s", wInject.Body.String())
	}
	if !strings.Contains(wInject.Body.String(), `"host":"vault.target.com"`) {
		t.Fatalf("expected host in response, got: %s", wInject.Body.String())
	}

	// Verify assets were saved to ks
	assets, err := ks.ListAssets(context.Background(), "sess-proxy-1")
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) == 0 {
		t.Fatalf("expected assets to be created from proxy inject, got 0")
	}
}
