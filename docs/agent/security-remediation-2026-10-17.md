# Security remediation verification — 2026-10-17

The concrete gateway, Docker control, operator API, browser and QC findings in
[the independent audit](audit-2026-10-15.md) have source fixes. This follow-up
records the additional compatibility and approval fixes and their verification
limits. Source changes and isolated verification do not establish deployment to
the original live stack.

## Follow-up fixes

- Keep `yummi_control` internal. Add a normal, UI-only ingress bridge so Docker
  can publish loopback ports 3000/9080. nginx accepts the exact observed host
  peer `172.22.0.1`; sandbox and broker never join ingress.
- Accept the production Debian Docker CLI's `Detach:false` exec-create field.
  Detached creation, root/privileged execution, other containers and lifecycle
  operations remain rejected by the constrained broker.
- Reject duplicate/case-aliased structured approval fields and reserved,
  documentation, benchmark, shared and NAT64 research addresses.
- Publish exact pending MCP arguments and an explicit command/threat approval
  kind in both graph and Eino. Clear that ephemeral authority on resolution or
  recovery. QC rechecks task/session/proposal identity and exact arguments.
- Freeze normalized graph MCP arguments before approval, including tools
  inferred from local-model shell guesses. Approved calls use the same MCP
  dispatch helper as automatic calls. Display labels never become shell code.
  Tool output can still route to the threat quarantine after command approval.
- Require ordered, task-bound browser evidence, fully decode bounded Chromium
  PNGs, reject reversed release claims, and require explicit original-source
  fixed-release evidence. Historical failures stay failed or unverified.
- Provide optional Make targets for real kernel, research and operator boundary
  checks. Reuse an existing Kali tool base; rebuild the current MCP binary and
  tests in Docker. A missing tool base is built explicitly by the prerequisite.

## Completed verification

| Check | Observed result |
| --- | --- |
| CLI, orchestrator and Rust module suites | Passed; gateway has 107 unit tests. |
| Final graph/Eino/invariant package rerun | Passed after the postapproval dispatch fix. |
| Race checks | Agent, Eino, broker, executor and controlaccess passed. |
| UI | 103 tests in 23 files; TypeScript/Vite production build passed. |
| Brain | 21 tests passed on the configured Python 3.14 environment. |
| Offline QC | 16 archived-evidence/authorization/PNG regressions passed. |
| Sandbox MCP | 88 top-level tests passed in isolated networkless Kali, including real Chromium and tmux. No host sandbox-MCP test execution. |
| Real kernel | IPv4/IPv6 mode matrix, burst DROP, DNS lockdown and approval-dependent established-flow revocation passed. |
| Research approval | Six real gateway/policy/SDK/installed-MCP/store scenarios passed. The full executor run had 21 passes and two existing daemon-dependent skips. |
| Operator/control boundary | 25 fresh checks passed on 17 October with rebuilt orchestrator/UI/broker images. Valid localhost Host plus spoofed forwarding headers still yielded sandbox 403; policy reads, host REST/WS and actual UID1000 broker execution behaved as expected. Real Chromium sidebar and toggle-fault scripts passed. Disposable resources were removed. |
| Graph approved MCP regression | Real store/executor tests passed for direct and inferred calls, frozen arguments and unavailable-client failure routing. No replacement MCP executor or model was introduced. |
| Installed graph approval integration | Passed on 17 October using existing images and freshly compiled current-source test binaries. Actual UID1000 MCP browser-session status and tmux calls completed after operator approval; injection-bearing output entered a separate threat hold with no command authorization metadata. |

The research harness uses a real gateway rule and retries after approval, then
gets an actual network error under `--network none`. It does not prove successful
external fetching. Kernel revocation applies to gateway-forwarded traffic in
restricted modes where no other allow matches; local/same-subnet policy traffic
and the operator-selected `allow_all` behavior are separate.

Existing Vite chunk-size, jsdom/React and LangGraph deprecation warnings remain.
No fresh paid-model agent scenario was run. The corrected historical record is
[documented separately](web-research-qc.md).

## Final integration status

A Linux-only graph integration test passed against the actual installed UID1000
MCP through operator approval, browser-session status, real tmux execution and
output threat quarantine. All six research approval subcases also passed again
on current source. These selected tests ran without skips.

For this final run, current-source test binaries were compiled in the existing
`yummi/orchestrator-tested:local` image with networking disabled. They and the
current entrypoint were copied into a disposable container of the existing
`yummi/research-integration:local` image. No images were rebuilt or pulled. The
runtime had `--network none`, no mounts, all capabilities dropped except the
operator gateway/UID-transition requirements, and an installed UID1000 MCP child.
The image IDs, source hashes, exit status and transcript hash were recorded;
only the two owned test containers were removed afterward.

The attempted clean Kali tool-base build filled the available disk; a subsequent
host verification attempt failed with `no space left on device`. Only this
task's generated temporary build outputs were removed, and the host graph/Eino/
invariant rerun then passed. Docker/OrbStack was observed stopped with its socket
absent. With explicit operator approval, OrbStack was then started and the final
tests above completed using existing images. No broad cache/data pruning was
performed. The earlier environment failures are not counted as test passes.

The original live services automatically started when OrbStack opened; they were
not rebuilt, recreated or reconfigured. They still include the old unrestricted
`tecnativa/docker-socket-proxy` container and original orchestrator/UI image IDs.
The corrected source is therefore **not deployed to that live stack**. Fresh
deployment and external research require separate verification.

## Reproduction

```sh
make test
make test-gateway-kernel
make test-research-integration
make test-control-boundary
# Or all three optional boundary targets:
make test-security-boundaries
```

The control runner requires a local Docker Unix socket and unused
172.21/22/23.0.0/24 networks. The kernel container is disposable, networkless and
privileged solely for network-namespace setup. The research container has no host
mounts, socket, credentials or external network. See [QA placement and evidence
contracts](../../qa/README.md).
