<- [Back to README](../README.md) · [Docs index](README.md)

# Development

This page assumes you've already completed [Installation](installation.md) - a working Go >= 1.27.2 toolchain with a C compiler, Rust/Cargo, and Node 22. It covers the fast, granular workflows for iterating on one piece of Yummi without rebuilding everything.

## Table of contents

- [Three Go modules — the mistake almost everyone makes once](#two-go-modules)
- [Fast rebuild after a source change](#rebase)
- [Fast feedback loops per component](#fast-loops)
- [Running a single test](#single-test)
- [Linting and vulnerability scanning](#lint-vuln)
- [Development bootstrap](#hot-reload)
- [The dev harnesses (`cmd/test_*`)](#dev-harnesses)

---

## Three Go modules — the mistake almost everyone makes once <a id="two-go-modules"></a>

This is worth repeating from [Architecture](architecture.md#build-ecosystems) because it causes real confusion the first time: `go.mod` at the repo root, `orchestrator/go.mod`, and `sandbox-mcp/go.mod` are **three independent Go modules**. Running `go test ./...` from the repo root only ever exercises the CLI module — every orchestrator and sandbox-mcp test is silently skipped, not failed, so there's no error to tip you off.

> [!IMPORTANT]
> **Mandatory sandbox execution placement**: Never run `go test` directly inside `sandbox-mcp` on the developer host machine. Live sandbox-MCP tests launch real tools, tmux sessions, and headless Chromium, which require Linux Docker, UID 1000, and the container runtime. Always use `make _test-sandbox-mcp` to build `sandbox-mcp/Dockerfile.test` and run real tests inside an isolated Kali container.

```bash
# WRONG — silently skips every orchestrator_test.go and sandbox-mcp test
go test ./...

# RIGHT
go test ./...                     # CLI (root module)
(cd orchestrator && go test ./...) # Orchestrator unit tests
make _test-sandbox-mcp            # Sandbox-MCP tests (inside isolated Kali container)
```

`make test` (and its granular pieces below) already get this right — it's specifically ad-hoc `go test`/`go build`/`go vet` invocations outside the Makefile where this bites people.

---

## Fast rebuild after a source change <a id="rebase"></a>

```bash
make rebase
```

The habitual `make stop && make close && make clean && make build && make start` wastes real time: `make build` compiles native binaries to `./bin` and `ui/dist` that the Docker images never consume at all, and neither `clean` nor `close` touch Docker's own build cache, images, or volumes. `make rebase` checks dependencies and ensures the model backend, then runs `down`, `build`, `up` — Docker's own layer cache does the real work (the `go mod download`/`npm install` layers are skipped when `go.sum`/`package-lock.json` haven't changed), so unchanged dependency layers can be reused. It retains Docker cache/images/volumes and may start Ollama or pull the selected model. For source edits without taking the stack down first, use `make start BUILD=1`; ordinary `make start` skips builds when all five local images exist. `make clean` removes native outputs, repository `*.db` files, and brain virtualenv/checkpoints; it does not clear Docker caches or volumes.

---

## Fast feedback loops per component <a id="fast-loops"></a>

```bash
make _build-cli           # just ./bin/yummi
make _build-orchestrator  # just ./bin/yummi-orchestrator (needs cgo — see Installation)
make _build-gateway       # just gateway/target/release/yummi-gateway
make _build-ui            # just ui/dist via vite build
```

```bash
make dev-orchestrator      # local Go API; does not start supporting containers
make dev-ui                # cd ui && npm run dev — Vite HMR dev server on :3000
```

`dev-orchestrator` is useful for API and prompt iteration, but it does not start a sandbox or set an Ollama URL. Export `YUMMI_OLLAMA_URL=http://127.0.0.1:11434` when model calls are needed. Any code path that executes a pentest command still needs the supporting sandbox/gateway/proxy stack.

`dev-ui`'s Vite server proxies `/api` and `/ws` to `http://127.0.0.1:9080` (`ui/vite.config.ts`), so pairing it with `dev-orchestrator` running on that port gives you HMR on the frontend against a real, fast-iterating backend — no Docker rebuild for either side while you're touching UI + API code together.

---

## Running a single test <a id="single-test"></a>

```bash
# orchestrator module — must cd or prefix, it's a separate go.mod (see above)
(cd orchestrator && go test ./internal/agent -run '^TestRunnerE2E_ApprovalGate$')
(cd orchestrator && go test ./internal/conductor -run '^TestValidatorApprovalRequired$')
(cd orchestrator && go test -race ./...)

# root module
go test ./internal/cli -run '^TestParseTopLevelOptionsDefaultUI$'

# gateway (Rust) — substring filter matches the checked-in test
(cd gateway && cargo test test_allow_in_scope)

# sandbox-mcp module — executes inside isolated Kali container
make _test-sandbox-mcp
```

Use real components and in-memory SQLite where appropriate. New tests must not introduce mock components, fake providers, injected resolvers, or replacement transports. Existing unit suites also contain scripted/local-server fixtures; their presence is not permission to add new replacements for the real execution boundary. `ui/src/test/setup.ts` only supplies missing jsdom browser APIs (`scrollIntoView`, `ResizeObserver`). Use `-race` for concurrency-sensitive Go packages such as the graph, gate, and execution store.

`make test-agent-live` deserves a special mention: it enables the real-model/real-sandbox test with `LIVE_TEST_MODEL` (default `qwen3:8b`). Start the stack first; missing Ollama, model, or sandbox dependencies are test failures once `YUMMI_LIVE=1` is enabled by the Make target:

```bash
make test-agent-live
```

The `cmd/test_agent`, `cmd/test_engine`, and `cmd/test_loop` binaries under `orchestrator/cmd/` are dev harnesses in the same spirit — they drive the real agent/conductor/loop against a real backend for manual exploration, not automated mocks.

---

## Linting and vulnerability scanning <a id="lint-vuln"></a>

```bash
make lint    # golangci-lint run — once per Go module: root, orchestrator, sandbox-mcp
make vuln    # govulncheck — supply-chain scan, all three Go modules
```

`make lint` running three times is not a typo — it's the same three-module split as above: `golangci-lint` operates per Go module, so it runs once at the repo root (CLI module), once in `orchestrator/`, and once in `sandbox-mcp/`. There is only **one** `.golangci.yml` in the repository (at the root) — `golangci-lint` discovers it by walking up from the current directory, so every pass reads the same config; you don't need a copy inside `orchestrator/` or `sandbox-mcp/` for shared rules to apply there too.

---

## Development bootstrap <a id="hot-reload"></a>

```bash
make dev
```

Runs `scripts/dev.sh`: the installer, followed by `docker compose up -d sandbox`. Both scripts hardcode Docker, so they do not honor `CONTAINER_ENGINE=podman` or Make's insecure-bridge overlay. It does not run a watcher, Vite, the orchestrator, gateway, or broker. Prefer the full stack plus `make dev-ui` or explicitly configured `make dev-orchestrator` for development requiring those services. The Compose UI already occupies port 3000 (and proxies port 9080); stop the conflicting service before binding a host dev server to either port.

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

Point `YUMMI_EINO_MODEL_BASE_URL` (or the harness's own model-base-URL flag) at the probe's `-listen` address instead of the real server, and it logs every `/chat/completions` turn — message/tool counts, the **server's own** `usage.prompt_tokens`/`completion_tokens`/`total_tokens` (not an estimate), `finish_reason`, and whether `tool_calls` fired for observing reported context growth turn by turn. `-dump-dir` additionally saves each turn's full raw request/response JSON, useful for inspecting a turn whose content came back empty or malformed.

All five compile to `orchestrator/test_agent`, `orchestrator/test_engine`, `orchestrator/test_loop`, `orchestrator/test_openai_backend`, `orchestrator/context_probe` if built in place — already covered by `.gitignore`, so building one locally won't accidentally get committed.

## Agent quality gates

`make _test-orchestrator` includes two model-independent quality contracts: `TestPersonaGoldenFixtures` reads every checked-in `persona/v0.1/golden_tests/*.yaml`, and offline tests exercise `EvaluateTaskQuality`. The evaluator rejects cosmetic completion, repeated *executions* (blocked proposals are tracked separately), failed plans, empty summaries, missing evidence digests, unsupported finding claims, wrong command families, and scenario-specific failure markers. The live suite loads the same skill registry and runs as the same UID `1000:1000` as the production sandbox bridge. The full metric contract and scenario list live in [Agent Quality & Evaluation](agent/quality-and-evaluation.md). For real-model regression testing against the sandbox, run:

```bash
make test-agent-eval LIVE_TEST_MODEL=qwen3:14b
```

To reproduce one failing scenario while iterating, set `YUMMI_EVAL_CASE` to its ID, for example `E5-largest`.

The live suite is assertion-bearing: plan failure, non-termination, missing successful work, command repetition, or threshold violations fail the test after the transcript is printed.

## Complete suites and boundary verification

`make test` includes root Go, orchestrator Go, gateway Rust, UI Vitest, Docker-contained sandbox-MCP, optional brain tests, and archived QC evidence checks. Brain tests skip when uv is absent; when installed they run `uv sync` and pytest. The QC suite needs Python 3. `make test` is not a live-model or packet-boundary proof.

```bash
make test
make test-security-boundaries
```

The second target runs disposable gateway kernel, research integration, and operator/control checks. It requires a running Docker daemon; kernel checks use a privileged networkless container, research checks use real installed UID-1000 sandbox MCP and gateway components, and control checks create an isolated stack through the local Docker Unix socket. These targets build images and create containers; run them deliberately.

Sandbox-MCP builds and tests belong in Docker. Use `make _test-sandbox-mcp` (networkless test container, no host mounts) or `make docker-build` for runtime images. The current aggregate `make build` also invokes a host `_build-sandbox-mcp`, which conflicts with that placement rule; use the individual CLI/orchestrator/gateway/UI native targets above instead.

`make lint` covers Go only. CI additionally runs gateway Clippy and the UI lint/build jobs; inspect `.github/workflows/ci.yml` for the actual matrix.
