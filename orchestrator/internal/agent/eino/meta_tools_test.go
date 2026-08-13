package eino

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// fakeMCPLister is a plain recording double for mcpLister — not a mock of
// Yummi's own code (internal/sandboxmcp.Client is exercised for real in its
// own package via the SDK's in-memory transport).
type fakeMCPLister struct {
	tools []*mcp.Tool
	err   error
}

func (f *fakeMCPLister) ListTools(_ context.Context) ([]*mcp.Tool, error) {
	return f.tools, f.err
}

func TestSearchPentestTools_ListsEverythingWithoutQuery(t *testing.T) {
	lister := &fakeMCPLister{tools: []*mcp.Tool{
		{Name: "amass_enum", Description: "Enumerate subdomains", InputSchema: map[string]any{"type": "object"}},
		{Name: "hydra_bruteforce", Description: "Brute-force credentials", InputSchema: map[string]any{"type": "object"}},
	}}
	st := newSearchPentestToolsTool(lister)

	out, err := st.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "amass_enum") || !strings.Contains(out, "hydra_bruteforce") {
		t.Fatalf("output = %q, want both tools listed", out)
	}
}

func TestSearchPentestTools_FiltersByQuery(t *testing.T) {
	lister := &fakeMCPLister{tools: []*mcp.Tool{
		{Name: "amass_enum", Description: "Enumerate subdomains", InputSchema: map[string]any{"type": "object"}},
		{Name: "hydra_bruteforce", Description: "Brute-force credentials", InputSchema: map[string]any{"type": "object"}},
	}}
	st := newSearchPentestToolsTool(lister)

	out, err := st.InvokableRun(context.Background(), `{"query":"brute"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if strings.Contains(out, "amass_enum") {
		t.Fatalf("output = %q, should not contain amass_enum for query 'brute'", out)
	}
	if !strings.Contains(out, "hydra_bruteforce") {
		t.Fatalf("output = %q, want hydra_bruteforce for query 'brute'", out)
	}
}

func TestSearchPentestTools_NoMatches(t *testing.T) {
	lister := &fakeMCPLister{tools: []*mcp.Tool{
		{Name: "amass_enum", Description: "Enumerate subdomains", InputSchema: map[string]any{"type": "object"}},
	}}
	st := newSearchPentestToolsTool(lister)

	out, err := st.InvokableRun(context.Background(), `{"query":"nonexistent"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "no sandbox tools matched") {
		t.Fatalf("output = %q, want a no-match message", out)
	}
}

func TestRunPentestTool_HappyPath(t *testing.T) {
	store, err := agent.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	task, err := store.CreateSubtask("sess-1", "enumerate subdomains", 5, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}

	mcpExec := &fakeMCPExecer{result: agent.ToolResult{Content: "example.com found 3 subdomains"}}
	rt := newRunPentestTool(pentestToolDeps{
		taskID: task.ID, sessionID: "sess-1", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := rt.InvokableRun(context.Background(), `{"tool":"amass_enum","args":{"domain":"example.com","passive":true},"rationale":"enumerate subdomains","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "3 subdomains") {
		t.Fatalf("output = %q, want the tool result content", out)
	}
	if mcpExec.calledTool != "amass_enum" {
		t.Fatalf("mcpExec called with tool = %q, want amass_enum", mcpExec.calledTool)
	}
	if mcpExec.calledArgs["domain"] != "example.com" {
		t.Fatalf("mcpExec args = %+v, missing domain", mcpExec.calledArgs)
	}
	if _, ok := mcpExec.calledArgs["rationale"]; ok {
		t.Fatalf("mcpExec args = %+v, rationale must not leak into the tool's own args", mcpExec.calledArgs)
	}
}

func TestRunPentestTool_MissingToolName_Rejected(t *testing.T) {
	store, _ := agent.NewStore(nil)
	task, _ := store.CreateSubtask("sess-1", "x", 5, "")
	mcpExec := &fakeMCPExecer{}
	rt := newRunPentestTool(pentestToolDeps{
		taskID: task.ID, sessionID: "sess-1", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := rt.InvokableRun(context.Background(), `{"tool":"","args":{},"rationale":"x","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "tool is required") {
		t.Fatalf("output = %q, want a tool-is-required rejection", out)
	}
	if mcpExec.calledTool != "" {
		t.Fatal("mcpExec should not have been called")
	}
}

func TestRunPentestTool_EmptyTarget_Rejected(t *testing.T) {
	store, _ := agent.NewStore(nil)
	task, _ := store.CreateSubtask("sess-1", "x", 5, "")
	mcpExec := &fakeMCPExecer{}
	rt := newRunPentestTool(pentestToolDeps{
		taskID: task.ID, sessionID: "sess-1", store: store, mcpExec: mcpExec,
		gate: agent.NewGate(), validator: nil,
		approvalMode: func() agent.ApprovalMode { return agent.ModeAuto },
	})

	out, err := rt.InvokableRun(context.Background(), `{"tool":"amass_enum","args":{"domain":""},"rationale":"x","risk_level":"low"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "needs a non-empty target") {
		t.Fatalf("output = %q, want an empty-target rejection", out)
	}
	if mcpExec.calledTool != "" {
		t.Fatal("mcpExec should not have been called")
	}
}
