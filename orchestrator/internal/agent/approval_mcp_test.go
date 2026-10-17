package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
)

// A real unavailable MCP client must produce an MCP execution failure, rather
// than sending the approval label to a shell. No model or tool is substituted.
func TestApprovedMCPUsesTypedExecutionAndFrozenArguments(t *testing.T) {
	for _, inferred := range []bool{false, true} {
		name := "typed"
		if inferred {
			name = "inferred"
		}
		t.Run(name, func(t *testing.T) {
			store, err := NewStore(openTestDB(t))
			if err != nil {
				t.Fatal(err)
			}
			task, err := store.Create("approval-mcp", "inspect local page", 4)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Transition(task.ID, StatusPlanned, nil); err != nil {
				t.Fatal(err)
			}
			if err = store.Transition(task.ID, StatusRunning, nil); err != nil {
				t.Fatal(err)
			}
			es := execution.NewStore()
			es.SetProcessor(executor.New(nil, nil, nil, nil).ProcessFunc())
			original := map[string]any{"url": "http://127.0.0.1:18888/browser-qa.html"}
			action := Action{Tool: "browser_open", Args: original, Rationale: "inspect local page", RiskLevel: "low"}
			if inferred {
				action.Tool, action.Args, action.Command = "", nil, "browser_open --url http://127.0.0.1:18888/browser-qa.html"
			}
			s := &GraphState{taskID: task.ID, store: store, gate: NewGate(), validator: conductor.NewValidator(),
				exec: NewStoreExecer(es, time.Second), pendingAct: action,
				cfg: Config{LocalModel: true, ApprovalMode: ModeAskPermission, ApprovalTimeout: time.Second,
					MCPExec: NewStoreMCPExecer(es, time.Second), MCPSchemaHints: map[string]string{"browser_open": "url:string"}}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if edge, err := toolNode(ctx, s); err != nil || edge != edgeRequiresApproval {
				t.Fatalf("tool gate: %q %v", edge, err)
			}
			// The caller's old map cannot change the action already proposed.
			original["url"] = "https://outside-authorized-goal.invalid/"
			type outcome struct {
				edge string
				err  error
			}
			done := make(chan outcome, 1)
			go func() { edge, err := approvalNode(ctx, s); done <- outcome{edge, err} }()
			pending := waitForStatus(t, store, task.ID, StatusAwaitingApproval, time.Second)
			var args map[string]any
			if pending.PendingMCPTool != "browser_open" || json.Unmarshal([]byte(pending.PendingMCPArgs), &args) != nil || args["url"] != "http://127.0.0.1:18888/browser-qa.html" {
				t.Fatalf("approval does not describe the frozen call: %+v", pending)
			}
			if err = s.gate.Resolve(task.ID, pending.PendingProposalID, true, ""); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				if got.err != nil || got.edge != edgeIsError {
					t.Fatalf("unavailable MCP result: %q %v", got.edge, got.err)
				}
			case <-ctx.Done():
				t.Fatal("approved action never returned")
			}
			records := es.ListBySession(task.SessionID, 10)
			if len(records) != 1 {
				t.Fatalf("execution count: %d", len(records))
			}
			rec := records[0]
			if rec.MCPTool != pending.PendingMCPTool || rec.MCPArgs != pending.PendingMCPArgs || rec.Command != pending.PendingCommand || rec.Status != execution.StatusFailed || !strings.Contains(rec.Error, "sandbox MCP client not configured") {
				t.Fatalf("approved typed call took the wrong path: %+v", rec)
			}
		})
	}
}
