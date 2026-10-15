# Browser / web research QC — 2026-10-14

Commands, builds, provider calls and Chromium in this follow-up ran through
Docker. No host browser or new mocks/replacement transports were used.
This does not certify historical execution logs.

## Implemented

- HTTP proxy connection setup remains bounded, but no longer cancels response
  streams after 15 seconds. Chunks flush immediately; truncated bodies abort.
  A real streaming regression failed before the fix and passed afterward.
- Negative research QC checks the intended error, real document length and
  source hashes; provider/network failures cannot pass as boundary validation.
- Persona requires exact source evidence, distinguishes affected/fixed releases
  and forbids fabricating expected application results through DOM edits.
- Required operator actions live above the composer, outside chat history:
  command/sub-agent approval, gateway permission, threat holds, resume/reply
  and extra-step controls. Single requests show just the necessary controls,
  not the task goal or a second explanatory wrapper. Pending decisions are
  locked against concurrent submissions; failures retain actionable requests.
- Session create/update/delete invalidations use the replayable WebSocket hub.
  The open sidebar refreshes without page reload and rejects stale responses.
  Reading history no longer forces the viewport to the latest message.

## Verified in this follow-up

- Complete sandbox-MCP suite: PASS in networkless Kali, real Chromium/tmux/tools.
- Orchestrator API, conductor, execution, agent/Eino, sandbox-MCP client and
  architectural invariant tests: PASS in Docker.
- UI: 103 tests / 23 files and production build PASS. Existing jsdom/React
  warnings and the large Vite chunk warning remain.
- Direct real OpenRouter/Exa search, original HTTPS fetch, pagination/find,
  private/metadata rejection and public Chromium navigation: PASS.
- Real browser edge cases: multi-tab lifecycle, cookie/session isolation,
  dialogs, SPA/form handling and adversarial research inputs: PASS.
- Real toggle error handling: HTTP500 does not falsely change acknowledged UI
  state; disabled MCP fetch fails closed; original toggle state is restored.
- Direct full Chromium: actual fill, click, resulting application status,
  PNG and sandbox/full-mode evidence: PASS.
- Compact command approval: visible and all buttons accessible with history
  scrolled to its beginning, at 1280x800 and 375x812; no duplicate history
  controls; actual decision reaches the backend: PASS.
- Live sidebar: externally created, renamed and deleted QC session appears/
  updates/disappears without page reload: PASS. Only temporary QC sessions
  were deleted.

## MiniMax/OpenRouter agent evidence

| Scenario | Result | Task |
| --- | --- | --- |
| Cited original-source research | PASS | 3348fe31-5e5c-489f-82d1-5318c59adc47 |
| Deep independent-source comparison | PASS | fe736696-3963-46d4-bd8b-18c7750e1f3d |
| Headless form/mobile PNG | PASS | b2d6b819-0b52-4d00-a232-dd967bd63dfb |
| Full-browser autonomous workflow | PASS | 33b2a15e-6bcb-41da-8b2c-9e00d05fae4a |

All four agent scenarios with live `minimax/minimax-m2.1` via OpenRouter pass the quality
gate with 0 evidence failures and verified artifact/result integrity.
- Form submission feedback is captured directly on click via ARIA live/status evaluation and verified through compact accessibility tree snapshots.
- Vendor versions and exact original-source HTTPS URLs are cited and distinguished from secondary inferences.
- Mobile (375x812) and desktop (1280x800) PNG screenshots are generated as real artifacts within sandbox boundaries.
- No mocks, doubles, or relaxed assertions were used. Egress filtering is fail-closed and enforced by the Rust gateway daemon.

## Reproduction / limits

Run the drivers in `qa/` inside the running sandbox as UID 1000:
`browser_research_qc.py`, `edge_cases_qc.py`, `web_search_toggle_qc.py`,
`operator_actions_qc.py` and `session_live_qc.py`. Keep direct and agent
reports separate in sandbox `/home/pentester/work/`. Start through
`make start` so saved provider/model configuration is exported.

The sandbox HTTP/CONNECT/WebSocket proxy checks hostname policy before
connecting; gateway nftables is the packet-level authority. CDP notifications
are observations, not enforcement. The networkless regression covers an
unavailable policy endpoint, not every live-policy combination.

Fetch supports documented public HTTPS text/HTML, not arbitrary private targets
or PDF extraction. CAPTCHA/login and model reliability remain explicit limits.
See [web-research.md](web-research.md).
