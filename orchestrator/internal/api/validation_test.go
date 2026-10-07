package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

func TestHandleCreateSessionValidation(t *testing.T) {
	db, err := session.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sm := session.NewManager(db)
	h := HandleCreateSession(sm)

	// Valid name
	req1 := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"name":"Valid-Session_Name 1"}`))
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid name, got %d: %s", w1.Code, w1.Body.String())
	}

	// Invalid name - too long
	longName := strings.Repeat("A", 101)
	req2 := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"name":"`+longName+`"}`))
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for too long name, got %d", w2.Code)
	}

	// Invalid name - bad characters
	req3 := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"name":"Session<script>"}`))
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsafe chars in name, got %d", w3.Code)
	}

	// Valid name - Turkish letters + ellipsis (auto-titles from a Turkish first
	// message must validate; the bilingual UI would otherwise silently keep the
	// placeholder "Yeni Sohbet").
	req4 := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"name":"Göz taraması 10.0.0.1…"}`))
	w4 := httptest.NewRecorder()
	h.ServeHTTP(w4, req4)
	if w4.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid Turkish name, got %d: %s", w4.Code, w4.Body.String())
	}
}

func TestHandleGetSessionValidation(t *testing.T) {
	db, err := session.OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sm := session.NewManager(db)

	r := chi.NewRouter()
	r.Get("/sessions/{id}", HandleGetSession(sm))

	// Malformed session ID
	req := httptest.NewRequest(http.MethodGet, "/sessions/session_id_with_!!!_invalid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed session ID, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateTargetValidation(t *testing.T) {
	ks, err := knowledge.NewStore(nil) // map-backed
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Post("/sessions/{sessionID}/targets", HandleCreateTarget(ks))

	// Valid target IP
	req1 := httptest.NewRequest(http.MethodPost, "/sessions/default/targets", strings.NewReader(`{"value":"10.0.0.1","in_scope":true,"notes":"Test"}`))
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid target IP, got %d: %s", w1.Code, w1.Body.String())
	}

	// Valid URL canonicalized to host
	reqURL := httptest.NewRequest(http.MethodPost, "/sessions/default/targets", strings.NewReader(`{"value":"https://api.target.com/v1/auth","in_scope":true}`))
	wURL := httptest.NewRecorder()
	r.ServeHTTP(wURL, reqURL)
	if wURL.Code != http.StatusCreated {
		t.Errorf("expected 201 for URL target, got %d: %s", wURL.Code, wURL.Body.String())
	}

	// Valid host:port canonicalized to host
	reqPort := httptest.NewRequest(http.MethodPost, "/sessions/default/targets", strings.NewReader(`{"value":"192.168.1.50:8080","in_scope":true}`))
	wPort := httptest.NewRecorder()
	r.ServeHTTP(wPort, reqPort)
	if wPort.Code != http.StatusCreated {
		t.Errorf("expected 201 for host:port target, got %d: %s", wPort.Code, wPort.Body.String())
	}

	// Invalid target IP
	req2 := httptest.NewRequest(http.MethodPost, "/sessions/default/targets", strings.NewReader(`{"value":"999.999.999.999","in_scope":true}`))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid target IP, got %d: %s", w2.Code, w2.Body.String())
	}

	// Invalid target hostname
	req3 := httptest.NewRequest(http.MethodPost, "/sessions/default/targets", strings.NewReader(`{"value":"not_a_valid_hostname","in_scope":true}`))
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid hostname, got %d: %s", w3.Code, w3.Body.String())
	}
}

func TestHandleCreateFindingValidation(t *testing.T) {
	ks, err := knowledge.NewStore(nil)
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Post("/sessions/{sessionID}/findings", HandleCreateFinding(ks))

	// Valid finding
	req1 := httptest.NewRequest(http.MethodPost, "/sessions/default/findings", strings.NewReader(`{"severity":"medium","title":"Valid Finding Title","description":"Some desc","evidence":"Some evidence"}`))
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid finding, got %d: %s", w1.Code, w1.Body.String())
	}

	// Invalid severity
	req2 := httptest.NewRequest(http.MethodPost, "/sessions/default/findings", strings.NewReader(`{"severity":"critical_bypass","title":"Finding Title"}`))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid severity, got %d", w2.Code)
	}

	// Over-length title
	longTitle := strings.Repeat("T", 201)
	req3 := httptest.NewRequest(http.MethodPost, "/sessions/default/findings", strings.NewReader(`{"severity":"high","title":"`+longTitle+`"}`))
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for over-length title, got %d", w3.Code)
	}
}

func TestHandleChatValidation(t *testing.T) {
	h := HandleChat(nil, nil, nil, nil, nil, 90*time.Second) // Assistant panel unavailable

	// Over-length message
	longMsg := strings.Repeat("M", 10001)
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message":"`+longMsg+`","session_id":"default"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for over-length message, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGatewayAddRuleValidation(t *testing.T) {
	h := HandleGatewayAddRule(nil)

	// Valid requests would require a mock/live client. We test structural checks before that.
	// Invalid TTL
	req1 := httptest.NewRequest(http.MethodPost, "/gateway/rules", strings.NewReader(`{"network":"10.0.0.1/32","ttl_secs":90000,"reason":"test"}`))
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for TTL > 86400, got %d: %s", w1.Code, w1.Body.String())
	}

	// Invalid Reason - special/unsafe characters
	req2 := httptest.NewRequest(http.MethodPost, "/gateway/rules", strings.NewReader(`{"network":"10.0.0.1/32","ttl_secs":60,"reason":"reason <script>"}`))
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsafe reason, got %d: %s", w2.Code, w2.Body.String())
	}
}
