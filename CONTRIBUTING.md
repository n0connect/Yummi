# Contributing to Yummi

Thanks for your interest in Yummi. It's a privacy-first, fail-closed AI
pentest conductor, and the contribution rules below exist mostly to protect
the one property everything else depends on: **the network trust boundary
fails closed.** Read [The prime directive](#the-prime-directive) before your
first change.

## Table of contents

- [The prime directive](#the-prime-directive)
- [Repository layout — three Go modules, one Rust crate, one SPA](#repository-layout)
- [Getting set up](#getting-set-up)
- [Build, test, lint](#build-test-lint)
- [Running a single test](#running-a-single-test)
- [The security invariants you must not break](#security-invariants)
- [Commit and PR conventions](#commit-and-pr-conventions)
- [Reporting security issues](#reporting-security-issues)

## The prime directive <a id="the-prime-directive"></a>

The Rust `gateway` installs **DROP-all** nftables rules before its IPC socket
even opens. Forwarded sandbox egress starts in `block_all`, which ignores
allow rules until the operator changes mode. Restricted modes also need
matching scope/approval; configured blocks retain precedence. Local/same-subnet
traffic is outside this FORWARD-chain boundary. If the gateway crashes, those rules persist. **Any change that
touches scope checking, egress logic, or nftables rule-install order must
preserve "deny until explicitly allowed."** When in doubt, fail closed.

The companion rule: **agent-controlled tools execute inside the sandbox.**
The agent enqueues work into the execution store, which the executor routes
through gateway checks to `sandbox.Bridge.Exec`, or to the sandbox-resident
MCP server through the persistent `sandboxmcp.Client` Docker exec transport.
The operator terminal also uses Docker exec as UID/GID `1000:1000`. Do not introduce a host-side command path
reachable from agent/LLM output. This is enforced in CI by AST-walking
fitness-function tests (see [below](#security-invariants)), not just by
convention — a violating change fails the build.

## Repository layout — three Go modules, one Rust crate, one SPA <a id="repository-layout"></a>

The single most common way to trip up here is treating the repo as one Go
module. It is **three separate Go modules** plus a Rust crate and a React app:

| Path | Module / stack | What lives here |
|------|----------------|-----------------|
| `./` (root) | Go module `github.com/n0connect/yummi` | CLI entrypoint only (`cmd/yummi`, `internal/cli`, `internal/config`, `internal/version`) |
| `orchestrator/` | Go module `.../orchestrator` | Everything AI/API/execution: conductor, agent engine, REST/WS API, gateway IPC client, execution store |
| `sandbox-mcp/` | Go module `.../sandbox-mcp` | The MCP server that runs *inside* the Kali container; compiled into `sandbox/Dockerfile` |
| `gateway/` | Rust crate `yummi-gateway` | The fail-closed nftables trust boundary (Linux/nftables only) |
| `ui/` | React 18 + Vite + TypeScript | The single operator web UI |

> Root `go test ./...` covers only the root module. Use
> `make _test-orchestrator` for the separate orchestrator module.
> **Sandbox-MCP builds/tests must run through Docker**: use
> `make _test-sandbox-mcp`, never host `go test` in `sandbox-mcp/`.
> Its real tools, tmux, and Chromium require the installed Linux environment
> and UID 1000. New tests must use real components, without fake providers,
> injected resolvers, replacement transports, or mock execution components.

## Getting set up <a id="getting-set-up"></a>

```bash
make conf      # engine/network, language, model; currently selects eino
make install   # bootstrap the dev environment
make start     # build missing images + run; UI at http://localhost:3000
make start BUILD=1 # rebuild all five images after source/config changes
```

Native development needs Go 1.27.2 (all three modules), a C compiler for
orchestrator cgo, Rust/Cargo, and Node 22. Container startup alone needs
a running Linux container engine with Compose, Make/Bash, curl, and the
selected model backend. Several legacy scripts and the CLI still hardcode
Docker even when Make selects Podman; read the installation caveats.
Prior macOS checks resolved an observed Docker Desktop forwarding failure
by switching to OrbStack. See the historical engine notes in
[docs/installation.md](docs/installation.md#docker-desktop-internal-network-bug);
compatibility still depends on the current engine and configuration.

## Build, test, lint <a id="build-test-lint"></a>

Run the relevant checks before opening a PR. The aggregate test target includes more suites than the regular CI matrix; CI also adds UI lint and gateway Clippy/audit:

```bash
make _build-cli _build-orchestrator _build-gateway _build-ui
make docker-build # runtime images, including Docker-built sandbox MCP
make test      # root/orchestrator Go, Rust, UI, isolated sandbox MCP, optional brain, archived QC
make lint      # golangci-lint (root + orchestrator + sandbox-mcp)
make vuln      # govulncheck on all three Go modules
```

New tests must exercise real components; retain the browser environment
shims required for jsdom/xterm. Existing local-server fixtures in unit tests
are not evidence of live model or gateway enforcement. The opt-in
`test-agent-*` targets require the real model and a live sandbox. Use
`make test-security-boundaries` for disposable real packet/research/control
checks, and report exactly which suites ran. Do not claim a live deployment
is verified from source checks or unit tests alone.

`make build` currently also compiles sandbox-MCP on the host, contrary to
the mandatory Docker placement rule. Use the native component targets and
Docker build/test targets above until that aggregate recipe is corrected.

## Running a single test <a id="running-a-single-test"></a>

```bash
# orchestrator module — must cd (separate go.mod)
(cd orchestrator && go test ./internal/agent -run '^TestRunnerE2E_ApprovalGate$')
(cd orchestrator && go test -race ./...)

# root module
go test ./internal/cli -run '^TestParseTopLevelOptionsDefaultUI$'

# gateway (Rust)
(cd gateway && cargo test test_allow_in_scope)

# UI
(cd ui && npm test)
```

## The security invariants you must not break <a id="security-invariants"></a>

`orchestrator/internal/security/invariants` holds **executable architectural
fitness functions** — Go tests that parse the source with `go/parser` and
fail if an invariant is violated. Notably:

- The agent packages must never import `os/exec` (single-exec-path).
- `internal/sandboxmcp` may only ever `exec` the literal `"docker"` binary.

If your change legitimately needs to touch these, update the invariant test
*and explain why in the PR* — don't route around it. A green invariant suite
is a load-bearing part of this project's security story.

## Commit and PR conventions <a id="commit-and-pr-conventions"></a>

- **[Conventional Commits](https://www.conventionalcommits.org/)**:
  `feat:`, `fix:`, `test:`, `docs:`, `chore:`, `build:`, `refactor:`.
  Releases and the `CHANGELOG` are generated from these by `release-please`,
  so the prefix matters.
- Keep changes on the correct side of the **conductor / executor boundary**:
  the conductor knows about the LLM, personas, proposals, and risk; the
  executor knows about the gateway, sandbox, and execution store. Neither
  should learn about the other's concerns.
- User-facing strings are **bilingual** (English/Turkish) and go through the
  UI i18n dictionary (`ui/src/shared/i18n/strings.ts`) or the conductor's
  `likelyTurkish()` path — don't inline a single-language literal.
- PRs should report relevant build, test, and lint results; include skips and known failures.
- Do not add AI co-author trailers to commits.

## Reporting security issues <a id="reporting-security-issues"></a>

**Do not open a public issue for a security vulnerability.** See
[SECURITY.md](SECURITY.md) for private disclosure.
