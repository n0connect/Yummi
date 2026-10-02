[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Sandbox Execution & MCP Tooling

This document traces the scoped execution pipeline used by agent tasks, approved chat proposals, direct API calls, and the persistent `sandbox-mcp` server inside the Kali container.

---

## Table of Contents

- [The Single Execution Pipeline](#pipeline)
- [Execution Flow & Gateway Trust Boundary](#execution-flow)
- [The Single Execution Pipeline](#pipeline)
- [Execution Flow & Gateway Trust Boundary](#execution-flow)
- [The 47 Sandbox MCP Tools](#mcp-tools)
  - [Tier 1: Individually-Typed Tools](#tier-1)
  - [Tier 2: Cataloged Pentest Tools](#tier-2)
  - [Tier 3: Concurrency & Pure-Go Utilities](#tier-3)
  - [Tier 4: Persistent Terminal Sessions (tmux)](#tier-4)
  - [Fallback: Free-Text Command Execution](#fallback)
- [Target Scope Resolution & Local Tool Exemption](#scope-resolution)
- [Argv Execution Safety vs Shell Execution](#argv-safety)
- [Container Execution Constraints](#container-constraints)

---

## The Single Execution Pipeline <a id="pipeline"></a>

Core Invariant: **`orchestrator/internal/agent` contains zero calls to `os/exec`. All commands flow through `execution.Store.Enqueue()` and are executed via `executor.RunScopedCommand()`.**

```text
+-----------------------------------------------------------------------------+
|                         SINGLE EXECUTION PIPELINE                           |
|                                                                             |
|   Agent (Graph / Eino) / Chat / API                                         |
|       |                                                                     |
|       v                                                                     |
|   execution.Store.Enqueue()                                                 |
|       |                                                                     |
|       v                                                                     |
|   executor.ProcessFunc                                                      |
|       |                                                                     |
|       +---> executor.RunScopedCommand()                                     |
|               |                                                             |
|               |-- 1. Extract targets (IP / Hostname)                        |
|               |-- 2. gateway.CheckHostname() (Pre-resolution policy)        |
|               |-- 3. gateway.CheckEgress()   (IP routing policy)            |
|               |-- 4. gateway.AddTemporaryRule() (If verdict == allow)       |
|               |-- 5. sandbox.Bridge.Exec() / sandboxmcp.Client.CallTool()   |
|                       |                                                     |
|                       v                                                     |
|               Kali Sandbox Container (UID 1000 pentester user)              |
+-----------------------------------------------------------------------------+
```

---

## Execution Flow & Gateway Trust Boundary <a id="execution-flow"></a>

```text
Orchestrator                      Gateway Daemon                     Kali Sandbox
     |                                  |                                  |
     |--- 1. CheckHostname(host) ------>|                                  |
     |<-- 2. Verdict ("allow"/"block") -|                                  |
     |                                  |                                  |
     |--- 3. CheckEgress(resolved_ip) ->|                                  |
     |<-- 4. Verdict ("allow"/"block") -|                                  |
     |                                  |                                  |
     |--- 5. AddTemporaryRule(ip/32) -->|                                  |
     |<-- 6. Rule Added (Expires 1h) ---| (nftables FORWARD open)          |
     |                                  |                                  |
     |--- 7. docker exec / MCP Call -------------------------------------->|
     |                                  |                                  | (Runs tool)
     |<-- 8. Execution Result (Stdout, Stderr, ExitCode) ------------------|
```

---

## The 47 Sandbox MCP Tools <a id="mcp-tools"></a>

`sandbox-mcp` is a standalone Go MCP server compiled directly into `/usr/local/bin/yummi-sandbox-mcp` inside the Kali container and accessed via persistent stdio over `docker exec -i`.

### Tier 1: Six Individually-Typed Tools <a id="tier-1"></a>
These tools expose explicit JSON schema parameters directly to the LLM's native function-calling interface:

1. **`nmap_scan`**: Network host discovery, port scanning, service detection, and OS fingerprinting.
2. **`whatweb_scan`**: Web application technology, CMS, server, and framework fingerprinting.
3. **`curl_probe`**: HTTP/S request inspection, header extraction, custom methods, and body payloads.
4. **`gobuster_dir`**: High-speed web directory and file brute-forcing with wordlists.
5. **`ffuf_fuzz`**: High-performance HTTP fuzzing for directories, parameters, and virtual hosts.
6. **`nikto_scan`**: Web server vulnerability scanning, misconfiguration detection, and default files.

### Tier 2: 32 Cataloged Pentest Tools <a id="tier-2"></a>
Dispatched dynamically via `search_pentest_tools` and `run_pentest_tool`:

- **Web & Bug Bounty**: `httpx_probe`, `dnsx_resolve`, `alterx_mutate`, `gau_fetch`, `katana_crawl`, `dalfox_scan`, `nuclei_scan`, `wpscan_scan`, `wafw00f_scan`, `arjun_scan`, `corsy_scan`, `graphql_cop_scan`, `jsluice_scan`, `screenshot_triage`.
- **Reconnaissance & OSINT**: `amass_enum`, `subfinder_enum`, `theharvester_scan`, `dnsenum_scan`, `naabu_scan`, `tlsx_scan`, `subzy_scan`, `cloud_enum_scan`.
- **Authentication & Database**: `sqlmap_scan`, `jwt_tool_inspect`, `hydra_bruteforce`, `medusa_bruteforce`.
- **Infrastructure & Network**: `enum4linux_scan`, `onesixtyone_scan`, `snmpwalk_enum`, `dirb_scan`.
- **Binary Triage & Reverse Engineering**: `checksec_audit`, `radare2_disasm`.

### Tier 3: Concurrency & Pure-Go Utilities <a id="tier-3"></a>
1. **`consolidate_hosts`**: Merges, deduplicates, and validates multiple host lists without shell pipes.
2. **`passive_recon_batch`**: Concurrently triggers `subfinder`, `amass`, `theHarvester`, and `gau` in parallel.

### Tier 4: Persistent Terminal Sessions (tmux) <a id="tier-4"></a>
First-class persistent terminal verbs backed by `tmux` inside the sandbox, solving the long-running command challenge (listeners, VPNs, reverse shells, interactive prompts) without stalling agent reasoning:

1. **`term_open`**: Opens a new persistent tmux session with configurable columns/rows and returns a session ID.
2. **`term_run`**: Types a command into a session. `wait=true` (default) blocks for exit code and output; `wait=false` fires and returns immediately for background processes (e.g. `nc -lvnp 4444`, `openvpn`, `python3 -m http.server`).
3. **`term_read`**: Reads current screen contents or scrollback history of an active session.
4. **`term_wait`**: Blocks until output matches a pattern (e.g., `"password:"`, `"Connection received"`) or an idle timeout expires.
5. **`term_keys`**: Sends raw keystrokes or tmux keys (`C-c`, `Enter`, navigation) to answer interactive prompts or interrupt processes.
6. **`term_list`**: Lists all active persistent terminal sessions with labels and dimensions.
7. **`term_close`**: Terminates and kills a session cleanly.

### Fallback: Free-Text Command Execution <a id="fallback"></a>
- **`run_command`**: Free-text shell execution for utilities (`git`, `find`, `awk`, `sed`) that do not wrap a specialized pentest tool.

---

## Target Scope Resolution & Local Tool Exemption <a id="scope-resolution"></a>

```text
MCP Tool Call Received
          |
          v
   NeedsTarget(tool)?
     |-- NO  (e.g., checksec_audit, radare2_disasm, jwt_tool_inspect,
     |             consolidate_hosts, term_open, term_run, term_read,
     |             term_wait, term_keys, term_list, term_close)
     |     \-- Executes immediately (No external network scope required, purely local)
     |
     \-- YES (e.g., nmap_scan, httpx_probe, nuclei_scan, arjun_scan)
           \-- Extracts target argument -> gateway.CheckHostname & CheckEgress -> Opens nftables rule
```

---

## Argv Execution Safety vs Shell Execution <a id="argv-safety"></a>

Inside `sandbox-mcp`, tool handlers execute binaries directly using parameter argv slices:
```go
exec.Command("nmap", "-sV", target) // Direct kernel execve, zero shell metacharacter injection
```

Unlike shell execution (`/bin/bash -lc "..."`), direct argv invocation guarantees that argument strings cannot break out of command boundaries via semicolons, pipes, or command substitution operators (`&&`, `||`, `;`, `$()`).

---

## Container Execution Constraints <a id="container-constraints"></a>

- **Non-Root Execution**: Runs strictly as UID 1000 (`pentester`). Capabilities (`NET_RAW`, `NET_ADMIN`) are granted selectively via `setcap` on specific binaries (e.g., `nmap`).
- **Execution Timeout**: Hard default ceiling of 10 minutes (`YUMMI_SANDBOX_EXEC_TIMEOUT`).
- **Output Buffering**: Stdout and Stderr are capped at 1 MiB per stream to prevent memory exhaustion.
- **Fail-Closed Default Route**: Sandbox container network routing directs all egress through the gateway daemon (`172.20.0.2`).
