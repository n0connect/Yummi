package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Action is the model's per-turn decision. The action kind is conveyed by the
// leading control tag (see protocol.go); these fields carry its JSON payload.
type Action struct {
	Action    string     `json:"action"`
	Command   string     `json:"command,omitempty"`
	StepID    string     `json:"step_id,omitempty"`
	Rationale string     `json:"rationale,omitempty"`
	RiskLevel string     `json:"risk_level,omitempty"`
	Steps     []PlanStep `json:"steps,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Summary   string     `json:"summary,omitempty"`
	Findings  []string   `json:"findings,omitempty"`
	Question  string     `json:"question,omitempty"`

	// Role/Subgoal carry a [HANDOFF]: delegate Subgoal to the sub-agent Role.
	Role    string `json:"role,omitempty"`
	Subgoal string `json:"subgoal,omitempty"`

	// MemKey/MemValue carry a [REMEMBER]: persist MemValue under MemKey.
	MemKey   string `json:"key,omitempty"`
	MemValue string `json:"value,omitempty"`

	// FindingsDetail is the optional rich form of findings. The brain MAY emit
	// these; when absent, freetext Findings are wrapped as info notes instead.
	FindingsDetail []FindingDetail `json:"findings_detail,omitempty"`
}

// FindingDetail is the optional rich form of a discovery. The brain MAY emit
// these; when absent, freetext Findings are wrapped as info notes instead.
type FindingDetail struct {
	Severity    string `json:"severity,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Target      string `json:"target,omitempty"`
}

// stripThinkTokens removes <think>...</think> blocks emitted by reasoning
// models (Qwen3, DeepSeek-R1, etc.) before JSON extraction.
// Handles both single-line and multi-line think blocks.
func stripThinkTokens(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end < 0 {
			// Unclosed tag — drop everything from <think> onward.
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len("</think>"):]
	}
	return s
}

// extractJSON strips think tokens, markdown fences, and surrounding prose,
// returning the outermost {...} block.
func extractJSON(raw string) string {
	trimmed := strings.TrimSpace(stripThinkTokens(raw))
	if idx := strings.Index(trimmed, "{"); idx > 0 {
		trimmed = trimmed[idx:]
	}
	if idx := strings.LastIndex(trimmed, "}"); idx >= 0 && idx < len(trimmed)-1 {
		trimmed = trimmed[:idx+1]
	}
	return trimmed
}

