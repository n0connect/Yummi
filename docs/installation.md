← [Back to README](../README.md) · [Docs index](README.md)

# Installation

This page is written for two different readers at once: someone installing Yummi for the **first time ever** on a machine they're not 100% sure is ready, and someone who already knows Docker/Go/Rust and just wants the exact commands and the platform-specific gotchas without wading through basics. Skim for the **bold** warnings if you're the second kind of reader — they mark the exact spots where people get stuck on the first try.

## Table of contents

- [Two ways to run Yummi — pick one first](#two-ways)
- [Requirements](#requirements)
- [Platform support matrix](#platform-support)
- [Linux](#linux)
- [macOS](#macos)
- [Windows](#windows)
- [Ollama setup and choosing a model tier](#ollama-setup)
- [Running the full stack (Docker Compose)](#full-stack)
- [Building native binaries](#native-binaries)
- [Verifying your install](#verifying)
- [First-run troubleshooting](#troubleshooting)

---

## Two ways to run Yummi — pick one first <a id="two-ways"></a>

Yummi has two genuinely different "run modes," and picking the wrong one for what you're trying to do is the single most common source of confusion for new users:

| | **`make start`** (Docker Compose, the real product) | **`make dev-orchestrator`** (bare Go, no sandbox) |
|---|---|---|
| What runs | All 5 containers: gateway, sandbox, orchestrator, docker-proxy, ui | Just the orchestrator Go process on your host |
| Can it actually run pentest tools? | **Yes** — the sandbox container is real Kali | **No** — there is no sandbox container to `docker exec` into; command execution will fail |
| Network enforcement? | **Yes**, real nftables in the gateway container | N/A — gateway isn't running |
| What it's for | Actually using Yummi as a pentest tool | Iterating on orchestrator's Go code with fast rebuilds, or exploring the API without touching Docker |

**If you just want to use Yummi, skip straight to [Running the full stack](#full-stack)** after satisfying the requirements below — you do not need to separately build any binaries by hand; `make start` builds the Docker images itself. Building native binaries ([below](#native-binaries)) is only necessary if you're developing the CLI/TUI, or want a `yummi` binary outside Docker.

---

## Requirements <a id="requirements"></a>

| Tool | Why | Minimum version note |
|---|---|---|
| **Docker** (with Compose v2, i.e. `docker compose`, not the old standalone `docker-compose`) | Runs all 5 services | Any recent Docker Desktop (macOS/Windows) or Docker Engine (Linux) |
| **Go** | Builds the CLI/TUI and (locally) the orchestrator | **≥ 1.24** — see the [version-skew warning](#go-version-skew) below |
| **Rust** (via `rustup`, not your distro's package) | Builds the gateway locally (`make _build-gateway`) | stable channel, edition 2021 (`gateway/Cargo.toml`) |
| **Node.js + npm** | Builds the React UI | Node 20 or 22 LTS — see the [Node version warning](#node-version) below |
| **A C compiler** (`cc`/`gcc`/`clang`) | **Not checked by `make _check-deps` — this is the #1 first-build failure.** See [cgo requirement](#cgo-requirement) below | Any recent version |
| **Ollama** | The LLM backend — Yummi makes zero cloud LLM calls | Any recent release |
| **curl** | Used by Makefile health checks | — |

`make _check-deps` (run automatically by `make build`/`make start`/`make test`) checks for `go`, `cargo`, `npm`, `docker`, and `curl` — but **not** for a C compiler. Keep reading.

### ⚠️ The cgo requirement — read this before your first `make build` <a id="cgo-requirement"></a>

The orchestrator uses `github.com/mattn/go-sqlite3` for its SQLite driver, which is a **cgo** binding — it wraps a real C SQLite implementation, not a pure-Go reimplementation. This means building the orchestrator natively (`make _build-orchestrator`, or anything that calls `go build` inside `orchestrator/`) requires `CGO_ENABLED=1` (the default for native builds) **and a working C compiler on your `PATH`**, even though nothing in this project looks like C code to you as a user.

If you skip this and just run `make build`, you will hit exactly one of these two errors, and both mean the same thing — no C compiler:

```
cgo: C compiler "cc" not found: exec: "cc": executable file not found in $PATH
```
```
# github.com/mattn/go-sqlite3
cgo: exec: "gcc": executable file not found in $PATH
```

**Fix, macOS:**
```bash
xcode-select --install
```
(This installs the Xcode Command Line Tools, which provides `clang`/`cc`. If you get "already installed," you already have it and the problem is something else — see [troubleshooting](#troubleshooting).)

**Fix, Linux (Debian/Ubuntu):**
```bash
sudo apt-get update && sudo apt-get install -y build-essential
```

**Fix, Linux (Fedora/RHEL):**
```bash
sudo dnf groupinstall -y "Development Tools"
```

The Docker path (`make start`, `make docker-build`) doesn't hit this — `orchestrator/Dockerfile`'s build stage runs inside `golang:1.22-bookworm`, which already has a full C toolchain. This requirement is **only** for building the orchestrator natively on your host.

### ⚠️ Go version skew between the two modules <a id="go-version-skew"></a>

This repository has **two separate Go modules with two different stated Go versions**:

- Root `go.mod` (the `yummi` CLI/TUI): `go 1.24.2`
- `orchestrator/go.mod`: `go 1.22`

Your machine has one Go toolchain, and it has to satisfy the *stricter* of the two — **install Go 1.24 or newer**, even if you only intend to build the orchestrator. If you have exactly Go 1.22 or 1.23 installed, `orchestrator/` will build fine on its own, but `go build ./cmd/yummi` (or `make _build-cli` / `make build`) will fail with a message like:

```
go: go.mod requires go >= 1.24.2 (running go 1.22.x; GOTOOLCHAIN=local)
```

Check what you have and upgrade if needed:
```bash
go version
```
Download the current release from [go.dev/dl](https://go.dev/dl/), or via your package manager (`brew install go` on macOS, most Linux distros' `golang` package, `winget install GoLang.Go` on Windows).

### ⚠️ Node.js version — a real, reproducible npm bug on Node 26 <a id="node-version"></a>

If `npm ci` inside `ui/` fails with `EBADF` errors that make no sense given your actual code, **check your Node version before you start debugging the repo.** This was independently reproduced *outside* this repository entirely on a very recent Node release (v26.5.0 via Homebrew) — it is an npm/Node environment issue, not a Yummi bug. **Node 20 or 22 LTS is the known-good range.** If you have multiple Node versions installed via Homebrew or `nvm`, point your shell at 22 for this project specifically rather than changing your global default:
```bash
# nvm
nvm use 22
# Homebrew, without changing the global `node` symlink
$(brew --prefix node@22)/bin/npm ci --prefix ui
```

---

## Platform support matrix <a id="platform-support"></a>

Read this table before choosing a machine to run Yummi on — one row is a hard architectural limitation, not a "not tested yet."

| Platform | Full stack (`make start`) | Native binaries | Notes |
|---|---|---|---|
| **Linux x86_64** | ✅ Full support, real nftables enforcement | ✅ | The reference platform — gateway's `network/nftables.rs` runs against a real Linux kernel directly |
| **Linux ARM64** (incl. Raspberry Pi 4/5, AWS Graviton) | ✅ Expected to work — every base image (`kalilinux/kali-rolling`, `golang`, `rust`, `node`, `nginx`, `tecnativa/docker-socket-proxy`, `debian:bookworm-slim`) publishes official `arm64` manifests | ✅ | Not independently load-tested on ARM64 hardware as part of this documentation pass — if you hit an ARM64-specific issue, it is almost certainly one specific `apt` package inside `sandbox/Dockerfile` lacking an arm64 build in Kali's repos, not gateway or orchestrator |
| **macOS (Apple Silicon, arm64)** | ✅ Full support **via Docker Desktop** | ✅ (CLI/TUI + orchestrator natively; gateway builds but enforces nothing outside Docker — see below) | Docker Desktop on macOS runs containers inside a real Linux VM, which genuinely has `nf_tables` support — this has been verified end-to-end (a DROP-all `FORWARD` chain plus a single-target Allow rule correctly gated a real connection) |
| **macOS (Intel, x86_64)** | ✅ Full support via Docker Desktop | ✅ | Same as above; just a different host CPU |
| **Windows 10/11 with WSL2** | ✅ Full support via Docker Desktop's WSL2 backend | Partial — the CLI/TUI cross-compiles fine (`GOOS=windows go build` — see the `*.exe` handling already in `.gitignore`); the orchestrator has not been validated running bare on Windows, and gateway is architecturally out of scope outside a Linux kernel | Docker Desktop for Windows uses a WSL2-based Linux VM by default, which — like the macOS case — is a real Linux kernel, so the same nftables enforcement path applies |
| **Windows without WSL2 / native only** | ❌ Not supported | Partial (CLI/TUI only) | No Linux kernel means no nftables anywhere in the stack — don't attempt the full stack here |
| **Bare Linux host, gateway binary run directly (no Docker)** | ⚠️ Runs, enforces **nothing** | ✅ compiles | This is the one combination that looks like it's working but isn't a security boundary — see the next callout |

### ⚠️ The one combination that silently doesn't enforce anything

Running the **compiled gateway binary directly on a host** — `cd gateway && cargo run` or executing `gateway/target/release/yummi-gateway` outside `docker compose` — will start, open its IPC socket, and *appear* to work. On a bare **macOS** host this genuinely enforces nothing, because there is no Linux kernel at all to hand nftables rules to. On a bare **Linux** host it can actually install real nftables rules (there's a real kernel right there) — but you're then relying on your own host's routing/network namespace setup matching what `docker-compose.yml`'s topology provides, which it doesn't unless you've replicated it yourself. **Treat "gateway enforces traffic" as true only when gateway is the container `yummi-gateway` started via `docker compose` / `make start`.** Any other invocation of the gateway binary is for build-testing or unit tests only, never a security boundary test.

---

## Linux <a id="linux"></a>

### x86_64 and ARM64

The steps are identical for both architectures — Docker picks the matching image manifest automatically.

```bash
git clone https://github.com/n0connect/yummi.git
cd yummi

# 1. System build tools (see the cgo callout above — this is required)
sudo apt-get update && sudo apt-get install -y build-essential curl
# Fedora/RHEL: sudo dnf groupinstall -y "Development Tools" && sudo dnf install -y curl

# 2. Docker (skip if already installed) — the official convenience script:
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"
# Log out and back in (or `newgrp docker`) for the group change to take effect —
# skipping this step is the #1 reason `docker compose` fails with a permissions
# error immediately after a fresh Docker install.

# 3. Go ≥ 1.24 — check your distro's package first, or use the official tarball:
go version   # if this prints < 1.24, see https://go.dev/dl/

# 4. Rust, via rustup (NOT your distro's `rustc` package — it's commonly older)
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source "$HOME/.cargo/env"

# 5. Node 20/22 LTS + npm, via your distro or nvm
node --version

# 6. Bootstrap Yummi's local state + Ollama + pull the default model
make install

# 7. Bring the whole stack up
make start
```

### Linux-specific networking note

`docker-compose.yml` sets `YUMMI_OLLAMA_URL: "http://host.docker.internal:11434"` for the orchestrator container, so it can reach an Ollama daemon running directly on your host (outside Docker). `host.docker.internal` is a magic hostname that **Docker Desktop provides automatically on macOS/Windows, but that native Linux Docker Engine does not resolve by default.** This repository's `docker-compose.yml` already adds the fix for you (`extra_hosts: ["host.docker.internal:host-gateway"]` on the orchestrator service, which maps that name to the host's own IP on Linux) — so as of this repository state, **you don't need to do anything extra for this.** If you ever see the orchestrator's logs complaining it cannot reach `host.docker.internal:11434` specifically on a Linux host, check that this `extra_hosts` entry hasn't been removed, and that your Docker Engine is version 20.10 or newer (the `host-gateway` special value requires it).

---

## macOS <a id="macos"></a>

### Apple Silicon (M1/M2/M3/M4, arm64) and Intel (x86_64)

The commands are identical for both; Docker Desktop and Homebrew both handle the architecture difference transparently.

```bash
git clone https://github.com/n0connect/yummi.git
cd yummi

# 1. Xcode Command Line Tools (provides `clang`/`cc` — see the cgo callout above)
xcode-select --install

# 2. Homebrew, if you don't already have it
#    (see https://brew.sh for the current one-line installer — not reproduced
#    here since piping a remote script into your shell is worth reading first)

# 3. Docker Desktop for Mac
brew install --cask docker
#    Open Docker.app once from Launchpad/Applications so it finishes its own
#    first-run setup and starts the background daemon — `docker info` will
#    hang or error until you've done this at least once.

# 4. Go, Rust, Node
brew install go rustup-init node@22
rustup-init -y
source "$HOME/.cargo/env"

# 5. Bootstrap + start
make install
make start
```

**Apple Silicon note:** every image this project builds from (`golang`, `rust`, `debian:bookworm-slim`, `kalilinux/kali-rolling`, `node`, `nginx`, `tecnativa/docker-socket-proxy`) publishes an `arm64` manifest, so `docker compose build` on an M-series Mac pulls native arm64 layers — you are not running anything under Rosetta/emulation for this project's own images.

**The nftables question, answered directly:** yes, this genuinely works on macOS, because Docker Desktop doesn't run containers against your Mac's own XNU kernel — it runs a real lightweight Linux VM underneath (`Docker Desktop's Linux VM`), and *that* VM has `nf_tables` support. The gateway container's `FORWARD`-chain rules are real, kernel-enforced rules inside that VM. This was directly verified as part of this project's own hardening work: a DROP-all ruleset plus a single explicit Allow rule correctly gated a real network connection end-to-end on macOS via the normal `make start` path.

---

## Windows <a id="windows"></a>

**Requires WSL2.** Yummi's full stack is not supported on Windows without it, because gateway needs a genuine Linux kernel and there is no cross-platform fallback (see [Architecture § What is *not* in this repository](architecture.md#not-in-scope) — the old cross-platform network-controller abstraction was intentionally removed from the codebase).

```powershell
# 1. Enable WSL2 (PowerShell as Administrator, one-time, requires a reboot)
wsl --install

# 2. Install Docker Desktop for Windows, and in its Settings → General,
#    confirm "Use the WSL 2 based engine" is checked (it is the default on
#    current Docker Desktop releases).

# 3. From inside your WSL2 distro (e.g. Ubuntu), follow the Linux instructions
#    above verbatim — build tools, Go, Rust, Node, `make install`, `make start`.
```

Everything after step 3 is identical to the [Linux](#linux) section — from inside a WSL2 shell, you *are* on a real Linux kernel with a real Docker daemon (shared with Docker Desktop), so the same commands apply unchanged.

If you only need the `yummi` CLI/TUI (no gateway, no sandbox, no real pentest execution) on native Windows without WSL2, `go build` cross-compiles cleanly:
```bash
GOOS=windows GOARCH=amd64 go build -o yummi.exe ./cmd/yummi
```
This produces a `.exe` (already accounted for in `.gitignore`) that can launch and drive the TUI against an orchestrator running elsewhere, but it cannot run the full stack itself.

---

## Ollama setup and choosing a model tier <a id="ollama-setup"></a>

Yummi's conductor has **no rule-based fallback** — if it can't reach Ollama, every chat message gets declined with an explanation, not a degraded-but-functional mode. Getting Ollama right matters more than any other single setup step.

```bash
# macOS
brew install ollama
# or the official installer for Linux/macOS:
curl -fsSL https://ollama.com/install.sh | sh
```
`make install` (via `scripts/install.sh`) does this for you automatically if `ollama` isn't already on your `PATH`, and also pulls the default chat model plus a small embedding model used for semantic memory retrieval.

### Picking a tier for your hardware

`YUMMI_OLLAMA_TIER` selects a preset (model + context window + thinking mode) from `orchestrator/internal/conductor/tier.go`. These are the **only four presets that exist** — there's no config file to tune this beyond what's below, and `YUMMI_OLLAMA_MODEL` set directly will override the tier's model if you set both:

| Tier | Model | Context window | Thinking mode | Approximate download size¹ | Rough RAM/VRAM needed¹ | Good fit for |
|---|---|---|---|---|---|---|
| `lite` | `qwen3:4b` | 8,192 tokens | off | ~2.5 GB | ~4–6 GB | Laptops, low-RAM VMs, quick smoke-testing |
| `standard` | `qwen3:8b` | 16,384 tokens | off | ~5.2 GB | ~8–10 GB | The Makefile's own default for `make test-agent-live` and local dev |
| `pro` | `qwen3:14b` | 16,384 tokens | off | ~9 GB | ~14–16 GB | **`docker-compose.yml`'s own default for the full stack** — described in-repo as "recommended default for most hardware" |
| `expert` | `qwen3:32b` | 32,768 tokens | **on** (extended reasoning) | ~20 GB | ~24–32 GB+ | Workstation-class GPUs; genuinely deeper multi-step reasoning at real latency cost |

¹ *Sizes and RAM figures are approximate, based on Ollama's default quantization for each tag at the time of writing — always let `ollama pull` and your own `ollama ps`/Activity Monitor be the source of truth for your actual hardware, not this table.*

**Two different defaults exist and that's intentional, not a bug:** the Makefile's own `OLLAMA_MODEL ?= qwen3:8b` (used by `make test-agent-live` and as the fallback for `make install`'s model pull) is the `standard` tier, while `docker-compose.yml` hardcodes `YUMMI_OLLAMA_TIER: "pro"` / `YUMMI_OLLAMA_MODEL: "qwen3:14b"` for the actual running stack. If you're wondering "why did it just pull an 8B model when I expected 14B" (or vice versa), it depends on *which* path you ran — `make install`/`make test-agent-live` vs. `make start`.

> **⚠️ Concretely, this means the exact install sequence recommended everywhere in this guide — `make install` followed by `make start` — pulls *two separate* large models, not one.** `scripts/install.sh` checks for an already-present model with a loose substring match (`ollama list | grep -q "qwen3"` — matches on the family name, not the specific tag), so it happily pulls `qwen3:8b` even though nothing needs it yet. Then `make start`'s own `_ollama-ensure` check is an exact, anchored match against `qwen3:14b` specifically — it doesn't recognize the `qwen3:8b` you just downloaded as satisfying it, so it pulls `qwen3:14b` too. Net result: **~14 GB across two models**, not the ~9 GB you'd expect from `docker-compose.yml`'s own default alone. This isn't dangerous, just a disk/bandwidth surprise worth knowing about before you start — if you only ever intend to run the Docker Compose stack, you can skip `make install`'s model pull altogether and let `make start` pull the one model it actually uses.

Set a tier by exporting the env var before `make start`, or editing `docker-compose.yml`'s `orchestrator.environment.YUMMI_OLLAMA_TIER` directly:
```bash
YUMMI_OLLAMA_TIER=lite make start
```

**Keep exactly one model loaded at a time.** `scripts/doctor.sh` checks this and warns if it isn't set:
```bash
# macOS
launchctl setenv OLLAMA_MAX_LOADED_MODELS 1 && killall Ollama && open -a Ollama
# Linux (systemd)
#   add `Environment=OLLAMA_MAX_LOADED_MODELS=1` to the ollama.service unit, then:
sudo systemctl restart ollama
```
Without this, pulling/using a second model (e.g. the embedding model alongside your chat model) can load both into VRAM simultaneously and contend for the same GPU memory the chat model needs.

---

## Running the full stack (Docker Compose) <a id="full-stack"></a>

```bash
make start
```

This one command: runs the dependency check, ensures Ollama is running and the model is pulled, **rebuilds every Docker image from current source** (never silently reuses a stale image), then brings the stack up in dependency order (each service's `depends_on: condition: service_healthy` enforces this): `gateway` and `docker-proxy` start immediately in parallel (neither depends on anything); `sandbox` waits for `gateway` to report healthy; `orchestrator` waits for **all three** of `gateway`, `sandbox`, and `docker-proxy` to be healthy before it starts; `ui` waits for `orchestrator`. First run takes longer — it's building 4 images from scratch (`docker-proxy` is a pre-built image, pulled rather than built), including a full Kali package install for sandbox.

```bash
make start-tui   # same as above, then immediately launches the terminal UI
```

Once it's up:
- Web UI: **http://localhost:3000**
- Orchestrator API: **http://127.0.0.1:9080** (bound to localhost only — not reachable from other machines on your network by design)

```bash
make stop     # docker compose stop — containers remain, fast to resume
make close    # stop everything AND stop Ollama, to free RAM/VRAM when you're done for the day
```

---

## Building native binaries <a id="native-binaries"></a>

Only needed if you're developing the CLI/TUI or orchestrator outside Docker, or want a standalone `yummi` binary:

```bash
make build
```
Builds, in order: `_build-cli` (→ `./bin/yummi`), `_build-ui` (→ `ui/dist`), `_build-orchestrator` (→ `./bin/yummi-orchestrator`), `_build-gateway` (→ `gateway/target/release/yummi-gateway`).

Or individually, for a faster feedback loop while you're only touching one piece:
```bash
make _build-cli           # just the CLI/TUI binary
make _build-orchestrator  # just the orchestrator (needs the cgo toolchain — see above)
make _build-gateway       # just the gateway (needs rustup's cargo, not a distro rustc)
make _build-ui            # just the UI (npm ci + vite build)
```

---

## Verifying your install <a id="verifying"></a>

```bash
make doctor
```
Runs `./bin/yummi --doctor` if it exists, falling back to `scripts/doctor.sh`. It checks: Docker present and reachable, Docker Compose v2 available, your CPU architecture (flags non-arm64 as merely a soft "consider" note, not an error — this project's *own* diagnostic assumes ARM64 is increasingly the common case rather than the exception), the `~/.yummi/` directory tree, whether Ollama is reachable, whether your configured model is present, and whether more than one model is loaded simultaneously (VRAM contention warning).

A healthy run ends with:
```
doctor: healthy
```

---

## First-run troubleshooting <a id="troubleshooting"></a>

These are the specific errors people actually hit on a first install, in the order you're likely to hit them.

**`cgo: C compiler "cc" not found`** → You're missing a C compiler. See [the cgo callout](#cgo-requirement) above — this is not optional, and it is not checked automatically before you build.

**`go: go.mod requires go >= 1.24.2`** → Your Go is too old for the *root* module even though it might be new enough for `orchestrator/`. See [the version-skew callout](#go-version-skew).

**`npm ci` fails with `EBADF`, or other file-descriptor errors that don't mention any of your actual code** → Check `node --version` before debugging anything else. See [the Node version callout](#node-version).

**Docker Compose can't reach Ollama; orchestrator logs show a connection failure to `host.docker.internal:11434`** → On Linux, confirm `docker-compose.yml`'s orchestrator service still has the `extra_hosts: ["host.docker.internal:host-gateway"]` entry (it ships with it as of this writing) and that your Docker Engine is ≥ 20.10. On any platform, confirm Ollama is actually running on the host (`curl http://localhost:11434/api/tags` from your host, not from inside a container) before assuming it's a networking problem.

**`docker compose up` fails with a permission error on Linux, right after a fresh Docker install** → You were added to the `docker` group but haven't started a new login session yet. Log out and back in, or run `newgrp docker` in your current shell, then retry.

**Every chat message gets declined with a bilingual "the model is unavailable" reply, and nothing about it looks like a normal error** → This is *correct*, not a bug — the conductor has no rule-based fallback. It means orchestrator can't reach Ollama at the URL in `YUMMI_OLLAMA_URL` (default `http://localhost:11434` outside Docker, `http://host.docker.internal:11434` inside the compose stack). Confirm Ollama is running and the URL matches where it's actually listening.

**A pentest command in chat gets refused with something that sounds like "I don't have permission," even for an ordinary target like a public hostname** → This is a **persona/UX issue that has already been fixed** in this repository's prompt design, not a gateway bug — the model is told explicitly not to pre-judge scope and to report a gateway block/approval-pending result factually rather than framing it as its own lack of permission. If you still see this behavior, check whether `persona/v0.1/safety_boundaries.yaml`'s `scope_enforcement` section and `orchestrator/internal/conductor/engine.go`'s `scopeSection` prompt text match what's described in [Security Model](security-model.md#egress-modes) — a stale or hand-edited persona file is the most likely cause.

**Gateway's own egress mode is `block` (the default) and every external target is refused, even though you expected it to just work** → This is fail-closed **by design** — see [Security Model § Egress modes](security-model.md#egress-modes). Set the mode to `allow` or `ask` from the UI's GATEWAY tab, or via `POST /api/v1/gateway/egress-mode`, and remember it **resets to `block` on every gateway restart** — it is not meant to be a "set once" switch.
