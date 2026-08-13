package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Direction is the sender of a protocol tag.
type Direction int

const (
	// AgentToLLM tags wrap observations the agent sends to the model.
	AgentToLLM Direction = iota
	// LLMToAgent tags lead the control messages the model sends back.
	LLMToAgent
)

// Tag is one entry in the closed agent↔LLM vocabulary.
// Developers add or remove protocol tags by editing Registry ONLY.
type Tag struct {
	Name    string
	Dir     Direction
	Summary string
	Payload string
}

// Registry is the ordered, closed set of every protocol tag.
// This is the single source of truth; Wrap and ProtocolReference derive from it.
var Registry = []Tag{
	// ── Agent → LLM (observation / envelope) ──
	{"TOOL_RESPONSE", AgentToLLM, "Command succeeded; output follows.", "raw or compressed output"},
	{"TOOL_RESPONSE_TRUNCATED", AgentToLLM, "Output too long; head+tail shown. Reply [SEND_FULL] for the complete output.", "compressed head+tail"},
	{"TOOL_RESPONSE_FULL", AgentToLLM, "Complete output you requested via [SEND_FULL].", "full output"},
	{"TOOL_FAILED", AgentToLLM, "Command ran but FAILED. Reason included. Do NOT repeat it — adapt.", "reason + output"},
	{"COMMAND_BLOCKED", AgentToLLM, "Command was malformed, or gateway rejected its target as out of network scope.", "reason"},
	{"DUPLICATE_BLOCKED", AgentToLLM, "You just ran this exact command. Choose a different one.", "command"},
	{"REPEATED_FAILURE", AgentToLLM, "Several commands failed in a row. STOP repeating — switch tool/route, check `--help`, replan, or finish/ask.", "failure count + reason + output"},
	{"STUCK", AgentToLLM, "Repeated pattern detected.", "recent commands"},
	{"ABNORMAL_PATTERN", AgentToLLM, "Unusual repetition; deciding next move with you before escalating to the operator.", "recent commands"},
	{"LONG_RUNNING", AgentToLLM, "Running command exceeded its soft time limit and is still running. Reply [CONTINUE] to keep waiting or [ABORT] to cancel.", "command + elapsed seconds"},
	{"OUTPUT_OVERFLOW", AgentToLLM, "Terminal output is abnormally large and is pressuring the context.", "size + compressed sample"},
	{"APPROVAL_DENIED", AgentToLLM, "Operator denied the gated command. Propose an alternative or finish.", "command + reason"},
	{"APPROVAL_TIMEOUT", AgentToLLM, "Approval timed out.", "command"},
	{"PLAN_UPDATED", AgentToLLM, "Your plan update was accepted.", "reason"},
	{"BUDGET_WARNING", AgentToLLM, "Few steps remain. Converge toward finishing.", "steps used/remaining"},
	{"UNKNOWN", AgentToLLM, "The agent could not classify this situation. Raw content follows. HIGHEST PRIORITY — you decide what to do.", "raw content"},
	{"OPERATOR_ANSWER", AgentToLLM, "The operator's reply to your [ASK_OPERATOR] question. Use it to decide your next action.", "operator's message"},

	// ── LLM → Agent (control / action) ──
	{"RUN_COMMAND", LLMToAgent, "Run a shell command.", `{"command","step_id","rationale","risk_level","findings"?,"findings_detail"?}`},
	{"UPDATE_PLAN", LLMToAgent, "Revise the plan.", `{"steps":[...],"reason"}`},
	{"FINISH", LLMToAgent, "Goal achieved. Before writing findings, re-scan every TOOL_RESPONSE above for concrete, specific facts (exact ports/versions/paths/cookies) rather than a generic summary — a specific fact like \"port 3306 open, MySQL 5.5.47\" is a real finding; \"open ports were found\" is not.", `{"summary","findings"?,"findings_detail"?}`},
	{"HANDOFF", LLMToAgent, "Delegate a focused subtask to a named specialist sub-agent. To delegate to several specialists AT ONCE (they run concurrently), use \"handoffs\" instead.", `{"role","subgoal"} or {"handoffs":[{"role","subgoal"},...]}`},
	{"REMEMBER", LLMToAgent, "Persist a structured memory (e.g. a code/connection map) under a key — never forgotten.", `{"key","value"}`},
	{"ASK_OPERATOR", LLMToAgent, "Blocked; need a human.", `{"question"}`},
	{"SEND_FULL", LLMToAgent, "Resend the last truncated output in full.", `{}`},
	{"CONTINUE", LLMToAgent, "Keep waiting / proceed (reply to a consult).", `{}`},
	{"ABORT", LLMToAgent, "Cancel the running command but keep the task.", `{}`},
	{"STOP_TASK", LLMToAgent, "Stop the whole task.", `{"reason"?}`},
}

// registryIndex is built once for O(1) name validation.
var registryIndex = func() map[string]Tag {
	m := make(map[string]Tag, len(Registry))
	for _, t := range Registry {
		m[t.Name] = t
	}
	return m
}()

func lookupTag(name string) (Tag, bool) {
	t, ok := registryIndex[name]
	return t, ok
}

// Wrap renders an agent→LLM message as "[TAG]\n<content>".
// An unregistered tag degrades to [UNKNOWN] so the agent never emits a
// label the model has not been told about (the protocol stays closed).
func Wrap(tag, content string) string {
	if _, ok := registryIndex[tag]; !ok {
		tag = "UNKNOWN"
	}
	return "[" + tag + "]\n" + content
}

