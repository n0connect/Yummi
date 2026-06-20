# Yummi

Privacy-first AI pentest conductor. Translates natural language into practical security workflows while keeping traffic control and operator authority explicit.

![CI](https://img.shields.io/badge/CI-pending-lightgrey)
![License](https://img.shields.io/badge/license-MIT-blue)
![Version](https://img.shields.io/badge/version-v0.1.0-lightgrey)

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                 User / Operator                     │
│           Terminal CLI  +  Web Dashboard            │
└────────────────────────┬────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────┐
│                    orchestrator  (Go)                         │
│   AI conductor · REST + WebSocket API · task loop   │
└────────────────────────┬────────────────────────────┘
                         │  Unix socket (IPC)
┌────────────────────────▼────────────────────────────┐
│                   gateway  (Rust)                     │
│     Fail-closed trust boundary · nftables · audit   │
└──────────────────┬──────────────────────────────────┘
                   │                        │
     ┌─────────────▼──────────┐   ┌────────▼──────────┐
     │    sandbox  (Kali Docker)   │   │  External network │
     │  Tools run here only   │   │  (gateway controls) │
     └────────────────────────┘   └───────────────────┘
```

**orchestrator** orchestrates the AI and API. **gateway** is the only component that can open external traffic. **sandbox** executes commands inside a hardened Kali container. If gateway crashes, all sandbox traffic is blocked (fail-closed nftables rules persist).

## Repository layout

```
cmd/yummi/          CLI entrypoint (binary: yummi)
internal/           Root module shared packages
  cli/              Terminal launch, color output
  config/           ~/.yummi/config.yaml loader
  version/          Build metadata (semver, git SHA)
orchestrator/                 Orchestrator — separate Go module
  cmd/orchestrator/           orchestrator server binary
  internal/         All orchestrator-specific packages (see orchestrator/README.md)
gateway/              Trust boundary — Rust binary
  src/              policy, ipc, network, observer, audit, watchdog
sandbox/                 Kali container image and entrypoint
ui/                 React/Vite frontend (served by nginx on :3000)
persona/            AI assistant YAML/JSON configuration
  v0.1/             Current persona version
scripts/            install.sh, dev.sh, doctor.sh
docs/               Architecture docs and design specs
  ARCHITECTURE.md   System design and component contracts
  superpowers/      Design specs and implementation plans
```

See the module README files for deeper documentation:
- [`orchestrator/README.md`](orchestrator/README.md) — API, conductor, agent loop, package contracts
- [`gateway/README.md`](gateway/README.md) — trust boundary, IPC protocol, policy engine
- [`sandbox/README.md`](sandbox/README.md) — container security constraints, adding tools
- [`ui/README.md`](ui/README.md) — frontend architecture, design tokens, API contract
- [`persona/README.md`](persona/README.md) — AI tiers, tool knowledge, golden tests

## Quick start

```bash
# 1. Bootstrap dependencies (Go, Rust, Node, Docker, Ollama)
make install

# 2. Build all binaries
make build

# 3. Start all services (Ollama + Docker compose)
make start

# Dashboard opens at http://localhost:3000
```

## CLI

```bash
./bin/yummi           # Start runtime and open dashboard
./bin/yummi --start   # Start services
./bin/yummi --stop    # Stop services
./bin/yummi --status  # Check service status
./bin/yummi --doctor  # Environment diagnostics
./bin/yummi --version # Version info
```

## Developer workflow

```bash
make build           # All binaries and UI assets (CLI + orchestrator + gateway + UI)
make test            # All test suites (Go + Rust + UI)
make lint            # golangci-lint (root + orchestrator)

# Granular
make _build-cli      # yummi CLI only
make _build-orchestrator       # orchestrator orchestrator only
make _build-gateway    # gateway Rust binary only
make _build-ui       # React/Vite UI assets only

make _test-cli       # Root module tests
make _test-orchestrator        # orchestrator module tests
make _test-gateway     # Rust tests
make _test-ui        # Vitest UI tests

# Local development servers
make dev-orchestrator          # orchestrator on :9080 (no Docker)
make dev-ui          # Vite HMR on :3000

# Docker
make docker-build    # Rebuild all images
make force-restart   # Stop + free VRAM + restart
make close           # Stop everything + kill Ollama
```

## Environment variables (orchestrator)

```bash
YUMMI_OLLAMA_URL=http://localhost:11434   # Ollama endpoint (empty = rule engine only)
YUMMI_OLLAMA_TIER=standard               # lite | pro | expert | standard
PENTEST_SCOPE_ALLOW=192.168.1.0/24       # Comma-separated allowed targets
PENTEST_SCOPE_BLOCK=192.168.1.1          # Comma-separated blocked targets
```

Full variable reference: [`orchestrator/README.md`](orchestrator/README.md#configuration)

## How it works

1. **Operator** describes a goal in the chat (e.g., "recon this subnet")
2. **Conductor** (orchestrator) classifies intent and either proposes a single command or triggers a multi-step agentic task
3. **Agent loop** (orchestrator) plans steps via LLM, executes them one by one, observes output, adapts — medium+ risk commands pause for operator approval
4. **Every command** passes through the gateway scope check before reaching sandbox
5. **sandbox** runs the command in an isolated Kali container; results stream back via WebSocket

## Known requirements

- Docker Desktop (macOS) or Docker Engine (Linux) must be running
- Ollama must be installed (`brew install ollama` on macOS)
- First `make start` pulls the model (~4 GB for qwen3:8b)

## License

MIT — see [LICENSE](LICENSE).
