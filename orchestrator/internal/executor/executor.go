// Package executor wires the sandbox bridge and gateway scope checking into the
// execution store's processing pipeline.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/n0connect/yummi/orchestrator/internal/dnsresolve"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/sandboxmcp"
	"github.com/n0connect/yummi/orchestrator/internal/scope"
)

// defaultResolver maps hostname targets to IPs via Cloudflare DNS — shared by
// RunScopedCommand and UnlockEgressTarget so both resolve a name the same way
// the sandbox does. Stateless; safe for concurrent use.
var defaultResolver = dnsresolve.New()

// ErrOutOfScope marks a command's target as rejected by gateway's scope
// policy. The returned error also carries a "GATEWAY_BLOCKED:" prefix in its
// message — that prefix is the stable marker agent/verify.go checks for to
// distinguish a scope rejection from a generic execution failure when only
// the error string (not the Go error value) survives into the execution
// record. errors.Is(err, ErrOutOfScope) is the correct check in Go code that
// still has the typed error.
var ErrOutOfScope = errors.New("target not in scope")

// ErrEgressApprovalRequired marks a command whose target the gateway placed in
// the "needs_approval" egress verdict (Ask mode, no open rule yet). The command
// is held/blocked for now; the operator-approval round-trip is a later task.
// The returned error also carries an "EGRESS_APPROVAL_REQUIRED:" message prefix
// for callers that only have the error string. This is distinct from
// ErrOutOfScope (a hard block) — fail-closed either way.
var ErrEgressApprovalRequired = errors.New("egress approval required")

// checkedTarget is a target that has passed gateway scope checking, with its
// resolved IPs known — no nftables rule opened yet.
type checkedTarget struct {
	target string
	ips    []string
}

// hostForScope reduces a URL-shaped target to its host; anything else is
// returned unchanged. Shell commands already come out of scope.ExtractTargets
// as hosts; MCP tool arguments can still be full URLs.
func hostForScope(target string) string {
	if !strings.Contains(target, "://") {
		return target
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return target
	}
	return u.Hostname()
}

// checkTargetScope asks the gateway's hostname policy and egress-verdict
// machinery whether target may be reached, without opening any rule yet.
// Extracted from RunScopedCommand's per-target loop so both the multi-target
// shell-command path and the single-target MCP path share one gateway
// decision path.
func checkTargetScope(ctx context.Context, pc *gateway.Client, target string, pinnedIPs []string) (checkedTarget, error) {
	if pc == nil {
		return checkedTarget{}, fmt.Errorf("gateway scope check unavailable: client not configured")
	}
	// MCP catalog tools pass URL-shaped targets (http://host:port/path). The
	// gateway scopes by host only, so reduce the URL to its host first.
	target = hostForScope(target)

	// Name-level scope gate, BEFORE resolution: for a hostname target,
	// consult the gateway's hostname allow/block policy. A "block" verdict
	// is a hard, name-based deny (e.g. *.facebook.com) — reject without ever
	// resolving or touching the IP-egress flow. "allow"/"neutral" proceed.
	// IP targets carry no name, so they skip this.
	if isHostnameTarget(target) {
		hv, err := pc.CheckHostname(target)
		if err != nil {
			slog.Error("checkTargetScope: gateway hostname check error — blocking (fail-closed)",
				"target", target, "error", err)
			return checkedTarget{}, fmt.Errorf("gateway hostname check unavailable: %w", err)
		}
		if hv == "block" {
			return checkedTarget{}, fmt.Errorf("GATEWAY_BLOCKED: hostname %q is blocked by policy: %w", target, ErrOutOfScope)
		}
	}

	// Resolve a hostname target to IP(s) through Cloudflare (the same
	// resolver the sandbox uses) so the gateway — which only understands
	// IPs — can rule on the very address(es) the sandbox will connect to.
	// An IP literal passes through unchanged; an unresolvable hostname
	// yields no IPs and falls back to the gateway's mode-based unresolved
	// verdict (and opens no rule, as before).
	var ips []string
	if len(pinnedIPs) > 0 && pinnedIPs[0] != "" {
		ips = pinnedIPs
	} else {
		ips = defaultResolver.Resolve(ctx, target)
	}
	checkAddr := target
	if len(ips) > 0 {
		checkAddr = ips[0]
	}

	verdict, err := pc.CheckEgress(checkAddr)
	if err != nil {
		slog.Error("checkTargetScope: gateway egress check error — blocking execution (fail-closed)",
			"target", target, "resolved", checkAddr, "error", err)
		return checkedTarget{}, fmt.Errorf("gateway scope check unavailable: %w", err)
	}
	switch verdict {
	case "allow":
		return checkedTarget{target: target, ips: ips}, nil
	case "block":
		return checkedTarget{}, fmt.Errorf("GATEWAY_BLOCKED: target %q is not in scope: %w", target, ErrOutOfScope)
	case "needs_approval":
		return checkedTarget{}, execution.EgressApprovalRequiredError{Target: target, IPs: ips}
	default:
		// Unknown/empty verdict: fail closed rather than proceed to exec.
		slog.Error("checkTargetScope: unknown gateway egress verdict — blocking execution (fail-closed)",
			"target", target, "verdict", verdict)
		return checkedTarget{}, fmt.Errorf("GATEWAY_BLOCKED: target %q got unknown egress verdict %q: %w", target, verdict, ErrOutOfScope)
	}
}

