package eino

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// Terminal tools reuse the pentestTool machinery but must NOT be rejected for
// lacking a network target (their catalog entries are target-free). These
// tests use the same fakeMCPExecer recording double the pentest tool tests
// use (defined in pentest_tools_test.go) — a seam double, not a mock of
// Yummi's own logic (real Store/Gate).

func TestTerminalTool_TermRun_DispatchesWithoutTargetGuard(t *testing.T) {
	store, err := agent.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	task, _ := store.CreateSubtask("sess-1", "drive a terminal", 5, "")

	mcpExec := &fakeMCPExecer{result: agent.ToolResult{Content: "uid=1000(pentester)"}}
	pt := newPentestTool(termRunSpec, pentestToolDeps{
		taskID: task.ID, sessionID: "sess-1", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := pt.InvokableRun(context.Background(),
		`{"id":"target","command":"id","rationale":"confirm user","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if mcpExec.calledTool != "term_run" {
		t.Fatalf("mcpExec called with tool = %q, want term_run", mcpExec.calledTool)
	}
	if mcpExec.calledArgs["id"] != "target" || mcpExec.calledArgs["command"] != "id" {
		t.Fatalf("mcpExec args = %+v, want id=target command=id", mcpExec.calledArgs)
	}
	// Approval bookkeeping must not leak into the MCP server's strict schema.
	if _, ok := mcpExec.calledArgs["rationale"]; ok {
		t.Fatalf("rationale must not be forwarded, got args = %+v", mcpExec.calledArgs)
	}
	if _, ok := mcpExec.calledArgs["risk_level"]; ok {
		t.Fatalf("risk_level must not be forwarded, got args = %+v", mcpExec.calledArgs)
	}
	if !strings.Contains(out, "uid=1000") {
		t.Fatalf("expected terminal output in result, got: %q", out)
	}
}

func TestTerminalTool_TermList_NoParamsDispatches(t *testing.T) {
	store, _ := agent.NewStore(nil)
	task, _ := store.CreateSubtask("sess-1", "list terminals", 5, "")

	mcpExec := &fakeMCPExecer{result: agent.ToolResult{Content: "no open sessions"}}
	pt := newPentestTool(termListSpec, pentestToolDeps{
		taskID: task.ID, sessionID: "sess-1", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := pt.InvokableRun(context.Background(), `{"rationale":"check state","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if mcpExec.calledTool != "term_list" {
		t.Fatalf("mcpExec called with tool = %q, want term_list", mcpExec.calledTool)
	}
	if !strings.Contains(out, "no open sessions") {
		t.Fatalf("expected list output, got: %q", out)
	}
}
