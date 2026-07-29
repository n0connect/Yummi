← [Back to README](../README.md) · [Docs index](README.md)

# Development

This page assumes you've already completed [Installation](installation.md) — a working Go ≥1.24 toolchain with a C compiler, Rust via rustup, and Node 20/22. It covers the fast, granular workflows for iterating on one piece of Yummi without rebuilding everything.

## Table of contents

- [Two Go modules — the mistake almost everyone makes once](#two-go-modules)
- [Fast feedback loops per component](#fast-loops)
- [Running a single test](#single-test)
- [Linting and vulnerability scanning](#lint-vuln)
- [Hot-reload dev mode](#hot-reload)
- [The dev harnesses (`cmd/test_*`)](#dev-harnesses)

---

## Two Go modules — the mistake almost everyone makes once <a id="two-go-modules"></a>

This is worth repeating from [Architecture](architecture.md#build-ecosystems) because it causes real confusion the first time: `go.mod` at the repo root and `orchestrator/go.mod` are **two independent Go modules**. Running `go test ./...` from the repo root only ever exercises the CLI/TUI module — every orchestrator test is silently skipped, not failed, so there's no error to tip you off. If you're touching anything under `orchestrator/`, you must `cd orchestrator` (or prefix each command with `cd orchestrator &&`) first:

```bash
# WRONG — silently skips every orchestrator_test.go
go test ./...

# RIGHT
cd orchestrator && go test ./...
```

`make test` (and its granular pieces below) already get this right — it's specifically ad-hoc `go test`/`go build`/`go vet` invocations outside the Makefile where this bites people.

---

## Fast feedback loops per component <a id="fast-loops"></a>

```bash
make _build-cli           # just ./bin/yummi
make _build-orchestrator  # just ./bin/yummi-orchestrator (needs cgo — see Installation)
make _build-gateway       # just gateway/target/release/yummi-gateway
make _build-ui            # just ui/dist via vite build
```

```bash
make dev-orchestrator      # cd orchestrator && go run ./cmd/orchestrator — no Docker, no sandbox
make dev-ui                # cd ui && npm run dev — Vite HMR dev server on :3000
```

`dev-orchestrator` is genuinely useful for iterating on API handlers, the conductor, or the agent's prompt logic with fast recompiles — but remember it has **no sandbox to `docker exec` into**, so any code path that actually runs a pentest command will fail. It's for everything *except* verifying real command execution. For that, you need the full `make start` stack.

`dev-ui`'s Vite server proxies `/api` and `/ws` to `http://127.0.0.1:9080` (`ui/vite.config.ts`), so pairing it with `dev-orchestrator` running on that port gives you HMR on the frontend against a real, fast-iterating backend — no Docker rebuild for either side while you're touching UI + API code together.

---

## Running a single test <a id="single-test"></a>

```bash
# orchestrator module — must cd or prefix, it's a separate go.mod (see above)
cd orchestrator && go test ./internal/agent -run TestRunnerE2E_ApprovalGate
cd orchestrator && go test ./internal/conductor -run TestValidatorApprovalRequired
cd orchestrator && go test -race ./...

# root module
go test ./internal/cli -run TestParseTopLevelOptionsDefaultUI

# gateway (Rust) — cargo test takes a name filter, not a fully-qualified path
cd gateway && cargo test test_allow_in_scope
```

Table-driven Go tests are the norm (e.g. `validator_test.go`), against real in-memory SQLite (`":memory:"`) fixtures — **this project has a strict, permanent, project-wide ban on mocking its own components.** The one file in the repository with "mock" in its vocabulary is `ui/src/test/setup.ts`, and even that isn't a mock of Yummi's own code — it's two jsdom environment stubs (`scrollIntoView`, `ResizeObserver`) for browser APIs jsdom doesn't implement, so components that call them don't throw during a test. Its own comment goes out of its way to state the boundary explicitly: *"No mocks allowed. `EventSource` tests must run against real E2E environment or be designed without mocks"* — i.e. `EventSource` (the SSE client the Logs tab uses) is deliberately left unstubbed, not stubbed. `-race` is used for concurrency-sensitive Go packages (the agent's graph state machine, the gate, the execution store).

`make test-agent-live` deserves a special mention: it drives a **real** `qwen3:8b` model against a **real, running** sandbox container — it's an actual end-to-end test, not a simulation, and it self-skips if the sandbox isn't already up (`make start` first):

```bash
make test-agent-live
```

The `cmd/test_agent`, `cmd/test_engine`, and `cmd/test_loop` binaries under `orchestrator/cmd/` are dev harnesses in the same spirit — they drive the real agent/conductor/loop against a real backend for manual exploration, not automated mocks.

---

## Linting and vulnerability scanning <a id="lint-vuln"></a>

```bash
make lint    # golangci-lint run — TWICE: once at the repo root, once `cd orchestrator`
make vuln    # govulncheck — supply-chain scan, both Go modules
```

`make lint` running twice is not a typo — it's the same two-module split as above: `golangci-lint` operates per Go module, so one invocation at the repo root lints the CLI/TUI module and a second, separate invocation (`cd orchestrator && golangci-lint run`) lints the orchestrator module. There is only **one** `.golangci.yml` in the repository (at the root) — `golangci-lint` discovers it by walking up from the current directory, so both passes read the same config; you don't need a second copy inside `orchestrator/` for shared rules to apply there too.

---

## Hot-reload dev mode <a id="hot-reload"></a>

```bash
make dev
```

Runs `scripts/dev.sh`, which bootstraps (`scripts/install.sh`) and brings up just the `sandbox` container in the background — intended to be paired with `make dev-orchestrator` (running the Go server directly on your host with fast recompiles) rather than the full `make start` stack, since you want the sandbox real but the orchestrator itself iterating quickly outside a rebuild-the-image loop.

---

## The dev harnesses (`cmd/test_*`) <a id="dev-harnesses"></a>

`orchestrator/cmd/test_agent`, `cmd/test_engine`, and `cmd/test_loop` are small, runnable Go programs (not `_test.go` files — genuine `main` packages) for driving specific layers interactively against real dependencies while developing:

```bash
cd orchestrator
go run ./cmd/test_engine   # exercise the conductor engine directly
go run ./cmd/test_loop     # exercise the agent's graph loop directly
go run ./cmd/test_agent    # exercise the full agent Runner
```

These compile to `orchestrator/test_agent`, `orchestrator/test_engine`, `orchestrator/test_loop` if built in place — already covered by `.gitignore`, so building one locally won't accidentally get committed.
