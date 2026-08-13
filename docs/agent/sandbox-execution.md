← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Sandbox Execution

This page traces the single **scheduled command** path used by agent tasks, approved chat proposals, and `POST /sandbox/exec`. The interactive Bash WebSocket is a separate human-driven PTY path and is documented explicitly below.

## Table of contents

- [The invariant, stated precisely](#the-invariant)
- [The chain, function by function](#the-chain)
- [What the agent itself is and isn't allowed to touch](#agent-boundary)
- [The other agent-reachable exec path: the terminal](#terminal-path)
- [MCP-backed pentest tools (Eino backend only)](#mcp)
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
      1. scope.ExtractTargets(command)                       [orchestrator/internal/scope/scope.go]
      2. for every explicit hostname: gateway.CheckHostname() — name-level policy, BEFORE resolution
      3. resolve and gateway.CheckEgress() every target       →  "allow" | "block" | "needs_approval"
      4. only if all allow: AddTemporaryRule(ip/32 or /128, ttl=3600s)
      5. sandbox.Bridge.Exec(ctx, command)
           → docker exec --user 1000:1000 yummi-sandbox /bin/bash -lc "<command>"
```

Every step is real code you can read start to finish — there is no step where "and then the agent just runs it" happens outside this sequence. `RunScopedCommand` is, by its own doc comment, the single call path every Go code path that executes a pentest command must go through — both the agent/chat pipeline (`ProcessFunc`, above) and the API's direct-exec endpoint (`POST /api/v1/sandbox/exec`, see [API Reference](../api-reference.md#sandbox)) converge on this exact function. **Gateway is the only component that decides "is this target reachable" — `RunScopedCommand` never makes that decision itself, only asks and obeys the answer**, including a fail-closed `default:` branch for any verdict string it doesn't recognize (see [Security Model § The core invariant](../security-model.md#fail-closed)).

## What the agent itself is and isn't allowed to touch <a id="agent-boundary"></a>

Looking at this chain end to end, notice what the agent package genuinely has no access to:

- It cannot decide a target is in scope — it can only receive `allow`/`block`/`needs_approval` from the gateway via the executor, and `verify.go`'s `Verify()` translates a `GATEWAY_BLOCKED:`-prefixed error string into `OutcomeBlocked` purely by string-prefix matching, not by re-deciding anything.
- It cannot open its own nftables rule — `AddTemporaryRule` is called by the executor, using a gateway client the agent package has no reference to.
- It cannot bypass the 10-minute default timeout or the 1 MiB output cap `sandbox.Bridge` enforces (`defaultTimeout`, `maxOutputBytes` in `sandbox/bridge.go`) — a runaway or extremely verbose command is bounded regardless of what the model asked for. The timeout is an operator-configurable ceiling (`YUMMI_SANDBOX_EXEC_TIMEOUT`, `Bridge.SetTimeout`), not something the model itself can raise or lower.

## The other sandbox exec path: the operator terminal <a id="terminal-path"></a>

The [Bash tab](../usage-guide.md#tab-bash)'s interactive terminal (`api/terminal.go`, backing `/ws/terminal`) is the **one other** path that reaches the sandbox. It is `docker exec` under the hood, but unlike `RunScopedCommand` it does not parse each shell line, call `CheckHostname`, or pre-open target rules. Its network packets still traverse gateway's nftables policy. Consequently, hostname-only rules cannot govern terminal commands or opaque targets loaded from files; use the agent/direct-exec path plus `block`/`ask` when that distinction matters.

## MCP-backed pentest tools (Eino backend only) <a id="mcp"></a>

Nineteen cataloged pentest programs — `nmap`, `whatweb`, `curl`, `gobuster`, `ffuf`, `nikto`, `amass`, `subfinder`, `theHarvester`, `dnsenum`, `enum4linux`, `onesixtyone`, `wafw00f`, `wpscan`, `nuclei`, `sqlmap`, `hydra`, `medusa`, `dirb` — are also reachable through a **second, deliberate** exec pattern: a real MCP server (`sandbox-mcp/`, a standalone Go module built from the [official `modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk), depending on nothing in `orchestrator/`) compiled into the Kali image and reached via a **persistent** `docker exec -i` session — held open, rather than `sandbox.Bridge.Exec`'s one-shot-buffered model. `internal/sandboxmcp.Client` (`orchestrator/internal/sandboxmcp/client.go`) is the orchestrator-side half: it spawns exactly `docker exec -i --user 1000:1000 <container> /usr/local/bin/yummi-sandbox-mcp` and nothing else — locked by `TestSandboxMCPOnlyExecsDocker` in `internal/security/invariants`, the same AST-walking discipline that guards `sandbox.Bridge.Exec`.

This exists specifically for the [Eino backend](overview.md#two-engines) — the tag-protocol graph engine still routes every command through `run_command`/`RunScopedCommand` above, unchanged. Eino's tool strategy has three tiers, cheapest/most-reliable first:

1. **Six individually-typed tools** (`nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan`) — the highest-value, most commonly used tools get a dedicated, fully-declared JSON schema sent to the model's own function-calling API, so the model picks structured arguments (`target`, `ports`, `service_detection`, ...) with API-level enforcement, rather than inventing flags from its own pretrained knowledge.
2. **Search-then-dispatch for the rest of the catalog** — `search_pentest_tools(query)` calls `sandboxmcp.Client.ListTools()` (the MCP protocol's own `tools/list`, free discovery — the SDK already provides it, it just wasn't being called before this) and returns name + description + JSON schema as text for anything not individually typed; `run_pentest_tool(tool, args, rationale, risk_level)` then dispatches by name through the exact same `runPentestCall` pipeline (validate → gate → mcp-exec → verify) the six typed tools use. This is what lets the catalog grow — new sandbox-mcp tools need zero Eino-side code changes to become reachable, only a schema-declared handler in `sandbox-mcp/`. The trade-off: args for a dispatched tool aren't enforced by the model's own function-calling layer the way a typed tool's are — but the sandbox-mcp server still validates them against its real per-tool JSON schema on arrival, so a malformed call comes back as a clear, retryable error rather than a silent bad shell string.
3. **`run_command`** — the free-text fallback for anything that isn't a cataloged pentest tool at all (`git`, `find`, `awk`, ...), unchanged from before this feature existed.

Inside the sandbox-resident server, each tool handler execs the real binary **directly with an argv slice** — `exec.Command("nmap", "-sV", target)`, never `bash -lc`. This is strictly safer than the shell-command path for these tools: no metacharacter injection surface at all, by construction.

The same security machinery still applies, reused rather than duplicated:

```
Eino pentestTool.InvokableRun / runPentestTool.InvokableRun (both call the shared runPentestCall)
  → conductor.Validator.ValidateChatResponse (risk classification, unchanged)
  → agent.Gate (ask_permission mode: blocks on operator approval, same StatusAwaitingApproval dance as run_command)
  → agent.MCPExecer.RunMCPTool → execution.Store.EnqueueMCP(sessionID, tool, args, displayCommand)
      → executor.ProcessFunc sees Record.MCPTool != "" → processMCP
          1. sandboxmcp.Target(tool, args) — the target comes straight from the structured
             argument (e.g. args["target"]), no scope.ExtractTargets string-parsing needed
          2. runScopedTarget(ctx, gatewayClient, target, ...) — the SAME checkTargetScope
             hostname-policy/egress-verdict/rule-opening logic RunScopedCommand's per-target
             loop uses, extracted into a shared helper so there is one gateway decision path,
             not two
          3. sandboxmcp.Client.CallTool(ctx, tool, args) — the actual MCP call, persistent session
          4. mcpResultToSandboxResult — adapts the MCP CallToolResult into the same *sandbox.Result
             shape the shell path returns, so output formatting/threatscan/Verify downstream are
             byte-for-byte the same code either way
```

`execution.Record` carries this uniformly: `MCPTool`/`MCPArgs` are two additional optional columns (empty for every shell-command record), while `Command` still holds a human-readable rendering (`sandboxmcp.RenderCommand`, e.g. `"nmap -sV -sC 10.0.0.5 -p 1-1000"`) for the UI, the approval prompt, and the audit trail — an MCP-backed execution looks identical to an operator approving a shell command, right down to the TRACE tab.

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
- **Output is capped while it is being read at 1 MiB per stream** (stdout/stderr independently); excess bytes are discarded and a truncation marker is appended. The whole call has a **10-minute default timeout** (`YUMMI_SANDBOX_EXEC_TIMEOUT` to override). Both are hard limits in `sandbox/bridge.go`, not configurable per-command by the model. A timeout or cancellation produces a structured `Result` (exit code `-1`, descriptive `Stderr`). Because `Exec` is a single blocking call with no incremental UI streaming, `execution.Store.runWithHeartbeat` republishes a "still running" event with elapsed time every few seconds while a command is in flight — see [Architecture § Data flow: a single chat command](../architecture.md#flow-single-command).
