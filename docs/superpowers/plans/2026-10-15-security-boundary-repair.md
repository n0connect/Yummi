# Security boundary repair

> **Historical record —** This dated plan/report describes its recorded revision and environment. It is not current implementation or deployment verification. Use the [maintained documentation](../../../docs/README.md) for current behavior. Original findings and results are preserved below.

Authorized scope: fix the concrete findings in `docs/agent/audit-2026-10-15.md`,
preserve existing work, and verify the real execution paths. No new model
provider or host research execution path is introduced.

## Design

1. Gateway: conditional logging followed by unconditional drop; hard lockdown
   before established/DNS allowances. Real routed Linux kernel regressions.
2. Docker: dedicated internal control network, no sandbox membership; replace
   broad Docker API prefix proxy with a broker restricted to the configured
   sandbox, UID/GID 1000, nonprivileged exec instances issued by that broker.
3. Operator API: explicit trusted peer boundary (loopback and the UI proxy),
   strict browser origin checks, no trust in forwarded IP headers. UI only on
   the control network plus a dedicated UI-only ingress bridge; published
   loopback ports preserve existing operator URLs. The normal ingress bridge
   is required for Docker/OrbStack host-port publication; control stays internal.
   A separate sandbox listener serves only bounded, read-only hostname
   and egress decisions; no operator mutations or session data.
4. Research: DNS and HTTP stay in sandbox. The read-only decision endpoint
   returns gateway verdicts; an MCP error preserves a typed approval request.
   The existing execution store and operator ResolveEgress flow approve pinned
   public IPs. Sandbox never changes gateway rules or performs host DNS.
5. Browser: waits occur under the driver lock before snapshot; real request
   idle/cancellation; visible live regions and bounded changed status feedback.
6. QC: original-source citations, ordered actions and task-bound artifacts;
   agent control runs in operator context, research/browser runs in sandbox.
   Pin a supported Brain Python interpreter.

## Implementation and verification

- Gateway, browser and QC/Brain are independent parallel assignments; root
  integrates topology, operator ingress, read-only policy and research holds.
- Reproduce failures before fixes, use real components, no new mocks/provider
  doubles or replacement transports. Sandbox-MCP builds/tests only in Docker.
- Run gateway kernel tests in disposable networkless containers, then all
  module suites and UI production build. Verify real Docker CLI exec, MCP
  discovery, streaming/TTY, denied non-sandbox/root/lifecycle requests and
  sandbox rejection from operator endpoints on an isolated stack.
- Preserve fail-closed startup and shutdown; malformed/unavailable policy,
  untrusted peer/origin, unknown exec ID, canceled wait and stale artifacts
  must fail. Check operator REST/WebSocket/desktop and research approval retry.
- Update audit/QC/security docs with actual results; do not relabel historical
  failures as passes or claim a paid agent run that was not performed.

## Review focus

- Docker path decoding, request size/fields, user/capability overrides, stream
  hijacking and exec-ID provenance across restart.
- Untrusted Origin with JSON/text/plain; forwarded-header spoofing; operator
  host ports; sandbox access via direct IP and Docker service names.
- DNS approval before DoH, all destination IP verdicts, redirects and rebinds;
  structured error authenticity and pinned-IP retry.
- Established TCP/UDP, DNS and log limiter saturation under lockdown; IPv6.
- Asynchronous status mutation, hidden/stale regions, canceled waits and
  network-idle; missing/mismatched executions and screenshot overwrites.