// ProtocolReference renders the registry into the markdown block injected
// once into the step system prompt. Editing Registry updates the prompt.
func ProtocolReference() string {
	var b strings.Builder
	b.WriteString("PROTOCOL — every message between you and the agent carries one tag.\n\n")
	b.WriteString("Messages the AGENT sends YOU (how to read its observations):\n")
	for _, t := range Registry {
		if t.Dir == AgentToLLM {
			fmt.Fprintf(&b, "  [%s] — %s\n", t.Name, t.Summary)
		}
	}
	b.WriteString("\nYOUR reply MUST begin with one control tag on its own line, then a JSON body:\n")
	for _, t := range Registry {
		if t.Dir == LLMToAgent {
			fmt.Fprintf(&b, "  [%s] %s — %s\n", t.Name, t.Payload, t.Summary)
		}
	}
	b.WriteString("\nExample reply:\n[RUN_COMMAND]\n{\"command\":\"nmap -sV 127.0.0.1\",\"step_id\":\"s1\",\"rationale\":\"fingerprint\",\"risk_level\":\"medium\"}\n")
	b.WriteString("\nFINDING VERIFICATION — \"findings\" is an unverified claim; \"findings_detail\" lets a finding earn a VERIFIED badge instead:\n" +
		`  [{"title":"...","evidence":"<exact substring copied from the tool output above>","step_id":"<id of the step whose output proves it>"}]` + "\n" +
		"This is checked against the real recorded output of that step, not taken on your word — quote evidence verbatim, do not paraphrase it, " +
		"or a true finding will fail the check and show as unverified.\n")
	return b.String()
}

// ControlMsg is a parsed LLM→agent reply: a control tag plus decoded payload.
type ControlMsg struct {
	Tag    string
	Action Action
}

// leadingTagPattern matches the first "[TAG_NAME]" token in a reply.
var leadingTagPattern = regexp.MustCompile(`\[([A-Z_]+)\]`)

// ParseControl extracts the control tag and JSON payload from an LLM reply.
// It reuses stripThinkTokens/extractJSON so reasoning-model noise (<think>,
// markdown fences, prose) is tolerated.
//
// The preferred form is a leading [TAG] from the LLM→agent vocabulary. Weak
// models frequently omit the tag and emit only the JSON payload, so when no
// valid tag is present the tag is INFERRED from the payload shape (command →
// RUN_COMMAND, etc.) — the agent must stay robust regardless of how poorly the
// model follows the format. An unrecognized control is an error (handled by the
// caller's retry path) — distinct from [UNKNOWN], which is agent→LLM only.
func ParseControl(raw string) (ControlMsg, error) {
	clean := stripThinkTokens(raw)

	// Preferred form: a leading [TAG] from the control vocabulary.
	if m := leadingTagPattern.FindStringSubmatch(clean); m != nil {
		if def, ok := registryIndex[m[1]]; ok && def.Dir == LLMToAgent {
			return decodeControl(m[1], clean[strings.Index(clean, "]")+1:])
		}
	}

	// Fallback: tagless JSON payload — infer the control from its shape.
	if strings.Contains(clean, "{") {
		var a Action
		if err := json.Unmarshal([]byte(extractJSON(clean)), &a); err == nil {
			if tag := inferControlTag(a); tag != "" {
				return ControlMsg{Tag: tag, Action: a}, nil
			}
		}
	}

	// Fallback: a bare bodiless control word emitted without brackets.
	if tag := bareControlKeyword(clean); tag != "" {
		return ControlMsg{Tag: tag}, nil
	}

	return ControlMsg{}, errors.New("control parse: no recognizable control tag or payload")
}

// decodeControl builds a ControlMsg for a known tag, decoding the JSON body when
// present (bodiless tags like CONTINUE carry no payload).
func decodeControl(tag, body string) (ControlMsg, error) {
	msg := ControlMsg{Tag: tag}
	if strings.Contains(body, "{") {
		if err := json.Unmarshal([]byte(extractJSON(body)), &msg.Action); err != nil {
			return ControlMsg{}, fmt.Errorf("control parse: bad JSON body for %s: %w", tag, err)
		}
	}
	return msg, nil
}

// inferControlTag maps a tagless JSON payload to its control tag by shape.
func inferControlTag(a Action) string {
	switch {
	case strings.TrimSpace(a.Command) != "":
		return "RUN_COMMAND"
	case (strings.TrimSpace(a.Role) != "" && strings.TrimSpace(a.Subgoal) != "") || len(a.Handoffs) > 0:
		return "HANDOFF"
	case strings.TrimSpace(a.MemKey) != "" && strings.TrimSpace(a.MemValue) != "":
		return "REMEMBER"
	case len(a.Steps) > 0:
		return "UPDATE_PLAN"
	case strings.TrimSpace(a.Question) != "":
		return "ASK_OPERATOR"
	case strings.TrimSpace(a.Summary) != "" || len(a.Findings) > 0:
		return "FINISH"
	}
	return ""
}

// bareControlKeyword detects a bodiless control word emitted without brackets.
func bareControlKeyword(clean string) string {
	upper := strings.ToUpper(strings.TrimSpace(clean))
	for _, name := range []string{"STOP_TASK", "SEND_FULL", "CONTINUE", "ABORT"} {
		if upper == name {
			return name
		}
	}
	return ""
}
