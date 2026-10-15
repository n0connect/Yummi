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
and mode evidence, but its historical fixed-path PNG has no capture-time hash
manifest and therefore cannot pass the new artifact-integrity gate. The cited
research case satisfies the source/summary checks.

No mock provider, browser, resolver, transport or invented execution record is
used by `qa/test_qc_evidence.py`. Evidence-removal checks retain only unchanged
records from this archive. Historical artifact manifests must never be created
retroactively to convert these records to PASS.