// runScopedTarget checks exactly one target's gateway scope, opens its
// nftables allow rule on success, and then calls execFn. Used by the MCP
// dispatch path (processMCP), whose structured tool arguments already name a
// single explicit target — no command-string parsing involved.
func runScopedTarget(ctx context.Context, pc *gateway.Client, target string, pinnedIPs []string, auditNote string, execFn func(ctx context.Context) (*sandbox.Result, error)) (*sandbox.Result, error) {
	checked, err := checkTargetScope(ctx, pc, target, pinnedIPs)
	if err != nil {
		return nil, err
	}
	warning := openAllowRules(pc, checked.target, checked.ips, auditNote)
	result, err := execFn(ctx)
	if warning != "" && result != nil {
		result.Warning = warning
	}
	return result, err
}

// RunScopedCommand is the single call path that asks gateway whether a
// command's target is in scope, opens the corresponding nftables allow rule
// if so, and only then runs the command in the sandbox. Every Go code path
// that executes a pentest command — the agent/chat pipeline (ProcessFunc,
// below) and the API's direct-exec endpoint — must route through this
// function (or its streaming counterpart, RunScopedCommandStream). Gateway
// is the only component that decides "is this target reachable"; this
// function never makes that decision itself, only asks. Thin wrapper around
// RunScopedCommandStream with a nil onChunk.
func RunScopedCommand(ctx context.Context, bridge *sandbox.Bridge, pc *gateway.Client, command string, pinnedIPs ...string) (*sandbox.Result, error) {
	return RunScopedCommandStream(ctx, bridge, pc, command, nil, pinnedIPs...)
}

// RunScopedCommandStream is RunScopedCommand's streaming counterpart: the
// exact same scope-check-then-exec shape (same target extraction, same
// per-target gateway checks, same rule-opening order), but the final exec
// goes through sandbox.Bridge.ExecStream with onChunk so a caller can
// observe the command's stdout/stderr as it arrives instead of only once it
// finishes. onChunk may be nil, in which case behavior is identical to
// RunScopedCommand.
func RunScopedCommandStream(ctx context.Context, bridge *sandbox.Bridge, pc *gateway.Client, command string, onChunk func(source string, chunk []byte), pinnedIPs ...string) (*sandbox.Result, error) {
	if bridge == nil {
		return nil, fmt.Errorf("sandbox bridge not configured")
	}

	targets := scope.ExtractTargets(command)
	if len(targets) == 0 {
		return bridge.ExecStream(ctx, command, onChunk)
	}
	if pc == nil {
		return nil, fmt.Errorf("gateway scope check unavailable: client not configured")
	}

	// Check every explicit target before opening any rule. Otherwise a
	// command could place an allowed host first and an out-of-scope host later.
	checked := make([]checkedTarget, 0, len(targets))
	for _, target := range targets {
		var targetPinned []string
		if len(targets) == 1 {
			targetPinned = pinnedIPs
		}
		c, err := checkTargetScope(ctx, pc, target, targetPinned)
		if err != nil {
			return nil, err
		}
		checked = append(checked, c)
	}

	// Only after all targets pass, open rules for every resolved address.
	var nftWarnings []string
	for _, item := range checked {
		if warning := openAllowRules(pc, item.target, item.ips, command); warning != "" {
			nftWarnings = append(nftWarnings, warning)
		}
	}
	nftWarning := strings.Join(nftWarnings, "; ")

	result, err := bridge.ExecStream(ctx, command, onChunk)
	if nftWarning != "" && result != nil {
		result.Warning = nftWarning
	}
	return result, err
}

// sandboxMCPClient is the minimal surface Executor needs from
// *sandboxmcp.Client — narrowed to an interface so executor_test.go can use a
// plain struct in place of a real docker-exec-backed client.
type sandboxMCPClient interface {
	CallTool(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error)
}

