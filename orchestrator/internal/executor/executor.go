// Package executor wires the sandbox bridge and gateway scope checking into the
// execution store's processing pipeline.
package executor

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/scope"
)

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
		if e.bridge == nil {
			return "", fmt.Errorf("sandbox bridge not configured")
		}

		slog.Info("executor: running command",
			"execution_id", rec.ID,
			"session_id", rec.SessionID,
			"command", rec.Command,
		)

		// Extract target from command for scope check (fail-closed).
		// If gateway is unreachable the command is blocked — the trust boundary must
		// be healthy before any execution is permitted.
		if target := scope.ExtractTarget(rec.Command); target != "" {
			if e.gateway == nil {
				return "", fmt.Errorf("gateway scope check unavailable: client not configured")
			}
			inScope, err := e.gateway.CheckScope(target)
			if err != nil {
				slog.Error("executor: gateway scope check error — blocking execution (fail-closed)",
					"target", target, "error", err)
				return "", fmt.Errorf("gateway scope check unavailable: %w", err)
			}
			if !inScope {
				return "", fmt.Errorf("target %q is not in scope (blocked by gateway)", target)
			}

			// Unlock the target in gateway's nftables FORWARD chain so sandbox's packets
			// actually reach it. TTL 3600s — rule expires even if orchestrator crashes.
			// The gateway only accepts IP/CIDR networks (it has no resolver), so we
			// turn the host into its single-address prefix (/32 for IPv4, /128 for
			// IPv6). A bare hostname has no CIDR form and would already have been
			// rejected by CheckScope above, so reaching here implies an IP.
			network := hostNetwork(target)
			if network == "" {
				slog.Warn("executor: target is not an IP, skipping nftables unlock (gateway needs a resolved IP)",
					"target", target)
			} else if _, _, nftErr := e.gateway.AddTemporaryRule(network, 3600, "approved execution: "+rec.Command); nftErr != nil {
				slog.Warn("executor: gateway nftables unlock failed (proceeding — check gateway logs)",
					"target", target, "error", nftErr)
			} else {
				slog.Info("executor: nftables allow rule added", "target", network)
			}
		}

		result, err := e.bridge.Exec(ctx, rec.Command)
		if err != nil {
			if result != nil {
				return result.Stderr, fmt.Errorf("command failed: %w", err)
			}
			return "", fmt.Errorf("exec error: %w", err)
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
