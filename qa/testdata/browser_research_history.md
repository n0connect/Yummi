# Archived real QC evidence

`browser_research_history.json` contains the unchanged task and execution API
responses read on 2026-10-15 from the existing sandbox deployment. It was obtained
by GET `/api/v1/tasks/{id}` followed by GET
`/api/v1/sessions/{session_id}/executions?limit=100`; no model was started and no
security mode or gateway rule was changed.

The entries, in order, are:

- `3348fe31-5e5c-489f-82d1-5318c59adc47`: cited original-source research.
- `fe736696-3963-46d4-bd8b-18c7750e1f3d`: deep independent-source research.
- `b2d6b819-0b52-4d00-a232-dd967bd63dfb`: headless browser/mobile PNG.
- `33b2a15e-6bcb-41da-8b2c-9e00d05fae4a`: full browser/desktop PNG.

These are archived observations, not a claim about the current runtime. Tests
must retain the failures: deep lacks exact HTTPS summary citations; full has
its last snapshot and screenshot before submit. Headless has valid action order
and mode evidence, but its historical fixed-path PNG has no original task-completion hash
observation and therefore cannot pass the new artifact-integrity gate. The cited
research case satisfies the source/summary checks.

No mock provider, browser, resolver, transport or invented execution record is
used by `qa/test_qc_evidence.py`. Evidence-removal checks retain only unchanged
records from this archive. Historical artifact manifests must never be created
retroactively to convert these records to PASS.

`browser_research_mobile.png` is the existing 375×812 Chromium screenshot read
from `/home/pentester/work/quality-mobile.png` in the original sandbox on
2026-10-16, using a read-only UID1000 Docker exec. SHA-256:
`1fc00cb5367cbea8df852009cac061ea31de54ea3d5b7cae0addfdee8e0eeaf2`.
It is a decoder test input, not a retroactive provenance/mtime manifest for the
historical task. Corruption/truncation tests derive invalid files from these
actual bytes; they introduce no provider, resolver, transport or tool-output
replacement. Reversed-summary and malformed approval-input tests are explicitly
invalid derivatives of the archived task/call inputs, not new historical claims.
