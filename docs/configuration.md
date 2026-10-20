[Documentation index](README.md) · [Installation](installation.md)

# Configuration reference

Configuration has separate layers. A variable being read by Go does not mean the checked-in Compose file forwards it into the container.

## Files and precedence

| Layer | Role |
|---|---|
| `.yummi.conf` | Gitignored **Make syntax**, included by the root Makefile. `make conf` rewrites this file. Explicit `make NAME=value` assignments win; file assignments normally override inherited shell variables. |
| [`.yummi.conf.example`](../.yummi.conf.example) | Example Make settings; no real keys. It is not loaded automatically. |
| [Compose](../docker-compose.yml) | Defines container environment, mounts, topology and build arguments. Direct `docker compose` reads its own environment/`.env`, not `.yummi.conf`. |
| `~/.yummi/config.yaml` | Optional root CLI lifecycle settings (`runtime.compose_file`, `runtime.sandbox_service`, `runtime.preferred_shell`); not the orchestrator/gateway configuration. `YUMMI_HOME` changes this location. |
| [Gateway YAML](../gateway/config/default.yaml) | Startup network scope, policy, IPC, audit, watchdog and fingerprint settings; copied into the gateway image. |
| `brain/.env` | Independent optional Python Brain settings, loaded by python-dotenv; existing process environment takes precedence. |

Use [Makefile](../Makefile), [orchestrator config loader](../orchestrator/internal/config/config.go) and [startup wiring](../orchestrator/cmd/orchestrator/main.go) together when changing settings. Read executable wiring as well as field comments when checking effective behavior.

## Make and wizard defaults

| Setting | Unconfigured Make/Compose value | `make conf` behavior |
|---|---|---|
| `CONTAINER_ENGINE` | `docker` | Detects/selects Docker or Podman. OrbStack is a Docker context, not a third engine value. |
| `YUMMI_NETWORK_MODE` | `secure` | Selects `secure` for OrbStack/native Docker; offers the explicitly confirmed `insecure-bridge` workaround for Docker Desktop/Podman. |
| `YUMMI_LANG` | `en` | Offers English/Turkish, suggesting from shell `LANG`. |
| `YUMMI_AGENT_BACKEND` | `graph` | Writes `eino` for local, cloud and custom model choices. |
| `YUMMI_OLLAMA_MODEL` | `gpt-oss:20b` | Local blank input chooses `gpt-oss:120b`. |
| Cloud endpoint/model | No override | Cloud choice writes OpenRouter base URL/provider and defaults model name to `deepseek/deepseek-chat-v3.1`; custom choice asks for base URL/model/key. |

Those model names are configuration defaults and dated project choices, not comparative recommendations or guarantees of autonomous reliability.

`make start` builds images only if missing or `BUILD=1` is given. After source, gateway YAML, or UI-language changes, use `make start BUILD=1`. Runtime environment changes need container recreation; UI language needs a new bundle. `ui/Dockerfile` builds its own bundle, so native `ui/dist` is not a Docker input.

## Model configuration

| Variable | Native loader default / meaning |
|---|---|
| `YUMMI_OLLAMA_URL` | Empty disables the native Ollama chat backend unless an endpoint override is configured. Compose sets `http://host.docker.internal:11434`. |
| `YUMMI_OLLAMA_MODEL` | `gpt-oss:20b`; an explicit tag wins over tier model selection. |
| `YUMMI_OLLAMA_TIER` | Unset; supported presets are `standard` (`gpt-oss:20b`) and `expert` (`gpt-oss:120b`). |
| `YUMMI_OLLAMA_NUM_CTX` | `0`: use resolved profile. Current presets use 32768 tokens. |
| `YUMMI_OLLAMA_THINKING` | Unset: use profile (current presets false). |
| `YUMMI_OLLAMA_FALLBACK_MODEL` | Empty: no local fallback model. Native loader setting; not forwarded by checked-in Compose. |
| `YUMMI_OLLAMA_EMBED_MODEL` | `nomic-embed-text`; embeddings need the model installed locally. Retrieval degrades to structured lookup if unavailable. Not forwarded by checked-in Compose. |
| `YUMMI_EINO_MODEL_BASE_URL` | Empty: Eino derives Ollama URL plus `/v1`. An explicit URL must contain the required path prefix. |
| `YUMMI_EINO_MODEL_NAME` | Empty: inherits configured model name. |
| `YUMMI_EINO_MODEL_API_KEY` | Empty: no supplied provider key. |
| `YUMMI_EINO_PROVIDER` | Empty: infer `ollama`, `openrouter` or `openai` from endpoint/key; setting provider alone does not choose an endpoint. |
| `YUMMI_EINO_MODEL_NUM_CTX` | `0`: inherit resolved profile. |
| `YUMMI_EINO_MODEL_MAX_TOKENS` | `0`: use client policy (8192 for recognized OpenRouter/OpenAI cloud URLs, no explicit cap for local URLs). Native loader setting; not forwarded by checked-in Compose. |

Despite the prefix, an explicit `YUMMI_EINO_MODEL_BASE_URL` changes **conductor/chat and graph planning/chat as well as the Eino tool-calling loop**. The Brain has a separate configuration below. Hosted inference sends supplied context to that provider; sandbox egress policy does not govern orchestrator-originated model traffic. Choose the local endpoint when local-only inference is required.

