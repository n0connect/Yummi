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
even opens; the sandbox has zero external connectivity until an explicit allow
rule lands. If the gateway crashes, those rules persist. **Any change that
touches scope checking, egress logic, or nftables rule-install order must
preserve "deny until explicitly allowed."** When in doubt, fail closed.

The companion rule: **pentest commands reach a shell through exactly one
path.** The agent never calls `exec.Command` itself — it enqueues into the
execution store, which the executor drains into `sandbox.Bridge.Exec`
(`docker exec` into the container). Do not introduce a host-side command path
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

> Tests under `orchestrator/internal/*` and `sandbox-mcp/` **only run from
> inside their own module directory.** `go test ./...` from the repo root
> silently skips them — they're different modules. Always `cd` first, or use
> the `make _test-*` targets, which do it for you.

## Getting set up <a id="getting-set-up"></a>

```bash
make conf      # one-time interactive setup (container engine, network mode, language, agent backend, model)
make install   # bootstrap the dev environment
make start     # build + run the full stack; UI at http://localhost:3000
```

You need Docker (Compose v2) or Podman, a Go toolchain, a Rust toolchain, and
Node. On macOS, **[OrbStack](https://orbstack.dev/) is the recommended
container engine** — Docker Desktop has a known VM-networking bug documented
in [docs/installation.md](docs/installation.md#docker-desktop-internal-network-bug).

## Build, test, lint <a id="build-test-lint"></a>

Before opening a PR, these must all pass — CI runs the same set:

```bash
make build     # CLI + orchestrator + gateway + UI
make test      # all four test suites (root Go, orchestrator Go, gateway Rust, UI vitest, sandbox-mcp Go)
make lint      # golangci-lint (root + orchestrator)
make vuln      # govulncheck on both Go modules
```

There are **no mocks of Yummi's own components** — tests exercise real
stores, engines, and (for the `test-agent-*` targets) the real model and a
live sandbox container. Please keep it that way: the only acceptable
"mock"-named files are third-party/browser environment shims the test runners
require (e.g. jsdom stubs for `xterm.js`).

## Running a single test <a id="running-a-single-test"></a>

```bash
# orchestrator module — must cd (separate go.mod)
cd orchestrator && go test ./internal/agent -run TestRunnerHappyPath
cd orchestrator && go test -race ./...

# root module
go test ./internal/cli -run TestDiskFree

# gateway (Rust)
cd gateway && cargo test policy::tests::test_expired_rule_cleanup

# UI
cd ui && npm test
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
- PRs should arrive with `make test` and `make lint` green.

## Reporting security issues <a id="reporting-security-issues"></a>

**Do not open a public issue for a security vulnerability.** See
[SECURITY.md](SECURITY.md) for private disclosure.
