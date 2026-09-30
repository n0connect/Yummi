[Back to Project README](../README.md) · [Documentation Index](README.md)

# Installation & Platform Setup

This guide covers environment prerequisites, container engine compatibility, model tier configuration, and native compilation steps.

## Table of Contents

- [Execution Modes](#two-ways)
- [System Requirements](#requirements)
  - [The Cgo Requirement](#cgo-requirement)
  - [Minimum Go Version](#go-version-skew)
  - [Node.js Runtime Compatibility](#node-version)
- [Platform Support Matrix](#platform-support)
  - [Docker Desktop VM Networking Limitation](#docker-desktop-internal-network-bug)
  - [Podman Compatibility Status](#podman-macos-gaps)
  - [Engine Selection and Insecure Bridge Workaround](#engine-and-network-mode)
- [Linux Setup](#linux)
- [macOS Setup](#macos)
- [Windows (WSL2) Setup](#windows)
- [Ollama Configuration and Model Tiers](#ollama-setup)
  - [Custom Models and Cloud Endpoints](#custom-model)
- [Pre-Build Configuration Wizard (make conf)](#make-conf)
- [Running the Full Stack (Docker Compose)](#full-stack)
- [Building Native Binaries](#native-binaries)
- [Post-Install Verification](#verifying)
- [Troubleshooting Reference](#troubleshooting)

---

## Execution Modes <a id="two-ways"></a>

Yummi provides two execution modes:

| Mode | Command | Running Components | Capabilities |
|---|---|---|---|
| **Full Stack (Production)** | `make start` | All 5 containers: gateway, sandbox, orchestrator, docker-proxy, ui | Full offensive security tools, real `nftables` fail-closed network enforcement, graphical desktop and terminal. |
| **Local Orchestrator (Development)** | `make dev-orchestrator` | Host Go process only (`:9080`) | Rapid iteration on REST/WS APIs and prompt assembly (no tool execution without container stack). |

For standard operations, proceed directly to [System Requirements](#requirements) and [Running the Full Stack](#full-stack).

---

## System Requirements <a id="requirements"></a>

| Tool | Purpose | Minimum Version |
|---|---|---|
| **Docker** | Orchestrates the 5-container architecture | Compose v2 (`docker compose`) |
| **Go** | Builds CLI and host orchestrator | >= 1.25.0 |
| **Rust** | Builds gateway locally (`make _build-gateway`) | Stable channel, Edition 2021 |
| **Node.js + npm** | Builds the React SPA | Node 20 or 22 LTS |
| **C Compiler** (`gcc`, `clang`) | Required for Go SQLite cgo bindings | Standard system C compiler |
| **Ollama / Cloud Provider** | LLM backend (local or remote API) | Ollama >= 0.3.0 or OpenRouter API key |
| **curl** | Health checks and installer scripts | Standard |

### The Cgo Requirement <a id="cgo-requirement"></a>

The Go orchestrator uses `github.com/mattn/go-sqlite3` for session persistence. Native builds (`make _build-orchestrator` or `go build` inside `orchestrator/`) require `CGO_ENABLED=1` and a functional host C compiler (`cc`/`gcc`/`clang`).

> [!NOTE]
> Containerized builds (`make start`, `make docker-build`) compile inside `golang:1.25-bookworm`, which includes the full C toolchain automatically. Host toolchains are only needed for direct host compilation.

**Installing C Toolchains:**
- **macOS**: `xcode-select --install`
- **Linux (Debian/Ubuntu)**: `sudo apt-get update && sudo apt-get install -y build-essential`
- **Linux (Fedora/RHEL)**: `sudo dnf groupinstall -y "Development Tools"`

### Minimum Go Version <a id="go-version-skew"></a>

The root module (`yummi` CLI) and `orchestrator/` require **Go >= 1.25.0**. Check your installed version:

```bash
go version
```

### Node.js Runtime Compatibility <a id="node-version"></a>

Use **Node 20 or 22 LTS** for UI builds. Certain non-LTS releases (such as Node 26) have known npm filesystem descriptor regressions.

```bash
# Recommended using nvm:
nvm use 22
```

---

## Platform Support Matrix <a id="platform-support"></a>

| Platform | Full Stack (`make start`) | Native Binaries | Architecture Notes |
|---|---|---|---|
| **Linux x86_64** | Supported (Full nftables enforcement) | Supported | Reference production platform. |
| **Linux ARM64** | Supported (Full nftables enforcement) | Supported | Multi-arch Docker manifests published for all images. |
| **macOS (Apple Silicon)** | Supported (via OrbStack recommended) | CLI + Orchestrator | Linux VM provides kernel nftables support. See Docker Desktop note below. |
| **macOS (Intel)** | Supported (via Linux container VM) | CLI + Orchestrator | Requires Linux VM engine for gateway packet filtering. |
| **Windows 10/11 (WSL2)** | Supported | Partial (CLI cross-compilation) | Runs via Docker Desktop WSL2 Linux backend. |
| **Windows (Native / non-WSL2)** | Unsupported | Partial (CLI only) | Gateway requires Linux kernel netfilter subsystems. |
| **Direct Host Gateway (No Docker)** | Unsupported Topology | Compiles | Requires manual routing and namespace configuration. |

### Docker Desktop VM Networking Limitation <a id="docker-desktop-internal-network-bug"></a>

> [!WARNING]
> On Docker Desktop for macOS (Apple Silicon), the VM networking layer (`vpnkit`) can drop forwarded traffic originating from containers attached to bridge networks configured with `internal: true`.
>
> **Symptom**: Sandbox egress commands time out despite valid gateway allow rules.
>
> **Remediation**:
> 1. **Recommended**: Switch to [OrbStack](https://orbstack.dev/) on macOS (drop-in Docker engine replacement utilizing Apple Virtualization.framework).
> 2. **Alternative**: Enable the opt-in `insecure-bridge` mode via `make conf`. Note that this removes the Docker-level `internal: true` constraint.

### Podman Compatibility Status <a id="podman-macos-gaps"></a>

On Podman 6 (macOS):
1. **DNS Resolution**: The default `aardvark-dns` resolver suppresses upstream queries on `internal: true` networks.
2. **Socket Proxying**: Podman socket emulation does not maintain a persistent connection to `docker-proxy`.

For macOS deployments, **OrbStack** is the recommended engine for zero-configuration compatibility.
   A workaround exists, but it trades away something else: recreating the network with DNS disabled entirely (`podman network create --disable-dns --internal ...` in place of what `podman compose up` would create on its own) makes the sandbox's own `dns:` config (from `docker-compose.yml`) take effect directly, fixing hostname resolution — confirmed working (`getent hosts scanme.nmap.org` returned a real answer). But disabling the network's DNS plugin also removes **container-name** resolution for everything else on that network, and `orchestrator`'s `DOCKER_HOST: tcp://docker-proxy:2375` depends on resolving `docker-proxy` by name — that specific env var would need to change to `docker-proxy`'s static compose IP (`tcp://172.20.0.5:2375`) to keep working. This is a real, workable fix in principle, but it requires an out-of-band `podman network create` step that plain `docker-compose.yml` has no field to express — it isn't currently a "just run `make start`" experience on Podman.

2. **The `docker-proxy` service's `/var/run/docker.sock` mount doesn't reach a working Docker-API-compatible socket under Podman**, independent of the DNS issue above (reproduces with default, unmodified network settings too). Symptom: `docker-proxy`'s own logs show `503` on every request (`GET /v1.24/containers/yummi-sandbox/json` included), and orchestrator's system status reports `"sandbox":"down","details":"container unreachable"` even though every container is individually healthy. `/var/run/docker.sock` on the host does resolve to Podman's own machine socket (`~/.local/share/containers/podman/machine/podman.sock`) — the symlink exists and is correct — but something in how that gets bind-mounted through Podman's virtualization layer into the `docker-proxy` container doesn't produce a live socket on the other end. **Not resolved** — this needs more investigation than this pass covered, and it means `docker exec` into sandbox (i.e. the agent actually running any command at all) does not currently work on Podman even after fixing the DNS issue above.

**Bottom line for Podman on macOS today:** the trust-boundary/network-forwarding architecture this project depends on is sound on Podman — better than on Docker Desktop, in fact, for the exact failure mode described above. But between the DNS-forwarding gap and the docker-proxy socket gap, a plain `make start`/`podman compose up` does not currently give you a fully working agent loop. **OrbStack remains the confirmed, no-workarounds-needed recommendation** for macOS; treat Podman as a promising but not-yet-there option until the docker-proxy socket issue specifically is understood. The Makefile's `insecure-bridge` mode (see [§ Engine selection and the insecure-bridge workaround](#engine-and-network-mode) below) fixes gap #1 (DNS forwarding) if you set `CONTAINER_ENGINE=podman` alongside it, but does **not** fix gap #2 (the docker-proxy socket) — sandbox exec still won't work, so it doesn't get Podman to a fully-working state on its own.

### Engine selection and the insecure-bridge workaround <a id="engine-and-network-mode"></a>

The `Makefile` supports both engines and an explicit, opt-in trade-off for the networking bugs described above, rather than silently picking one behavior.

**Recommended: configure it once with `make conf`.** It asks which of the four setups below you're on, and writes the answer to a gitignored `.yummi.conf` in the repo root, so every later `make build`/`make start`/`make force-restart` picks it up automatically — no flags to remember or repeat:

```bash
make conf
#   1) OrbStack                    recommended — no known trade-offs
#   2) Docker Desktop
#   3) Podman
#   4) Linux native Docker Engine   no VM, no known bug
```

Choosing option 2 or 3 walks you through the same `I UNDERSTAND` trade-off confirmation described below before it writes anything — this is the only place the confirmation is ever asked; `make build`/`make start`/`make rebase`/`make force-restart` afterward just reuse the persisted, already-confirmed choice without re-prompting. Run `make conf` again anytime to change your answer (it overwrites `.yummi.conf`); a command-line flag on any individual `make` invocation (e.g. `make start CONTAINER_ENGINE=podman YUMMI_NETWORK_MODE=insecure-bridge`) still overrides whatever `.yummi.conf` has for that one run, but bypasses the interactive confirmation since it isn't going through `make conf` — use `YUMMI_NETWORK_MODE_CONFIRM=yes` for scripting/CI to make that explicit rather than accidental.

Under the hood, `make conf` is just setting these two variables, which you can also pass directly (useful for CI or a one-off run without persisting anything):

```bash
make start                                            # CONTAINER_ENGINE=docker (default), YUMMI_NETWORK_MODE=secure (default)
make start CONTAINER_ENGINE=podman                    # same compose file, run through podman compose instead
make start YUMMI_NETWORK_MODE=insecure-bridge          # drops yummi_internal's `internal: true` — see below
make start CONTAINER_ENGINE=podman YUMMI_NETWORK_MODE=insecure-bridge
```

`YUMMI_NETWORK_MODE=insecure-bridge` loads `docker-compose.override.insecure-bridge.yml` on top of the base compose file, which removes `internal: true` from `yummi_internal` — the flag [Architecture § Network topology](architecture.md#network-topology) describes as making sandbox's network "architecturally absent" of any route out, independent of gateway's own nftables rules. Without it, sandbox's containment depends **entirely** on gateway's nftables staying correct — one fewer of the independent fail-closed layers described in [Security Model § The core invariant](security-model.md#fail-closed), not zero security, but a real and permanent reduction for as long as this mode is used.

Because that's a genuine security trade-off and not a default anyone should hit by accident, `make conf` — when you pick option 2 or 3 — runs it through a guard (`_check-network-mode`) that prints the trade-off in full and requires you to type `I UNDERSTAND` before proceeding. That confirmation fires once, at `make conf` time; `make start`/`make rebase`/`make force-restart` no longer carry `_check-network-mode` as a prerequisite, so they silently reuse whatever mode is already persisted in `.yummi.conf` instead of re-asking on every invocation. Passing `YUMMI_NETWORK_MODE=insecure-bridge` directly on a one-off `make` command line (bypassing `make conf` entirely) skips the prompt too — that's the deliberate scripting/CI path; `YUMMI_NETWORK_MODE_CONFIRM=yes` documents that intent explicitly when you do.

**This exists specifically as a workaround for the engine bugs on this page, not as a supported alternative security posture.** If you're on Docker Desktop, switching to OrbStack fixes the underlying bug with zero trade-offs and is the actually-recommended path — reach for `insecure-bridge` (option 2 in `make conf`) only if you genuinely cannot change engines. If you're on Podman, remember it only closes the DNS gap, not the docker-proxy socket gap, so it isn't sufficient on its own today — `make conf`'s option 3 prints that caveat explicitly.

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

# 3. Go ≥ 1.25 — check your distro's package first, or use the official tarball:
go version   # if this prints < 1.25, see https://go.dev/dl/

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

**The nftables question:** a macOS container engine runs gateway inside a Linux VM, so the gateway can use real `nf_tables`; the native macOS binary cannot. Kernel support alone does not prove the whole forwarding path works, which is why the engine-specific availability and socket caveats below still matter.

---

## Windows <a id="windows"></a>

**Requires WSL2.** Yummi's full stack is not supported on Windows without it, because gateway needs a genuine Linux kernel and there is no cross-platform fallback (see [Architecture § What is *not* in this repository](architecture.md#not-in-scope) — the old cross-platform network-controller abstraction was intentionally removed from the codebase).

```powershell
# 1. Enable WSL2 (PowerShell as Administrator, one-time, requires a reboot)
wsl --install

# 2. Install Docker Desktop for Windows, and in its Settings -> General,
#    confirm "Use the WSL 2 based engine" is checked (it is the default on
#    current Docker Desktop releases).

# 3. From inside your WSL2 distro (e.g. Ubuntu), follow the Linux instructions
#    above verbatim — build tools, Go, Rust, Node, `make install`, `make start`.
```

Everything after step 3 is identical to the [Linux](#linux) section — from inside a WSL2 shell, you *are* on a real Linux kernel with a real Docker daemon (shared with Docker Desktop), so the same commands apply unchanged.

If you only need the `yummi` CLI (no gateway, no sandbox, no real pentest execution) on native Windows without WSL2, `go build` cross-compiles cleanly:
```bash
GOOS=windows GOARCH=amd64 go build -o yummi.exe ./cmd/yummi
```
This produces a `.exe` (already accounted for in `.gitignore`) that can open the web UI against an orchestrator running elsewhere, but it cannot run the full stack itself.

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

`YUMMI_OLLAMA_TIER` selects a preset (model + context window + thinking mode) from `orchestrator/internal/conductor/tier.go`. These are the **only two presets that exist** — OpenAI's gpt-oss family only ships in two real sizes on Ollama, unlike the four-rung ladder a single dense model family (e.g. qwen3's 4b/8b/14b/32b) can offer. There's no config file to tune this beyond what's below. If both variables are set, the more specific `YUMMI_OLLAMA_MODEL` override wins.

| Tier | Model | Context window | Thinking mode | Approximate download size¹ | Rough RAM/VRAM needed¹ | Good fit for |
|---|---|---|---|---|---|---|
| `standard` | `gpt-oss:20b` | 32,768 tokens | off | ~13 GB (MXFP4) | 16 GB+ | **The registry, Compose, and install default** — native reliable tool-calling, pairs with the Eino backend; runs without a dedicated GPU |
| `expert` | `gpt-oss:120b` | 32,768 tokens | off | ~65 GB (MXFP4) | 80 GB GPU, or 64 GB+ Mac with offload | Workstation-class hardware; same architecture and tool-calling reliability at ~6x the parameters |

¹ *Sizes and RAM figures are approximate, based on Ollama's default MXFP4 quantization for each tag at the time of writing — always let `ollama pull` and your own `ollama ps`/Activity Monitor be the source of truth for your actual hardware, not this table.*

**The install and runtime defaults are intentionally the same:** `make install`, `make start`, Compose, and `scripts/doctor.sh` all use `gpt-oss:20b` unless `YUMMI_OLLAMA_MODEL`/`OLLAMA_MODEL` overrides it. The opt-in live agent test (`make test-agent-live`) still defaults `LIVE_TEST_MODEL` to a smaller qwen3 tag, deliberately independent of this tier registry — see that Makefile target's own comment for why.

Set a tier by exporting the env var before `make start`, or editing `docker-compose.yml`'s `orchestrator.environment.YUMMI_OLLAMA_TIER` directly:
```bash
YUMMI_OLLAMA_TIER=expert make start
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

### Bringing your own model <a id="custom-model"></a>

The two tiers above are presets for convenience, not a whitelist — **`YUMMI_OLLAMA_MODEL` accepts any Ollama model tag you have pulled (or will pull).** If your hardware can run something bigger or different than `gpt-oss` — a large open-weight MoE model like Kimi K2 or DeepSeek-V3, a Llama 3.1 405B quant, a fine-tune, whatever `ollama pull <tag>` accepts — point Yummi at it directly:

```bash
ollama pull <your-model-tag>       # check the exact tag at https://ollama.com/library
YUMMI_OLLAMA_MODEL=<your-model-tag> make start
```

Or interactively: `make conf` -> model choice -> **Custom** -> type the exact tag. This persists to `.yummi.conf` like every other `make conf` choice, so you don't need to set the env var by hand again.

**Two things worth knowing before you do this:**

1. **Context window.** A model tag that isn't one of the two named tiers is treated as a **custom tag wrapped under the Standard profile** (`orchestrator/internal/conductor/tier.go`'s `TierRegistry.Resolve`) — 32,768 tokens of context, thinking mode off — regardless of how much context the model itself actually supports. If you're running something with real long-context headroom and want Yummi to use more of it, set:
   ```bash
   YUMMI_OLLAMA_NUM_CTX=131072 YUMMI_OLLAMA_MODEL=<your-model-tag> make start
   ```
   (or the equivalent prompt in `make conf`'s custom-model step). `YUMMI_OLLAMA_THINKING=true` similarly opts a custom model into thinking-mode request options if it supports one. This only affects the **graph backend** — see the next point.

2. **Backend choice matters more for a custom model than for the gpt-oss tiers.** The **Eino backend** (`YUMMI_AGENT_BACKEND=eino`, or `make conf`'s backend question) doesn't wrap the model in any tier profile at all — no context cap, no thinking-mode toggle from Yummi's side — it just talks to whatever `YUMMI_OLLAMA_MODEL` names via Ollama's OpenAI-compatible endpoint, so a big model's own context window (set via its Modelfile's `PARAMETER num_ctx`, or Ollama's own `OLLAMA_CONTEXT_LENGTH` env var) applies unmodified. Eino was specifically built for larger models with **native, reliable tool-calling** (see [Agent Overview § Two engines](agent/overview.md#two-engines)) — if the model you're bringing supports function-calling well, Eino is very likely the better fit over the graph engine's tag-protocol design, which targets smaller/weaker models that can't be trusted with structured tool-calling output.

3. **Cloud & Frontier Models via OpenRouter or OpenAI Endpoints.** You can point the Eino agent backend directly at OpenRouter or any OpenAI-compatible `/v1/chat/completions` endpoint:
   ```bash
   YUMMI_AGENT_BACKEND=eino \
   YUMMI_EINO_PROVIDER=openrouter \
   YUMMI_EINO_MODEL_BASE_URL=https://openrouter.ai/api/v1 \
   YUMMI_EINO_MODEL_NAME=anthropic/claude-3.5-sonnet \
   YUMMI_EINO_MODEL_API_KEY=sk-or-v1-... \
   make start
   ```
   Or for a local OpenAI-compatible inference server (LM Studio, vLLM, Ollama OpenAI endpoint):
   ```bash
   YUMMI_AGENT_BACKEND=eino \
   YUMMI_EINO_PROVIDER=openai \
   YUMMI_EINO_MODEL_BASE_URL=http://host.docker.internal:1234/v1 \
   YUMMI_EINO_MODEL_NAME=openai/gpt-oss-20b \
   YUMMI_EINO_MODEL_NUM_CTX=32768 \
   make start
   ```

4. **Multi-Agent Frontier Framework: The LangGraph Brain (`brain/`).** If you want a supervisor + specialist multi-agent architecture running Claude 3.5 Sonnet or GPT-4o through LangGraph over the same fail-closed execution substrate:
   ```bash
   make brain-install
   # Set your API keys in brain/.env (or export YUMMI_BRAIN_MODEL_API_KEY)
   make brain-run GOAL="Enumerate example.com and report all subdomains, live web endpoints, and potential XSS/SQLi vulnerabilities"
   ```
   See [brain/README.md](../brain/README.md) for full details.

---

## Pre-Build Configuration Wizard (`make conf`) <a id="make-conf"></a>

Before executing `make build` or `make start`, you should run the interactive setup wizard:

```bash
make conf
```

`make conf` asks a structured series of questions and persists your decisions to a machine-local, gitignored file: `.yummi.conf`. All subsequent build and start operations automatically inherit these parameters without needing manual environment variables.

### Configuration Decisions Handled by `make conf`:

1. **Container Engine & Network Security Mode**:
   - `1) OrbStack` -> `CONTAINER_ENGINE=docker`, `YUMMI_NETWORK_MODE=secure` (recommended on macOS).
   - `2) Docker Desktop` -> `CONTAINER_ENGINE=docker`, `YUMMI_NETWORK_MODE=insecure-bridge` (requires typing `I UNDERSTAND` to accept the security trade-off).
   - `3) Podman` -> `CONTAINER_ENGINE=podman`, `YUMMI_NETWORK_MODE=insecure-bridge`.
   - `4) Linux native Docker Engine` -> `CONTAINER_ENGINE=docker`, `YUMMI_NETWORK_MODE=secure` (production default).

2. **UI Display Language (`YUMMI_LANG`)**:
   - `1) English (en)`
   - `2) Türkçe (tr)`
   - **CRITICAL**: The UI language is compiled directly into the React/Vite single-page application at **build time**. It cannot be changed at runtime via an in-app toggle. To switch languages later, run `make conf` and rebuild (`make start` or `make docker-ui`).

3. **Agent Backend Engine (`YUMMI_AGENT_BACKEND`)**:
   - `1) Graph`: 11-node deterministic state machine using the closed tag protocol (`RUN_COMMAND`, `FINISH`, `HANDOFF`, etc.). Optimized for small local models (`gpt-oss:20b`, `qwen3:8b`).
   - `2) Eino Local`: CloudWeGo Eino ReAct loop with native tool-calling against larger models running on local Ollama.
   - `3) Eino Cloud (OpenRouter / OpenAI)`: Cloud frontier models with native tool-calling:
     - Provider selection: OpenRouter (`https://openrouter.ai/api/v1`), OpenAI (`https://api.openai.com/v1`), or Custom base URL.
     - Model name: e.g., `anthropic/claude-3.5-sonnet`, `deepseek/deepseek-chat`, `openai/gpt-4o`.
     - API Key: Hidden input during typing; securely written to `.yummi.conf` (never tracked by git).

4. **Local Intent & Conductor Model (`YUMMI_OLLAMA_MODEL`)**:
   - The conversational conductor and intent classifier always run on local Ollama for zero API cost:
     - `1) Standard`: `gpt-oss:20b` (Default, runs comfortably on 16 GB+ RAM).
     - `2) Expert`: `gpt-oss:120b` (High capability, requires 64 GB+ RAM or 80 GB VRAM).
     - `3) Custom`: Any pulled Ollama model tag with optional custom context length (`YUMMI_OLLAMA_NUM_CTX`, e.g., 131072).

5. **LangGraph Multi-Agent Brain Setup (`brain/`)**:
   - Optional Python supervisor + specialist multi-agent topology driving the same Kali sandbox and gateway via MCP. Prompts for provider, model name, and API key.

### Persisted Configuration File (`.yummi.conf`)

`make conf` writes out variables in standard shell format:
```bash
CONTAINER_ENGINE=docker
YUMMI_NETWORK_MODE=secure
YUMMI_LANG=en
YUMMI_AGENT_BACKEND=eino
YUMMI_OLLAMA_MODEL=gpt-oss:20b
YUMMI_EINO_MODEL_BASE_URL=https://openrouter.ai/api/v1
YUMMI_EINO_MODEL_NAME=anthropic/claude-3.5-sonnet
YUMMI_EINO_MODEL_API_KEY=sk-or-v1-...
YUMMI_EINO_PROVIDER=openrouter
```

---

## Running the full stack (Docker Compose) <a id="full-stack"></a>

```bash
make start
```

This one command: runs the dependency check, ensures Ollama is running and the model is pulled, **rebuilds every Docker image from current source** (never silently reuses a stale image), brings the stack up in dependency order (each service's `depends_on: condition: service_healthy` enforces this): `gateway` and `docker-proxy` start immediately in parallel (neither depends on anything); `sandbox` waits for `gateway` to report healthy; `orchestrator` waits for **all three** of `gateway`, `sandbox`, and `docker-proxy` to be healthy before it starts; `ui` waits for `orchestrator`. This is the one command for the normal local workflow. First run takes longer — it's building 4 images from scratch (`docker-proxy` is a pre-built image, pulled rather than built), including a full Kali package install for sandbox.

```bash
make app                  # alias for 'start' — kept for anyone with the old habit memorized
```

Once it's up:
- Web UI: **http://localhost:3000** — this is the only operator surface; `./bin/yummi` (no flags) just opens this same URL in an app-style browser window for convenience
- Orchestrator API: **http://127.0.0.1:9080** (bound to localhost only — not reachable from other machines on your network by design)

```bash
make stop     # docker compose stop — containers remain, fast to resume
make close    # stop everything AND stop Ollama, to free RAM/VRAM when you're done for the day
```

---

## Building native binaries <a id="native-binaries"></a>

Only needed if you're developing the CLI or orchestrator outside Docker, or want a standalone `yummi` binary:

```bash
make build
```
Builds, in order: `_build-cli` (-> `./bin/yummi`), `_build-ui` (-> `ui/dist`), `_build-orchestrator` (-> `./bin/yummi-orchestrator`), `_build-gateway` (-> `gateway/target/release/yummi-gateway`).

Or individually, for a faster feedback loop while you're only touching one piece:
```bash
make _build-cli           # just the CLI binary
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

**`cgo: C compiler "cc" not found`** -> You're missing a C compiler. See [the cgo callout](#cgo-requirement) above — this is not optional, and it is not checked automatically before you build.
 
**`go: go.mod requires go >= 1.24.2`** -> Your Go is too old for the *root* module even though it might be new enough for `orchestrator/`. See [the version-skew callout](#go-version-skew).
 
**`npm ci` fails with `EBADF`, or other file-descriptor errors that don't mention any of your actual code`** -> Check `node --version` before debugging anything else. See [the Node version callout](#node-version).
 
**Docker Compose can't reach Ollama; orchestrator logs show a connection failure to `host.docker.internal:11434`** -> On Linux, confirm `docker-compose.yml`'s orchestrator service still has the `extra_hosts: ["host.docker.internal:host-gateway"]` entry (it ships with it as of this writing) and that your Docker Engine is >= 20.10. On any platform, confirm Ollama is actually running on the host (`curl http://localhost:11434/api/tags` from your host, not from inside a container) before assuming it's a networking problem.
 
**`docker compose up` fails with a permission error on Linux, right after a fresh Docker install`** -> You were added to the `docker` group but haven't started a new login session yet. Log out and back in, or run `newgrp docker` in your current shell, then retry.
 
**Every chat message gets declined with a bilingual "the model is unavailable" reply`** -> The conductor has no rule-based fallback. `YUMMI_OLLAMA_URL` defaults to empty for a native orchestrator run, so set it explicitly (usually `http://127.0.0.1:11434`). Compose sets `http://host.docker.internal:11434`. Confirm Ollama is running and the configured URL matches where it is listening.
 
**A pentest command in chat gets refused with something that sounds like "I don't have permission," even for an ordinary target like a public hostname`** -> This is a **persona/UX issue that has already been fixed** in this repository's prompt design, not a gateway bug — the model is told explicitly not to pre-judge scope and to report a gateway block/approval-pending result factually rather than framing it as its own lack of permission. If you still see this behavior, check whether `persona/v0.1/safety_boundaries.yaml`'s `scope_enforcement` section and `orchestrator/internal/conductor/engine.go`'s `scopeSection` prompt text match what's described in [Security Model](security-model.md#egress-modes) — a stale or hand-edited persona file is the most likely cause.
 
**Gateway's own egress mode is `block` (the default) and every external target is refused, even though you expected it to just work`** -> This is fail-closed **by design** — see [Security Model § Egress modes](security-model.md#egress-modes). Set the mode to `allow` or `ask` from the UI's GATEWAY tab, or via `POST /api/v1/gateway/egress-mode`, and remember it **resets to `block` on every gateway restart** — it is not meant to be a "set once" switch.
