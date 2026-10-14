# Browser and web research

Release status: Production-hardened with in-sandbox HTTP/CONNECT/WebSocket connection proxy (`browser_proxy.go`), generation-based log buffer isolation, fail-closed state management, and edge-cases verification (`qa/edge_cases_qc.py`, `qa/web_search_toggle_qc.py`). See [QC verification](web-research-qc.md).

Research executes in `sandbox-mcp` as UID 1000. Neither the host nor the
orchestrator performs research HTTP or DNS. All external connections traverse
the existing gateway. The fixed internal orchestrator policy endpoint is read
only; sandbox tools cannot edit gateway rules. Existing gateway hostname and
packet policy apply; the tools never open egress automatically.

Research DNS and the agent Chromium profile use Cloudflare DNS-over-HTTPS at
`1.1.1.1:443`, entirely inside the sandbox. This environment's UDP/TCP port-53
queries timed out while real HTTPS/DoH worked. Rules-only deployments must
permit that resolver IP as well as approved destinations; no host DNS fallback
or policy bypass is used. Chromium preferences are documented by the
[Chromium resolver preference definitions](https://chromium.googlesource.com/chromium/src/+/5977f0b3b658787f95f42d1390d3f6129b0f12c9/chrome/common/pref_names.h).

## Setup & Operator Control

Web research is disabled by default. Operators can toggle it at runtime:
- **Interactive UI Toggle**: Click the globe button (`btn-web-search`) directly in the chat composer bar. When active, it pulses with phosphor-teal (`aria-pressed="true"`). If disabled or write fails, it rolls back gracefully and research tools immediately fail closed.
- **REST API Control**: `GET /api/v1/system/web-search` queries the state; `POST /api/v1/system/web-search` updates state atomically (`{"enabled": true|false}`) via `/tmp/yummi_web_search_state`.
- **Environment Configuration**: Set `YUMMI_WEB_SEARCH_ENABLED=true`,
`YUMMI_WEB_SEARCH_MODEL=minimax/minimax-m2.1`, and
`YUMMI_WEB_SEARCH_API_KEY` with an OpenRouter credential. Credentials are never arguments, output, or provider error bodies.
Prefer a dedicated restricted research key: a key in sandbox environment can
be read by other commands running with the same sandbox user's authority.

Search sends the public query to OpenRouter/Exa and incurs provider charges.
Only send public information; do not include private engagement data.
The operator must permit the provider and desired source destinations through
gateway policy. In block/ask/rules-only mode a denied connection returns an
actionable error rather than granting access. Research supports public HTTPS,
port 443, and rejects private/link-local/loopback/reserved DNS destinations.

## Agent tools

- `web_search`: `query`, `max_results` (1–10), `include_domains`,
  `exclude_domains`, and `depth` (`standard` or `deep`). Returns provider
  excerpts with actual citation URLs. No usable citations is an error.
- `web_fetch`: `url`, `offset`, `max_chars` (256–12000), `find`. Fetches original
  HTML/plain text, strips executable markup, returns a source digest and text
  window with `next_offset`. Bodies are bounded at 1 MiB; redirects at three.
- `browser_session`: `status` reports actual mode and isolated session labels;
  `reset` closes every browser session before changing modes.
- Existing browser tools cover navigation, accessible snapshots and refs,
  keyboard/mouse actions, JavaScript, screenshots, cookies/storage and tabs.
  `browser_open(session=...)` selects an isolated cookie/storage context. Tabs
  are context-scoped. `headless=false` uses the sandbox desktop only.

Research results carry `UNTRUSTED_WEB_CONTENT`, URL, title, retrieval timestamp,
SHA-256 and unclassified trust. The UI displays clickable source cards with
escaped excerpts. Provider snippets and hashes are not independent verification
of claims, and web research does not prove the assessment target vulnerable.

## Limits

HTTPS research does not execute JavaScript or accept arbitrary headers/cookies;
use authorized browser tools for those pages. CAPTCHA/login may require the
operator. PDF parsing is not implemented in the plain-text fetch tool. Website
availability, provider quotas and access controls remain external constraints;
the agent must report them truthfully. Content heuristics and the existing
orchestrator threat scan reduce injection risk but are not a complete defense.
Browser navigation is serialized in the active session; concurrently steering
the same browser from independent agent tasks is not an isolated workflow.
The persistent MCP transport survives completion/cancellation of individual
tool-call contexts; orchestrator shutdown explicitly closes it. Normal research
JSON windows are retained up to 16 KiB in model observations, rather than being
cut into invalid head/tail fragments. Larger reports remain bounded valid JSON
with an explicit `EXCERPT_TRUNCATED` notice; fetch smaller windows or use `find`.
Model summaries can still be wrong despite correct source text. Operators must
check consequential claims against the displayed originals; live QC checks
actual source/argument/browser artifacts rather than trusting task status.

## Validation

`sandbox-mcp/Dockerfile.test` runs real Chromium and real tools in a networkless
Kali test container, including cookie/storage isolation, context-scoped tabs,
viewport screenshot dimensions and state-file confinement. Live QC additionally
requires the rebuilt stack, configured provider and real Minimax agent calls.
No provider, browser, resolver or Yummi component is mocked in added tests.

Primary integration references: [OpenRouter web search annotations and filters](https://openrouter.ai/docs/guides/features/plugins/web-search),
[Rod browser contexts](https://pkg.go.dev/github.com/go-rod/rod#Browser.Incognito),
[Go HTML tokenizer](https://pkg.go.dev/golang.org/x/net/html).
