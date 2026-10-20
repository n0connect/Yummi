# Browser / web research QC — 2026-10-14, corrected 2026-10-16

> Historical QC record, preserved with its corrections. The subsequent rebuilt deployment and fresh external/agent checks are recorded in the [17 October snapshot](live-deployment-2026-10-17.md). The isolated-only statements below describe the earlier checks, not the later deployment or current runtime state.

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

## Historical direct checks reported on 2026-10-14

The following direct checks were reported in that follow-up. They are not new
16 October runs and do not validate the four agent scenarios below. The
independent audit subsequently found firewall, browser-wait and evidence-gate
defects; see [the preserved audit and remediation addendum](audit-2026-10-15.md).
The original live gateway was in `allow_all`; a healthy service alone did not
prove restricted egress.

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

| Scenario | Corrected result | Task / evidence limit |
| --- | --- | --- |
| Cited original-source research | PASS for the inspected record | `3348fe31-5e5c-489f-82d1-5318c59adc47`: actual search/vendor fetches and full HTTPS citations were present. |
| Deep independent-source comparison | FAIL | `fe736696-3963-46d4-bd8b-18c7750e1f3d`: final citations omitted `https://` and the required explicit fixed-release source evidence was missing. |
| Headless form/mobile PNG | UNVERIFIED under the strict artifact gate | `b2d6b819-0b52-4d00-a232-dd967bd63dfb`: historical action order and 375×812 dimensions were observed, but no original task-completion task/execution/hash observation binds the legacy PNG. |
| Full-browser autonomous workflow | FAIL | `33b2a15e-6bcb-41da-8b2c-9e00d05fae4a`: final snapshot/PNG preceded submit click; the final summary contradicted execution order. Its legacy artifact is also unverified. |

The previous claim that all four passed with zero evidence failures is withdrawn.
A screenshot's dimensions and a task marked completed cannot establish ordered
interaction or artifact provenance. The strict checker requires task/session
binding, successful tool records, fill → resolved submit click → result snapshot
→ screenshot order, exact HTTPS sources, and an original task-completion artifact
observation. That observation records bytes/hash/dimensions at task completion;
it is not cryptographic attestation of the original capture instant. Release-clause
checks are a bounded language heuristic, not semantic proof. Auditing does not
invent missing observations or relabel old records as new runs.

## Isolated remediation verification — 2026-10-15/16

- Real Linux nftables regressions passed for IPv4 and IPv6: logging burst
  exhaustion cannot bypass DROP; `block_all` stops gateway-forwarded DNS and
  established TCP. Approvals survive mode changes; in rules-only/ask mode,
  expiry/removal revokes established traffic that depended on those approvals
  and has no other matching allow. Local/private policy traffic remains reachable.
- The four actual Chromium audit regressions cover fresh post-wait snapshots,
  real request-idle waiting, ARIA live DIV content and current visible click status.
  Their fixes were verified in isolated networkless Kali, not a host browser.
- The real research approval harness passed all six subcases with the installed
  sandbox MCP child running UID/GID1000 and no effective/permitted/ambient caps.
  Literal-IP fetch and hostname DoH each hold on pinned `1.1.1.1`; the operator
  installs an actual gateway rule and the execution retries without a second hold.
  Denial, `block_all`, malformed policy inputs, absent mutator routes and unavailable
  policy/gateway fail closed. No provider credentials or replacement transports
  are supplied. Postapproval fetch/DNS fails because runtime is `--network none`;
  this is not a successful external fetch or paid-model research run.
- Disposable control-boundary checks passed operator host REST/WebSocket ingress,
  sandbox rejection from operator/UI/broker paths, actual UID1000 execution through
  the constrained broker, and real operator Chromium sidebar/toggle checks.

The original live containers were not rebuilt, recreated or restarted for these
isolated checks. The corrected source and passing isolated tests do not certify
that the original running stack has the new topology or enforcement. No new paid
agent scenario replaced the historical records above.

## Reproduction / limits

Follow [qa/README.md](../../qa/README.md) for the role-specific driver commands.
Research/browser tools run as UID1000 inside the sandbox. Operator UI checks run
in the explicit Docker operator-QA image sharing the disposable UI network
namespace; they do not run in the sandbox or use a host browser. Keep direct and
agent reports separate. Fresh paid-model agent scenarios require an intentionally
rebuilt deployment and operator-selected provider configuration; no such new run
was performed for this remediation.

For local isolated regression targets and equivalent Docker commands, see
[the project README](../../README.md#isolated-security-regressions).
The sandbox HTTP/CONNECT/WebSocket proxy checks hostname policy before
connecting; gateway nftables is the packet-level authority. CDP notifications
are observations, not enforcement. The networkless regression covers an
unavailable policy endpoint, not every live-policy combination.

Fetch supports documented public HTTPS text/HTML, not arbitrary private targets
or PDF extraction. CAPTCHA/login and model reliability remain explicit limits.
See [web-research.md](web-research.md).
