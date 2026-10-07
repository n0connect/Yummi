package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

	// Role/Subgoal carry a single-target [HANDOFF]: delegate Subgoal to the
	// sub-agent Role. Handoffs carries the fan-out form — multiple targets
	// dispatched CONCURRENTLY in one [HANDOFF] (see handoffTargets in
	// node_handoff.go, which normalizes either form into one list). Both forms
	// coexist so a model that only ever emits the single-target shape keeps
	// working unchanged.
	Role     string        `json:"role,omitempty"`
	Subgoal  string        `json:"subgoal,omitempty"`
	Handoffs []HandoffSpec `json:"handoffs,omitempty"`

	// MemKey/MemValue carry a [REMEMBER]: persist MemValue under MemKey.
	MemKey   string `json:"key,omitempty"`
	MemValue string `json:"value,omitempty"`

	// FindingsDetail is the optional rich form of findings. The brain MAY emit
	// these; when absent, freetext Findings are wrapped as info notes instead.
	FindingsDetail []FindingDetail `json:"findings_detail,omitempty"`

	// Tool/Args carry an [MCP_TOOL]: dispatch the named sandbox-mcp tool
	// (cataloged pentest tool or terminal-session verb) with these arguments,
	// through the exact same execution/scope/threatscan pipeline RUN_COMMAND
	// uses — see node_tool.go's toolNode. Mutually exclusive with Command.
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

// HandoffSpec is one delegation target inside a fan-out [HANDOFF]'s
// "handoffs" list — the same (role, subgoal) pair the single-target form
// carries directly on Action.
type HandoffSpec struct {
	Role    string `json:"role"`
	Subgoal string `json:"subgoal"`
}

