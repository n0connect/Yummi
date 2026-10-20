[Back to Project README](../README.md) · [Documentation Index](README.md)

# Installation & Platform Setup

Yummi runs its operator UI, orchestrator, gateway, sandbox, and Docker exec broker as five Compose services. The web UI is the operator surface; the optional CLI opens that UI and provides lifecycle and diagnostic helpers.

## Execution modes <a id="two-ways"></a>

| Mode | Command | Purpose |
|---|---|---|
| Container stack | `make start` | Runs all five services; UI at `http://localhost:3000`. |
| Local orchestrator | `make dev-orchestrator` | Runs the Go API on the host for development; supporting containers are still required for tool execution. |

## Requirements <a id="requirements"></a>

For container startup, install Git, Make, Bash, a running Linux container engine with Compose v2, and curl. Local inference also needs Ollama and sufficient memory for the selected model. `make start` checks the selected engine and daemon; it does not require host Go, Rust, or npm. Image builds download dependencies and the sandbox toolset.

For native development and the broader Make targets:

| Tool | Checked-in requirement or use |
|---|---|
| Go | All three `go.mod` files declare **1.27.2**. |
| C compiler | Native orchestrator builds need SQLite cgo bindings. |
| Rust/Cargo | Stable toolchain for the Edition 2021 gateway crate; the container builder uses Rust 1.86. |
| Node.js/npm | Node 22 matches `ui/Dockerfile`; CI uses Node 20. |
| Python 3 | Archived QC checks (`make _test-qc`). |
| uv | Optional `brain/` installation and tests; see [brain README](../brain/README.md). |
| golangci-lint | `make lint`. |

`make install`, `make rebase`, `make docker-build`, `make test`, and several other developer targets invoke `_check-deps`, which checks for host Go, Cargo, npm, the selected engine, Compose, and curl. This check confirms executable presence, not version compatibility.

### Cgo <a id="cgo-requirement"></a>

The orchestrator uses `github.com/mattn/go-sqlite3`. Native builds require `CGO_ENABLED=1` and a working C compiler. Install Xcode Command Line Tools on macOS (`xcode-select --install`), or the distribution's C build tools on Linux (for Debian/Ubuntu, `sudo apt-get install build-essential`). Container builds supply their own compiler.

### Go version <a id="go-version-skew"></a>

Check `go version` against `go.mod`, `orchestrator/go.mod`, and `sandbox-mcp/go.mod`. Go may download the required toolchain when `GOTOOLCHAIN=auto`; an older installed binary alone is no guarantee that offline builds work. The Go container builders use `golang:1.27.2-bookworm`.

### Node version <a id="node-version"></a>

Use Node 22 to match the UI container build. The UI package does not declare a separate `engines` minimum. For native UI work, run `cd ui && npm ci` before starting Vite.

## Platform support <a id="platform-support"></a>

The gateway enforces packets through Linux nftables and needs `NET_ADMIN`. Native Linux Docker Engine provides that kernel directly; macOS and Windows need a Linux container VM (WSL2 on Windows). Building the gateway on macOS does not establish a usable host firewall boundary. Compose builds local images; the release workflow does not currently declare multi-platform image builds, so do not assume published images cover every architecture.

### Docker Desktop internal-network limitation <a id="docker-desktop-internal-network-bug"></a>

Previous project checks on Apple Silicon observed sandbox forwarding failures with Docker Desktop when the source bridge had `internal: true`, despite gateway allow rules. Switching to OrbStack resolved that setup's forwarding failure. This is historical environment evidence, not a guarantee about every engine release. Diagnose the current engine and gateway policy separately; an egress timeout can also be the expected `block_all` policy.

### Podman on macOS <a id="podman-macos-gaps"></a>

Previous checks recorded two gaps: upstream DNS forwarding on internal networks, and a host socket mount that left the exec broker returning errors. Those results do not establish compatibility for the current Podman release. The Makefile can select `podman compose`, but `scripts/install.sh`, `scripts/dev.sh`, the CLI, and the doctor script still invoke Docker directly. Selecting Podman in `.yummi.conf` does not rewrite those helpers.

### Engine and network mode <a id="engine-and-network-mode"></a>

`CONTAINER_ENGINE` defaults to `docker`; `YUMMI_NETWORK_MODE` defaults to `secure`. The optional `insecure-bridge` mode adds `docker-compose.override.insecure-bridge.yml`, removing `internal: true` from `yummi_internal`. This removes an independent containment layer and leaves gateway routing/firewall correctness more exposed.

`make conf` detects or asks for the engine setup: OrbStack and native Linux Docker choose `secure`; Docker Desktop and Podman choose `insecure-bridge` and require the `_check-network-mode` confirmation. Subsequent startup does not repeat that confirmation. Command-line overrides bypass it; `YUMMI_NETWORK_MODE_CONFIRM=yes` only suppresses the explicit guard when it is invoked.

```bash
make start CONTAINER_ENGINE=docker YUMMI_NETWORK_MODE=secure
```

