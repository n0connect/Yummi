# QC runtime placement and evidence

`browser_research_qc.py` separates direct checks from operator control:

- `--direct-only --output NEW_PATH` runs **inside sandbox as UID 1000**. MCP,
  research provider requests and Chromium stay inside the gateway-routed sandbox.
- `--agents-only --output NEW_PATH` runs on the **operator host** against
  `http://127.0.0.1:9080/api/v1` (override `YUMMI_QC_API` for a trusted operator
  network). It creates tasks through the operator API, never through the sandbox
  policy listener. It uses `docker exec --user 1000:1000` only to create the
  per-task artifact directory and inspect PNG metadata. This mode starts real
  paid model calls; run only after security boundary verification and operator
  authorization.
- `--audit-report OLD_REPORT --output NEW_PATH` runs operator-side and starts
  no models. Task identity, dedicated session membership, execution lifetime,
  required tools, sources, argument/output evidence, action order and artifacts
  are recomputed from current API records. Existing report statuses and
  `missing_tools` values are never trusted. Audit accepts trusted local reports,
  not arbitrary adversarial report manifests.

Automatic approvals require authoritative `pending_approval_kind=command`,
`pending_mcp_tool`, JSON `pending_mcp_args` and a proposal ID from the task API.
The task/session/proposal and exact structured arguments are rechecked immediately
before approval. Display commands grant no authority. Threat holds, missing or
changed metadata, extra navigation, tab/log/script actions and any deviation
from the case's exact source/app/ref/viewport/artifact constraints stop the case.
Actual prior executions are replayed through the same constraints. The UI action
helper's optional budget case authorizes only one read-only session-status call.

Use a new output path on every invocation: existing reports are never
overwritten. Each new browser task has a unique session directory
`/home/pentester/work/qc/{session_id}/screenshot.png`; the session must contain
exactly that one task. Execution storage has no task-ID column, so the checker
also requires matching session IDs, `agent_task` mode and task lifetime bounds.
Each task-completion PNG observation binds its SHA-256, decoded pixel hash,
dimensions, size, nanosecond mtime and screenshot execution ID to that task/session. The screenshot must be the only
capture, occur after the real form-result snapshot, and have an mtime within its
execution interval. Later runs cannot reuse this path. This is an observation at
task completion, not a cryptographic attestation of the original capture instant. A missing historical observation remains unverified;
auditing cannot mint one. The inspector strictly decodes non-interlaced 8-bit
RGB/RGBA Chromium PNGs, validates every chunk CRC, IHDR/IDAT/IEND structure,
zlib completion, scanline filters and decoded pixel bounds. Unsupported formats
and corrupt/truncated files fail verification. File size is capped at 32 MiB
and decoded scanlines at 64 MiB.

Browser evidence requires actual app navigation, a resolved query fill and submit
click, an explicit post-click result snapshot, then a screenshot. `browser_eval`
or arbitrary shell output invalidates the controlled workflow. Browser mode must
come from the real sandbox session status, and both mobile and desktop PNG
sizes are checked. Research requires exact HTTPS citations for successfully
fetched original documents plus affected/fixed evidence. A bounded clause heuristic
requires explicit affected 8.5–9.7 and fixed 9.8 associations in the summary,
plus an explicit original-source fixed-release statement. This is not a semantic
proof of arbitrary prose; uncertain wording fails for review. The deep case also
fetches the vendor release notes instead of treating a vulnerable-version upper
bound alone as an explicit fixed-release statement. Search citations alone
cannot stand in for independent original-source fetches.

Run the offline checker regression using archived **real** task records:

```sh
PYTHONPATH=qa python3 -m unittest discover -s qa -p 'test_qc_evidence.py' -v
```

See `testdata/browser_research_history.md` for provenance and the intentionally
failing historical cases. These offline checker tests execute no sandbox tools
and are safe on the host; sandbox-MCP tests continue to require the Docker/Kali
runner.

The UI helpers run in an explicitly operator-owned, disposable Linux QA image,
not in `yummi-sandbox`. Build the current sandbox image first (including the
current installed MCP binary), then:

```sh
docker build -f qa/Dockerfile.operator-ui -t yummi/operator-ui-qc:local .
docker run --name yummi-operator-ui-qc --network container:yummi-ui \
  --cap-drop ALL --security-opt no-new-privileges --read-only \
  --tmpfs /tmp:rw,nosuid,nodev \
  --tmpfs /home/pentester:rw,nosuid,nodev,mode=1777 \
  yummi/operator-ui-qc:local session_live_qc.py
docker cp yummi-operator-ui-qc:/home/pentester/work ./operator-ui-qc-artifacts
docker rm yummi-operator-ui-qc
```

This container shares only the operator UI's network namespace. It receives no
Docker socket, provider credentials, host browser/profile mounts, or agent MCP
connection. `YUMMI_QC_CONTEXT=operator-ui`, a marker installed only in this image,
UID 1000, Docker runtime, `YUMMI_QC_UI_URL=http://127.0.0.1:80`, and
`YUMMI_QC_API=http://127.0.0.1:80/api/v1` are all required by its context guard.
The normal sandbox has no marker and cannot run the operator API helpers.
Research tools are disabled in this QA MCP wrapper; all Chromium actions are
local UI checks. It does not forward an operator-authorized port into the
engagement sandbox.

Use the same image command with `research_ui_qc.py SESSION_ID` to inspect real
persisted source cards, `operator_actions_qc.py` to run a new paid real-agent
approval workflow, or `trace_terminal_qc.py` for the trace layout checks.
`trace_terminal_qc.py --terminal` inspects actual engagement-sandbox tmux through
the existing operator `/sandbox/exec` API, not the QA container's own tmux.
Run paid cases only after boundary verification and explicit authorization.

`web_search_toggle_qc.py` also uses `/sandbox/exec` for fixed Python maintenance
commands: it induces a real write failure on the engagement-sandbox toggle file,
checks the actual HTTP500 and visible UI state, restores the file, disables
research through the operator API, and checks the actual installed sandbox MCP
reports **disabled**. No replacement transport/state file is used. Finally it
restores both acknowledged state and exact original file contents/absence.
This is an explicit temporary state mutation; do not run it during other tasks.
Its output must not be presented as a read-only check.

`research_scope.py --operator-context` is a read-only host-side diagnostic. It
reads actual mode/rules from the operator API and resolves the fixed research
hosts only through `docker exec --user 1000:1000` in the actual sandbox. It does
not open rules, change mode, or resolve sources on the host. A blocked DNS lookup
is reported as unresolved rather than changing policy to make QC pass.