// FindingDetail is the optional rich form of a discovery. The brain MAY emit
// these; when absent, freetext Findings are wrapped as info notes instead.
type FindingDetail struct {
	Severity    string `json:"severity,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Target      string `json:"target,omitempty"`
	// StepID cites the plan step whose actual tool output supports this
	// finding — required for the finding to earn a Verified verdict (see
	// verifyFindingDetail in verdict.go). May be omitted on a RUN_COMMAND's
	// own findings (defaults to that command's own step); MUST be explicit
	// on a FINISH's findings, since FINISH isn't tied to one step.
	StepID string `json:"step_id,omitempty"`
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

// internalTagLinePattern matches a standalone protocol-tag line — a weak
// model echoing its own LLM→agent wire format (e.g. a literal "[FINISH]"
// line) inside free text it authored. Mirrors
// conductor.sanitizeVisibleModelText's identical pattern; kept as its own
// copy here rather than a cross-package import, since agent and conductor
// are deliberately independent (see CLAUDE.md's conductor/executor
// separation note) and each protects its own wire-visible text.
var internalTagLinePattern = regexp.MustCompile(`(?m)^\s*\[(RUN_COMMAND|UPDATE_PLAN|FINISH|HANDOFF|REMEMBER|ASK_OPERATOR|SEND_FULL|CONTINUE|ABORT|STOP_TASK)\]\s*$`)

// specialTokenPattern strips OpenAI-style "<|...|>" special/control tokens
// (e.g. gpt-oss's Harmony format: <|channel|>, <|message|>, <|constrain|>).
// Live-observed via the Eino backend against gpt-oss-20b served through
// LM Studio: when a turn's "finish" call isn't cleanly routed into a
// tool_calls entry, Eino falls back to the raw assistant message content —
// and that content can be the model's own unparsed Harmony-channel text,
// putting literal control tokens straight into a wire-visible Task.Summary.
// This is a generic net (any vendor using this delimiter convention), not a
// gpt-oss special case; it only removes the token markers, it does not (and
// cannot, without vendor-specific channel semantics) reconstruct which part
// of the surrounding text was the intended "final" answer.
var specialTokenPattern = regexp.MustCompile(`<\|[a-zA-Z0-9_]+\|>`)

// SanitizeAgentText is the chokepoint for model-authored free text that
// becomes wire-visible on Task (Summary/Findings/Question, sent via
// GET /api/v1/tasks/{id} and every task.status websocket event). This must be
// the only line of defense, applied once here rather than trusted to be
// re-implemented client-side. Strips <think> reasoning-model noise, any
// standalone protocol-tag line the model echoed from its own wire format,
// and any raw "<|...|>" special/control token that leaked through unparsed.
func SanitizeAgentText(s string) string {
	s = stripThinkTokens(s)
	s = internalTagLinePattern.ReplaceAllString(s, "")
	s = specialTokenPattern.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// SanitizeFindings applies SanitizeAgentText to every finding string, and
// drops the now-empty ones the same way filterEmpty does — mirrors the
// existing empty-string reject used elsewhere in this package (graph.go).
func SanitizeFindings(findings []string) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		if clean := SanitizeAgentText(f); clean != "" {
			out = append(out, clean)
		}
	}
	return out
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

var enumMarkerRe = regexp.MustCompile(`(?:^|[\s(])(\d{1,2})[).]`)

// leadingNumberMarkerRe strips a leading "1. " / "1) " the model added to a
// single audited line despite AuditGoalParts explicitly asking for none.
var leadingNumberMarkerRe = regexp.MustCompile(`^\d{1,2}[).]\s*`)

// CountEnumeratedParts returns how many distinct parts an operator's goal
// EXPLICITLY enumerates via "1) ... 2) ... 3) ..." or "1. ... 2. ..." markers,
// but only when they form a consecutive run starting at 1 (so "python 3.14"
// or "top 3 files" never register). Returns 0 for any goal that is a single
// free-form instruction — in that case the model's own decomposition is the
// only signal and is left untouched.
//
// This exists because the planner LLM was observed collapsing a clearly
// three-part goal ("1) ... 2) ... 3) ...") into a SINGLE plan step — parts 2
// and 3 evaporated at planning time, before execution, and the per-step
// ledger downstream could only ever track the one step it was given. A
// deterministic floor on the enumerated-parts case catches that class of
// under-decomposition without touching genuinely single-intent goals.
func CountEnumeratedParts(goal string) int {
	seen := map[int]bool{}
	for _, m := range enumMarkerRe.FindAllStringSubmatch(goal, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			seen[n] = true
		}
	}
	count := 0
	for i := 1; seen[i]; i++ {
		count = i
	}
	if count >= 2 {
		return count
	}
	return 0
}

// SplitEnumeratedParts returns the operator's own text for each explicitly
// enumerated part of the goal ("1) ... 2) ... 3) ..."), or nil when the goal
// isn't a consecutive enumeration of >=2 parts (same gate as
// CountEnumeratedParts). This is the deterministic decomposition used when the
// planner LLM under-decomposes: re-asking the model was proven insufficient
// (it stubbornly re-collapsed a 3-part goal to 1 step even when explicitly
// told to produce one step per part), so the split is done in code from the
// operator's own markers — no model judgment involved.
func SplitEnumeratedParts(goal string) []string {
	want := CountEnumeratedParts(goal)
	if want < 2 {
		return nil
	}
	contentStart := map[int]int{} // index just AFTER the "N)" / "N." marker
	markerStart := map[int]int{}  // index of the "N" digit itself
	for _, loc := range enumMarkerRe.FindAllStringSubmatchIndex(goal, -1) {
		n, err := strconv.Atoi(goal[loc[2]:loc[3]])
		if err != nil {
			continue
		}
		if _, seen := contentStart[n]; !seen {
			contentStart[n] = loc[1]
			markerStart[n] = loc[2]
		}
	}
	var parts []string
	for i := 1; i <= want; i++ {
		start := contentStart[i]
		end := len(goal)
		if ms, ok := markerStart[i+1]; ok {
			end = ms
		}
		if part := strings.Trim(strings.TrimSpace(goal[start:end]), " ,;.\t\n"); part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) < 2 {
		return nil
	}
	return parts
}

// LikelyMultiPart is a cheap, deterministic pre-filter for whether a goal is
// worth spending an AuditGoalParts LLM call on at all. Without it, the audit
// would fire on EVERY non-numbered goal — the overwhelming majority in real
// usage — costing an extra model round-trip on plainly single-intent goals
// ("scan 10.0.0.1 for open ports") for zero benefit, and, worse, risking a
// spurious split if the model's audit reply doesn't perfectly follow the
// "one line for a single request" instruction. This only looks for the
// coordinating language that actually signals multiple distinct asks
// (English/Turkish conjunctions, sequencing words, list punctuation) — a
// false positive just costs one harmless audit call that AuditGoalParts'
// own single-line/no-signal cases absorb; a false negative (a genuinely
// multi-part goal with none of these cues) is rare and, same as
// CountEnumeratedParts, is accepted as out of scope rather than reached for
// with a fragile full-NLU heuristic.
func LikelyMultiPart(goal string) bool {
	lower := strings.ToLower(goal)
	for _, cue := range []string{" and ", " ve ", " ayrıca ", " also ", " then ", " sonra ", ", ", "; "} {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

// AuditGoalParts asks the model, in a narrow single-purpose call kept
// entirely separate from planning, to enumerate the distinct
// separately-actionable requests in a goal. CountEnumeratedParts/
// SplitEnumeratedParts only catch goals with explicit "1) ... 2) ..."
// markers — real operator phrasing is far more often natural-language
// coordination with no numbers at all ("scan X and also tell me Y", "X'i
// tara ve Y'yi kontrol et"), which the marker regex cannot see and which the
// SAME planner LLM call that already proved unreliable at this (see
// SplitEnumeratedParts's doc comment: it re-collapsed a 3-part goal to 1
// step even when explicitly told to produce one step per part) has no
// independent check against. Splitting "what does this goal ask for" from
// "how do we accomplish it" into two separate, narrower calls is the fix:
// judging whether a plan covers a goal is a substantially easier task than
// producing the plan, so a model unreliable at the latter can still be
// reliable at the former. Best-effort: any LLM error, empty result, or
// pathological output (more than maxAuditedParts lines — almost certainly a
// parsing failure or the model rambling, not a real goal with that many
// distinct asks) returns nil so callers can safely fall back to treating the
// goal as single-part.
func AuditGoalParts(ctx context.Context, llm LLM, goal string) []string {
	if llm == nil || strings.TrimSpace(goal) == "" {
		return nil
	}
	system := "You audit operator goals for a penetration-testing agent. You do not plan or execute anything."
	user := "GOAL: " + goal + "\n\n" +
		"List each distinct, separately-actionable request in this goal, one per line, in terse language " +
		"close to the goal's own wording. A single recon action against one or more targets is ONE request " +
		"even if it covers multiple hosts — only split when the goal asks for genuinely different kinds of " +
		"work. If the goal is a single request, output exactly one line and nothing else. No numbering, no " +
		"bullets, no commentary."
	raw, err := llm.Chat(ctx, system, user)
	if err != nil {
		return nil
	}
	const maxAuditedParts = 8
	var parts []string
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*• \t")
		line = leadingNumberMarkerRe.ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
		if len(parts) > maxAuditedParts {
			return nil
		}
	}
	if len(parts) < 2 {
		return nil
	}
	return parts
}

// ScaledStepBudget returns the step-budget ceiling a goal actually needs,
// given how many distinct parts it was detected to have (SplitEnumeratedParts
// or AuditGoalParts — 0 or 1 means single-intent). Never returns less than
// configured. Shared by both agent backends (graph's stepNode budget check,
// eino's MaxIterations) so a scaling change only needs to happen once.
//
// A multi-part goal ("1) ... 2) ... 3) ..." or "scan X and also check Y")
// reproducibly needs MUCH more real headroom than a flat default provides,
// proven live against a real target with real cataloged pentest tools: three
// separate runs either exhausted the entire step budget or came within one
// step of it — including a run with only TWO requested parts (gobuster +
// nikto) that never even reached the second part, because ONE tool hitting
// real-world friction (a missing wordlist, in that case) was enough to burn
// the whole budget on its own. A flat higher default doesn't fit either: a
// genuinely single-intent goal ("what OS is this") finishes correctly in a
// handful of real steps today and gains nothing from a bigger ceiling except
// a weaker runaway-loop backstop. Scaling with the goal's OWN detected
// complexity is the proportionate fix: budget grows only when there's real
// reason to expect it's needed.
func ScaledStepBudget(configured, numParts int) int {
	if numParts < 2 {
		return configured
	}
	// baseOverhead covers the model's own opening orientation and its final
	// finish/report turn, independent of how many parts there are. perPart is
	// deliberately generous — a single friction-prone tool alone has been
	// observed to consume close to a full flat-15 budget across multiple
	// runs, so "a few steps per part" would just reproduce the exact
	// shortfall this exists to fix. Capped at maxScaledBudget as a sanity
	// backstop, not an expected ceiling — the budget must still mean
	// something as a runaway-loop guard.
	const baseOverhead, perPart, maxScaledBudget = 6, 12, 80
	scaled := baseOverhead + perPart*numParts
	if scaled > maxScaledBudget {
		scaled = maxScaledBudget
	}
	if scaled > configured {
		return scaled
	}
	return configured
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

	// Budget-regime guidance: the "(%d/%d steps used, %d remaining)" header
	// above always shows the raw numbers, but a bare count doesn't tell the
	// model what to DO about it — research on budget-aware tool use (Budget
	// Tracker, arXiv 2511.17006) found that an explicit low-budget signal
	// measurably changes agent behavior at the SAME total budget, because a
	// model unaware it's running low can't adapt its strategy and keeps
	// exploring instead of converging. Two thresholds, both ratio-based so
	// they scale correctly whether StepBudget is the flat default or
	// ScaledStepBudget's larger multi-part ceiling: CRITICAL fires with at
	// most one meaningful step of slack left; LOW fires under a quarter of
	// the budget remaining. Silent otherwise — a healthy budget needs no
	// extra noise every turn.
	if remaining := task.StepBudget - task.StepsUsed; task.StepBudget > 0 && remaining > 0 {
		switch {
		case remaining <= max(1, task.StepBudget/10):
			b.WriteString("\nBUDGET CRITICAL: essentially no steps left. Finish now with whatever evidence you have, or explicitly mark unreachable plan parts — do not start new exploration.\n")
		case remaining <= max(2, task.StepBudget/4):
			b.WriteString("\nBUDGET LOW: few steps remain. Converge toward finishing — prefer the most direct remaining action over further exploration.\n")
		}
	}

	// Explicit remaining-goals pressure: the ○/✓ plan view above is easy for a
	// model to under-weight after answering the first part of a multi-part
	// goal, so restate what is still open and that FINISH is not yet valid.
	// stepNode enforces this deterministically too (premature-FINISH guard);
	// this block just makes the guard fire less often.
	if !planTerminal(task.Plan) {
		if pending := pendingStepDescriptions(task.Plan); len(pending) > 0 {
			fmt.Fprintf(&b, "\nGOAL PARTS STILL OPEN — do NOT FINISH until each is done (✓) or explicitly skipped:\n%s\n",
				strings.Join(pending, "\n"))
		}
	}

	if lastObservation != "" {
		b.WriteString("\nLAST TOOL OUTPUT:\n")
		b.WriteString(CompressObservation(lastObservation))
		if !strings.HasSuffix(lastObservation, "\n") {
			b.WriteString("\n")
		}
	}
	if planTerminal(task.Plan) && task.StepsUsed > 0 {
		b.WriteString("\nALL PLAN STEPS ARE TERMINAL. Reply with FINISH now. The summary must directly " +
			"answer the GOAL using exact values and units from the tool output. Do not make claims about " +
			"vulnerabilities, processes, users, or network activity unless a shown command actually observed them.\n")
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
PROTOCOL in the system prompt), followed by its JSON body. Every "findings"
entry (here or in FINISH) MUST be traceable to a command you actually ran —
one shown above with a ✓ or in LAST TOOL OUTPUT. Never attribute a finding to
a tool or technique you did not execute, even if it was in your original
plan. Example:
[RUN_COMMAND]
{"command":"<exact shell command>","step_id":"<plan step id>","rationale":"<1 sentence>","risk_level":"low|medium|high|critical","findings":["<discovery — omit if none>"]}`)
	return b.String()
}

