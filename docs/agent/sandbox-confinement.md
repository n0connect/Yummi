# Sandbox execution confinement

## Root cause

The browser driver previously searched for a system browser when
`/usr/bin/chromium` was missing. The live browser test accepted any browser
found by Rod, and `_test-sandbox-mcp` ran `go test` directly on the developer
machine. Together these allowed a test to launch the developer's Chrome,
write its agent profile, and probe the developer's localhost CDP endpoint.
The runtime MCP client still dispatches through `docker exec --user 1000:1000`;
the source audit did not find a new host research HTTP client.

## Correction

`requireSandboxRuntime` executes before MCP startup, browser discovery/profile
access/CDP probing, tool subprocess capture, and tmux invocation. It requires
Linux, UID 1000, the Docker `/.dockerenv` marker, and the installed regular,
non-group/world-writable `/usr/local/bin/yummi-sandbox-mcp` binary. There is no
environment override. Browser discovery is restricted to `/usr/bin/chromium`.

These checks prevent accidental execution placement. They are not cryptographic
attestation and do not protect against a malicious container administrator.
Gateway network policy remains the network enforcement authority.

## Real validation in Docker

`sandbox-mcp/Dockerfile.test` compiles the test binary in a Go builder container
and runs the rejection tests there. It then copies that binary into the real
Kali sandbox image. The test container runs as UID 1000 with `--network none`,
all capabilities dropped, a read-only root filesystem, temporary writable
`/tmp` and `/home/pentester`, no host bind mounts and no Docker socket. Real
Chromium runs headless; real tmux runs inside the container. No fake browser,
provider, resolver, or transport is added.

The test image removes nmap's file capabilities and puts `/usr/lib/nmap` first
on PATH so the real executable selects unprivileged connect scanning instead
of Kali's wrapper forcing `--privileged`. It can execute under `--cap-drop ALL`.
The normal engagement image is unaffected. The separate `boundary-audit`
Docker build target runs the existing orchestrator architecture invariant
suite with networking disabled.

The Make target `_test-sandbox-mcp` uses this Docker workflow. A local
`yummi/sandbox:local` image is required. The normal running engagement sandbox
is not modified or restarted by these tests.

## Web research state

The operator subsequently authorized the sandbox-only implementation.
`web_search` and `web_fetch` are registered in sandbox-MCP; their HTTP and DoH
clients execute only there. See [web research](web-research.md) for current
configuration and limits. The local content heuristics are not equivalent to
`threatscan.ScopeStrict` and are not complete injection protection. Research
execution and its tests remain inside Docker.

Live agent QC also exposed a pre-existing lifetime bug: the persistent MCP
Docker transport used the first execution's cancellable context. Completing
that execution killed the MCP process and lost Chromium pages/refs. The
transport now outlives individual calls and is closed by Client.Close during
orchestrator shutdown. A real Docker/MCP/Chromium regression first reproduced
the loss, then passed with page state and refs preserved.

## Scope of repair

No host browser, process, settings, cache or profile is inspected or cleaned by
this repair. Repository source is read from a read-only container mount and
edits are confined to the repository. Existing operator-side CLI lifecycle
and UI launch functionality is outside this agent-execution repair.

## Verification — 2026-10-13

- Docker builder: both confinement regression tests passed; production MCP
  binary rejected startup outside the required sandbox runtime.
- Networkless Kali test container: complete sandbox-MCP suite passed, including
  real Chromium DOM interaction, real tmux sessions, and real nmap execution.
- Docker `boundary-audit` target: all seven existing orchestrator architectural
  invariant tests passed with network disabled.
- Repository `git diff --check`, executed inside Docker, passed.

The corrected sandbox and orchestrator images have now been rebuilt and
deployed through Docker-contained `make start`. No host-native browser or test
runner was launched. The operator's manually selected egress mode was preserved
during subsequent orchestrator-only rebuilds.