// Executor connects approved commands to the sandbox container via gateway scope checks.
type Executor struct {
	bridge     *sandbox.Bridge
	gateway    *gateway.Client
	knowledge  *knowledge.Store
	sandboxMCP sandboxMCPClient
}

// New creates an Executor with the given sandbox bridge, gateway client,
// knowledge store (knowledge may be nil to disable procedural capture), and
// sandbox-MCP client (mcpClient may be nil — a record with a non-empty
// MCPTool then fails cleanly instead of panicking, matching this codebase's
// nil-safe-degradation pattern).
func New(bridge *sandbox.Bridge, pc *gateway.Client, ks *knowledge.Store, mcpClient sandboxMCPClient) *Executor {
	return &Executor{
		bridge:     bridge,
		gateway:    pc,
		knowledge:  ks,
		sandboxMCP: mcpClient,
	}
}

// recordTacticOutcome derives a mechanical worked/failed outcome from an
// execution and records a target-scrubbed tactic. Nil store is a no-op.
func recordTacticOutcome(ctx context.Context, ks *knowledge.Store, command string, ok bool) {
	if ks == nil || command == "" {
		return
	}
	outcome := "failed"
	if ok {
		outcome = "worked"
	}
	if err := ks.RecordTactic(ctx, command, outcome, nil, ""); err != nil {
		slog.Warn("executor: record tactic failed", "error", err)
	}
}

// combinedOutput concatenates stdout, stderr, and any non-fatal gateway
// warning into the single string form the execution record stores — shared
// by the shell (ProcessFunc) and MCP (processMCP) paths, whose result
// shapes are unified by mcpResultToSandboxResult specifically so this one
// formatter covers both.
func combinedOutput(result *sandbox.Result) string {
	var out strings.Builder
	if result.Stdout != "" {
		out.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		if out.Len() > 0 {
			out.WriteString("\n--- stderr ---\n")
		}
		out.WriteString(result.Stderr)
	}
	if result.Warning != "" {
		if out.Len() > 0 {
			out.WriteString("\n--- warning ---\n")
		}
		out.WriteString(result.Warning)
	}
	return out.String()
}

// handleExecError classifies a scoped-exec error for the caller: an egress
// approval hold is returned as-is (recEgressErr, true) so ProcessFunc/
// processMCP can hand it straight to the execution store; anything else is
// recorded as a failed tactic and reduced to (output, error) in the same
// shape ProcessFunc must return. Shared tail for both exec paths' error
// branches, which previously duplicated this classification identically.
func handleExecError(ctx context.Context, ks *knowledge.Store, command string, result *sandbox.Result, err error) (output string, outErr error) {
	var egressErr execution.EgressApprovalRequiredError
	if errors.As(err, &egressErr) {
		return "", egressErr
	}
	recordTacticOutcome(ctx, ks, command, false)
	if result != nil {
		return result.Stderr, fmt.Errorf("command failed: %w", err)
	}
	return "", err
}

// ProcessFunc returns an execution.ProcessFunc that can be registered
// on the execution store via SetProcessor.
func (e *Executor) ProcessFunc() execution.ProcessFunc {
	return func(ctx context.Context, rec execution.Record) (string, error) {
		if rec.MCPTool != "" {
			return e.processMCP(ctx, rec)
		}

		slog.Info("executor: running command",
			"execution_id", rec.ID,
			"session_id", rec.SessionID,
			"command", rec.Command,
		)

		var pinned []string
		if rec.PinnedIPs != "" {
			_ = json.Unmarshal([]byte(rec.PinnedIPs), &pinned)
		}

		// A no-op sink outside the store's own process() loop (execution.OutputSink
		// degrades gracefully — see its doc comment), so this line is safe even
		// when ProcessFunc is invoked directly, e.g. in tests.
		sink := execution.OutputSink(ctx)
		onChunk := func(source string, chunk []byte) {
			sink(source, string(chunk))
		}
		result, err := RunScopedCommandStream(ctx, e.bridge, e.gateway, rec.Command, onChunk, pinned...)
		if err != nil {
			// Egress Ask mode: the gateway wants operator approval before the
			// target may be reached. Hold the execution (fail-closed, no
			// traffic) instead of failing it — ResolveEgress resumes or ends it.
			return handleExecError(ctx, e.knowledge, rec.Command, result, err)
		}

		out := combinedOutput(result)
		if result.ExitCode != 0 {
			recordTacticOutcome(ctx, e.knowledge, rec.Command, false)
			return out, fmt.Errorf("exit code %d", result.ExitCode)
		}
		recordTacticOutcome(ctx, e.knowledge, rec.Command, true)

		slog.Info("executor: command completed",
			"execution_id", rec.ID,
			"exit_code", result.ExitCode,
			"duration_ms", result.Duration,
		)

		return out, nil
	}
}

