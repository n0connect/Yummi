// Package executor wires the sandbox bridge and gateway scope checking into the
// execution store's processing pipeline.
package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/dnsresolve"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
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

// RunScopedCommand is the single call path that asks gateway whether a
// command's target is in scope, opens the corresponding nftables allow rule
// if so, and only then runs the command in the sandbox. Every Go code path
// that executes a pentest command — the agent/chat pipeline (ProcessFunc,
// below) and the API's direct-exec endpoint — must route through this
// function. Gateway is the only component that decides "is this target
// reachable"; this function never makes that decision itself, only asks.
func RunScopedCommand(ctx context.Context, bridge *sandbox.Bridge, pc *gateway.Client, command string) (*sandbox.Result, error) {
	if bridge == nil {
		return nil, fmt.Errorf("sandbox bridge not configured")
	}

	if target := scope.ExtractTarget(command); target != "" {
		if pc == nil {
			return nil, fmt.Errorf("gateway scope check unavailable: client not configured")
		}

		// Resolve a hostname target to IP(s) through Cloudflare (the same
		// resolver the sandbox uses) so the gateway — which only understands
		// IPs — can rule on the very address(es) the sandbox will connect to.
		// An IP literal passes through unchanged; an unresolvable hostname
		// yields no IPs and falls back to the gateway's mode-based unresolved
		// verdict (and opens no rule, as before).
		ips := defaultResolver.Resolve(ctx, target)
		checkTarget := target
		if len(ips) > 0 {
			checkTarget = ips[0]
		}

		verdict, err := pc.CheckEgress(checkTarget)
		if err != nil {
			slog.Error("RunScopedCommand: gateway egress check error — blocking execution (fail-closed)",
				"target", target, "resolved", checkTarget, "error", err)
			return nil, fmt.Errorf("gateway scope check unavailable: %w", err)
		}
		switch verdict {
		case "allow":
			// proceed to nftables unlock + exec below
		case "block":
			return nil, fmt.Errorf("GATEWAY_BLOCKED: target %q is not in scope: %w", target, ErrOutOfScope)
		case "needs_approval":
			return nil, fmt.Errorf("EGRESS_APPROVAL_REQUIRED: %q needs operator approval: %w", target, ErrEgressApprovalRequired)
		default:
			// Unknown/empty verdict: fail closed rather than proceed to exec.
			slog.Error("RunScopedCommand: unknown gateway egress verdict — blocking execution (fail-closed)",
				"target", target, "verdict", verdict)
			return nil, fmt.Errorf("GATEWAY_BLOCKED: target %q got unknown egress verdict %q: %w", target, verdict, ErrOutOfScope)
		}

		// Open an allow rule for every resolved IPv4 (bounded) so a hostname
		// that round-robins across a small set stays reachable; a bare IP
		// target opens just its own /32.
		nftWarning := openAllowRules(pc, target, ips, command)

		result, err := bridge.Exec(ctx, command)
		if nftWarning != "" && result != nil {
			result.Warning = nftWarning
		}
		return result, err
	}

	return bridge.Exec(ctx, command)
}

// Executor connects approved commands to the sandbox container via gateway scope checks.
type Executor struct {
	bridge    *sandbox.Bridge
	gateway   *gateway.Client
	knowledge *knowledge.Store
}

// New creates an Executor with the given sandbox bridge, gateway client, and
// knowledge store (knowledge may be nil to disable procedural capture).
func New(bridge *sandbox.Bridge, pc *gateway.Client, ks *knowledge.Store) *Executor {
	return &Executor{
		bridge:    bridge,
		gateway:   pc,
		knowledge: ks,
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

// ProcessFunc returns an execution.ProcessFunc that can be registered
// on the execution store via SetProcessor.
func (e *Executor) ProcessFunc() execution.ProcessFunc {
	return func(ctx context.Context, rec execution.Record) (string, error) {
		slog.Info("executor: running command",
			"execution_id", rec.ID,
			"session_id", rec.SessionID,
			"command", rec.Command,
		)

		result, err := RunScopedCommand(ctx, e.bridge, e.gateway, rec.Command)
		if err != nil {
			// Egress Ask mode: don't fail the execution and don't record a
			// tactic outcome — the command never ran. Translate to the store's
			// sentinel so it holds the record for operator egress approval
			// (the store deliberately does not import this package).
			if errors.Is(err, ErrEgressApprovalRequired) {
				return "", execution.ErrEgressApprovalRequired
			}
			recordTacticOutcome(ctx, e.knowledge, rec.Command, false)
			if result != nil {
				return result.Stderr, fmt.Errorf("command failed: %w", err)
			}
			return "", err
		}

		// Build output combining stdout, stderr, and any non-fatal gateway warning.
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

		if result.ExitCode != 0 {
			recordTacticOutcome(ctx, e.knowledge, rec.Command, false)
			return out.String(), fmt.Errorf("exit code %d", result.ExitCode)
		}
		recordTacticOutcome(ctx, e.knowledge, rec.Command, true)

		slog.Info("executor: command completed",
			"execution_id", rec.ID,
			"exit_code", result.ExitCode,
			"duration_ms", result.Duration,
		)

		return out.String(), nil
	}
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
		if _, _, kernelWarning, nftErr := pc.AddTemporaryRule(network, 3600, "approved execution: "+command); nftErr != nil {
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
// gateway needs. Resolved IPs each become a single-address /32 prefix; an
// IP-literal target with no resolution falls back to its own /32; a bare
// unresolvable hostname yields nothing (no rule can be opened).
//
// IPv6 is intentionally dropped: the gateway's nftables `allowed_targets` set
// is `type ipv4_addr`, so a v6 allow rule is accepted over IPC but fails the
// kernel unlock. Filtering here avoids opening rules that cannot work (an
// IPv6-only target is simply unreachable through this gateway). If the gateway
// gains a v6 set, lift this filter.
func resolvedNetworks(target string, ips []string) []string {
	if len(ips) == 0 {
		if n := ipv4Network(target); n != "" {
			return []string{n}
		}
		return nil
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if n := ipv4Network(ip); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// ipv4Network returns the single-address CIDR the gateway needs for target,
// but only when it is an IPv4 address ("1.2.3.4" → "1.2.3.4/32") or an IPv4
// CIDR (passed through). Returns "" for IPv6, a hostname, or any non-IP — the
// gateway's allow set is IPv4-only and it has no resolver of its own.
func ipv4Network(target string) string {
	if prefix, err := netip.ParsePrefix(target); err == nil {
		if prefix.Addr().Is4() {
			return target
		}
		return ""
	}
	if addr, err := netip.ParseAddr(target); err == nil && addr.Is4() {
		return target + "/32"
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
func UnlockEgressTarget(pc *gateway.Client, target, command string) error {
	if pc == nil {
		return fmt.Errorf("gateway client not configured")
	}
	ips := defaultResolver.Resolve(context.Background(), target)
	networks := resolvedNetworks(target, ips)
	if len(networks) == 0 {
		return fmt.Errorf("cannot approve egress for %q: it did not resolve to any IP", target)
	}
	var firstErr error
	opened := 0
	for _, network := range networks {
		if _, _, _, err := pc.AddTemporaryRule(network, 3600, "egress approved: "+command); err != nil {
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
