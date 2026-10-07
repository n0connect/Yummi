package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

// toolNode validates, gates, and executes the buffered command — or, for an
// [MCP_TOOL] action, the buffered MCP tool call (see mcpDisplayCommand and
// the branch below). It detects stuck patterns and prior failures, and
// supervises long-running shell commands via the consult channel.
func toolNode(ctx context.Context, s *GraphState) (string, error) {
	action := s.pendingAct
	task, ok := s.store.Get(s.taskID)
	if !ok {
		return "", ErrTaskNotFound
	}

	// Deterministic safety net, local models only: a small/weaker model
	// given the real tool name ("term_open") but asked to express a call as
	// JSON will often fall back to its MUCH stronger prior for "identifier
	// followed by flags" and invent a nonexistent CLI command instead
	// (`term_open --label check5`) — live-reproduced repeatedly even with a
	// worked [MCP_TOOL] example right next to it in the prompt (see
	// ProtocolReference's own doc comment: that fix alone wasn't enough).
	// Rather than keep trying to out-prompt a strong pattern-matching prior,
	// meet the model where it naturally wants to express itself: detect a
	// RUN_COMMAND whose verb is a real cataloged tool name and transparently
	// rewrite it into the equivalent MCP_TOOL call before it ever reaches
	// the validator. A wrong flag name still self-corrects via the EXISTING
	// MCPSchemaHints-on-failure enrichment below — this only has to get the
	// TOOL right, not the exact args.
	if s.cfg.LocalModel {
		if tool, args, ok := InferMCPToolFromShellGuess(action.Command, s.cfg.MCPSchemaHints); ok {
			action.Tool, action.Args, action.Command = tool, args, ""
		}
	}

	// An [MCP_TOOL] action carries a tool name, not a shell command — synthesize
	// a human-readable display string so the validator, stuck/duplicate/thrash
	// windows, and the approval UI all have something to show, exactly the role
	// action.Command plays for RUN_COMMAND below.
	command := action.Command
	if action.Tool != "" {
		command = mcpDisplayCommand(action.Tool, action.Args)
	}

	proposal := conductor.Proposal{
		ID:        uuid.NewString(),
		Command:   command,
		Rationale: action.Rationale,
		RiskLevel: action.RiskLevel,
	}
	validated := s.validate(proposal)
	if len(validated.Proposals) == 0 {
		reason := strings.Join(validated.Warnings, "; ")
		if reason == "" {
			reason = "command rejected by validator"
		}
		return s.blockCommand(Wrap("COMMAND_BLOCKED", reason+
			"\nThe command was empty or malformed after normalization. Propose a concrete, well-formed command."))
	}
	p := validated.Proposals[0]
	s.pendingCommand = p.Command

	// Stuck detection before execution.
	hash := commandHash(p.Command)
	s.cmdWindow = pushWindow(s.cmdWindow, hash, stuckWindowSize)
	s.recentCmds = pushWindow(s.recentCmds, p.Command, stuckWindowSize)
	if isStuck(s.cmdWindow) {
		// Surface the loop to the model as an observation and let it choose its
		// next move normally at the step node (a different command, update_plan,
		// or finish). Keep the current command as the new baseline: clearing the
		// window entirely would let the exact same command execute again after
		// every third blocked proposal.
		obs := Wrap("ABNORMAL_PATTERN", "You are repeating the same commands. Recent: ["+
			strings.Join(s.recentCmds, ", ")+"]. STOP repeating — choose a DIFFERENT command, "+
			"update_plan with a new approach, or finish if the goal is already met.")
		s.cmdWindow = []string{hash}
		s.recentCmds = []string{p.Command}
		return s.blockCommand(obs)
	}
	// Block re-running any command already seen in the recent window — not just
	// the immediately-previous one. This catches A→B→A→B style oscillation at
	// the first repeat instead of letting it cycle until stuck detection.
	if windowCount(s.cmdWindow, hash) >= 2 {
		return s.blockCommand(Wrap("DUPLICATE_BLOCKED", "You already ran this exact command recently: `"+p.Command+"`\n"+
			"Re-running it produces the same output you already have. Choose a DIFFERENT command, "+
			"update the plan, or finish if the goal is met."))
	}
	// Tool-level thrash: the model is rotating trivially-different invocations of
	// the SAME tool (ps -ef / ps -ejH / ps aux), which exact-hash detection above
	// misses. Nudge it to stop re-querying the same thing in different forms and
	// either analyze what it already has or move on. Non-blocking: the model is
	// told, not silenced. Reset the window so the nudge restarts cleanly.
	if tool, thrashing := toolThrash(s.recentCmds); thrashing {
		obs := Wrap("ABNORMAL_PATTERN", "You have run `"+tool+"` "+
			"several times with only trivially-different flags. These produce the SAME class of "+
			"information you already have — you are not learning anything new. STOP re-querying with "+
			"`"+tool+"`: analyze the output you already collected, switch to a DIFFERENT tool/approach, "+
			"update_plan, or finish if the goal is met.")
		s.cmdWindow = nil
		s.recentCmds = nil
		return s.blockCommand(obs)
	}

	// The command passed validation, stuck, and duplicate checks — it is about
	// to run (now in auto mode, or after approval). Surface it on the timeline.
	// Exact command + model rationale already have their dedicated audited
	// records. Activity is a public progress surface, so keep it semantic.
	s.emit(ActCommand, "Command dispatched", p.Rationale)

	// Approval is mode-driven, not risk-driven: in ask_permission mode EVERY
	// command gates on the operator; in auto mode none do — with exactly one
	// hard exception. ForceApprovalReason is set only for a small set of
	// known irreversible/destructive command shapes (validator.go's
	// destructiveFloor: rm -rf, dd to a device, mkfs, shred) that classifyRisk's
	// shell-shape allowlist and the model's own risk_level both can miss.
	// Auto mode exists so an operator can let ordinary risk run unattended —
	// it was never meant to also mean "run a disk-wipe unsupervised because a
	// small local model misjudged it as low risk."
	//
	// task.AutoApproveNonCritical (see its own doc comment on Task) is a
	// narrower, per-task, operator-opted-in escape from ask_permission mode
	// specifically — it never touches ForceApprovalReason's hard gate, and
	// only ever skips the gate for a risk level below high/critical.
	skipForTaskOverride := task.AutoApproveNonCritical && p.ForceApprovalReason == "" && !conductor.IsHighSeverity(p.RiskLevel)
	if (s.cfg.ApprovalMode == ModeAskPermission && !skipForTaskOverride) || p.ForceApprovalReason != "" {
		s.pendingProposal = p
		return edgeRequiresApproval, nil
	}

	// [MCP_TOOL] dispatch: no ExecHandle/long-running-consult support (yet) —
	// StoreMCPExecer.RunMCPTool is a single bounded blocking call, matching
	// exactly how the Eino backend's own runToolPipeline already handles
	// every cataloged pentest tool and terminal verb (see its own comment on
	// "wait timeout" as a recoverable TOOL_TIMEOUT rather than a live
	// consult). Parity with that existing precedent, not a new gap.
	if action.Tool != "" {
		if s.cfg.MCPExec == nil {
			return s.blockCommand(Wrap("COMMAND_BLOCKED",
				"MCP_TOOL is not available on this deployment (no MCPExec configured). Use RUN_COMMAND instead."))
		}
		s.executedCmds = append(s.executedCmds, p.Command)
		result, execErr := s.cfg.MCPExec.RunMCPTool(execution.ContextWithRationale(ctx, p.Rationale), task.SessionID, action.Tool, action.Args, p.Command)
		if execErr != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if strings.Contains(execErr.Error(), "wait timeout") {
				s.consecutiveFailures++
				body := p.Command + "\n" + execErr.Error() + "\nNarrow the scope or switch approach — this tool ran too long and was not allowed to keep going."
				return s.failStep("TOOL_FAILED", body, "tool timeout"), nil
			}
			return "", fmt.Errorf("mcp exec: %w", execErr)
		}
		// Enrich a failed call with the tool's real field names — a raw
		// JSON-schema rejection ("unexpected additional properties [\"name\"]")
		// names what was WRONG but never what's RIGHT, and the model has no
		// catalog-wide schema dump (deliberately, see MCPCatalog) to fall back
		// on. Live-observed need: jinx-20b guessed term_open took "name"; the
		// real field is "label". Without this, that guess is a dead end
		// instead of a one-step correction.
		if result.IsError {
			if hint := s.cfg.MCPSchemaHints[action.Tool]; hint != "" {
				result.Content = strings.TrimRight(result.Content, "\n") + "\nValid fields for " + action.Tool + ": " + hint
			}
		}
		return observeResult(s, result), nil
	}

	// Supervised execution: wait up to SoftTimeout, then consult on long-running.
	// Carrying the model's own stated rationale onto the Record this creates
	// (see execution.ContextWithRationale's doc comment) — purely additive,
	// Execer's own interface never needed to change.
	handle, err := s.exec.Start(execution.ContextWithRationale(ctx, p.Rationale), task.SessionID, p.Command)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("exec start: %w", err)
	}
	s.executedCmds = append(s.executedCmds, p.Command)
	result, done := handle.Wait(s.cfg.SoftTimeout)
	if !done {
		s.pendingHandle = handle
		s.consultCaller = nodeTool
		// Distinguish a genuinely slow command from one blocked on operator
		// egress approval (gateway Ask mode). The latter would otherwise be
		// reported as "still running", which is misleading — the command isn't
		// progressing, it's waiting for the operator to approve the external
		// target in the egress bar.
		if target, held := handle.EgressHold(); held {
			s.consultTag = "AWAITING_EGRESS_APPROVAL"
			s.consultBody = fmt.Sprintf("Command `%s` is held awaiting your egress approval for %q. Approve or deny it in the egress bar, then reply [CONTINUE] to keep waiting, or [ABORT] to cancel.",
				p.Command, target)
		} else {
			s.consultTag = "LONG_RUNNING"
			s.consultBody = fmt.Sprintf("Command still running: `%s` (~%.0fs elapsed). Reply [CONTINUE] to keep waiting or [ABORT] to cancel.",
				p.Command, s.cfg.SoftTimeout.Seconds())
		}
		return edgeLongRunning, nil
	}
	return observeResult(s, result), nil
}