// processMCP handles a record dispatched through the sandbox-resident MCP
// server. It mirrors the shell path's scope-check-then-exec shape via
// runScopedTarget, using the record's already-structured target
// (sandboxmcp.Target) instead of scope.ExtractTargets.
func (e *Executor) processMCP(ctx context.Context, rec execution.Record) (string, error) {
	if e.sandboxMCP == nil {
		return "", fmt.Errorf("sandbox MCP client not configured")
	}
	var args map[string]any
	if rec.MCPArgs != "" {
		if err := json.Unmarshal([]byte(rec.MCPArgs), &args); err != nil {
			return "", fmt.Errorf("invalid mcp args: %w", err)
		}
	}
	execFn := func(ctx context.Context) (*sandbox.Result, error) {
		start := time.Now()
		callResult, err := e.sandboxMCP.CallTool(ctx, rec.MCPTool, args)
		if err != nil {
			return nil, fmt.Errorf("mcp call failed: %w", err)
		}
		return mcpResultToSandboxResult(rec.Command, callResult, time.Since(start)), nil
	}

	// A tool with no target field at all (checksec_audit, radare2_disasm,
	// consolidate_hosts) touches no network — nothing for the gateway to
	// scope-check, and requiring one anyway made every such tool fail every
	// real dispatch (see sandboxmcp.NeedsTarget's doc comment). Every other
	// tool keeps the exact same scope-check-then-exec path as before.
	var result *sandbox.Result
	var err error
	if sandboxmcp.NeedsTarget(rec.MCPTool) {
		target, ok := sandboxmcp.Target(rec.MCPTool, args)
		if !ok {
			return "", fmt.Errorf("mcp tool %q: no target found in arguments", rec.MCPTool)
		}
		result, err = runScopedTarget(ctx, e.gateway, target, nil, rec.Command, execFn)
	} else {
		result, err = execFn(ctx)
	}
	if err != nil {
		return handleExecError(ctx, e.knowledge, rec.Command, result, err)
	}

	out := combinedOutput(result)
	if result.ExitCode != 0 {
		recordTacticOutcome(ctx, e.knowledge, rec.Command, false)
		return out, fmt.Errorf("exit code %d", result.ExitCode)
	}
	recordTacticOutcome(ctx, e.knowledge, rec.Command, true)
	return out, nil
}

// mcpResultToSandboxResult adapts an MCP CallToolResult into the same
// *sandbox.Result shape the shell path returns, so downstream output
// formatting is identical either way. IsError maps to a synthetic non-zero
// exit code (1) — an MCP tool call carries a pass/fail flag, not a real
// process exit code.
func mcpResultToSandboxResult(command string, result *mcp.CallToolResult, dur time.Duration) *sandbox.Result {
	var text strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(tc.Text)
		}
	}
	stdout, stderr, exitCode := text.String(), "", 0
	if result.IsError {
		stdout, stderr, exitCode = "", text.String(), 1
	}
	return &sandbox.Result{
		Command: command, ExitCode: exitCode,
		Stdout: stdout, Stderr: stderr, Duration: dur.Milliseconds(),
	}
}

// maxRuleDescription mirrors the gateway's hard cap on AddTemporaryRule's
// description field (gateway/src/ipc/mod.rs) — a longer string doesn't just
// get logged oddly, the whole request is rejected and the nftables rule is
// never installed. Agent-generated recon commands are frequently long
// multi-step shell pipelines, so this is reachable in practice, not
// theoretical: an untruncated 485-char command plus an 18-21 char label
// exceeds it. See [[project_conductor_cloud_and_live_verification]] memory —
// found live against example.com's candidate-consolidation step.
const maxRuleDescription = 500

// ruleDescription builds an AddTemporaryRule description from a label and the
// triggering command, truncating (on a rune boundary) so the combined string
// never exceeds maxRuleDescription.
func ruleDescription(label, command string) string {
	desc := label + command
	if len(desc) <= maxRuleDescription {
		return desc
	}
	const ellipsis = "...(truncated)"
	budget := maxRuleDescription - len(label) - len(ellipsis)
	if budget <= 0 {
		r := []rune(label)
		if len(r) > maxRuleDescription {
			r = r[:maxRuleDescription]
		}
		return string(r)
	}
	runes := []rune(command)
	kept := 0
	for kept < len(runes) && len(string(runes[:kept+1])) <= budget {
		kept++
	}
	return label + string(runes[:kept]) + ellipsis
}

