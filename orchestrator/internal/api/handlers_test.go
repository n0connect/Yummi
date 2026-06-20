package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

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
