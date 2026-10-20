[Back to Project README](../../README.md) · [Docs index](../README.md) · [Sandbox execution](sandbox-execution.md)

# Browser automation

The sandbox MCP server drives `/usr/bin/chromium` through Rod/CDP inside the unified Kali sandbox as UID 1000. Headful mode uses the XFCE/noVNC desktop; headless mode stays in the same sandbox. Browser features, screenshots and model summaries do not establish that a workflow succeeded; inspect the actual application result and ordered execution evidence.

## Architecture <a id="architecture"></a>

```text
Graph MCP_TOOL / Eino tool call
  -> execution.Store -> executor.processMCP
  -> persistent sandboxmcp Docker transport
  -> sandbox BrowserDriver / RodDriver
  -> sandbox HTTP/CONNECT proxy -> gateway-forwarded egress
  -> Chromium (headful DISPLAY=:1 or headless)
```

`browser_open` has a URL target in the orchestrator catalog and is checked before dispatch. Later interactions, script execution, new tabs and resources can create additional connections without a new orchestrator target field. The local browser proxy checks hostname policy before opening an upstream socket for HTTP, CONNECT and WebSocket upgrades. It resolves names inside the sandbox. It does not install gateway rules or make every page action an independent operator-approved origin grant.

The proxy reads the fixed, read-only `http://172.20.0.3:9081/v1/hostname-policy` surface. An unavailable/denying hostname policy fails closed for non-loopback destinations. Loopback browser traffic is deliberately local. Gateway nftables remains authoritative for forwarded egress; local/same-subnet traffic is outside that forwarding boundary. CDP request notifications are observations, not the enforcement mechanism.

## Snapshots and references <a id="snapshot-ref"></a>

`browser_snapshot` produces a compact DOM-derived accessibility-style representation with `@eN` element references. It is not a complete browser accessibility-tree dump or a fixed token size. It supports interactive controls, semantic content, open Shadow DOM and same-origin frame traversal. `interactive_only=false` includes more page text, including visible ARIA status/live regions.

Refresh snapshots after navigation or DOM changes; references are ephemeral and can become stale. A successful click response is not enough to prove the intended application state. Observe a fresh post-action result before taking the evidence screenshot.

Example graph actions (the same argument object is used in Eino tool calls):

```text
[MCP_TOOL]
{"tool":"browser_interact","args":{"action":"fill","ref":"@e4","value":"OpenSSH"},"step_id":"s1","risk_level":"low"}
```

Use the real `@eN` from the most recent snapshot, not the illustrative reference above.

## Tools <a id="tools"></a>

Use the installed server's MCP discovery for the current schemas.

| Tool | Behavior |
|---|---|
| `browser_open` | Navigate, select a named isolated session, configure headers, wait for the requested lifecycle and return a fresh snapshot. |
| `browser_snapshot` | Read compact page state, optionally filtering content/selector/depth. |
| `browser_interact` | Click/fill/type, key presses, select/check, hover/upload, scroll, coordinate/drag actions, viewport, clipboard and explicit waits. |
| `browser_eval` | Execute JavaScript in the current page context. Script output and DOM edits are not independent application evidence. |
| `browser_screenshot` | Capture viewport/full-page PNG under allowed sandbox paths, optionally with element annotations. |
| `browser_storage` | Read/set cookies, read/clear storage and save/load confined session state files. |
| `browser_tabs` | List/create/switch/close tabs within the active isolated session. |
| `browser_logs` | Read console/dialog/network observations and optionally clear the bounded buffer. Dialogs are dismissed to avoid blocking automation. |
| `browser_session` | Report actual mode/session labels, or reset all browser sessions before changing mode. |

Interaction action names and argument aliases are defined in `sandbox-mcp/browser.go`, `browser_types.go` and `browser_rod.go`. File upload/storage/screenshot access is subject to sandbox path validation; it does not grant host filesystem access.

## Waiting and mode <a id="headless-config"></a>

Navigation supports `networkidle`, `domcontentloaded` and load behavior. `wait_ms` is applied before the returned snapshot. Network-idle waiting tracks real requests; pages with persistent traffic may not become idle within the timeout. Wait/cancellation errors must be reported instead of interpreted as page success.

Without an explicit override, Chromium prefers headful mode on `DISPLAY=:1`; `YUMMI_BROWSER_HEADLESS=true` (or `1`) selects headless. `browser_open` can set `headless` explicitly. A failed headful launch may fall back to headless, so verify actual `browser_session(action="status")` output before claiming a desktop run. Once running, changing mode requires `browser_session(action="reset")`; reset closes all contexts/pages. Environment settings apply inside the sandbox container, not the developer shell alone.

Headful Chromium can be watched and operated in the UI DESKTOP tab. Rod stealth patches reduce some automation signals; they do not guarantee anti-bot/CAPTCHA bypass. Operator interaction or unavailable sites can still block a workflow.

## Sessions and sharing <a id="session-sharing"></a>

`browser_open(session="admin")` selects a named incognito context with separate cookies/storage; tabs are context-scoped. Profiles are isolated within one browser driver, but the active-session selection is shared. Concurrent independent tasks steering that driver do not have fully isolated browser control.

`browser_storage` can export an authenticated session for authorized sandbox CLI use. Cookies and state files are credentials: their presence is not permission for another target or role. The persistent MCP transport preserves browser state between completed calls; orchestrator shutdown closes it.

## Graph and Eino <a id="dual-backend"></a>

Graph uses `[MCP_TOOL]` and the discovered tool names; Eino uses native wrappers or its schema discovery/dispatch tools. Both use the execution store, command/threat gates and persistent sandbox transport. A command gate and the gateway egress mode are independent. Missing catalog/client configuration must produce an unavailable-tool result, not host execution.

See [QC evidence requirements](../../qa/README.md), [dated browser/research QC](web-research-qc.md) and the [17 October deployment snapshot](live-deployment-2026-10-17.md). The latter reports successful direct browser workflows and failed strict autonomous browser sequence cases separately; it is not a certification of every workflow or the current running image.

Source: `sandbox-mcp/browser.go`, `browser_rod.go`, `browser_proxy.go`, `confinement.go` and `orchestrator/internal/executor/executor.go`.