// mcpDisplayCommand renders an [MCP_TOOL] action as a human-readable
// command-equivalent string — the same role action.Command plays for
// RUN_COMMAND — so the validator, stuck/duplicate/thrash windows, and the
// approval UI never need to know MCP calls exist as a distinct shape.
func mcpDisplayCommand(tool string, args map[string]any) string {
	if len(args) == 0 {
		return tool + "()"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return tool + "(?)"
	}
	return tool + "(" + string(b) + ")"
}

// InferMCPToolFromShellGuess detects a shell-shaped command whose verb is a
// real cataloged MCP tool name and parses its "--flag value" /
// "--flag=value" arguments into the equivalent MCP_TOOL args map. Exported
// because two independent dispatch paths hit the identical failure mode: the
// graph agent loop's toolNode (a local model told to use [MCP_TOOL] instead
// invents a CLI-shaped RUN_COMMAND — see that call site) and the
// conductor's single-command suggest/approve flow (internal/api's
// HandleChatApprove, which has no [MCP_TOOL] concept at all and would
// otherwise always try to shell-exec a cataloged tool name and 127-fail).
// known is keyed by tool name (MCPSchemaHints' own keys, reused rather than
// a second lookup table); ok is false for any command whose first word
// isn't a known tool name, so an ordinary shell command is never touched.
// Deliberately simple (whitespace-split, no shell-quote handling beyond
// stripping a surrounding quote pair) — this only needs to catch the common
// single-word-value guesses actually observed live; a wrong or unparsed
// field still self-corrects via MCPSchemaHints' existing on-failure
// enrichment (graph path) or the MCP server's own validation error
// (conductor path, surfaced as an ordinary failed execution record).
func InferMCPToolFromShellGuess(command string, known map[string]string) (tool string, args map[string]any, ok bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil, false
	}
	verb := fields[0]
	if _, isKnown := known[verb]; !isKnown {
		return "", nil, false
	}
	args = map[string]any{}
	for i := 1; i < len(fields); i++ {
		tok := fields[i]
		if !strings.HasPrefix(tok, "--") {
			continue
		}
		tok = strings.TrimPrefix(tok, "--")
		if eq := strings.IndexByte(tok, '='); eq >= 0 {
			args[tok[:eq]] = strings.Trim(tok[eq+1:], `"'`)
			continue
		}
		if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
			args[tok] = strings.Trim(fields[i+1], `"'`)
			i++
		} else {
			args[tok] = true
		}
	}
	return verb, args, true
}

// blockCommand records msg as both the rolling memory observation and the
// node's immediate lastObs (what the next step prompt shows), then routes to
// the error edge — the shared outcome for every toolNode guard (validator
// rejection, stuck/duplicate/thrash detection) that rejects a command before
// it ever runs. Any window reset the caller needs must happen BEFORE calling
// this, since it only handles the observation + edge, not the windows.
func (s *GraphState) blockCommand(msg string) (string, error) {
	s.memCtx.AddObservation(msg)
	s.lastObs = msg
	return edgeIsError, nil
}
