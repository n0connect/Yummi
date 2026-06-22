package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

func TestHandleSandboxExecAddsTemporaryRuleOnInScopeTarget(t *testing.T) {
	var addRuleCalled bool
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		switch req.Type {
		case "check_scope":
			return gateway.Response{Type: "scope_check_result", IP: req.IP, InScope: true}
		case "add_temporary_rule":
			addRuleCalled = true
			return gateway.Response{Type: "rule_added", RuleID: "r1", ExpiresAt: 9999}
		default:
			return gateway.Response{Type: "error", Message: "unexpected request"}
		}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent") // Exec will fail — fine, we only assert the gateway call happened first

	body := strings.NewReader(`{"command":"nmap -sV 10.0.0.5"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandbox/exec", body)
	w := httptest.NewRecorder()
	HandleSandboxExec(bridge, pc, nil).ServeHTTP(w, req)

	if !addRuleCalled {
		t.Fatal("expected AddTemporaryRule to be called for an in-scope target (this was the known bug — scope-checked but never unlocked)")
	}
}

func TestHandleSandboxExecBlocksTargetedCommandWhenGatewayUnavailable(t *testing.T) {
	h := HandleSandboxExec(sandbox.NewBridge("unused-container"), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandbox/exec", strings.NewReader(`{"command":"nmap -sV 10.0.0.1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when gateway is unavailable, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "gateway scope check unavailable") {
		t.Fatalf("expected gateway unavailable error, got: %s", w.Body.String())
	}
}
