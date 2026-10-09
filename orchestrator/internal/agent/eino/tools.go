package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// ── run_command ──────────────────────────────────────────────────────────

// runCommandDeps is everything the run_command tool needs from its owning
// EinoRunner — passed in explicitly (no package-level state) so each task's
// tool instance is independently testable.
type runCommandDeps struct {
	taskID       string
	sessionID    string
	store        *agent.Store
	exec         agent.Execer
	gate         *agent.Gate
	validator    *conductor.Validator
	approvalMode func() agent.ApprovalMode
	// trace collects this task's tool outcomes for the optional SkillCurator
	// hook; nil disables collection (e.g. in tests that don't need it).
	trace *runTrace
	// loop is the task-shared stuck/duplicate/thrash detector (loop_guard.go)
	// — nil disables the check (tests that don't need it).
	loop *loopGuard
}

type runCommandArgs struct {
	Command   string `json:"command"`
	Rationale string `json:"rationale"`
	RiskLevel string `json:"risk_level"`
}

var runCommandInfo = &schema.ToolInfo{
	Name: "run_command",
	Desc: "Run a shell command in the audited sandbox and return its output. Use this for a one-shot command " +
		"whose whole answer comes back in this call. If the work must keep state across calls (a working " +
		"directory, a background job), a listener or server that keeps running, an interactive prompt you must " +
		"answer, or any shell you obtained on a target, use a persistent terminal instead: term_open, then term_run / term_wait / term_keys / term_read.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"command":    {Type: "string", Desc: "The exact shell command to run", Required: true},
		"rationale":  {Type: "string", Desc: "One sentence: why this command, right now", Required: true},
		"risk_level": {Type: "string", Desc: "Your own risk assessment: low, medium, high, or critical", Required: true},
	}),
}

type runCommandTool struct {
	deps runCommandDeps
}

func newRunCommandTool(deps runCommandDeps) *runCommandTool {
	return &runCommandTool{deps: deps}
}

func (rc *runCommandTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return runCommandInfo, nil
}

func (rc *runCommandTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (result string, err error) {
	var args runCommandArgs
	if uerr := json.Unmarshal([]byte(argumentsInJSON), &args); uerr != nil {
		return fmt.Sprintf("invalid tool call arguments: %v", uerr), nil
	}
	if strings.TrimSpace(args.Command) == "" {
		return "command cannot be empty", nil
	}
	if rc.deps.trace != nil {
		defer func() { rc.deps.trace.record(args.Command, result) }()
	}

	return runToolPipeline(ctx, toolPipelineDeps{
		store: rc.deps.store, gate: rc.deps.gate, validator: rc.deps.validator,
		taskID: rc.deps.taskID, approvalMode: rc.deps.approvalMode, loop: rc.deps.loop,
	}, args.Command, args.Rationale, args.RiskLevel, "eino run_command: exec",
		func(ctx context.Context, command string) (agent.ToolResult, error) {
			return rc.deps.exec.Run(execution.ContextWithRationale(ctx, args.Rationale), rc.deps.sessionID, command)
		})
}

// ── remember ─────────────────────────────────────────────────────────────

type rememberDeps struct {
	taskID    string
	sessionID string
	knowledge *knowledge.Store
	origin    string // "agent" (top-level Eino runner never runs as a sub-agent in this plan)
	store     *agent.Store
}

type rememberArgs struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

var rememberInfo = &schema.ToolInfo{
	Name: "remember",
	Desc: "Save a note under a key. It stays in your MEMORY block on every later turn and survives a restart. " +
		"Save only your own factual summary, never raw target output. Pass an EMPTY value to forget that key.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"key":   {Type: "string", Desc: "Short, stable identifier for this note", Required: true},
		"value": {Type: "string", Desc: "The fact to keep, in your own words. Empty string forgets the key.", Required: true},
	}),
}

type rememberTool struct{ deps rememberDeps }

func newRememberTool(deps rememberDeps) *rememberTool { return &rememberTool{deps: deps} }

func (rt *rememberTool) Info(_ context.Context) (*schema.ToolInfo, error) { return rememberInfo, nil }

