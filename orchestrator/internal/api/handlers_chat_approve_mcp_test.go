package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

// approveProposal is a small helper shared by both tests below: puts a
// proposal in the approval store, POSTs /chat/approve for it, and returns
// the resulting execution.Record so the test can assert on how it was
// dispatched (plain shell vs MCP). MCPTool/MCPArgs/Command are set at
// Enqueue/EnqueueMCP time, before the async processor ever runs, so a
// single Get right after the HTTP call is deterministic — no need to wait
// for (or race with) the processor goroutine actually executing.
func approveProposal(t *testing.T, hints map[string]string, command string) execution.Record {
	t.Helper()
	approvals := conductor.NewApprovalStore(0)
	es := execution.NewStore()
	es.SetProcessor(func(context.Context, execution.Record) (string, error) { return "ok", nil })

	proposal := conductor.Proposal{ID: "p1", Command: command, RiskLevel: "low"}
	approvals.Put("sess1", proposal)

	h := HandleChatApprove(approvals, es, nil, hints)
	body := `{"session_id":"sess1","proposal_id":"p1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/approve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Execution struct {
				ID string `json:"id"`
			} `json:"execution"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	rec, ok := es.Get(resp.Data.Execution.ID)
	if !ok {
		t.Fatalf("execution record %q not found", resp.Data.Execution.ID)
	}
	return rec
}

// TestHandleChatApprove_KnownMCPToolVerb_DispatchesViaMCP is the regression
// test for the conductor's single-command suggest/approve flow, which has
// no [MCP_TOOL] concept at all: before this fix, approving a model's
// CLI-shaped guess at a cataloged tool name ("term_open --label check1")
// always shell-exec'd it and guaranteed a 127 "command not found". Reuses
// agent.InferMCPToolFromShellGuess — the exact same detection already
// proven for the graph agent loop's identical failure mode.
func TestHandleChatApprove_KnownMCPToolVerb_DispatchesViaMCP(t *testing.T) {
	hints := map[string]string{"term_open": "label, cols, rows"}
	rec := approveProposal(t, hints, "term_open --label check1")

	if rec.MCPTool != "term_open" {
		t.Fatalf("expected MCPTool=term_open, got record: %+v", rec)
	}
	if !strings.Contains(rec.MCPArgs, `"label":"check1"`) {
		t.Fatalf("expected args to carry label=check1, got %q", rec.MCPArgs)
	}
}

// TestHandleChatApprove_UnknownVerb_StaysPlainShell proves the rewrite only
// ever fires for a REAL cataloged tool name — an ordinary shell command
// must still dispatch exactly as before this fix.
func TestHandleChatApprove_UnknownVerb_StaysPlainShell(t *testing.T) {
	hints := map[string]string{"term_open": "label, cols, rows"}
	rec := approveProposal(t, hints, "nmap -sV 127.0.0.1")

	if rec.MCPTool != "" {
		t.Fatalf("expected an ordinary shell dispatch (no MCPTool), got record: %+v", rec)
	}
	if rec.Command != "nmap -sV 127.0.0.1" {
		t.Fatalf("expected the command unchanged, got %q", rec.Command)
	}
}