// ParsePlan parses the plan LLM response into pending steps.
func ParsePlan(raw string) ([]PlanStep, error) {
	var out struct {
		Steps []PlanStep `json:"steps"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &out); err != nil {
		return nil, fmt.Errorf("plan parse: %w", err)
	}
	if len(out.Steps) == 0 {
		return nil, errors.New("plan parse: no steps")
	}
	for i := range out.Steps {
		out.Steps[i].Status = StepPending
		if out.Steps[i].ID == "" {
			out.Steps[i].ID = fmt.Sprintf("s%d", i+1)
		}
	}
	return out.Steps, nil
}

// BuildPlanPrompt returns system and user prompts for the planning call.
// Network scope is no longer surfaced here — gateway is the sole authority and
// enforces scope transparently at execution time.
func BuildPlanPrompt(persona AgentPersona, skills, goal string) (system, user string) {
	var b strings.Builder
	b.WriteString(persona.Identity)
	if p := strings.TrimSpace(persona.Principles); p != "" {
		b.WriteString("\n\nREASONING PRINCIPLES:\n" + p)
	}
	if p := strings.TrimSpace(persona.Planning); p != "" {
		b.WriteString("\n\n" + p)
	}
	if sk := strings.TrimSpace(skills); sk != "" {
		b.WriteString("\n\n" + sk)
	}
	b.WriteString("\n\nOUTPUT: reply with ONLY this JSON, nothing else:\n" +
		`{"steps":[{"id":"s1","description":"<concrete action>"},{"id":"s2","description":"..."}]}`)
	return b.String(), "GOAL: " + goal
}

// EstimateTokens returns a rough token count for text (chars / 4).
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// observation compression sizes — generous enough for nmap/nikto output
const (
	compressHeadBytes = 1400
	compressTailBytes = 1400
)

// CompressObservation returns head + tail with a middle marker for large outputs.
// This keeps prompts bounded while preserving the semantically dense edges of
// tool output (headers and summaries appear at head; results at tail).
func CompressObservation(output string) string {
	if len(output) <= compressHeadBytes+compressTailBytes {
		return output
	}
	// Align head at a newline boundary (UTF-8 rune boundary first — output is
	// attacker-influenced tool output, so a raw byte cut can split a rune).
	head := output[:utf8SafeCut(output, compressHeadBytes)]
	if nl := strings.LastIndex(head, "\n"); nl > 0 {
		head = output[:nl+1]
	}
	// Align tail at a newline boundary.
	tail := output[utf8SafeCut(output, len(output)-compressTailBytes):]
	if nl := strings.Index(tail, "\n"); nl >= 0 && nl < len(tail)-1 {
		tail = tail[nl+1:]
	}
	lineCount := strings.Count(output, "\n") + 1
	return fmt.Sprintf("%s\n...[%d lines, %d bytes total]...\n\n%s",
		strings.TrimRight(head, "\n"), lineCount, len(output), tail)
}

// llmParseRetryHint returns an escalating format reminder for parse-error retries.
func llmParseRetryHint(attempt int) string {
	switch attempt {
	case 0:
		return "\n\nIMPORTANT: Respond with ONLY a JSON object, no prose or markdown."
	case 1:
		return "\n\nPrevious response was not valid JSON. Output ONLY one JSON object, starting with { and ending with }."
	default:
		return "\n\nJSON ONLY, no prose, no markdown: {\"action\":\"...\", ...}"
	}
}

// BuildStepPrompt renders the full task state + last observation into the
// per-turn prompt. summary is the compressed history from MemoryContext (may be "").
func BuildStepPrompt(task Task, lastObservation, summary, preamble string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GOAL: %s\n\n", task.Goal)

	// preamble carries the pre-rendered context blocks (SKILLS / DELEGATION /
	// MEMORY); each already includes its own header. Rendered verbatim.
	if strings.TrimSpace(preamble) != "" {
		b.WriteString(preamble)
		if !strings.HasSuffix(preamble, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if summary != "" {
		fmt.Fprintf(&b, "PRIOR CONTEXT (compressed):\n%s\n\n", summary)
	}

	// Show accumulated findings so far.
	if len(task.Findings) > 0 {
		b.WriteString("FINDINGS SO FAR:\n")
		for _, f := range task.Findings {
			fmt.Fprintf(&b, "  • %s\n", f)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "PLAN (%d/%d steps used, %d remaining):\n",
		task.StepsUsed, task.StepBudget, task.StepBudget-task.StepsUsed)
	for _, s := range task.Plan {
		icon := "○"
		switch s.Status {
		case StepDone:
			icon = "✓"
		case StepSkipped:
			icon = "–"
		}
		fmt.Fprintf(&b, "  %s [%s] %s", icon, s.ID, s.Description)
		if s.Result != "" {
			// Truncate stored results to keep prompt bounded.
			res := s.Result
			if len(res) > 120 {
				res = res[:120] + "…"
			}
			fmt.Fprintf(&b, "\n    └─ result: %s", res)
		}
		b.WriteString("\n")
	}

	if lastObservation != "" {
		b.WriteString("\nLAST TOOL OUTPUT:\n")
		b.WriteString(CompressObservation(lastObservation))
		if !strings.HasSuffix(lastObservation, "\n") {
			b.WriteString("\n")
		}
	}

	// Convergence nudge: when little budget remains, push the model to wrap up
	// instead of opening new lines of investigation or repeating commands.
	if remaining := task.StepBudget - task.StepsUsed; remaining <= 2 {
		fmt.Fprintf(&b, "\n⚠ Only %d step(s) left. Do NOT start new investigations or repeat commands. "+
			"Review what you already gathered above and FINISH now with a summary and findings.\n", remaining)
	}

	b.WriteString(`
─────────────────────────────────────────
Decide the single next move. Reply with ONE control tag on its own line (see
PROTOCOL in the system prompt), followed by its JSON body. Example:
[RUN_COMMAND]
{"command":"<exact shell command>","step_id":"<plan step id>","rationale":"<1 sentence>","risk_level":"low|medium|high|critical","findings":["<discovery — omit if none>"]}`)
	return b.String()
}

// StepSystemPrompt is the system prompt for per-turn step calls. The model-facing
// persona prose comes entirely from the persona document; the code appends only
// the protocol contract. Network scope is enforced transparently by gateway and
// is no longer injected into the prompt.
func StepSystemPrompt(persona AgentPersona) string {
	var b strings.Builder
	b.WriteString(persona.Identity)
	if p := strings.TrimSpace(persona.Principles); p != "" {
		b.WriteString("\n\nREASONING PRINCIPLES:\n" + p)
	}
	b.WriteString("\n\n" + ProtocolReference())
	return b.String()
}
