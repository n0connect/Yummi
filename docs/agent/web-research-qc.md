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
| Cited original-source research | PASS | 51b8d16c-7ebb-4bc4-a87c-ac8caf1f99e2 |
| Deep independent-source comparison | PASS after persona correction | 4f967fdf-3643-472c-8577-7abc8f8b2b09 |
| Headless form/mobile PNG | PASS | 25bc336e-3f83-4af6-a28a-78b76917f388 |
| Full-browser autonomous workflow | FAIL: final summary claimed a form result absent from actual observations | 88a5bab6-30d3-4072-8ab4-d494bf0d5679 |

The full browser tools work directly, but MiniMax's autonomous sequence is not
yet a clean quality-gate pass. Do not describe all workflows as flawless or
universally production-grade. No failure was converted to PASS by relaxing
assertions. The backend ignores task-creation `step_budget`; exhausting the
actual runtime budget was not forced by substituting an invented pause.

One deployment used direct `docker compose up` and unintentionally restored
the default local model. This was corrected through `make start`; runtime was
verified as cloud `minimax/minimax-m2.1`, and gateway remained operator-selected
`allow_all`. Agent QC now checks the actual runtime model before creating tasks.

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
