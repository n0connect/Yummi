# Live deployment verification — 2026-10-17

The source fixes from `05b8ab6` are now deployed to the actual five-service
Compose stack. This supersedes the earlier isolated-only deployment status in
[the remediation report](security-remediation-2026-10-17.md).

## Deployment and preservation

- No task was active before maintenance. The existing 128 sessions were
  preserved and counted again after startup.
- Sandbox home data and the stopped orchestrator/gateway data volumes were
  archived with a SHA256 manifest under the operator's private
  `~/.yummi/backups/yummi-live-1792250474847/` directory. Sandbox home was restored
  before starting its replacement. Configuration snapshots contain credentials
  and remain private; they are not repository artifacts.
- Existing model/backend/research settings were retained. No provider key was
  placed in a tool argument or operator-QA container.
- The existing Kali tool image was reused. Current sandbox-MCP source was built
  inside Docker without network access and installed together with current
  entrypoint/profile files in an incremental image. Rolling Kali package
  downloads were not repeated. The four other service images were rebuilt.
- Named data volumes and host workspace mounts were retained. The new UI-only
  ingress and internal control networks were created by Compose.

| Service | Running image ID prefix | Networks |
| --- | --- | --- |
| gateway | `ea7ff2f6360e` | internal, external |
| sandbox | `eb69ddf78d75` | internal only |
| orchestrator | `0a9d93bdf61a` | internal, control, external |
| UI | `dc4a415e09ac` | control, ingress |
| constrained Docker broker | `7fa98de8401d` | control only |

The old unrestricted `tecnativa` proxy is no longer running. All five services
were healthy in the final check. Both loopback host ports 3000 and 9080 served
the current operator surface.

## Live results

| Verification | Result |
| --- | --- |
| Sandbox/operator isolation | 12 checks passed: valid-Host requests and spoofed forwarding headers cannot reach the operator API; UI/broker names and IPs are unreachable; the separate policy surface is readable and has no operator mutation routes. |
| Packet lockdown | Twelve real sandbox TCP attempts and one DNS request were blocked in `block_all`; the live gateway forwarding counter increased by 13. |
| Real broker execution | Operator exec returned UID1000 through the constrained broker. |
| Operator Chromium | Session create/rename/delete appeared without reload. Real toggle write failures returned HTTP500 without falsely updating UI state; disabled research failed closed. Both scripts passed and restored toggle state. |
| Direct external research | Passed: registration, private/metadata rejection, query bounds, actual OpenRouter search, original HTTPS fetch, pagination/find/not-found and real public Chromium navigation. |
| Direct browser workflows | Both real form fill→click→result snapshot→PNG sequences passed. Actual modes were headless and full XFCE; fully decoded PNG sizes were 375×812 and 1280×800. These are direct API checks, not autonomous-agent passes. |
| Cited research agent | Fresh completed task `b28c09a6-3ca3-420a-9d4a-cdfbee7bf539` passed the strict checker. |
| Deep research agent | Completed task `c62a4b5e-ae8b-4353-89a7-294a0736177a` passed a fresh API-record audit after the citation-tokenizer correction described below. |
| Strict headless browser agent | Failed/stopped: it proposed an additional snapshot after filling, before the required submit click. A prompt clarification did not resolve this in the retry; both original failures remain recorded. |
| Strict full-browser agent | Failed/stopped for the same extra-snapshot sequence deviation. No screenshot or task-completion evidence was invented. |

### QC correction found by the live run

The deep agent's summary contained all three exact fetched HTTPS URLs followed
by prose colons. The citation tokenizer incorrectly included those colons in
the URLs and reported three missing citations. The unchanged real task and
execution records are archived in
`qa/testdata/research_deep_live_2026-10-17.json`.

A new regression failed before the tokenizer correction and passed afterward.
Literal URLs remain accepted, and trailing prose punctuation is also recognized;
scheme-less citations and changed path suffixes still fail. All 18 QC regressions
passed. A fresh audit of the same completed task passed without another model
call or edits to its summary/observations. Historical failures in the earlier
fixture retain their original results.

The cited-case instruction now explicitly requires the original release notes
with the exact authorized window/find arguments; the earlier optional wording
did not match the fixed-release evidence contract. Browser instructions also
make the existing exact-call constraint explicit; their checker was not relaxed.

## Cleanup and remaining limitation

External checks used `ask` with eleven expiring exact IPv4/IPv6 rules for the
resolver and approved research sources. No `allow_all` mode was used. All eleven
test rules were removed afterward; the original command approval mode was
retained and the gateway was restored to **`block_all`**. The fixture HTTP server,
operator-QA containers and temporary QA image were removed. QC sessions and
their successful/failed evidence were retained.

The live deployment and component checks are complete. Autonomous browser
sequence conformance remains a quality issue with the configured model: the
same APIs work in direct checks, but the strict agent runs did not pass. This
report does not claim four successful agent scenarios.

The local run summary/transcripts are under
`/tmp/yummi-live-1792250474847/`; private backups are stored separately as above.
