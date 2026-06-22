package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

func TestHandleGatewayRulesList(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type != "list_rules" {
			return gateway.Response{Type: "error", Message: "unexpected request type"}
		}
		expiresAt := int64(9999)
		return gateway.Response{
			Type: "rule_list",
			Rules: []gateway.RuleView{
				{ID: "manual-1", Action: "allow", Network: "10.0.0.5/32", Description: "[manual] test", ExpiresAt: &expiresAt, Source: "manual"},
			},
		}
	})
	c := gateway.NewClient(socket)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/rules", nil)
	w := httptest.NewRecorder()
	HandleGatewayRules(c).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "manual") {
		t.Fatalf("expected rules body to contain the manual rule, got: %s", w.Body.String())
	}
}

func TestHandleGatewayAddRuleRequiresReason(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		return gateway.Response{Type: "rule_added", RuleID: "manual-1", ExpiresAt: 1700}
	})
	c := gateway.NewClient(socket)

	body := strings.NewReader(`{"network":"10.0.0.5/32","ttl_secs":600,"reason":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/rules", body)
	w := httptest.NewRecorder()
	HandleGatewayAddRule(c).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty reason, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGatewayAddRuleSucceeds(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type != "add_manual_rule" || req.Reason == "" {
			return gateway.Response{Type: "error", Message: "unexpected request or missing reason"}
		}
		return gateway.Response{Type: "rule_added", RuleID: "manual-1", ExpiresAt: 1700}
	})
	c := gateway.NewClient(socket)

	body := strings.NewReader(`{"network":"10.0.0.5/32","ttl_secs":600,"reason":"emergency access for finding F-12"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/rules", body)
	w := httptest.NewRecorder()
	HandleGatewayAddRule(c).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "manual-1") {
		t.Fatalf("expected rule_id in body, got: %s", w.Body.String())
	}
}

func TestHandleGatewayAuditDegradesWhenUnreachable(t *testing.T) {
	c := gateway.NewClient("/nonexistent/socket")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/audit", nil)
	w := httptest.NewRecorder()
	HandleGatewayAudit(c).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("degraded audit should still return 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatalf("expected degraded-status body, got: %s", w.Body.String())
	}
}
