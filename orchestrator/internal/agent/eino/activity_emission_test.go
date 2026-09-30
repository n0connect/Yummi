package eino

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver used across orchestrator (session/execution)

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// activityTestStore opens a real in-memory SQLite-backed Store — ListActivity
// is a DB-only read path (see Store.ListActivity), unlike the pure in-memory
// task map NewStore(nil) gives every other test in this package, so the
// activity-emission assertions below need this instead.
func activityTestStore(t *testing.T) *agent.Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := agent.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// activity_emission_test.go covers a real gap found live: driving the Eino
// backend against a real OpenRouter model through a real multi-stage task
// produced 27 real, successfully-persisted execution.Records — but the
// operator's AGENT tab (backed by agent_activity, not executions) showed
// nothing but the task's opening line for the run's entire duration. The
// graph backend has always emitted ActCommand/ActCommandDone per step
// (node_tool.go/graph.go); runToolPipeline — shared by every pentest tool,
// run_command, and the terminal-session verbs — never did.

func TestRunToolPipeline_EmitsActCommandAndActCommandDoneOnSuccess(t *testing.T) {
	store := activityTestStore(t)
	task, err := store.CreateSubtask("sess-activity", "scan it", 5, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}

	mcpExec := &fakeMCPExecer{result: agent.ToolResult{Content: "Nmap done: 1 host up"}}
	pt := newPentestTool(nmapScanSpec, pentestToolDeps{
		taskID: task.ID, sessionID: "sess-activity", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	if _, err := pt.InvokableRun(context.Background(),
		`{"target":"10.0.0.5","rationale":"confirm host is up","risk_level":"low"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	events := store.ListActivity("sess-activity", 20)
	var sawCommand, sawCommandDone bool
	for _, e := range events {
		if e.Kind == agent.ActCommand {
			sawCommand = true
			if !strings.Contains(e.Detail, "confirm host is up") {
				t.Errorf("ActCommand detail = %q, want the rationale", e.Detail)
			}
		}
		if e.Kind == agent.ActCommandDone {
			sawCommandDone = true
			if !strings.Contains(e.Detail, "Nmap done") {
				t.Errorf("ActCommandDone detail = %q, want the tool output", e.Detail)
			}
		}
	}
	if !sawCommand {
		t.Error("expected an ActCommand activity event, got none — the AGENT tab would show nothing for this step")
	}
	if !sawCommandDone {
		t.Error("expected an ActCommandDone activity event, got none")
	}
}

// A blocked command (out-of-scope target, mirroring the graph backend's own
// COMMAND_BLOCKED path) must still announce intent via ActCommand — the
// operator should see "the agent tried this" — but must NOT get a false
// ActCommandDone, since nothing actually completed.
func TestRunToolPipeline_BlockedCommand_NoActCommandDone(t *testing.T) {
	store := activityTestStore(t)
	task, err := store.CreateSubtask("sess-blocked", "scan it", 5, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}

	// Simulates the real path: the command DID run (execFn returns no Go
	// error), but the gateway rejected the target as out of scope —
	// executor.RunScopedCommand tags this with the stable gatewayBlockedMarker
	// prefix Verify() recognizes (see verify.go).
	mcpExec := &fakeMCPExecer{result: agent.ToolResult{
		IsError: true, Content: "GATEWAY_BLOCKED: target 10.0.0.5 is out of scope",
	}}
	pt := newPentestTool(nmapScanSpec, pentestToolDeps{
		taskID: task.ID, sessionID: "sess-blocked", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := pt.InvokableRun(context.Background(),
		`{"target":"10.0.0.5","rationale":"try it","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "COMMAND_BLOCKED") {
		t.Fatalf("expected a COMMAND_BLOCKED result, got: %q", out)
	}

	events := store.ListActivity("sess-blocked", 20)
	var sawCommand bool
	for _, e := range events {
		if e.Kind == agent.ActCommand {
			sawCommand = true
		}
		if e.Kind == agent.ActCommandDone {
			t.Errorf("did not expect ActCommandDone for a blocked command, got detail=%q", e.Detail)
		}
	}
	if !sawCommand {
		t.Error("expected an ActCommand event even for a command that ended up blocked — the operator should see the agent tried")
	}
}

// unknownToolHandler is the recovery path for a real live-testing incident:
// DeepSeek called "nuclei_scan" directly (a cataloged tool discoverable only
// via search_pentest_tools/run_pentest_tool, never individually registered)
// and Eino's ToolsNode aborted the entire task with a framework-level error
// before this handler existed. It must name both real recovery paths and
// must not blow up on its own.
func TestUnknownToolHandler_NamesRecoveryPath(t *testing.T) {
	out, err := unknownToolHandler(context.Background(), "nuclei_scan", `{"target":"http://x"}`)
	if err != nil {
		t.Fatalf("unknownToolHandler returned an error, want a recoverable string result: %v", err)
	}
	for _, want := range []string{"nuclei_scan", "run_pentest_tool", "search_pentest_tools"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected result to mention %q, got: %s", want, out)
		}
	}
}

// TestRunToolPipeline_ExecTimeout_RecoversInsteadOfAbortingTask covers a real
// live-testing incident: DeepSeek chose `nmap -sU -p 1-1000` (a UDP sweep,
// inherently slow) against the lab target; it hit the sandbox's hard exec
// timeout (internal/agent/exec.go's hardCap) 8m51s in, and that timeout's Go
// error propagated all the way up through Eino's ToolsNode as a fatal
// NodeRunError, aborting the entire task over one overly-broad scan choice.
func TestRunToolPipeline_ExecTimeout_RecoversInsteadOfAbortingTask(t *testing.T) {
	store, err := agent.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	task, err := store.CreateSubtask("sess-1", "scan it", 5, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}

	mcpExec := &fakeMCPExecer{err: fmt.Errorf("wait timeout: mcp tool nmap_scan did not finish within 10m0s")}
	pt := newPentestTool(nmapScanSpec, pentestToolDeps{
		taskID: task.ID, sessionID: "sess-1", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := pt.InvokableRun(context.Background(),
		`{"target":"10.0.0.5","rationale":"udp sweep","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun returned a Go error — this is exactly what aborts the task; want a recoverable string result instead: %v", err)
	}
	if !strings.Contains(out, "TOOL_TIMEOUT") {
		t.Errorf("expected a TOOL_TIMEOUT result the model can adapt to, got: %q", out)
	}
}
