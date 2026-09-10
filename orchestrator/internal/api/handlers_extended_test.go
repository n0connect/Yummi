package api

import (
	"context"
	"encoding/json"
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
	r.Get("/api/v1/sessions/{sessionID}/scope-rules", HandleListScopeRules(ks))
	r.Post("/api/v1/sessions/{sessionID}/scope-rules", HandleCreateScopeRules(ks))
	r.Delete("/api/v1/sessions/{sessionID}/scope-rules/{ruleID}", HandleDeleteScopeRule(ks))
	r.Post("/api/v1/sessions/{sessionID}/scope-rules/classify", HandleClassifyScope(ks))
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

func TestScopeRuleHandlers(t *testing.T) {
	ks, err := knowledge.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := setupTestRouter(ks, nil)
	sessionID := "sess-scope-1"

	// 1. Bulk-import a program's published scope in one call.
	bulkBody := strings.NewReader(`{"rules":[
		{"pattern":"*.example.com","kind":"include"},
		{"pattern":"internal.example.com","kind":"exclude","note":"staging, out of program scope"}
	]}`)
	reqBulk := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/scope-rules", bulkBody)
	wBulk := httptest.NewRecorder()
	r.ServeHTTP(wBulk, reqBulk)
	if wBulk.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", wBulk.Code, wBulk.Body.String())
	}
	var saved []knowledge.ScopeRule
	if err := json.Unmarshal(wBulk.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("want 2 saved rules, got %d: %s", len(saved), wBulk.Body.String())
	}

	// 2. List.
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/scope-rules", nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wList.Code, wList.Body.String())
	}
	if !strings.Contains(wList.Body.String(), "internal.example.com") {
		t.Fatalf("expected rule in list, got: %s", wList.Body.String())
	}

	// 3. Classify — exclude must win over the broader include.
	classifyBody := strings.NewReader(`{"hostnames":["app.example.com","internal.example.com","unrelated.com"]}`)
	reqClassify := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/scope-rules/classify", classifyBody)
	wClassify := httptest.NewRecorder()
	r.ServeHTTP(wClassify, reqClassify)
	if wClassify.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wClassify.Code, wClassify.Body.String())
	}
	var verdicts map[string]knowledge.ScopeVerdict
	if err := json.Unmarshal(wClassify.Body.Bytes(), &verdicts); err != nil {
		t.Fatalf("decode classify response: %v", err)
	}
	if !verdicts["app.example.com"].InScope {
		t.Fatalf("app.example.com should be in scope: %+v", verdicts)
	}
	if verdicts["internal.example.com"].InScope {
		t.Fatalf("internal.example.com should be excluded: %+v", verdicts)
	}
	if verdicts["unrelated.com"].InScope {
		t.Fatalf("unrelated.com should be out of scope (whitelist miss): %+v", verdicts)
	}

	// 4. Delete.
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sessionID+"/scope-rules/"+saved[0].ID, nil)
	wDel := httptest.NewRecorder()
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d: %s", wDel.Code, wDel.Body.String())
	}

	// 5. Malformed rule is rejected, not silently dropped.
	badBody := strings.NewReader(`{"pattern":"","kind":"include"}`)
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/scope-rules", badBody)
	wBad := httptest.NewRecorder()
	r.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty pattern, got %d: %s", wBad.Code, wBad.Body.String())
	}
}
