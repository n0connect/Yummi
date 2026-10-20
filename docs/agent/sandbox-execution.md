[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Sandbox execution and MCP tooling

Graph/Eino task commands and approved chat proposals enter `execution.Store`; the executor selects shell execution or a structured MCP call. Agent code never executes a process on the developer machine. The operator `/api/v1/sandbox/exec` endpoint directly uses the same scoped shell executor without creating an agent task or execution-store record.

## Execution pipeline <a id="pipeline"></a>

```text
Graph / Eino / approved chat proposal
  -> execution.Store.Enqueue
  -> executor.ProcessFunc
     -> shell: RunScopedCommandStream -> sandbox.Bridge.ExecStream
     -> MCP: processMCP -> sandboxmcp.Client.CallTool
  -> Docker broker -> sandbox UID/GID 1000:1000
```

The shell bridge invokes `/bin/bash -lc` inside the configured sandbox. The MCP client opens a persistent `docker exec -i --user 1000:1000 ... /usr/local/bin/yummi-sandbox-mcp` transport. Its lifetime outlasts individual call contexts and it closes at orchestrator shutdown. The constrained broker permits only the configured sandbox and user; it is not a general Docker API.

## Gateway decisions <a id="execution-flow"></a>

For shell targets, `scope.ExtractTargets` feeds hostname-policy checks and IP resolution, then gateway egress checks. All extracted targets must be allowed before temporary rules are opened. A block returns `ErrOutOfScope`; a `needs_approval` verdict holds the execution in `awaiting_egress_approval`. Operator resolution installs the approved rule and requeues the execution. Gateway availability fails closed where a network decision is required.

For MCP, `sandboxmcp/catalog.go` supplies the target field and display rendering. A target-bearing call goes through `runScopedTarget`; other calls dispatch directly into sandbox-MCP. **No target field does not mean no network activity.** Terminal commands, in-page browser actions, file-driven tools and research may initiate connections without an orchestrator-extracted target. The gateway's packet policy remains authoritative for forwarded egress; the browser proxy and research client also apply their sandbox policy checks. Local/same-subnet traffic is outside that FORWARD boundary.

Research `web_search`/`web_fetch` return typed pinned-IP approval requests from their sandbox policy client. `processMCP` translates these into the same execution egress hold. Research tools never mutate gateway rules themselves.

## Discover the installed tools <a id="mcp-tools"></a>

Use MCP `tools/list` through the installed sandbox transport for current names, input schemas and descriptions. `sandbox-mcp/tools.go:registerTools` registers typed wrappers, the extended tool catalog, consolidation/passive recon utilities, terminals, browsers and research. A hardcoded tool count can drift from both source and the running image.

| Family | Examples / reference |
|---|---|
| Typed wrappers | `nmap_scan`, `whatweb_scan`, `curl_probe`, `gobuster_dir`, `ffuf_fuzz`, `nikto_scan` |
| Extended pentest wrappers | `sandbox-mcp/tools_extended.go`; names such as `sqlmap_scan`, `nuclei_scan`, `httpx_probe` |
| Utilities | `consolidate_hosts` merges host lists; `passive_recon_batch` invokes configured recon binaries concurrently |
| Persistent tmux | `term_open`, `term_run`, `term_read`, `term_wait`, `term_keys`, `term_list`, `term_close` |
| Chromium | [Browser automation](browser-automation.md) |
| Public research | `web_search`, `web_fetch`; [configuration and limits](web-research.md) |

`search_pentest_tools` and `run_pentest_tool` are Eino-side discovery/dispatch tools, not names to assume the sandbox MCP server registers. Eino can also wrap discovered schemas natively. Its `run_command` fallback is orchestrator-side and still dispatches to the sandbox.

## Persistent terminals <a id="tier-4"></a>

`term_open` creates a sandbox tmux session and returns an `id`. Pass that `id` to subsequent calls. `term_run` defaults to waiting for completion; `wait=false` returns after starting a listener or other long-running command. `term_read` reads screen/scrollback; `term_wait` waits for a pattern or idle interval; `term_keys` sends text/keys; `term_close` kills the selected session. Discover the exact schema and timeout bounds before automating them.

Terminal-session tools share the execution approval path, but their target-free catalog entry is not a network exemption. Running `term_run` can initiate arbitrary sandbox traffic subject to gateway packet policy.

## Argument and output boundaries <a id="argv-safety"></a>

Typed pentest wrappers build direct argv slices, avoiding shell interpretation of argument metacharacters. The generic shell bridge and terminal command tools intentionally interpret shell commands; tool-specific languages and flags still have their own semantics. A display command is an audit label, not a safe substitute for the actual structured arguments.

Shell execution defaults to a ten-minute bridge timeout (`YUMMI_SANDBOX_EXEC_TIMEOUT`) and caps captured stdout/stderr at 1 MiB per stream. Typed MCP tools have their own command/time limits; persistent sessions have separate lifecycles. Shell execution can publish incremental `execution.output` chunks plus elapsed-time heartbeats. Completed bounded output remains in the execution record; chunks are live-only and are not replayed on reconnect.

## Runtime and workbench <a id="container-constraints"></a>

Sandbox-MCP requires Linux Docker, UID 1000, the Docker marker and the installed sandbox binary before startup, process capture, tmux or browser launch. Chromium discovery is restricted to `/usr/bin/chromium`; no developer browser fallback is permitted. Run tests/builds using [Docker confinement](sandbox-confinement.md), including `make _test-sandbox-mcp`; never run its Go tests on the host.

The unified sandbox provides XFCE/noVNC, tmux, installed engagement tools, a writable workbench and `/workspace` mounts. `/etc/yummi/pwnbox.md` is the on-box inventory and usage reference; source is `sandbox/pwnbox.md`. Wordlist environment variables come from `/etc/profile.d/yummi-pwnbox.sh` for login shells. Exact tools and mounted assets depend on the built image and Compose configuration.

Source: `orchestrator/internal/executor/executor.go`, `internal/sandbox/bridge.go`, `internal/sandboxmcp/client.go`, `catalog.go`, `sandbox-mcp/tools.go`, `capture.go`, `terminal.go` and `confinement.go`.