## Operator, storage and execution settings

These are native-loader defaults. Compose supplies fixed overrides where shown; assigning an unforwarded variable in `.yummi.conf` does not change the container setting.

| Variable | Native default | Compose setting / note |
|---|---|---|
| `YUMMI_ORCHESTRATOR_HOST` / `_PORT` | `127.0.0.1` / `9080` | `0.0.0.0` / `9080` inside the container; no orchestrator host port publication. |
| `YUMMI_OPERATOR_ALLOWED_PEERS` | `127.0.0.1/32,::1/128` | Adds exactly UI `172.21.0.4/32`; authorization uses the socket peer, local Host and Origin checks. |
| `YUMMI_SANDBOX_POLICY_ADDR` | Empty: listener disabled | `172.20.0.3:9081`; read-only hostname and literal-IP policy checks. |
| `YUMMI_HOME` | User home plus `.yummi` | Native root for default data/TLS/generated skills. |
| `YUMMI_DB_PATH` | `$YUMMI_HOME/sessions.db` | `/var/lib/yummi/orchestrator/sessions.db` in named volume. |
| `YUMMI_PERSONA_DIR` | Existing `persona/v0.1` or `../persona/v0.1` | `/persona/v0.1`, mounted read-only. |
| `YUMMI_AUTO_SKILL_DIR` | `$YUMMI_HOME/skills` | `/var/lib/yummi/orchestrator/skills`, writable approved-skill store. |
| `YUMMI_DOCKER_SOCKET` | First existing local socket candidate, else `/var/run/docker.sock` | Orchestrator uses `DOCKER_HOST=tcp://docker-proxy:2375`; only broker mounts the host socket. |
| `YUMMI_SANDBOX_CONTAINER` | `yummi-sandbox` | Same fixed name passed to broker and orchestrator. |
| `YUMMI_SANDBOX_DESKTOP_ADDR` | `sandbox:6080` | Default; not host-published. |
| `YUMMI_SANDBOX_EXEC_TIMEOUT` | Unset/invalid: bridge default 10 minutes | Not forwarded by checked-in Compose. |
| `YUMMI_GATEWAY_SOCKET` | `/run/yummi/gateway.sock` | Read-only IPC volume mounted in orchestrator. |
| `YUMMI_GATEWAY_TLS` / `YUMMI_TLS_DIR` | `false` / `$YUMMI_HOME/tls` | TLS scaffolding is not an operational Rust IPC TLS contract; leave disabled for current Unix IPC. Not forwarded by Compose. |
| `YUMMI_DATA_RETENTION_DAYS` | `0`: disabled | Forwarded; positive values enable pruning of supported log/history tables, not blanket deletion of knowledge or gateway audit. |

The host publishes `127.0.0.1:3000` and `127.0.0.1:9080` through UI nginx. This is a local single-operator access model without login/RBAC. The sandbox cannot access operator mutation routes through its policy listener.

## Sandbox research and browser

`YUMMI_WEB_SEARCH_ENABLED` (default `false`), `YUMMI_WEB_SEARCH_MODEL` and `YUMMI_WEB_SEARCH_API_KEY` are exported by Make and forwarded only into the sandbox. Enabled search requires configured model/key. Provider HTTP, DNS, source fetching and content parsing execute there, under gateway policy; provider credentials are not tool arguments. The operator web-search toggle is an additional runtime state, not permission to open gateway egress. See [Web Research](agent/web-research.md).

Browser environment such as `YUMMI_BROWSER_HEADLESS` belongs inside the sandbox and is not currently interpolated from the host by Compose. Discover per-call schemas through MCP `tools/list`. See [Browser Automation](agent/browser-automation.md) for actual mode reporting and reset behavior.

Gateway IP/hostname scope is configured in its YAML and through supported operator runtime rule endpoints; it is not an orchestrator `YUMMI_SCOPE_*` setting. Runtime mode resets to `block_all` on restart; local/same-subnet traffic is outside the forwarding boundary. See [Security Model](security-model.md) for policy-versus-kernel limits.

## Optional Python Brain

| Variable | Python loader default |
|---|---|
| `YUMMI_ORCHESTRATOR_URL` | `http://127.0.0.1:9080` |
| `YUMMI_BRAIN_MODEL_BASE_URL` | `https://openrouter.ai/api/v1` |
| `YUMMI_BRAIN_MODEL_NAME` | `anthropic/claude-3.5-sonnet` (the wizard can write a different default) |
| `YUMMI_BRAIN_MODEL_API_KEY` | Required, nonempty |
| `YUMMI_BRAIN_SESSION_ID` | `brain-default` |
| `YUMMI_BRAIN_CHECKPOINT_DB` | `yummi_brain_checkpoints.sqlite` |

Make exports the Brain model settings and session ID. For variables not exported by Make, supply them in the process environment or `brain/.env`. These settings do not change the Go model configuration. `make brain-install` currently checks for an API key as well as `uv`; direct `cd brain && uv sync` installs dependencies without that Make guard. See [Brain](../brain/README.md) for its operator API authority and lifecycle differences.
