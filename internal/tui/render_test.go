package tui

import (
	"strings"
	"testing"
)

func TestRenderActivityChips(t *testing.T) {
	if got := (ActivityItem{Kind: "skill_applied", Summary: "recon"}).Render(80); !strings.Contains(got, "skill") || !strings.Contains(got, "recon") {
		t.Fatalf("skill chip wrong: %q", got)
	}
	if got := (ActivityItem{Kind: "memory_recalled", Summary: "nmap -sV <TARGET>"}).Render(80); !strings.Contains(got, "recalled") {
		t.Fatalf("memory chip wrong: %q", got)
	}
	// command/command_result are NOT chips (rendered via exec line).
	if got := (ActivityItem{Kind: "command", Summary: "whoami"}).Render(80); got != "" {
		t.Fatalf("command should not be a chip, got %q", got)
	}
	// nested events are indented.
	if got := (ActivityItem{Kind: "finding", Summary: "redis", Nested: true}).Render(80); !strings.Contains(got, "└") {
		t.Fatalf("nested finding should indent: %q", got)
	}
}

func TestRenderExecMark(t *testing.T) {
	ok := ExecItem{Command: "whoami", Status: "completed", Output: "root"}.Render(80)
	if !strings.Contains(ok, "✓") || !strings.Contains(ok, "whoami") || !strings.Contains(ok, "root") {
		t.Fatalf("ok exec wrong: %q", ok)
	}
	bad := ExecItem{Command: "badcmd", Status: "failed", Output: "not found"}.Render(80)
	if !strings.Contains(bad, "✗") {
		t.Fatalf("failed exec should mark ✗: %q", bad)
	}
}

// Regression: the orchestrator's threat-scan banner (execution.SecurityBanner)
// warns the operator that tool output looked like a content-layer injection
// attempt. AssistantPanel.tsx surfaces it as a visible "⚠ security_banner"
// alert box; the TUI decoded the same field (execMsg.Banner) but only ever
// used it to guard text fed back to the LLM — the operator's own screen never
// showed it. A human-visible warning is itself a security control, so
// ExecItem must render the banner too.
func TestRenderExecItemShowsSecurityBanner(t *testing.T) {
	out := ExecItem{Command: "curl target.example", Status: "completed", Output: "ok", Banner: "[THREAT] possible prompt injection in output"}.Render(80)
	if !strings.Contains(out, "possible prompt injection in output") {
		t.Fatalf("expected security banner to be visible in rendered exec item, got %q", out)
	}
}

func TestDecodeFrameTypes(t *testing.T) {
	if msg := decodeFrame([]byte(`{"type":"execution.status","data":{"command":"whoami","status":"completed","output":"root"}}`)); func() bool {
		e, ok := msg.(execMsg)
		return ok && e.Command == "whoami" && e.Status == "completed"
	}() == false {
		t.Fatalf("execution.status not decoded: %#v", msg)
	}

	if msg := decodeFrame([]byte(`{"type":"agent.activity","data":{"kind":"skill_applied","summary":"recon","parent_task_id":""}}`)); func() bool {
		a, ok := msg.(activityMsg)
		return ok && a.Kind == "skill_applied" && a.Summary == "recon"
	}() == false {
		t.Fatalf("agent.activity not decoded: %#v", msg)
	}

	if msg := decodeFrame([]byte(`{"type":"task.status","data":{"id":"t1","status":"awaiting_approval","pending_command":"nmap","pending_proposal_id":"p1"}}`)); func() bool {
		tk, ok := msg.(taskMsg)
		return ok && tk.ID == "t1" && tk.PendingCommand == "nmap" && tk.PendingPropID == "p1"
	}() == false {
		t.Fatalf("task.status not decoded: %#v", msg)
	}

	if msg := decodeFrame([]byte(`{"type":"gateway.status","data":[]}`)); msg != nil {
		t.Fatalf("irrelevant frame should decode to nil, got %#v", msg)
	}
}

func TestToWS(t *testing.T) {
	if toWS("http://127.0.0.1:9080") != "ws://127.0.0.1:9080" {
		t.Fatal("http→ws conversion wrong")
	}
	if toWS("https://x:9080") != "wss://x:9080" {
		t.Fatal("https→wss conversion wrong")
	}
}
