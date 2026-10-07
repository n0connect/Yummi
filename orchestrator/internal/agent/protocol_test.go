package agent

import (
	"strings"
	"testing"
)

func TestRegistryNoDuplicateNames(t *testing.T) {
	seen := map[string]bool{}
	for _, tag := range Registry {
		if tag.Name == "" {
			t.Fatal("registry has an empty tag name")
		}
		if seen[tag.Name] {
			t.Fatalf("duplicate tag name in registry: %q", tag.Name)
		}
		seen[tag.Name] = true
	}
}

func TestRegistryHasUnknownFallback(t *testing.T) {
	if _, ok := lookupTag("UNKNOWN"); !ok {
		t.Fatal("registry must contain the UNKNOWN fallback tag")
	}
}

func TestWrapRoundTrip(t *testing.T) {
	out := Wrap("TOOL_RESPONSE", "open port 80")
	if !strings.HasPrefix(out, "[TOOL_RESPONSE]\n") {
		t.Fatalf("missing tag prefix: %q", out)
	}
	if !strings.Contains(out, "open port 80") {
		t.Fatalf("content lost: %q", out)
	}
}

func TestWrapUnknownTagDegradesToUnknown(t *testing.T) {
	out := Wrap("NOT_A_REAL_TAG", "payload")
	if !strings.HasPrefix(out, "[UNKNOWN]\n") {
		t.Fatalf("unregistered tag must degrade to [UNKNOWN], got %q", out)
	}
}

func TestWrapRegisteredOperationalTags(t *testing.T) {
	for _, tag := range []string{"TASK_RECOVERED", "AWAITING_EGRESS_APPROVAL", "APPROVAL_DENIED", "INCOMPLETE_GOALS"} {
		out := Wrap(tag, "content")
		if !strings.HasPrefix(out, "["+tag+"]\n") {
			t.Fatalf("tag %q should be registered and not degrade to UNKNOWN, got %q", tag, out)
		}
	}
}

func TestProtocolReferenceListsBothDirections(t *testing.T) {
	ref := ProtocolReference(false)
	for _, name := range []string{"TOOL_RESPONSE", "TOOL_FAILED", "UNKNOWN", "RUN_COMMAND", "STOP_TASK", "SEND_FULL"} {
		if !strings.Contains(ref, name) {
			t.Errorf("protocol reference missing tag %q", name)
		}
	}
}

// TestProtocolReference_LocalExamples_OnlyWhenRequested is the regression
// test for a live-observed gap: a local 20B model told to use MCP_TOOL
// instead invented a nonexistent CLI command, because RUN_COMMAND had a
// copyable worked example here and nothing else did. localModel=true adds
// one for every non-trivial tag (HANDOFF/UPDATE_PLAN/REMEMBER/ASK_OPERATOR
// have the identical gap) — but only on request, so a cloud model's prompt
// (and local's, before this existed) stays byte-for-byte unchanged.
func TestProtocolReference_LocalExamples_OnlyWhenRequested(t *testing.T) {
	without := ProtocolReference(false)
	for _, marker := range []string{"term_open", "[HANDOFF]\n{", "[UPDATE_PLAN]\n{", "[REMEMBER]\n{", "[ASK_OPERATOR]\n{"} {
		if strings.Contains(without, marker) {
			t.Errorf("localModel=false must not add worked examples, found %q in:\n%s", marker, without)
		}
	}
	with := ProtocolReference(true)
	for _, marker := range []string{"[MCP_TOOL]\n{\"tool\":\"term_open\"", "[HANDOFF]\n{\"role\"", "[UPDATE_PLAN]\n{\"steps\"", "[REMEMBER]\n{\"key\"", "[ASK_OPERATOR]\n{\"question\""} {
		if !strings.Contains(with, marker) {
			t.Errorf("localModel=true must add a worked example containing %q, got:\n%s", marker, with)
		}
	}
}

func TestParseControl_RunCommand(t *testing.T) {
	raw := "[RUN_COMMAND]\n{\"command\":\"nmap -sV 127.0.0.1\",\"risk_level\":\"medium\",\"step_id\":\"s1\"}"
	msg, err := ParseControl(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Tag != "RUN_COMMAND" {
		t.Fatalf("tag=%q", msg.Tag)
	}
	if msg.Action.Command != "nmap -sV 127.0.0.1" || msg.Action.RiskLevel != "medium" {
		t.Fatalf("payload not decoded: %+v", msg.Action)
	}
}

func TestParseControl_WithThinkAndFence(t *testing.T) {
	raw := "<think>let me scan</think>\n```\n[FINISH]\n{\"summary\":\"done\"}\n```"
	msg, err := ParseControl(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Tag != "FINISH" || msg.Action.Summary != "done" {
		t.Fatalf("got tag=%q action=%+v", msg.Tag, msg.Action)
	}
}

func TestParseControl_BodilessControl(t *testing.T) {
	for _, raw := range []string{"[CONTINUE]", "[ABORT]\n", "[SEND_FULL]\n{}"} {
		msg, err := ParseControl(raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if msg.Tag == "" {
			t.Fatalf("%q: empty tag", raw)
		}
	}
}

// Weak models (qwen3:8b) often omit the leading tag and emit only the JSON
// payload — ParseControl must infer the control from the payload shape.
func TestParseControl_TaglessPayloadInferred(t *testing.T) {
	cases := []struct {
		raw     string
		wantTag string
	}{
		{`{"command":"whoami","step_id":"s1","risk_level":"low","findings":[]}`, "RUN_COMMAND"},
		{`{"summary":"done","findings":["x"]}`, "FINISH"},
		{`{"question":"which target?"}`, "ASK_OPERATOR"},
		{`{"steps":[{"id":"s1","description":"x"}],"reason":"pivot"}`, "UPDATE_PLAN"},
	}
	for _, c := range cases {
		msg, err := ParseControl(c.raw)
		if err != nil {
			t.Fatalf("%q: %v", c.raw, err)
		}
		if msg.Tag != c.wantTag {
			t.Errorf("%q: tag=%q want %q", c.raw, msg.Tag, c.wantTag)
		}
	}
}

func TestParseControl_BareKeyword(t *testing.T) {
	msg, err := ParseControl("ABORT")
	if err != nil || msg.Tag != "ABORT" {
		t.Fatalf("bare keyword: tag=%q err=%v", msg.Tag, err)
	}
}

func TestParseControl_Unrecognized_IsError(t *testing.T) {
	if _, err := ParseControl("just some prose with no tag"); err == nil {
		t.Fatal("expected error for tagless reply")
	}
	if _, err := ParseControl("[NOT_A_TAG] with no json payload"); err == nil {
		t.Fatal("expected error for unregistered control tag")
	}
}