## Linux <a id="linux"></a>

Install a Linux Docker Engine and Compose v2, Make, Bash, curl, and Git using your distribution's supported installation process. Confirm that the daemon is running and your user can access it:

```bash
docker info
docker compose version
git clone https://github.com/n0connect/yummi.git
cd yummi
make conf
make start
```

For local inference, install/start Ollama before startup; `make start` can start an installed Ollama daemon and pull the configured model. Compose reaches host Ollama at `http://host.docker.internal:11434`, with `extra_hosts: host-gateway` supplied for Linux. Ollama must listen on an address reachable from that container; a host-only loopback listener may need reconfiguration. `OLLAMA_URL` changes Make's host-side probe URL; it does not change Compose's fixed `YUMMI_OLLAMA_URL` value.

For native development, install the toolchains listed above. `make install` creates local state, builds the sandbox using **docker compose**, installs npm dependencies, installs Ollama if absent, and pulls the selected chat model plus an optional embedding model. It performs that Ollama work even if a remote model endpoint is configured. It does not reliably start Ollama before pulling; start the daemon first.

The install script uses `YUMMI_HOME` (default `~/.yummi`) for local files. Compose sandbox mounts instead use `YUMMI_HOST_DATA_DIR` (default `~/.yummi`): `wordlists` and `payloads` read-only, `workspace` writable. Use matching paths when customizing them.

## macOS <a id="macos"></a>

Use a Linux container VM. OrbStack is the engine that resolved the project's recorded Docker Desktop forwarding problem; see the caveats above. Start the engine yourself before `make start` and confirm `docker info` succeeds. Then use the Linux clone/config/start steps.

For native builds, install Xcode Command Line Tools, Go 1.27.2 or a compatible auto-downloading toolchain, Rust/Cargo, and Node 22. Host architecture and available upstream image layers determine whether a build runs natively or needs emulation; no cross-platform success is implied by a macOS build.

## Windows / WSL2 <a id="windows"></a>

Use WSL2 with a Linux distribution and Docker Desktop's WSL integration enabled. Clone into the distribution's Linux filesystem (for example `~/yummi`) and run the Linux commands there. Native Windows is not the full-stack execution environment; the gateway and sandbox need Linux. The CLI is a web launcher/helper, not a native replacement operator client.

## Model setup <a id="ollama-setup"></a>

Without an endpoint override, the Go conductor and agent use Ollama. The tier registry in `orchestrator/internal/conductor/tier.go` defines:

| Tier | Model | Context | Thinking default |
|---|---|---|---|
| `standard` | `gpt-oss:20b` | 32,768 | off |
| `expert` | `gpt-oss:120b` | 32,768 | off |

Bare Make/Compose defaults select Standard; the current `make conf` local-model prompt instead defaults to `gpt-oss:120b`. Model availability, memory usage, and task quality depend on the model/server and hardware. The live evaluation model (`LIVE_TEST_MODEL`, default `qwen3:8b`) is independent of the application model.

Use Make command-line assignments for reliable overrides of persisted `.yummi.conf` values:

```bash
make start YUMMI_OLLAMA_MODEL=gpt-oss:120b
make start YUMMI_OLLAMA_TIER=expert
```

A persisted explicit `YUMMI_OLLAMA_MODEL` wins over tier selection; override or remove that model when changing tiers. `OLLAMA_MODEL` is Make's explicit model override and is exported as `YUMMI_OLLAMA_MODEL`. Plain shell environment assignments can be superseded by values in the included `.yummi.conf`.

### Custom models and endpoints <a id="custom-model"></a>

Any Ollama tag supported by your installed server can be selected. A custom tag receives the Standard tier profile by default. `YUMMI_OLLAMA_NUM_CTX` and `YUMMI_OLLAMA_THINKING` override that profile. Eino inherits the resolved context, with `YUMMI_EINO_MODEL_NUM_CTX` taking precedence for its own calls; supported Ollama-compatible calls receive the context option. Server capabilities still determine which options take effect.

```bash
make start YUMMI_AGENT_BACKEND=eino YUMMI_OLLAMA_MODEL=gpt-oss:120b YUMMI_OLLAMA_NUM_CTX=32768
```

An explicit `YUMMI_EINO_MODEL_BASE_URL` replaces Ollama for the **conductor and graph loop as well as Eino**. Set the matching model and API key, and include the endpoint's required path prefix. A local compatible server keeps model calls local; choosing a remote endpoint sends model prompts and observations to that provider. Research-provider configuration is separate and executes inside the gateway-routed sandbox; see [Web Research](agent/web-research.md).

```bash
make start YUMMI_AGENT_BACKEND=eino \
  YUMMI_EINO_PROVIDER=openai \
  YUMMI_EINO_MODEL_BASE_URL=http://host.docker.internal:1234/v1 \
  YUMMI_EINO_MODEL_NAME=your-server-model-name
```

