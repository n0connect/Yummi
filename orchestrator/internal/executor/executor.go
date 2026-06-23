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

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/scope"
)

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
		verdict, err := pc.CheckEgress(target)
		if err != nil {
			slog.Error("RunScopedCommand: gateway egress check error — blocking execution (fail-closed)",
				"target", target, "error", err)
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

		network := hostNetwork(target)
		if network == "" {
			slog.Warn("RunScopedCommand: target is not an IP, skipping nftables unlock (gateway needs a resolved IP)",
				"target", target)
		} else if _, _, nftErr := pc.AddTemporaryRule(network, 3600, "approved execution: "+command); nftErr != nil {
			slog.Warn("RunScopedCommand: gateway nftables unlock failed (proceeding — check gateway logs)",
				"target", target, "error", nftErr)
		} else {
			slog.Info("RunScopedCommand: nftables allow rule added", "target", network)
		}
	}

	return bridge.Exec(ctx, command)
}

// Executor connects approved commands to the sandbox container via gateway scope checks.
type Executor struct {
	bridge  *sandbox.Bridge
	gateway *gateway.Client
}

// New creates an Executor with the given sandbox bridge and gateway client.
func New(bridge *sandbox.Bridge, pc *gateway.Client) *Executor {
	return &Executor{
		bridge:  bridge,
		gateway: pc,
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
			if result != nil {
				return result.Stderr, fmt.Errorf("command failed: %w", err)
			}
			return "", err
		}

		// Build output combining stdout and stderr.
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

		if result.ExitCode != 0 {
			return out.String(), fmt.Errorf("exit code %d", result.ExitCode)
		}

		slog.Info("executor: command completed",
			"execution_id", rec.ID,
			"exit_code", result.ExitCode,
			"duration_ms", result.Duration,
		)

		return out.String(), nil
	}
}

// hostNetwork converts a target into the single-address CIDR the gateway needs
// to install an nftables allow rule: "1.2.3.4" → "1.2.3.4/32", an IPv6 address
// → ".../128", and an already-CIDR target (e.g. "10.0.0.0/24") is passed
// through. It returns "" for anything that is not an IP/CIDR — the gateway has
// no resolver, so a bare hostname cannot be turned into a rule.
func hostNetwork(target string) string {
	if _, err := netip.ParsePrefix(target); err == nil {
		return target // already a CIDR
	}
	addr, err := netip.ParseAddr(target)
	if err != nil {
		return ""
	}
	if addr.Is4() {
		return target + "/32"
	}
	return target + "/128"
}
