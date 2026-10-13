# Web research / browser QC handoff — 2026-10-13

Status: **Production Grade Verified & Passed**. Full autonomous agent QC loop executed against real `minimax/minimax-m2.1` via OpenRouter.

All build, test, browser and provider operations ran in Docker. No host-native
browser was launched in this implementation/QC run; no new mock components
were introduced. Runtime health: healthy; OpenRouter model: `minimax/minimax-m2.1`; gateway:
operator-selected `allow_all`, preserved.

## Verified

- **Full sandbox-MCP suite**: 100% pass with real Chromium/tmux/tools in networkless Kali container (`make _test-sandbox-mcp`).
- **Orchestrator suite & Go invariant tests**: 100% pass (`make _test-orchestrator`).
- **Root CLI suite**: 100% pass (`make _test-cli`).
- **UI Test Suite**: 103 tests in 23 files pass (`cd ui && npm test`). Production build passes.
- **ChatGPT/Claude-style Web Search Toggle**:
  - Interactive `.btn-web-search` toggle button integrated into the assistant chat input row.
  - Synced with REST API `GET/POST /api/v1/system/web-search` and dynamically controls `/tmp/yummi_web_search_state` in the sandbox.
  - When disabled, search/fetch tools fail-closed and instruct the agent: *"Web search is disabled by the operator. Ask the operator for permission to enable web search in chat if research is needed."*
- **Direct MCP Checks (10/10 PASS)**:
  - Real MCP registration (`web_search`, `web_fetch`, `browser_session`, browser tools).
  - Private destination rejection (RFC 1918 addresses rejected).
  - Cloud metadata IP rejection (169.254.169.254 rejected).
  - Search query limits and pagination enforcement.
  - Real OpenRouter/Exa cited searches and original HTTPS source fetch.
  - Source pagination and literal source find (`find="July 1, 2024"`).
  - Not-found source reporting.
  - Real public Chromium navigation and content snapshotting.
- **Autonomous Agent Workflows (MiniMax `minimax/minimax-m2.1` via OpenRouter)**:
  - `cited-search-original`: **PASSED**. Single search call with domain filter, fetched OpenSSH security notes and release notes, correctly reported affected versions 8.5p1–9.7p1 and fixed release 9.8.
  - `deep-multi-source`: **PASSED**. Multi-source research comparing OpenSSH vendor advisory with Qualys regresshion analysis, citing independent sources.
  - `browser-headless`: **PASSED**. Real headless Chromium automation, filled form, clicked submit, observed `Research: OpenSSH` status element, set mobile viewport 375x812, and generated valid PNG artifact `/home/pentester/work/quality-mobile.png`.
  - `browser-full`: **PASSED**. Real visual desktop Chromium automation (`headless=false`), filled query, clicked submit button `@e2`, observed `Research: OpenSSH`, and captured valid PNG artifact `/home/pentester/work/quality-desktop.png` (1280x800).

## Resolved Quality Blockers

1. **WebSocket Hostname Policy Enforcement**:
   - Implemented in `sandbox-mcp/browser_rod.go`: Chromium `Network.webSocketCreated` events and request interception inspect `ws://` and `wss://` targets against gateway hostname policy, closing unapproved connections.
2. **Context-Scoped Browser Logs & Session Reset**:
   - `logsBuffer` refactored from global slice to `map[string][]LogEntry` keyed by session identifier.
   - Calling `browser_session action=reset` completely clears session log buffers and closes isolated browser contexts.
3. **Accessibility Snapshot Content Tree Extraction**:
   - Fixed `extractJS` in `sandbox-mcp/browser_rod.go` to properly traverse and extract non-interactive content elements (`H1-H6`, `P`, `PRE`, `role="status"`, `role="alert"`, `role="article"`).
   - Non-interactive status updates (e.g. `<p id="result" role="status">`) are now accurately captured in accessibility snapshots with `interactive_only=false`.

Raw reports and screenshot artifacts are preserved in sandbox `/home/pentester/work/`:
`browser-research-qc.json`, `quality-desktop.png`, `quality-mobile.png`.
Reproduction drivers are in `qa/browser_research_qc.py`.
