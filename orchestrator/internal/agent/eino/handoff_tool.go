package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

// handoffDeps is everything the handoff tool needs to delegate a focused
// subtask to a specialist sub-agent — the same DelegationCoordinator and
// role map the graph backend's node_handoff.go uses, so both backends share
// one concurrency quota, one addressable/interruptible run registry, and one
// TRACE view.
type handoffDeps struct {
	taskID      string
	sessionID   string
	store       *agent.Store
	subAgents   map[string]*agent.Runner
	hints       map[string]string
	delegations *agent.DelegationCoordinator
	timeout     time.Duration
}

type handoffArgs struct {
	Role    string `json:"role"`
	Subgoal string `json:"subgoal"`
}

func handoffInfo(subAgents map[string]*agent.Runner, hints map[string]string) *schema.ToolInfo {
	desc := "Hand a focused subtask to a specialist sub-agent. Call this more than once in the same turn to delegate " +
		"to several specialists at once — they run concurrently, not one after another."
	if block := buildDelegationBlock(subAgents, hints); block != "" {
		desc += " " + block
	}
	return &schema.ToolInfo{
		Name: "handoff",
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"role":    {Type: "string", Desc: "The specialist role to delegate to", Required: true},
			"subgoal": {Type: "string", Desc: "One clear sentence describing the focused subtask", Required: true},
		}),
	}
}

type handoffTool struct {
	deps handoffDeps
}

func newHandoffTool(deps handoffDeps) *handoffTool { return &handoffTool{deps: deps} }

func (ht *handoffTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return handoffInfo(ht.deps.subAgents, ht.deps.hints), nil
}

func (ht *handoffTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args handoffArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("invalid tool call arguments: %v", err), nil
	}
	role := strings.TrimSpace(args.Role)
	subgoal := strings.TrimSpace(args.Subgoal)
	sub, ok := ht.deps.subAgents[role]
	if !ok || subgoal == "" {
		return fmt.Sprintf("handoff rejected: unknown role %q or empty subgoal; available roles are listed in this tool's description", role), nil
	}

	ht.emit(agent.ActHandoffIn, role+" specialist delegated", "")

	subCtx, cancel := context.WithTimeout(ctx, ht.deps.timeout)
	defer cancel()

	delegationID, childCtx, err := ht.deps.delegations.Begin(subCtx, ht.deps.taskID, role, subgoal)
	if err != nil {
		reason := err.Error()
		ht.emit(agent.ActHandoffOut, role+" specialist could not complete the subtask", reason)
		return fmt.Sprintf("Sub-agent %q could not start on subtask %q: %s; handle it yourself or finish", role, subgoal, reason), nil
	}
	res, runErr := sub.RunSubtask(childCtx, ht.deps.sessionID, subgoal, ht.deps.taskID, func(taskID string) {
		ht.deps.delegations.BindTask(delegationID, taskID)
	})
	ht.deps.delegations.Finish(delegationID, runErr)

	if runErr != nil {
		reason := runErr.Error()
		if subCtx.Err() == context.DeadlineExceeded {
			reason = fmt.Sprintf("timed out after %s", ht.deps.timeout)
		}
		ht.emit(agent.ActHandoffOut, role+" specialist could not complete the subtask", reason)
		return fmt.Sprintf("Sub-agent %q failed on subtask %q: %s; handle it yourself or finish", role, subgoal, reason), nil
	}

	// Content-layer fail-closed, identical policy to node_handoff.go: the
	// sub-agent's summary is LLM-generated and can echo injected content it
	// observed from hostile tool output.
	summary := threatscan.StripANSI(res.Summary)
	var b strings.Builder
	fmt.Fprintf(&b, "Sub-agent %q completed subtask: %s\n", role, subgoal)
	if summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", summary)
		if banner := threatscan.Banner(summary); banner != "" {
			fmt.Fprintf(&b, "%s\n", banner)
		}
	}
	if len(res.Findings) > 0 {
		b.WriteString("Findings (also recorded to shared knowledge):\n")
		for _, f := range res.Findings {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	ht.emit(agent.ActHandoffOut, role+" specialist completed the subtask", summary)
	return b.String(), nil
}

func (ht *handoffTool) emit(kind agent.ActivityKind, summary, detail string) {
	if ht.deps.store == nil {
		return
	}
	ht.deps.store.RecordActivity(agent.ActivityEvent{
		SessionID: ht.deps.sessionID, TaskID: ht.deps.taskID,
		Kind: kind, Summary: summary, Detail: detail,
	})
}