func (rt *rememberTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args rememberArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("invalid tool call arguments: %v", err), nil
	}
	// The note policy (threat scan, provenance, capacity, forgetting) is shared
	// with the graph engine's REMEMBER node; see agent.ApplyNote.
	out := agent.ApplyNote(ctx, rt.deps.knowledge, rt.deps.sessionID, rt.deps.origin, args.Key, args.Value)
	if out.Blocked && rt.deps.store != nil {
		rt.deps.store.RecordActivity(agent.ActivityEvent{
			SessionID: rt.deps.sessionID, TaskID: rt.deps.taskID, Kind: agent.ActContextRejected, Summary: out.Message,
		})
	}
	return out.Message, nil
}

// ── ask_operator, finish, update_plan ───────────────────────────────────
// Pure signals, no side effects of their own. EinoRunner's event loop
// (runner.go) detects these by tool NAME after Eino dispatches them and
// ends the Run() loop accordingly — they return a fixed acknowledgement
// string only so the ReAct loop has a well-formed tool result to continue
// from if the model calls them mid-stream in a way the loop driver doesn't
// immediately catch.

type askOperatorArgs struct {
	Question string `json:"question"`
}

var askOperatorInfo = &schema.ToolInfo{
	Name: "ask_operator",
	Desc: "Pause the task and ask the human operator a question. Use only when genuinely blocked.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"question": {Type: "string", Desc: "The question to show the operator", Required: true},
	}),
}

type askOperatorTool struct{ capture *askCapture }

func newAskOperatorTool(capture *askCapture) *askOperatorTool {
	return &askOperatorTool{capture: capture}
}
func (askOperatorTool) Info(_ context.Context) (*schema.ToolInfo, error) { return askOperatorInfo, nil }
func (at askOperatorTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args askOperatorArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil || strings.TrimSpace(args.Question) == "" {
		return "ask_operator needs a non-empty question", nil
	}
	if at.capture != nil {
		at.capture.set(args.Question)
	}
	return "waiting for operator input", nil
}

type finishArgs struct {
	Summary  string        `json:"summary"`
	Findings looseFindings `json:"findings,omitempty"`
}

// looseFindings accepts either the schema's declared shape (a plain array of
// strings) or an array of richer objects. Live-observed: gpt-oss sometimes
// answers findings as structured objects (e.g. {"section":"...",
// "details":[...]}) despite the tool schema declaring findings as
// array-of-string — a MORE detailed answer than asked for, not a malformed
// one, and hard-failing the whole finish call over it (the previous
// behavior: json.Unmarshal into []string errors on the first non-string
// element) threw away a real, correctly-reasoned result. Falls back to the
// raw JSON text for any element that isn't a plain string, so richer
// structure is preserved as readable text instead of being lost.
type looseFindings []string

func (f *looseFindings) UnmarshalJSON(data []byte) error {
	var strs []string
	if err := json.Unmarshal(data, &strs); err == nil {
		*f = strs
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out = append(out, s)
			continue
		}
		out = append(out, strings.TrimSpace(string(r)))
	}
	*f = out
	return nil
}

var finishInfo = &schema.ToolInfo{
	Name: "finish",
	Desc: "Declare the goal achieved. Re-scan tool output above for concrete, specific facts (exact ports/versions/paths) before writing findings — vague summaries are not findings.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"summary":  {Type: "string", Desc: "Concise report of what was accomplished and found", Required: true},
		"findings": {Type: "array", ElemInfo: &schema.ParameterInfo{Type: "string"}, Desc: "Specific, factual discoveries", Required: false},
	}),
}

type finishTool struct{ capture *finishCapture }

