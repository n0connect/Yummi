← [Back to README](../README.md) · [Docs index](README.md)

# Installation

This page is written for two different readers at once: someone installing Yummi for the **first time ever** on a machine they're not 100% sure is ready, and someone who already knows Docker/Go/Rust and just wants the exact commands and the platform-specific gotchas without wading through basics. Skim for the **bold** warnings if you're the second kind of reader — they mark the exact spots where people get stuck on the first try.

## Table of contents

- [Two ways to run Yummi — pick one first](#two-ways)
- [Requirements](#requirements)
- [Platform support matrix](#platform-support)
  - [⚠️ Docker Desktop VM networking can silently drop `internal: true` traffic — fixed by switching to OrbStack](#docker-desktop-internal-network-bug)
  - [⚠️ Podman on macOS: two separate compatibility gaps, currently unresolved](#podman-macos-gaps)
  - [Engine selection and the insecure-bridge workaround](#engine-and-network-mode)
- [Linux](#linux)
- [macOS](#macos)
- [Windows](#windows)
- [Ollama setup and choosing a model tier](#ollama-setup)
  - [Bringing your own model](#custom-model)
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

**If you just want to use Yummi, skip straight to [Running the full stack](#full-stack)** after satisfying the requirements below — you do not need to separately build any binaries by hand; `make start` builds the Docker images itself. Building native binaries ([below](#native-binaries)) is only necessary if you're developing the CLI, or want a `yummi` binary outside Docker.

---

## Requirements <a id="requirements"></a>

| Tool | Why | Minimum version note |
|---|---|---|
| **Docker** (with Compose v2, i.e. `docker compose`, not the old standalone `docker-compose`) | Runs all 5 services | Any recent Docker Desktop (macOS/Windows) or Docker Engine (Linux) |
| **Go** | Builds the CLI and (locally) the orchestrator | **≥ 1.25** — see the [version note](#go-version-skew) below |
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

The Docker path (`make start`, `make docker-build`) doesn't hit this — `orchestrator/Dockerfile`'s build stage runs inside `golang:1.25-bookworm`, which already has a full C toolchain. This requirement is **only** for building the orchestrator natively on your host.

### ⚠️ Minimum Go version <a id="go-version-skew"></a>

The root `go.mod` (the `yummi` CLI) and `orchestrator/go.mod` both currently require `go 1.25.0` — one Go toolchain on your machine satisfies both. (The third Go module, `sandbox-mcp/`, pins a higher `go 1.26.5` via its MCP SDK dependency, but it's compiled inside its own Docker build stage and isn't part of a normal host build — only relevant if you use `make _build-sandbox-mcp` for local dev.)

If your installed Go is older than what a module requires, `go build`/`make build` fails with a message like:

```
go: go.mod requires go >= 1.25.0 (running go 1.24.x; GOTOOLCHAIN=local)
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
| **macOS (Apple Silicon, arm64)** | ✅ Confirmed via OrbStack (recommended). ⚠️ Docker Desktop and Podman caveats are documented below | ✅ CLI + orchestrator; gateway is Linux-only at runtime | Container engines provide a Linux VM, but working forwarding and Docker-socket behavior depend on the engine; do not infer support only from successful image builds |
| **macOS (Intel, x86_64)** | ⚠️ Requires a Linux container engine; the same engine-specific caveats apply and this path is not independently validated here | ✅ CLI + orchestrator; gateway is Linux-only at runtime | Host architecture changes image selection, not the gateway's Linux requirement |
| **Windows 10/11 with WSL2** | ✅ Full support via Docker Desktop's WSL2 backend | Partial — the CLI cross-compiles fine (`GOOS=windows go build` — see the `*.exe` handling already in `.gitignore`); the orchestrator has not been validated running bare on Windows, and gateway is architecturally out of scope outside a Linux kernel | Docker Desktop for Windows uses a WSL2-based Linux VM by default, which — like the macOS case — is a real Linux kernel, so the same nftables enforcement path applies |
| **Windows without WSL2 / native only** | ❌ Not supported | Partial (CLI only) | No Linux kernel means no nftables anywhere in the stack — don't attempt the full stack here |
| **Bare Linux host, gateway binary run directly (no Docker)** | ⚠️ Unsupported topology | ✅ compiles/runs | It may change nftables in the host namespace, but Compose's sandbox routing topology is absent unless you reproduce it yourself |

### ⚠️ The one combination that silently doesn't enforce anything

Running the compiled gateway directly is not the supported security topology. On **macOS**, `main.rs` returns a Linux-only error before loading policy. On **Linux**, the process can install nftables rules in its current namespace, but those rules protect the intended sandbox only if you have independently reproduced the Compose routing, source IP, capabilities, and interfaces. Use the Compose-started `yummi-gateway` container for the repository's documented boundary.

### ⚠️ Docker Desktop VM networking can silently drop `internal: true` traffic <a id="docker-desktop-internal-network-bug"></a>

**Observed on Docker Desktop (multiple versions, including after updating to a newer release), Apple Silicon.** Confirmed to *not* reproduce on [OrbStack](https://orbstack.dev/) — see below. This is an *availability* bug, not a security regression — when it happens, gateway still fails closed and makes correct scope/egress decisions; the symptom is legitimate, approved traffic never actually arriving, not unauthorized traffic getting through.

**Symptom:** an operator approves egress for a real external target (e.g. `scanme.nmap.org`), the UI shows "Dış ağ erişimi onaylandı" / egress approved, the command runs — and then just times out with no data. DNS resolution inside the sandbox fails (`getent hosts <target>` returns nothing), or a tool reports "Cannot resolve hostname" / connection timeouts, even though every gateway-side decision was correct.

**Root cause, isolated with a controlled experiment** (not a guess — this was reproduced outside Yummi's own containers entirely, changing exactly one variable): a container acting as a router between two custom Docker bridge networks (exactly gateway's own role between `yummi_internal` and `yummi_external`) reliably fails to forward *any* traffic — verified with byte-accurate interface counters (`/proc/net/dev`) showing zero packets ever arriving at the router container's internal-facing interface, not just a failed DNS reply — when the "internal" network was created with the `internal: true` flag. The exact same topology, same nftables/iptables rules, same masquerade rule, with `internal: true` removed, forwarded traffic perfectly. A plain single-hop container (no router) on either flag setting also worked fine, and the macOS host's own networking was unaffected throughout. This isolates the failure to Docker Desktop's own VM-level networking overlay (something above the Linux kernel's netfilter, which was independently confirmed correct via `nft list ruleset` inside gateway) specifically mishandling `internal: true` combined with a container forwarding traffic out of it.

Things that were tried and did **not** fix it on the affected machine: restarting or force-recreating the `gateway`/`sandbox` containers, live-disconnecting and reconnecting them from the network, a full Docker Desktop restart, enabling Docker Desktop's own **"Use kernel networking for UDP"** setting (Settings → Resources → Network), disabling an active VPN/network-filtering app, and updating Docker Desktop to a newer version. None of these are Yummi configuration — they're all Docker Desktop / host-level things to rule out first if you hit this, since none of them were the actual cause here.

**Confirmed: switching to [OrbStack](https://orbstack.dev/) (a different Docker Desktop alternative for macOS, using Apple's Virtualization.framework and its own network stack instead of vpnkit) resolves this completely, with zero changes to Yummi's code or `docker-compose.yml`.** The exact same `internal: true` config, the exact same gateway/sandbox images, rebuilt and run unmodified under OrbStack: DNS resolution inside sandbox succeeds, and a real scan against an external target (`scanme.nmap.org`) completes a real HTTP request end to end. This narrows the root cause specifically to Docker Desktop's own vpnkit-based VM networking layer, not a Docker-version quirk, not the Linux kernel, and not anything in this repository — a from-scratch scratch-network reproduction (two custom bridges, a router container, `internal: true` toggled as the only variable) showed the identical pass/fail split between the two engines.

**This is exactly the network flag `docker-compose.yml`'s `yummi_internal` uses by design** (see [Architecture § Network topology](architecture.md#network-topology)) — it is not something to "just remove," since it's load-bearing for the fail-closed security model described throughout this documentation set, not an accidental config choice. If you hit this symptom on Docker Desktop:
- Confirm it's this bug and not a real scope/config issue: check `docker exec yummi-gateway cat /proc/net/dev | grep eth1` before and after a test command from the sandbox — if the RX counter never increments at all despite the sandbox's own TX counter increasing, you're looking at this, not a gateway policy decision.
- **Or skip the manual check entirely: `make doctor` (`scripts/doctor.sh`) automates exactly this**, once `gateway`/`sandbox` are running. It adds a real temporary gateway allow rule for a known external IP, retries an actual `curl` from inside `sandbox` for up to ~15s (rule propagation genuinely takes a few seconds even on a healthy engine — the check accounts for that instead of false-positiving on it), and reports `[fail]` with this exact remediation only if the sandbox still cannot reach the allowed target after that window.
- **The known-good fix: switch to OrbStack.** `docker compose build && docker compose up -d` works unmodified — OrbStack is a drop-in Docker CLI/API-compatible replacement for Docker Desktop on macOS, no compose file changes needed.
- If you'd rather stay on Docker Desktop, testing on a Linux host (native Docker Engine, no VM in the path at all) is also unaffected — see the [platform support matrix](#platform-support) above. No Docker-Desktop-side setting was found that fixes this while staying on Docker Desktop.
- **If you genuinely can't switch engines**, there's an opt-in Makefile escape hatch — see [§ Engine selection and the insecure-bridge workaround](#engine-and-network-mode) below. It trades away one of this project's independent fail-closed layers to route around the bug, and requires you to type a confirmation every time; it is not a substitute for OrbStack, just a last resort.

### ⚠️ Podman on macOS: two separate compatibility gaps, currently unresolved <a id="podman-macos-gaps"></a>

**Observed on Podman 6.0.2 (`applehv` machine type), Apple Silicon.** Unlike the Docker Desktop issue above, the core thing this whole security model depends on — traffic from `yummi_internal` actually being forwarded through gateway to a real external target — **works correctly on Podman**. This was verified the same rigorous way as the OrbStack confirmation: a real HTTP request to a real external IP (`scanme.nmap.org`'s own address) completed end to end (`200 OK`, real response body) through gateway's nftables rules, with the interface-counter check (`/proc/net/dev` on gateway's internal-facing interface) confirming real packets actually arrive, not just that a request "looked" successful. **`internal: true` itself is not the problem here** — this rules out, a second time and on a third distinct engine, any theory that the flag itself is what's broken.

What doesn't currently work, though, are two unrelated things:

1. **Hostname resolution from inside `yummi_internal` returns NXDOMAIN for real external names.** Podman's embedded DNS resolver (`aardvark-dns`) refuses to forward queries upstream for any network created with `internal: true`, regardless of what DNS servers are configured — this looks like a deliberate policy in `aardvark-dns` itself (an internal network shouldn't leak DNS queries either), not a bug, but it conflicts with this project's explicit design assumption that "DNS resolution is not the trust boundary, so it's fine for it to succeed even on an internal network" (see [Architecture § Network topology](architecture.md#network-topology)). Concretely: `nikto -h scanme.nmap.org` fails to resolve; the identical command against the target's raw IP works.

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

Choosing option 2 or 3 walks you through the same `I UNDERSTAND` trade-off confirmation described below before it writes anything — `make conf` doesn't bypass that gate, it just saves you from retyping the flags on every command afterward. Run `make conf` again anytime to change your answer (it overwrites `.yummi.conf`); a command-line flag on any individual `make` invocation (e.g. `make start CONTAINER_ENGINE=podman`) still overrides whatever `.yummi.conf` has, for one-off use.

Under the hood, `make conf` is just setting these two variables, which you can also pass directly (useful for CI or a one-off run without persisting anything):

```bash
make start                                            # CONTAINER_ENGINE=docker (default), YUMMI_NETWORK_MODE=secure (default)
make start CONTAINER_ENGINE=podman                    # same compose file, run through podman compose instead
make start YUMMI_NETWORK_MODE=insecure-bridge          # drops yummi_internal's `internal: true` — see below
make start CONTAINER_ENGINE=podman YUMMI_NETWORK_MODE=insecure-bridge
```

`YUMMI_NETWORK_MODE=insecure-bridge` loads `docker-compose.override.insecure-bridge.yml` on top of the base compose file, which removes `internal: true` from `yummi_internal` — the flag [Architecture § Network topology](architecture.md#network-topology) describes as making sandbox's network "architecturally absent" of any route out, independent of gateway's own nftables rules. Without it, sandbox's containment depends **entirely** on gateway's nftables staying correct — one fewer of the independent fail-closed layers described in [Security Model § The core invariant](security-model.md#fail-closed), not zero security, but a real and permanent reduction for as long as this mode is used.

Because that's a genuine security trade-off and not a default anyone should hit by accident, `make start`/`make force-restart` (and `make conf`, when you pick option 2 or 3) run it through a guard (`_check-network-mode`) that prints the trade-off in full and requires you to type `I UNDERSTAND` before proceeding — every time, not just once. For scripting or CI, `YUMMI_NETWORK_MODE_CONFIRM=yes` skips the interactive prompt (the warning text still prints) — use it deliberately, not as a way to forget the trade-off exists.

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

# 2. Install Docker Desktop for Windows, and in its Settings → General,
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

`YUMMI_OLLAMA_TIER` selects a preset (model + context window + thinking mode) from `orchestrator/internal/conductor/tier.go`. These are the **only four presets that exist** — there's no config file to tune this beyond what's below. If both variables are set, the more specific `YUMMI_OLLAMA_MODEL` override wins.

| Tier | Model | Context window | Thinking mode | Approximate download size¹ | Rough RAM/VRAM needed¹ | Good fit for |
|---|---|---|---|---|---|---|
| `lite` | `qwen3:4b` | 8,192 tokens | off | ~2.5 GB | ~4–6 GB | Laptops, low-RAM VMs, quick smoke-testing |
| `standard` | `qwen3:8b` | 16,384 tokens | off | ~5.2 GB | ~8–10 GB | The lightweight default for `make test-agent-live` (`LIVE_TEST_MODEL`) |
| `pro` | `qwen3:14b` | 16,384 tokens | off | ~9 GB | ~14–16 GB | **`docker-compose.yml`'s own default for the full stack** — described in-repo as "recommended default for most hardware" |
| `expert` | `qwen3:32b` | 32,768 tokens | **on** (extended reasoning) | ~20 GB | ~24–32 GB+ | Workstation-class GPUs; genuinely deeper multi-step reasoning at real latency cost |

¹ *Sizes and RAM figures are approximate, based on Ollama's default quantization for each tag at the time of writing — always let `ollama pull` and your own `ollama ps`/Activity Monitor be the source of truth for your actual hardware, not this table.*

**The install and runtime defaults are intentionally the same:** `make install`, `make start`, Compose, and `scripts/doctor.sh` all use `qwen3:14b` unless `YUMMI_OLLAMA_MODEL`/`OLLAMA_MODEL` overrides it. The only smaller default is the opt-in live agent test: `make test-agent-live` uses `LIVE_TEST_MODEL=qwen3:8b` so a development smoke test does not require the full runtime model.

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

### Bringing your own model <a id="custom-model"></a>

The four tiers above are presets for convenience, not a whitelist — **`YUMMI_OLLAMA_MODEL` accepts any Ollama model tag you have pulled (or will pull).** If your hardware can run something bigger or different than `qwen3` — a large open-weight MoE model like Kimi K2 or DeepSeek-V3, a Llama 3.1 405B quant, a fine-tune, whatever `ollama pull <tag>` accepts — point Yummi at it directly:

```bash
ollama pull <your-model-tag>       # check the exact tag at https://ollama.com/library
YUMMI_OLLAMA_MODEL=<your-model-tag> make start
```

Or interactively: `make conf` → model choice → **Custom** → type the exact tag. This persists to `.yummi.conf` like every other `make conf` choice, so you don't need to set the env var by hand again.

**Two things worth knowing before you do this:**

1. **Context window.** A model tag that isn't one of the four named tiers is treated as a **custom tag wrapped under the Pro profile** (`orchestrator/internal/conductor/tier.go`'s `TierRegistry.Resolve`) — 16,384 tokens of context, thinking mode off — regardless of how much context the model itself actually supports. If you're running something with real long-context headroom and want Yummi to use more of it, set:
   ```bash
   YUMMI_OLLAMA_NUM_CTX=131072 YUMMI_OLLAMA_MODEL=<your-model-tag> make start
   ```
   (or the equivalent prompt in `make conf`'s custom-model step). `YUMMI_OLLAMA_THINKING=true` similarly opts a custom model into thinking-mode request options if it supports one. This only affects the **graph backend** — see the next point.

2. **Backend choice matters more for a custom model than for the qwen3 tiers.** The **Eino backend** (`YUMMI_AGENT_BACKEND=eino`, or `make conf`'s backend question) doesn't wrap the model in any tier profile at all — no context cap, no thinking-mode toggle from Yummi's side — it just talks to whatever `YUMMI_OLLAMA_MODEL` names via Ollama's OpenAI-compatible endpoint, so a big model's own context window (set via its Modelfile's `PARAMETER num_ctx`, or Ollama's own `OLLAMA_CONTEXT_LENGTH` env var) applies unmodified. Eino was specifically built for larger models with **native, reliable tool-calling** (see [Agent Overview § Two engines](agent/overview.md#two-engines)) — if the model you're bringing supports function-calling well, Eino is very likely the better fit over the graph engine's tag-protocol design, which targets smaller/weaker models that can't be trusted with structured tool-calling output.

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
Builds, in order: `_build-cli` (→ `./bin/yummi`), `_build-ui` (→ `ui/dist`), `_build-orchestrator` (→ `./bin/yummi-orchestrator`), `_build-gateway` (→ `gateway/target/release/yummi-gateway`).

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

**`cgo: C compiler "cc" not found`** → You're missing a C compiler. See [the cgo callout](#cgo-requirement) above — this is not optional, and it is not checked automatically before you build.

**`go: go.mod requires go >= 1.24.2`** → Your Go is too old for the *root* module even though it might be new enough for `orchestrator/`. See [the version-skew callout](#go-version-skew).

**`npm ci` fails with `EBADF`, or other file-descriptor errors that don't mention any of your actual code** → Check `node --version` before debugging anything else. See [the Node version callout](#node-version).

**Docker Compose can't reach Ollama; orchestrator logs show a connection failure to `host.docker.internal:11434`** → On Linux, confirm `docker-compose.yml`'s orchestrator service still has the `extra_hosts: ["host.docker.internal:host-gateway"]` entry (it ships with it as of this writing) and that your Docker Engine is ≥ 20.10. On any platform, confirm Ollama is actually running on the host (`curl http://localhost:11434/api/tags` from your host, not from inside a container) before assuming it's a networking problem.

**`docker compose up` fails with a permission error on Linux, right after a fresh Docker install** → You were added to the `docker` group but haven't started a new login session yet. Log out and back in, or run `newgrp docker` in your current shell, then retry.

**Every chat message gets declined with a bilingual "the model is unavailable" reply** → The conductor has no rule-based fallback. `YUMMI_OLLAMA_URL` defaults to empty for a native orchestrator run, so set it explicitly (usually `http://127.0.0.1:11434`). Compose sets `http://host.docker.internal:11434`. Confirm Ollama is running and the configured URL matches where it is listening.

**A pentest command in chat gets refused with something that sounds like "I don't have permission," even for an ordinary target like a public hostname** → This is a **persona/UX issue that has already been fixed** in this repository's prompt design, not a gateway bug — the model is told explicitly not to pre-judge scope and to report a gateway block/approval-pending result factually rather than framing it as its own lack of permission. If you still see this behavior, check whether `persona/v0.1/safety_boundaries.yaml`'s `scope_enforcement` section and `orchestrator/internal/conductor/engine.go`'s `scopeSection` prompt text match what's described in [Security Model](security-model.md#egress-modes) — a stale or hand-edited persona file is the most likely cause.

**Gateway's own egress mode is `block` (the default) and every external target is refused, even though you expected it to just work** → This is fail-closed **by design** — see [Security Model § Egress modes](security-model.md#egress-modes). Set the mode to `allow` or `ask` from the UI's GATEWAY tab, or via `POST /api/v1/gateway/egress-mode`, and remember it **resets to `block` on every gateway restart** — it is not meant to be a "set once" switch.