`make start` skips its Ollama setup when an endpoint override is present, except when its selected model equals `LIVE_TEST_MODEL`. Embeddings are a separate optional local Ollama feature. The optional Python brain has independent `YUMMI_BRAIN_*` configuration; see [brain README](../brain/README.md).

## Configuration wizard <a id="make-conf"></a>

```bash
make conf
```

The wizard writes `.yummi.conf` (gitignored). It detects or asks for engine/network mode, asks for English/Turkish UI language, then local Ollama, OpenRouter, or a custom compatible model endpoint, and optional brain setup. It currently writes `YUMMI_AGENT_BACKEND=eino` for every model choice; select Graph explicitly with `make start YUMMI_AGENT_BACKEND=graph` or edit the configuration. It does not prompt for custom context/thinking overrides.

API keys are masked during entry but saved as plaintext in `.yummi.conf`; keep that file private. It is read by Make, not automatically by arbitrary `go run` or bare Compose commands. `make conf` overwrites the file, so preserve additional manual settings before rerunning it. See [.yummi.conf.example](../.yummi.conf.example) for optional variables.

UI language is fixed at build time. After changing `YUMMI_LANG`, rebuild and recreate the UI with `make start BUILD=1`; `make docker-ui` only builds its image. Native `_build-ui` is incremental: after a command-line-only language change, remove `ui/dist/index.html` before rebuilding if no source/config timestamp changed.

## Full stack <a id="full-stack"></a>

```bash
make start             # builds if any required local image is absent
make start BUILD=1     # rebuilds all five images from source, then starts
make app               # alias of start
```

When all five local images already exist, ordinary `make start` **skips image builds**. Use `BUILD=1` after source changes. `make rebase` also rebuilds, after taking the Compose stack down, and retains image cache and data volumes.

Gateway, sandbox, and broker have no startup dependencies on one another. Orchestrator waits for all three to be healthy; `yummi-ui` waits for orchestrator. Health checks confirm service liveness, not packet-policy correctness.

Host loopback ports **3000 and 9080 both enter UI nginx**. nginx proxies API/WebSocket requests to the operator API on the isolated control network. The orchestrator publishes no host port. Sandbox can access only the separate read-only policy listener on `172.20.0.3:9081`; it cannot use the operator API or Docker broker. See [network topology](architecture.md#network-topology).

```bash
make stop    # stops five runtime services; containers remain
make close   # Compose down, then kills local Ollama/model/Yummi processes
```

`make close` affects host model processes too. Gateway restart resets egress to `block_all`; an allow rule alone does not override lockdown. Select the appropriate mode in GATEWAY before authorized external work. Same-subnet traffic lies outside this forwarded-egress boundary.

## Native binaries <a id="native-binaries"></a>

For host CLI/API/UI/gateway development, build the component you need:

```bash
make _build-cli
make _build-orchestrator
make _build-gateway
make _build-ui
```

`make build` currently also invokes a **host sandbox-MCP compilation** through `_build-sandbox-mcp`. That implementation conflicts with the repository's mandatory Docker placement for sandbox-MCP builds. Use `make docker-build` to build the runtime images, and `make _test-sandbox-mcp` for its build/test cycle; do not run the sandbox MCP binary or tests on the host. Native outputs are not inputs to the runtime Docker builds.

## Verification and diagnostics <a id="verifying"></a>

Read-only initial checks:

```bash
docker compose ps
curl -fsS http://127.0.0.1:9080/healthz
curl -fsS http://127.0.0.1:9080/api/v1/system/status
curl -fsS http://127.0.0.1:9080/api/v1/gateway/egress-mode
```

`make doctor` first runs `./bin/yummi --doctor`; **any failure**, including a missing binary, invokes `scripts/doctor.sh`. The CLI checks Docker/Compose, daemon, architecture, and local state directories. The fallback script adds model checks and a live forwarding canary: when the stack is reachable, it temporarily switches egress to `allow`, adds a temporary `1.1.1.1/32` rule, and runs sandbox curl, then attempts to remove the rule and restore the prior mode. It is therefore a state-changing diagnostic, and restoration is best-effort. Neither result alone proves the full security boundary. Explicit disposable checks are available with `make test-security-boundaries`; see [Development](development.md).

## Troubleshooting <a id="troubleshooting"></a>

- **Old behavior after edits:** use `make start BUILD=1`; existing images are otherwise reused.
- **Go version error:** all three modules require Go 1.27.2; check toolchain availability.
- **Missing C compiler:** install host build tools for native orchestrator cgo builds.
- **Ollama unavailable:** verify its daemon, selected model, and container-reachable listener. For native API development, set `YUMMI_OLLAMA_URL` explicitly; its native default is empty. An endpoint override replaces the model backend instead.
- **External commands blocked:** inspect GATEWAY mode and scope. `block_all` denies forwarded egress including DNS even when allow rules exist, and returns on every gateway restart.
- **Linux socket permission error:** confirm Docker daemon access and your current login's group membership; inspect SELinux policy where applicable.
- **Engine forwarding errors:** compare the historical engine notes above with current configuration and packet evidence before attributing a timeout to the engine.