// StepSystemPrompt is the system prompt for per-turn step calls. The model-facing
// persona prose comes entirely from the persona document; the code appends only
// the protocol contract. Network scope is enforced transparently by gateway and
// is no longer injected into the prompt.
//
// mcpCatalog, when non-empty, is one compact line of tool names (see
// main.go's buildMCPCatalogLine) appended after the protocol reference — NOT
// per-tool JSON schemas. Every step re-renders this whole prompt from
// scratch (graph never relies on conversation history), so anything added
// here is paid for on EVERY turn, not once: a verbose per-tool breakdown
// would noticeably eat a local model's context window over a long task.
// The model already knows common tool flags from pretraining (same
// rationale as the persona's "no catalogue needed" stance on shell tools);
// MCP_TOOL's own registry Payload tells it args is a best-guess JSON object
// that a validation error will correct if wrong, so no schema dump is
// needed here either.
//
// localModel gates ProtocolReference's extra MCP_TOOL worked example — see
// that function's doc comment. false reproduces this function's exact
// pre-existing output (cloud models, and local before this existed).
func StepSystemPrompt(persona AgentPersona, mcpCatalog string, localModel bool) string {
	var b strings.Builder
	b.WriteString(persona.Identity)
	if p := strings.TrimSpace(persona.Principles); p != "" {
		b.WriteString("\n\nREASONING PRINCIPLES:\n" + p)
	}
	b.WriteString("\n\n" + ProtocolReference(localModel))
	if mcpCatalog != "" {
		b.WriteString("\n\nMCP TOOLS — dispatch any of these via [MCP_TOOL] (name, then args as a best-guess flat JSON object):\n" + mcpCatalog)
	}
	return b.String()
}