// openAllowRules installs a 1-hour temporary gateway allow rule for each
// resolved IP of target (or target's own /32·/128 when it is already an IP and
// ips is empty). It never fails the caller — a rule-install problem is returned
// as an operator-visible warning string appended to the command output, since
// the command still ran; "" means every rule went in cleanly. Shared by the
// direct-run and post-approval retry paths so they open rules identically.
func openAllowRules(pc *gateway.Client, target string, ips []string, command string) string {
	networks := resolvedNetworks(target, ips)
	if len(networks) == 0 {
		slog.Warn("executor: target has no resolvable IP, skipping nftables unlock (gateway needs an IP)",
			"target", target)
		return ""
	}
	var warnings []string
	for _, network := range networks {
		if _, _, kernelWarning, nftErr := pc.AddTemporaryRule(network, 3600, ruleDescription("approved execution: ", command)); nftErr != nil {
			slog.Warn("executor: gateway nftables unlock failed (proceeding — check gateway logs)",
				"target", target, "network", network, "error", nftErr)
			warnings = append(warnings, fmt.Sprintf("gateway nftables unlock for %s failed (%v)", network, nftErr))
		} else if kernelWarning != "" {
			slog.Warn("executor: gateway accepted the rule but its kernel unlock failed",
				"target", target, "network", network, "warning", kernelWarning)
			warnings = append(warnings, kernelWarning)
		} else {
			slog.Info("executor: nftables allow rule added", "target", network)
		}
	}
	return strings.Join(warnings, "; ")
}

// resolvedNetworks turns a target + its resolved IPs into the CIDR list the
// gateway needs. Resolved IPs each become a single-address /32 (v4) or /128
// (v6) prefix via ipNetwork; an IP-literal target with no resolution falls
// back to its own prefix; a bare unresolvable hostname yields nothing (no
// rule can be opened). Both address families are supported end to end — the
// gateway's nftables ruleset carries a parallel `allowed_targets_v6` set
// (gateway/src/network/nftables.rs) that CheckEgress/AddTemporaryRule route
// into based on the IP's own family, so no filtering is needed here.
func resolvedNetworks(target string, ips []string) []string {
	if len(ips) == 0 {
		if n := ipNetwork(target); n != "" {
			return []string{n}
		}
		return nil
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if n := ipNetwork(ip); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// isHostnameTarget reports whether target is a hostname (not an IP literal or
// CIDR) — i.e. something the gateway's name-level hostname policy applies to.
func isHostnameTarget(target string) bool {
	if _, err := netip.ParseAddr(target); err == nil {
		return false
	}
	if _, err := netip.ParsePrefix(target); err == nil {
		return false
	}
	return true
}

// ipNetwork returns the single-address CIDR the gateway needs for target.
// Returns a /32 format for IPv4 addresses or /128 format for IPv6 addresses.
// Passed CIDR prefixes are returned unchanged. Returns "" for hostnames or invalid IPs.
func ipNetwork(target string) string {
	if _, err := netip.ParsePrefix(target); err == nil {
		return target
	}
	if addr, err := netip.ParseAddr(target); err == nil {
		if addr.Is4() {
			return target + "/32"
		}
		return target + "/128"
	}
	return ""
}

// UnlockEgressTarget opens temporary gateway allow rule(s) for target so a
// command held in execution.StatusAwaitingEgressApproval can be retried and
// reach it. It is the unlock callback the API's resolve-egress handler passes
// to execution.Store.ResolveEgress — keeping the gateway client out of the
// execution store. A hostname target is resolved via Cloudflare DNS (the same
// resolver the sandbox uses) and a rule is opened for each resolved IP. Returns
// an error only when nothing could be opened — an unresolvable hostname (no IP
// to rule on) or a gateway IPC failure.
func UnlockEgressTarget(pc *gateway.Client, target, command string, pinnedIPs ...string) error {
	if pc == nil {
		return fmt.Errorf("gateway client not configured")
	}
	var ips []string
	if len(pinnedIPs) > 0 && pinnedIPs[0] != "" {
		ips = pinnedIPs
	} else {
		ips = defaultResolver.Resolve(context.Background(), target)
	}
	networks := resolvedNetworks(target, ips)
	if len(networks) == 0 {
		return fmt.Errorf("cannot approve egress for %q: it did not resolve to any IP", target)
	}
	var firstErr error
	opened := 0
	for _, network := range networks {
		if _, _, _, err := pc.AddTemporaryRule(network, 3600, ruleDescription("egress approved: ", command)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		opened++
	}
	if opened == 0 {
		return fmt.Errorf("gateway rule install failed: %w", firstErr)
	}
	return nil
}