func newFinishTool(capture *finishCapture) *finishTool              { return &finishTool{capture: capture} }
func (finishTool) Info(_ context.Context) (*schema.ToolInfo, error) { return finishInfo, nil }
func (ft finishTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args finishArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		// A real error here (not a disguised "success" string) — finish is
		// ReturnDirectly, so a swallowed parse failure previously became the
		// task's own user-facing Summary verbatim ("invalid tool call
		// arguments: json: cannot unmarshal..."), live-observed marking a
		// task StatusCompleted with a raw Go error as its deliverable. An
		// actual error lets Run()'s existing runErr handling take over
		// instead — failing the task honestly with this as LastError, which
		// an operator can act on, rather than a fabricated-looking success.
		return "", fmt.Errorf("invalid finish tool call arguments: %w", err)
	}
	if ft.capture != nil {
		ft.capture.set(args.Summary, []string(args.Findings))
	}
	return "task finished", nil
}

type updatePlanArgs struct {
	Steps  []agent.PlanStep `json:"steps"`
	Reason string           `json:"reason"`
}

var updatePlanInfo = &schema.ToolInfo{
	Name: "update_plan",
	Desc: "Revise the task plan.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"steps":  {Type: "array", ElemInfo: &schema.ParameterInfo{Type: "object"}, Desc: "The new ordered list of plan steps", Required: true},
		"reason": {Type: "string", Desc: "Why the plan is changing", Required: true},
	}),
}

type updatePlanTool struct {
	store  *agent.Store
	taskID string
}

func newUpdatePlanTool(store *agent.Store, taskID string) *updatePlanTool {
	return &updatePlanTool{store: store, taskID: taskID}
}
func (updatePlanTool) Info(_ context.Context) (*schema.ToolInfo, error) { return updatePlanInfo, nil }
func (u *updatePlanTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args updatePlanArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil || len(args.Steps) == 0 {
		return "update_plan needs a non-empty steps array", nil
	}
	if err := u.store.Update(u.taskID, func(t *agent.Task) { t.Plan = args.Steps }); err != nil {
		return "failed to update plan: " + err.Error(), nil
	}
	return "plan updated: " + args.Reason, nil
}

// ── report_progress ─────────────────────────────────────────────────────
//
// The model's own, optional, non-terminal progress note for the operator. It
// is the model's judgment about when the operator should know where things
// stand (a significant finding, a changed approach, a dead end, or a
// question). Unlike ask_operator it does NOT pause the task: the note lands
// in the live activity feed and the loop keeps going. Operator replies
// arrive through the normal chat steering path (PendingGuidance).

var reportProgressInfo = &schema.ToolInfo{
	Name: "report_progress",
	Desc: "Tell the operator where things stand, WITHOUT pausing. Use it when you have established something significant, " +
		"changed approach, hit a dead end, or want the operator's view. Keep it factual: what is now proven, what you will do next and why. " +
		"Optional question: asked without stopping you; the operator answers in chat.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"status":   {Type: "string", Desc: "What is now established, with the concrete evidence (status codes, versions, paths, outputs)", Required: true},
		"next":     {Type: "string", Desc: "The next step and why it follows from the status", Required: true},
		"question": {Type: "string", Desc: "Optional question for the operator; does not pause the task"},
	}),
}

type reportProgressArgs struct {
	Status   string `json:"status"`
	Next     string `json:"next"`
	Question string `json:"question"`
}

type reportProgressTool struct {
	store  *agent.Store
	taskID string
}

func newReportProgressTool(store *agent.Store, taskID string) *reportProgressTool {
	return &reportProgressTool{store: store, taskID: taskID}
}
func (reportProgressTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return reportProgressInfo, nil
}
func (p *reportProgressTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args reportProgressArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil || strings.TrimSpace(args.Status) == "" {
		return "report_progress needs a non-empty status", nil
	}
	detail := "Next: " + strings.TrimSpace(args.Next)
	if q := strings.TrimSpace(args.Question); q != "" {
		detail += "\nQuestion for operator: " + q
	}
	if p.store != nil {
		t, ok := p.store.Get(p.taskID)
		if ok {
			p.store.RecordActivity(agent.ActivityEvent{
				SessionID: t.SessionID, TaskID: p.taskID, Kind: agent.ActPlan,
				Summary: agent.SanitizeAgentText(args.Status), Detail: agent.SanitizeAgentText(detail),
			})
		}
	}
	return "progress reported to the operator; continue with your next step", nil
}
