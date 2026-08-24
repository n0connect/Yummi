← [Back to README](../README.md) · [Docs index](README.md)

# Development

This page assumes you've already completed [Installation](installation.md) — a working Go ≥1.24 toolchain with a C compiler, Rust via rustup, and Node 20/22. It covers the fast, granular workflows for iterating on one piece of Yummi without rebuilding everything.

## Table of contents

- [Two Go modules — the mistake almost everyone makes once](#two-go-modules)
- [Fast rebuild after a source change](#rebase)
- [Fast feedback loops per component](#fast-loops)
- [Running a single test](#single-test)
- [Linting and vulnerability scanning](#lint-vuln)
- [Hot-reload dev mode](#hot-reload)
- [The dev harnesses (`cmd/test_*`)](#dev-harnesses)

---

## Two Go modules — the mistake almost everyone makes once <a id="two-go-modules"></a>

This is worth repeating from [Architecture](architecture.md#build-ecosystems) because it causes real confusion the first time: `go.mod` at the repo root and `orchestrator/go.mod` are **two independent Go modules**. Running `go test ./...` from the repo root only ever exercises the CLI module — every orchestrator test is silently skipped, not failed, so there's no error to tip you off. If you're touching anything under `orchestrator/`, you must `cd orchestrator` (or prefix each command with `cd orchestrator &&`) first:

```bash
# WRONG — silently skips every orchestrator_test.go
go test ./...

# RIGHT
cd orchestrator && go test ./...
```

`make test` (and its granular pieces below) already get this right — it's specifically ad-hoc `go test`/`go build`/`go vet` invocations outside the Makefile where this bites people.

---

## Fast rebuild after a source change <a id="rebase"></a>

```bash
make rebase
```

The habitual `make stop && make close && make clean && make build && make start` wastes real time: `make build` compiles native binaries to `./bin` and `ui/dist` that the Docker images never consume at all, and neither `clean` nor `close` touch Docker's own build cache, images, or volumes. `make rebase` is exactly `down`, `build`, `up` — Docker's own layer cache does the real work (the `go mod download`/`npm install` layers are skipped when `go.sum`/`package-lock.json` haven't changed), so a source-only change rebuilds in seconds instead of minutes. It never prunes Docker's cache/images/volumes and never touches Ollama — reach for `make clean` or `docker system prune` when you actually need a from-scratch rebuild.

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

`dev-orchestrator` is useful for API and prompt iteration, but it does not start a sandbox or set an Ollama URL. Export `YUMMI_OLLAMA_URL=http://127.0.0.1:11434` when model calls are needed. Any code path that executes a pentest command still needs the supporting sandbox/gateway/proxy stack.

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

Table-driven Go tests are common (for example `validator_test.go`), with map-backed stores or in-memory SQLite where appropriate. Scripted clients and local test servers isolate external/nondeterministic boundaries such as Ollama, HTTP, and gateway IPC; integration/live tests retain the real execution path. `ui/src/test/setup.ts` only supplies missing jsdom browser APIs (`scrollIntoView`, `ResizeObserver`). Use `-race` for concurrency-sensitive Go packages such as the graph, gate, and execution store.

`make test-agent-live` deserves a special mention: it enables the real-model/real-sandbox test with `LIVE_TEST_MODEL` (default `qwen3:8b`). Start the stack first; missing Ollama, model, or sandbox dependencies are test failures once `YUMMI_LIVE=1` is enabled by the Make target:

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

`make lint` running twice is not a typo — it's the same two-module split as above: `golangci-lint` operates per Go module, so one invocation at the repo root lints the CLI module and a second, separate invocation (`cd orchestrator && golangci-lint run`) lints the orchestrator module. There is only **one** `.golangci.yml` in the repository (at the root) — `golangci-lint` discovers it by walking up from the current directory, so both passes read the same config; you don't need a second copy inside `orchestrator/` for shared rules to apply there too.

---

## Hot-reload dev mode <a id="hot-reload"></a>

```bash
make dev
```

Runs `scripts/dev.sh`, which bootstraps (`scripts/install.sh`) and brings up just the `sandbox` container in the background — intended to be paired with `make dev-orchestrator` (running the Go server directly on your host with fast recompiles) rather than the full `make start` stack, since you want the sandbox real but the orchestrator itself iterating quickly outside a rebuild-the-image loop.

---

## The dev harnesses (`cmd/test_*`) <a id="dev-harnesses"></a>

`orchestrator/cmd/test_agent`, `cmd/test_engine`, `cmd/test_loop`, `cmd/test_openai_backend`, and `cmd/context_probe` are small, runnable Go programs (not `_test.go` files — genuine `main` packages) for driving specific layers interactively against real dependencies while developing:

```bash
cd orchestrator
go run ./cmd/test_engine          # exercise the conductor engine directly
go run ./cmd/test_loop            # exercise the agent's graph loop directly
go run ./cmd/test_agent           # exercise the full agent Runner (graph backend)
go run ./cmd/test_openai_backend  # scripted multi-part-goal scenarios against the Eino backend + a real OpenAI-compatible model
```

`test_openai_backend` takes `EINO_STEP_BUDGET` and `SCENARIO_ID` env filters to reproduce one scenario in isolation, and reuses `eino.NewOpenAIChatClient` — the same client the orchestrator itself uses when `YUMMI_EINO_MODEL_BASE_URL` points the Eino backend at a non-Ollama server.

`context_probe` is a different kind of tool: a transparent reverse proxy in front of an OpenAI-compatible chat-completions server, not a scripted test.

```bash
go run ./cmd/context_probe -listen :8899 -target http://localhost:1234
```

Point `YUMMI_EINO_MODEL_BASE_URL` (or the harness's own model-base-URL flag) at the probe's `-listen` address instead of the real server, and it logs every `/chat/completions` turn — message/tool counts, the **server's own** `usage.prompt_tokens`/`completion_tokens`/`total_tokens` (not an estimate), `finish_reason`, and whether `tool_calls` fired — the only way to actually measure context growth turn-by-turn in a long Eino session instead of guessing whether it's building up. `-dump-dir` additionally saves each turn's full raw request/response JSON, useful for inspecting a turn whose content came back empty or malformed.

All five compile to `orchestrator/test_agent`, `orchestrator/test_engine`, `orchestrator/test_loop`, `orchestrator/test_openai_backend`, `orchestrator/context_probe` if built in place — already covered by `.gitignore`, so building one locally won't accidentally get committed.

## Agent quality gates

`go test ./...` includes two model-independent quality contracts: `TestPersonaGoldenFixtures` reads every checked-in `persona/v0.1/golden_tests/*.yaml`, and offline tests exercise `EvaluateTaskQuality`. The evaluator rejects cosmetic completion, repeated *executions* (blocked proposals are tracked separately), failed plans, empty summaries, missing evidence digests, unsupported finding claims, wrong command families, and scenario-specific failure markers. The live suite loads the same skill registry and runs as the same UID `1000:1000` as the production sandbox bridge. The full metric contract and scenario list live in [Agent Quality & Evaluation](agent/quality-and-evaluation.md). For real-model regression testing against the sandbox, run:

```bash
make test-agent-eval LIVE_TEST_MODEL=qwen3:14b
```

To reproduce one failing scenario while iterating, set `YUMMI_EVAL_CASE` to its ID, for example `E5-largest`.

The live suite is assertion-bearing: plan failure, non-termination, missing successful work, command repetition, or threshold violations fail the test after the transcript is printed.
