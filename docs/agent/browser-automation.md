[Back to Project README](../../README.md) · [Docs index](../README.md) · [Sandbox Execution](sandbox-execution.md)

# Full-Capability Browser Automation

Yummi provides an autonomous, full-capability browser automation engine built inside the unified Kali sandbox container. Designed to match and exceed the capabilities of Anthropic (Claude in Chrome / Computer Use) and OpenAI (Operator / Codex CUA), the engine operates on token-efficient accessibility trees while preserving the fail-closed network security model.

---

## Table of Contents

- [Architectural Overview](#architecture)
- [Accessibility Tree & @eN Reference Paradigm](#snapshot-ref)
- [In-Sandbox Connection Proxy & Hostname Policy](#browser-proxy)
- [Anti-Bot Evasion & Headful X11 Execution](#anti-bot)
- [Operator Live Observability via noVNC](#observability)
- [Gateway Scope Enforcement & Fail-Closed Egress](#gateway-scope)
- [The 9 Browser MCP Tools](#tools)
- [Session Sharing & Tool Chaining](#session-sharing)
- [Dual-Backend Compatibility (Graph & Eino)](#dual-backend)

---

## Architectural Overview <a id="architecture"></a>

```text
+-----------------------------------------------------------------------------+
|                     BROWSER AUTOMATION PIPELINE                             |
|                                                                             |
|   Agent (Eino Native Function Calling / Graph [MCP_TOOL])                   |
|       |                                                                     |
|       v                                                                     |
|   orchestrator / executor.processMCP                                        |
|       |                                                                     |
|       +--> Gateway CheckScope (Only for browser_open with target URL)       |
|       |       | (nftables allow rule opened if in-scope)                    |
|       |                                                                     |
|       v                                                                     |
|   sandboxmcp.Client (Persistent docker exec -i)                             |
|       |                                                                     |
|       v                                                                     |
|   yummi-sandbox-mcp (BrowserDriver interface -> RodDriver)                  |
|       |                                                                     |
|       +--> In-Sandbox Connection Proxy (browser_proxy.go)                   |
|       |       | (Enforces gateway hostname policy on HTTP/S and WS/WSS)     |
|       v CDP (stealth.Page)                                                  |
|   Chromium Browser (DISPLAY=:1, X11 headful, 1600x900)                      |
|       |                                                                     |
|       v X11 Framebuffer                                                     |
|   TigerVNC (:1) -> websockify (:6080) -> UI [DESKTOP] Tab (noVNC)           |
+-----------------------------------------------------------------------------+
```

---

## Accessibility Tree & @eN Reference Paradigm <a id="snapshot-ref"></a>

Traditional scraping tools flood the model's context window with 50,000+ tokens of raw HTML, leading to context rot, high costs, and hallucinations.

Yummi extracts an **interactive Accessibility Tree snapshot** via Chrome DevTools Protocol (CDP). Non-interactive structural divs and hidden elements are filtered out, while semantic content elements (`H1-H6`, `P`, `PRE`, `LI` as `listitem`, `TD`/`TH` as `cell`, `BLOCKQUOTE`, `CODE`) are extracted alongside interactive controls (`input`, `button`, `select`, `a`), producing a compact **200–400 token representation** with numbered `@eN` references:

```text
Page: DVWA - Vulnerability: SQL Injection
URL: http://172.20.0.5/vulnerabilities/sqli/

@e1 [heading] "Vulnerability: SQL Injection"
@e2 [form]
  @e3 [text] "User ID:"
  @e4 [input type="text"] name="id"
  @e5 [button type="submit"] "Submit"
```

The model interacts with elements with near-100% precision:
```json
[MCP_TOOL]
{"tool": "browser_interact", "args": {"action": "fill", "ref": "@e4", "value": "1' OR '1'='1"}}
[MCP_TOOL]
{"tool": "browser_interact", "args": {"action": "click", "ref": "@e5"}}
```

When a click triggers navigation or dynamic DOM mutation, references are marked stale, and the agent refreshes the snapshot via `browser_snapshot`.

---

## In-Sandbox Connection Proxy & Hostname Policy <a id="browser-proxy"></a>

To guarantee fail-closed security for dynamic in-page resources, redirects, and WebSockets:
- An internal HTTP/CONNECT/WebSocket proxy (`sandbox-mcp/browser_proxy.go`) runs locally inside the sandbox.
- Standard HTTP/S requests are forwarded via `http.Transport.RoundTrip`, preserving request bodies and stream integrity.
- WebSocket upgrades (`Upgrade: websocket`) and `CONNECT` tunnels are intercepted and verified against the gateway hostname policy (`GET /api/v1/gateway/hostname-policy`) **before** establishing any upstream TCP socket.
- Blocked destinations are terminated immediately with HTTP 403 Forbidden, preventing unapproved data exfiltration or policy bypass over WebSocket streams.

---

## Anti-Bot Evasion & Headful X11 Execution <a id="anti-bot"></a>

Headless browsers are quickly detected and blocked by Cloudflare, Akamai, Datadome, and Turnstile. Yummi defeats anti-bot detection through a 3-layer defense:

1. **True Headful Desktop**: Chromium does not run with `--headless`. It runs as a real X11 desktop client on `DISPLAY=:1` inside the TigerVNC/XFCE environment. Viewport, CSS pixels, window rendering, and font metrics are authentic desktop measurements.
2. **CDP Stealth Injection**: `go-rod/stealth` masks `navigator.webdriver`, normalizes permissions, emulates genuine plugin arrays, and mirrors standard desktop Linux Chromium profiles.
3. **Human-in-the-Loop Override**: If an interactive CAPTCHA puzzle is encountered, the operator can switch to the `[DESKTOP]` tab in Yummi's UI, solve it in 5 seconds with their own mouse, and instruct the agent to proceed.

---

## Operator Live Observability via noVNC <a id="observability"></a>

Because Chromium runs on `DISPLAY=:1`, the operator can open the **`[DESKTOP]`** tab in the Yummi Web UI to watch the AI agent operate Chromium in real-time. Buttons clicking, text typing, and pages navigating are completely visible.

---

## Gateway Scope Enforcement & Fail-Closed Egress <a id="gateway-scope"></a>

Yummi maintains its strict fail-closed network boundary:
- `browser_open` specifies `TargetField: "url"` in `orchestrator/internal/sandboxmcp/catalog.go`.
- Before Chromium connects to a remote site, `executor.processMCP` extracts the destination host and evaluates it against Gateway policy (`checkTargetScope`).
- If approved, temporary nftables allow rules are provisioned. If out-of-scope or blocked, `ErrOutOfScope` is returned and navigation is prevented.
- Subsequent verbs (`browser_interact`, `browser_eval`, `browser_snapshot`) operate against the authorized origin with `TargetField: ""` (`NeedsTarget() == false`).

---

## The 9 Browser MCP Tools <a id="tools"></a>

1. **`browser_open`**: Navigate to URL, wait for stability (`networkidle`, `domcontentloaded`, `load`), and return the initial `@eN` snapshot. Supports custom HTTP headers via `headers: {"Authorization": "Bearer ...", "X-Forwarded-For": "..."}`. Configures automatic download handling to `/home/pentester/work/downloads/`.
2. **`browser_snapshot`**: Generate a token-efficient interactive element tree (~200-400 tokens) with recursive traversal through open Shadow DOM and same-origin `<iframe>` documents, including non-interactive content blocks (`li`, `td`, `th`, `blockquote`, `code`).
3. **`browser_interact`**: Execute actions against `@eN` references, CSS selectors, or screen coordinates:
   - `click` / `dblclick`: Mouse click with JavaScript fallback and DOM-settle waiting.
   - `right_click` / `context_menu`: Context menu invocation via right mouse button.
   - `check` / `uncheck`: Automated toggling of checkboxes and radio buttons.
   - `fill`: Reactive input filling with JavaScript `input` and `change` event dispatching.
   - `type`: Keyboard character-by-character input.
   - `press`: Key press events (`Enter`, `Tab`, `Escape`, `Backspace`, `Delete`, `Space`, `Ctrl+A`, Arrow keys).
   - `select`: Dropdown option selection by visible text or regex.
   - `hover`: Mouse hover for dynamic dropdowns and flyout menus.
   - `upload`: File upload for multipart forms, restricted safely to the sandbox workspace.
   - `scroll` / `scroll_to`: Smooth scrolling up or down by pixel amounts, or scrolling specific elements into view.
   - `click_at` / `mouse_click`: Coordinate-based native mouse clicking at `(x, y)` on the viewport (essential for canvas elements, SVG maps, and custom UI components).
   - `move_to`: Moves the mouse cursor to `(x, y)`.
   - `drag_and_drop`: Native mouse drag-and-drop from source (`ref` or `x, y`) to destination (`to_ref` or `to_x, to_y`).
   - `set_viewport`: Dynamic viewport resizing (`width`, `height`, mobile emulation) for testing mobile-responsive web apps.
   - `clipboard_read` / `clipboard_write`: Read or write the system/browser clipboard.
   - `wait_for_selector` / `wait`: Wait for dynamic DOM elements to appear or pause for asynchronous SPA rendering.
4. **`browser_eval`**: Execute arbitrary JavaScript in page context (DOM XSS detection, checking `window` variables, triggering events).
5. **`browser_screenshot`**: Capture full-page or viewport PNGs to `/home/pentester/work/` (accepts `output_file` or `path`). When `annotate: true`, injects a high-contrast Set-of-Marks (SoM) visual overlay:
   - Blue badges (`#3b82f6`): Buttons
   - Green badges (`#10b981`): Inputs, textareas, and select elements
   - Amber badges (`#f59e0b`): Hyperlinks
   - Red badges (`#ef4444`): Other clickable or custom interactive elements
6. **`browser_storage`**: Inspect cookies (`get_cookies`), inject session cookies (`set_cookie`), extract `localStorage` and `sessionStorage` (`get_storage`), wipe session state (`clear`), export login sessions (`save_state`), or load saved state (`load_state`).
7. **`browser_tabs`**: Multi-tab control (`list`, `new`, `switch`, `close`). Automatically tracks tabs created via `target="_blank"`.
8. **`browser_logs`**: Retrieve console messages (`console.log`, `console.error`), dialog messages (`alert`, `confirm`), and network traffic (outgoing requests, HTTP 4xx/5xx errors, JSON API calls). JavaScript dialogs are automatically dismissed to prevent CDP locking. Isolated per driver session generation. Clears log buffer on demand when `clear: true`.
9. **`browser_session`**: Isolated session profiles and security roles (`action: "status"` or `"reset"`). Enables testing role-based access control (e.g. `admin` vs `victim` vs unauthenticated) with segregated cookie storage and clean session wiping.

---

## Headless vs. Headful Control <a id="headless-config"></a>

By default, Yummi launches Chromium in true headful mode on `DISPLAY=:1` inside the TigerVNC/XFCE container, allowing the operator to watch live agent actions in the `[DESKTOP]` tab.

For CI/CD pipelines, headless testing, or resource-constrained headless environments, the engine can be switched to pure headless mode by setting:
```bash
export YUMMI_BROWSER_HEADLESS=true
```
If headful launch fails due to an unavailable X11 display, the engine automatically falls back to headless mode without failing the tool call.

---

## Session Sharing & Tool Chaining <a id="session-sharing"></a>

After the agent completes an authentication flow in the browser:
```json
[MCP_TOOL]
{"tool": "browser_storage", "args": {"action": "get_cookies"}}
```
The resulting cookie payload can immediately be passed to CLI tools:
```bash
sqlmap -u "http://target.local/profile.php?id=1" --cookie="session=xyz123"
ffuf -u "http://target.local/FUZZ" -H "Cookie: session=xyz123" -w common.txt
```
This enables seamless handoff between browser reconnaissance and terminal exploitation.
