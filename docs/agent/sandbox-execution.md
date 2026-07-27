← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Sandbox Execution

This page traces the **one** path every command takes, from something the model or an operator wants to run, to a real shell inside a real Kali container. Understanding this chain is what makes the rest of the agent's design legible: no matter how many nodes, sub-agents, or skills are involved in *deciding* what to run, there is exactly one place where "run it" actually happens, and everything else routes through it.

## Table of contents

- [The invariant, stated precisely](#the-invariant)
- [The chain, function by function](#the-chain)
- [What the agent itself is and isn't allowed to touch](#agent-boundary)
- [The other agent-reachable exec path: the terminal](#terminal-path)
- [Paths that exist but are not agent-controllable](#non-agent-paths)
- [What actually happens inside the container](#inside-container)

---

## The invariant, stated precisely <a id="the-invariant"></a>

`agent/exec.go` contains **zero** calls to `os/exec` or anything that spawns a process — it only ever calls `execution.Store.Enqueue()`. This is not a convention the agent package happens to follow; it's a structural fact you can verify by grepping the file. Every node in the graph that appears to "run a command" — `tool`, `approval` (which runs the command inline on operator approval), `threat_hold` (which is about a command's *output*, not running a new one) — ultimately goes through this same enqueue-and-drain path, never a direct call.

## The chain, function by function <a id="the-chain"></a>

```
agent (any node)
  → execution.Store.Enqueue(command)                       [orchestrator/internal/execution/store.go]
  → executor.ProcessFunc drains the queue                    [orchestrator/internal/executor/executor.go]
  → executor.RunScopedCommand(ctx, bridge, gatewayClient, command)
      1. scope.ExtractTarget(command)                        [orchestrator/internal/scope/scope.go]
      2. if the target is a hostname: gateway.CheckHostname() — name-level policy, BEFORE resolution
      3. dnsresolve.Resolve(target)                           [orchestrator/internal/dnsresolve] — Cloudflare 1.1.1.1
      4. gateway.CheckEgress(resolvedIP)  →  "allow" | "block" | "needs_approval"
      5. if allow: gateway.AddTemporaryRule(ip/32 or /128, ttl=3600s)   — opens the nftables hole
      6. sandbox.Bridge.Exec(ctx, command)
           → docker exec --user 1000:1000 yummi-sandbox /bin/bash -lc "<command>"
```

Every one of these six steps is real code you can read start to finish — there is no step where "and then the agent just runs it" happens outside this sequence. `RunScopedCommand` is, by its own doc comment, the single call path every Go code path that executes a pentest command must go through — both the agent/chat pipeline (`ProcessFunc`, above) and the API's direct-exec endpoint (`POST /api/v1/sandbox/exec`, see [API Reference](../api-reference.md#sandbox)) converge on this exact function. **Gateway is the only component that decides "is this target reachable" — `RunScopedCommand` never makes that decision itself, only asks and obeys the answer**, including a fail-closed `default:` branch for any verdict string it doesn't recognize (see [Security Model § The core invariant](../security-model.md#fail-closed)).

## What the agent itself is and isn't allowed to touch <a id="agent-boundary"></a>

Looking at this chain end to end, notice what the agent package genuinely has no access to:

- It cannot decide a target is in scope — it can only receive `allow`/`block`/`needs_approval` from the gateway via the executor, and `verify.go`'s `Verify()` translates a `GATEWAY_BLOCKED:`-prefixed error string into `OutcomeBlocked` purely by string-prefix matching, not by re-deciding anything.
- It cannot open its own nftables rule — `AddTemporaryRule` is called by the executor, using a gateway client the agent package has no reference to.
- It cannot bypass the 120-second default timeout or the 1 MiB output cap `sandbox.Bridge` enforces (`defaultTimeout`, `maxOutputBytes` in `sandbox/bridge.go`) — a runaway or extremely verbose command is bounded regardless of what the model asked for.

## The other agent-reachable exec path: the terminal <a id="terminal-path"></a>

The [Bash tab](../usage-guide.md#tab-bash)'s interactive terminal (`api/terminal.go`, backing `/ws/terminal`) is the **one other** path that reaches the sandbox, and it is also `docker exec` under the hood — a human typing into it hits the same container the same way a `RunScopedCommand` call does. It is deliberately not a bypass of the trust boundary; it's a different *initiator* (a human at a keyboard instead of the model), reaching the same enforced destination.

## Paths that exist but are not agent-controllable <a id="non-agent-paths"></a>

For completeness, here's everywhere else `exec.Command` (or equivalent) appears in the codebase, and why none of them expand the agent's own reach:

- **`internal/cli/*`** (root module) — Docker lifecycle management (`make start`/`stop` equivalents) and browser launching for the CLI's default no-subcommand behavior. Purely operator-invoked, from a human running the `yummi` binary directly — never reachable from anything the model outputs.
- **`internal/mcp/client.go`** — spawns an MCP server process on the host. This package is real and tested, but **not imported by any production code path today** (see [Architecture § What is not in this repository](../architecture.md#not-in-scope)) — it's dormant, not wired into the agent's decision loop, so it currently expands neither the agent's capabilities nor its attack surface. If this ever changes, it would need its own security review before being treated as covered by anything described on this page.

If you're extending this codebase and find yourself writing a new `exec.Command`/`os.StartProcess` call anywhere reachable from agent or LLM output, that is very likely the wrong design by this project's own standing invariant — route it through `executor.RunScopedCommand` instead, or reconsider whether it needs to be agent-reachable at all.

## What actually happens inside the container <a id="inside-container"></a>

`sandbox.Bridge.Exec` shells out to exactly:

```bash
docker exec --user 1000:1000 yummi-sandbox /bin/bash -lc "<command>"
```

- **`--user 1000:1000`** — every command runs as the unprivileged `pentester` user (`sandbox/Dockerfile` creates it at UID/GID 1000), never root, regardless of what the command itself is. `nmap`'s specific need for raw-socket privileges is handled by a targeted `setcap cap_net_admin,cap_net_raw=eip` on the `nmap` binary itself at image build time — a scalpel, not a blanket root grant.
- **`/bin/bash -lc`** — a login, non-interactive shell for each invocation. There is no persistent shell session across commands; the container's own entrypoint (`sandbox/entrypoint.sh`) just sets the default route through gateway and then `exec sleep infinity` — every single command is a fresh `docker exec`, with no state (environment variables set by a previous command, a `cd` into a directory) carrying over between one command and the next unless the sandbox's filesystem itself was changed.
- **Output is capped at 1 MiB per stream** (stdout/stderr independently) and the whole call has a **120-second default timeout** — both hard limits in `sandbox/bridge.go`, not configurable per-command by the model. A timeout or a cancellation both produce a structured `Result` (exit code `-1`, a descriptive `Stderr`) rather than an ambiguous error, so the graph's `observeResult` can tell a real command failure apart from an infrastructure-level cutoff.
