SHELL      := /bin/bash
MAKEFLAGS  += --no-print-directory
.DEFAULT_GOAL := help

# ── Project identity ──────────────────────────────────────────────────────────
BINARY           := yummi
ORCHESTRATOR_BINARY        := yummi-orchestrator
BIN_DIR          := ./bin
CLI_PKG          := ./cmd/yummi
ORCHESTRATOR_PKG           := ./cmd/orchestrator
GATEWAY_DIR        := gateway
UI_DIR           := ui
UI_DIST_DIR      := $(UI_DIR)/dist

# ── Source file collections for incremental builds ───────────────────────────
CLI_SRCS          := $(shell find cmd/yummi internal -type f -name '*.go' 2>/dev/null) go.mod go.sum
ORCHESTRATOR_SRCS := $(shell find orchestrator -type f -name '*.go' 2>/dev/null) orchestrator/go.mod orchestrator/go.sum
SANDBOX_MCP_SRCS  := $(shell find sandbox-mcp -type f -name '*.go' 2>/dev/null) sandbox-mcp/go.mod sandbox-mcp/go.sum
GATEWAY_SRCS      := $(shell find $(GATEWAY_DIR)/src $(GATEWAY_DIR)/config -type f 2>/dev/null) $(GATEWAY_DIR)/Cargo.toml $(GATEWAY_DIR)/Cargo.lock
UI_SRCS           := $(shell find $(UI_DIR)/src $(UI_DIR)/public -type f 2>/dev/null) $(UI_DIR)/index.html $(UI_DIR)/vite.config.ts $(wildcard $(UI_DIR)/tsconfig*.json) $(wildcard .yummi.conf)


# ── Local configuration (written by `make conf`; gitignored, per-machine) ────
# Run `make conf` once to pick your container engine setup interactively; it
# persists CONTAINER_ENGINE / YUMMI_NETWORK_MODE / YUMMI_OLLAMA_MODEL / etc.
# here so you never have to pass them on the command line again. Command-line
# overrides (e.g. `make start CONTAINER_ENGINE=podman`) still win over this
# file. The `-` prefix means "don't complain if the file doesn't exist yet".
# MUST come before OLLAMA_MODEL below (or anything else reading
# YUMMI_OLLAMA_MODEL/YUMMI_OLLAMA_TIER via `:=`, which expands immediately at
# the point it's written) — otherwise a value `make conf` just saved here
# would be invisible to that immediate expansion, silently ignored on the
# very next `make start`.
-include .yummi.conf

OLLAMA_MODEL    ?= $(if $(YUMMI_OLLAMA_MODEL),$(YUMMI_OLLAMA_MODEL),$(if $(filter expert,$(YUMMI_OLLAMA_TIER)),gpt-oss:120b,gpt-oss:20b))
LIVE_TEST_MODEL ?= qwen3:8b
OLLAMA_URL      := http://localhost:11434
export YUMMI_OLLAMA_MODEL := $(OLLAMA_MODEL)
export YUMMI_OLLAMA_NUM_CTX
export YUMMI_OLLAMA_THINKING
export YUMMI_DATA_RETENTION_DAYS
# Optional: point only the Eino agent backend at a different OpenAI-compatible
# server/model than Ollama (e.g. a local LM Studio instance) — see
# docker-compose.yml's orchestrator env block and config.Config's doc
# comments. All blank by default: no effect unless set in the environment or
# .yummi.conf.
export YUMMI_EINO_MODEL_BASE_URL
export YUMMI_EINO_MODEL_NAME
export YUMMI_EINO_MODEL_API_KEY
export YUMMI_EINO_PROVIDER
export YUMMI_EINO_MODEL_NUM_CTX
export YUMMI_WEB_SEARCH_ENABLED
export YUMMI_WEB_SEARCH_MODEL
export YUMMI_WEB_SEARCH_API_KEY
# The LangGraph brain (brain/) is a fully separate Python process — see
# brain/README.md. It never reads YUMMI_OLLAMA_*/YUMMI_EINO_* (those govern
# the Go orchestrator's own conductor/eino backends); these are its own,
# independent model config, exported the same way for the same reason: so
# `make conf` writing them to .yummi.conf is enough, no flag repeated by
# hand on every `make brain-run`. All blank by default — brain-run fails
# fast with a clear message if YUMMI_BRAIN_MODEL_API_KEY is unset, same
# fail-fast discipline as brain/src/yummi_brain/config.py's own load().
export YUMMI_BRAIN_MODEL_BASE_URL
export YUMMI_BRAIN_MODEL_NAME
export YUMMI_BRAIN_MODEL_API_KEY
export YUMMI_BRAIN_SESSION_ID

# ── Container engine selection ────────────────────────────────────────────────
# CONTAINER_ENGINE: docker (default) or podman. Both speak the same `compose`
# subcommand and the same docker-compose.yml — see docs/installation.md for
# per-engine caveats (Docker Desktop's VM networking bug, Podman's two gaps).
# Usage: make start CONTAINER_ENGINE=podman  (or `make conf` to set it once)
CONTAINER_ENGINE ?= docker
COMPOSE          := $(CONTAINER_ENGINE) compose

# YUMMI_NETWORK_MODE: secure (default) or insecure-bridge.
# insecure-bridge removes yummi_internal's `internal: true` flag — a real
# security-relevant trade-off, not a default, and gated behind an explicit
# interactive confirmation in `_check-network-mode` below — asked once, when
# `make conf` actually sets the mode, not re-asked on every later command
# that just reuses the already-confirmed, already-persisted choice. Only reach for
# this if you're stuck on an engine affected by the networking bugs
# documented in docs/installation.md and can't switch to OrbStack.
# Usage: make start YUMMI_NETWORK_MODE=insecure-bridge  (or `make conf`)
YUMMI_NETWORK_MODE ?= secure
COMPOSE_FILES := -f docker-compose.yml
ifeq ($(YUMMI_NETWORK_MODE),insecure-bridge)
COMPOSE_FILES += -f docker-compose.override.insecure-bridge.yml
endif

# YUMMI_LANG: en (default) or tr — the UI's build-time display language.
# There is no runtime switch (see ui/src/shared/i18n/strings.ts): changing
# this requires `make conf` + a rebuild. Propagated to every UI build path as
# VITE_YUMMI_LANG — the native path (_build-ui, dev-ui) via this exported env
# var, the Docker path (yummi-ui image) via docker-compose.yml's build arg,
# which reads the same YUMMI_LANG from the environment/.yummi.conf.
YUMMI_LANG ?= en
export VITE_YUMMI_LANG := $(YUMMI_LANG)

# YUMMI_AGENT_BACKEND: graph (default) or eino — which agentic engine the
# orchestrator uses. graph is the tag-protocol state machine (best for
# small/local models); eino is native-tool-calling (best for larger models
# with reliable function-calling). Runtime env var, read by main.go — unlike
# YUMMI_LANG this does not need a UI rebuild, just exported before `make start`.
YUMMI_AGENT_BACKEND ?= graph
export YUMMI_AGENT_BACKEND

VERSION    ?= v1.0.1b
GIT_SHA    := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS    := -s -w \
	-X github.com/n0connect/yummi/internal/version.Semver=$(VERSION) \
	-X github.com/n0connect/yummi/internal/version.GitSHA=$(GIT_SHA) \
	-X github.com/n0connect/yummi/internal/version.BuildDate=$(BUILD_DATE)

# ── ANSI colors (disabled if NO_COLOR is set) ─────────────────────────────────
ifndef NO_COLOR
  RESET  := \033[0m
  BOLD   := \033[1m
  DIM    := \033[2m
  CYAN   := \033[36m
  GREEN  := \033[32m
  YELLOW := \033[33m
  RED    := \033[31m
  WHITE  := \033[97m
else
  RESET  :=
  BOLD   :=
  DIM    :=
  CYAN   :=
  GREEN  :=
  YELLOW :=
  RED    :=
  WHITE  :=
endif

define log_info
printf "$(CYAN)[INFO]$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_pass
printf "$(GREEN)[PASS]$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_error
printf "$(RED)[ERROR]$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_warn
printf "$(YELLOW)[WARN]$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_section
printf "\n$(BOLD)$(CYAN)[INFO] ══ $(1) ══$(RESET)\n"
endef

# ── Phony declarations ────────────────────────────────────────────────────────
.PHONY: help conf \
	build start rebase stop clean force-restart close \
	_check-deps _check-network-mode _build-cli _build-ui _build-orchestrator _build-gateway _build-sandbox-mcp \
	test test-agent-live test-agent-eval test-agent-benchmark bench-target-up bench-target-down \
	lab-target-up lab-target-down \
	lint vuln doctor install dev dev-ui dev-orchestrator \
	app \
	_test-cli _test-orchestrator _test-gateway _test-ui _test-sandbox-mcp _test-brain \
	docker-build docker-orchestrator docker-ui \
	_container-ensure _ollama-ensure \
	_check-brain-deps brain-install brain-run brain-mcp

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  DEPENDENCY GUARD
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
_check-deps:
	@$(call log_section,Dependency Validation)
	@$(call log_info,System,Checking required cross-platform tools...)
	@missing_count=0; \
	missing_tools=""; \
	if command -v go >/dev/null 2>&1; then \
		$(call log_pass,Dependencies,Go is installed.); \
	else \
		$(call log_error,Dependencies,Go is not installed.); \
		missing_count=$$((missing_count + 1)); \
		missing_tools="$$missing_tools\n    - Go: https://golang.org/dl/ (or 'brew install go' / 'apt install golang')"; \
	fi; \
	if command -v cargo >/dev/null 2>&1; then \
		$(call log_pass,Dependencies,Rust/Cargo is installed.); \
	else \
		$(call log_error,Dependencies,Rust/Cargo is not installed.); \
		missing_count=$$((missing_count + 1)); \
		missing_tools="$$missing_tools\n    - Rust: https://rustup.rs/ (or 'brew install rust' / 'apt install cargo')"; \
	fi; \
	if command -v npm >/dev/null 2>&1; then \
		$(call log_pass,Dependencies,Node.js/npm is installed.); \
	else \
		$(call log_error,Dependencies,Node.js/npm is not installed.); \
		missing_count=$$((missing_count + 1)); \
		missing_tools="$$missing_tools\n    - Node.js & npm: https://nodejs.org/ (or 'brew install node' / 'apt install nodejs npm')"; \
	fi; \
	if command -v $(CONTAINER_ENGINE) >/dev/null 2>&1; then \
		$(call log_pass,Dependencies,$(CONTAINER_ENGINE) is installed.); \
	else \
		$(call log_error,Dependencies,$(CONTAINER_ENGINE) is not installed.); \
		missing_count=$$((missing_count + 1)); \
		missing_tools="$$missing_tools\n    - $(CONTAINER_ENGINE): Docker (https://docker.com/ or 'brew install --cask orbstack') / Podman (https://podman.io/)"; \
	fi; \
	if $(CONTAINER_ENGINE) compose version >/dev/null 2>&1; then \
		$(call log_pass,Dependencies,$(CONTAINER_ENGINE) compose is available.); \
	else \
		$(call log_error,Dependencies,$(CONTAINER_ENGINE) compose is not available.); \
		missing_count=$$((missing_count + 1)); \
		missing_tools="$$missing_tools\n    - $(CONTAINER_ENGINE) compose: Docker Compose v2 plugin required (see docs/installation.md)"; \
	fi; \
	if command -v curl >/dev/null 2>&1; then \
		$(call log_pass,Dependencies,curl is installed.); \
	else \
		$(call log_error,Dependencies,curl is not installed.); \
		missing_count=$$((missing_count + 1)); \
		missing_tools="$$missing_tools\n    - curl: required for HTTP health checks and Ollama verification"; \
	fi; \
	if [ "$$missing_count" -gt 0 ]; then \
		printf "\n$(RED)[ERROR]$(RESET) $(BOLD)Dependencies$(RESET) $(DIM)%d required tool(s) missing:$(RESET)\n" "$$missing_count"; \
		printf "$$missing_tools\n\n"; \
		exit 1; \
	fi; \
	$(call log_pass,System,All required cross-platform tools are present.)

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  NETWORK-MODE GUARD  (opt-in security trade-off, never silent)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
_check-network-mode:
ifeq ($(YUMMI_NETWORK_MODE),insecure-bridge)
	@printf "\n$(RED)$(BOLD)[WARNING] YUMMI_NETWORK_MODE=insecure-bridge$(RESET)\n"
	@printf "$(RED)This removes docker-compose.yml's \`internal: true\` flag from yummi_internal.$(RESET)\n"
	@printf "$(RED)sandbox's network stops being architecturally incapable of reaching the\n"
	@printf "outside world — containment now depends ENTIRELY on gateway's nftables rules\n"
	@printf "staying correct. One fewer independent fail-closed layer, not zero security,\n"
	@printf "but a real, permanent reduction for as long as this mode is used.$(RESET)\n\n"
	@printf "$(YELLOW)This exists to work around engine-specific networking bugs — see\n"
	@printf "docs/installation.md's Docker Desktop and Podman callouts. If you can switch\n"
	@printf "to OrbStack instead, that fixes the Docker Desktop case with zero trade-offs.$(RESET)\n\n"
ifeq ($(CONTAINER_ENGINE),podman)
	@printf "$(YELLOW)CONTAINER_ENGINE=podman: this flag fixes Podman's DNS-forwarding gap, but\n"
	@printf "NOT its separate docker-proxy socket issue — sandbox exec may still fail.\n"
	@printf "See docs/installation.md#podman-macos-gaps.$(RESET)\n\n"
endif
ifneq ($(YUMMI_NETWORK_MODE_CONFIRM),yes)
	@printf "$(BOLD)Type exactly 'I UNDERSTAND' to continue, anything else aborts: $(RESET)"; \
	read ans; \
	[ "$$ans" = "I UNDERSTAND" ] || { $(call log_error,network-mode,Aborted — confirmation not given.); exit 1; }
else
	@$(call log_info,network-mode,YUMMI_NETWORK_MODE_CONFIRM=yes — skipping interactive confirmation.)
endif
	@$(call log_pass,network-mode,Proceeding with insecure-bridge network mode.)
endif

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  CONTAINER-ENGINE RUNTIME GUARD  (daemon connectivity & auto-bootstrap)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
_container-ensure:
	@$(call log_section,Container Engine Verification)
	@$(call log_info,$(CONTAINER_ENGINE),Checking daemon connectivity...)
	@if $(CONTAINER_ENGINE) info >/dev/null 2>&1; then \
		$(call log_pass,$(CONTAINER_ENGINE),Daemon is running and responsive); \
	else \
		$(call log_info,$(CONTAINER_ENGINE),Daemon not responding — attempting auto-start...); \
		started=0; \
		if [ "$$(uname -s)" = "Darwin" ]; then \
			ctx="$$($(CONTAINER_ENGINE) context show 2>/dev/null || true)"; \
			if [ "$$ctx" = "orbstack" ] && [ -d "/Applications/OrbStack.app" ]; then \
				$(call log_info,$(CONTAINER_ENGINE),Launching OrbStack (matching active context '$$ctx')...); \
				open -g -a OrbStack >/dev/null 2>&1 || open -a OrbStack >/dev/null 2>&1 || true; \
				started=1; \
			elif [ "$$ctx" = "desktop-linux" ] && [ -d "/Applications/Docker.app" ]; then \
				$(call log_info,$(CONTAINER_ENGINE),Launching Docker Desktop (matching active context '$$ctx')...); \
				open -g -a Docker >/dev/null 2>&1 || open -a Docker >/dev/null 2>&1 || true; \
				started=1; \
			elif [ -d "/Applications/OrbStack.app" ]; then \
				$(call log_info,$(CONTAINER_ENGINE),Launching OrbStack...); \
				open -g -a OrbStack >/dev/null 2>&1 || open -a OrbStack >/dev/null 2>&1 || true; \
				started=1; \
			elif [ -d "/Applications/Docker.app" ]; then \
				$(call log_info,$(CONTAINER_ENGINE),Launching Docker Desktop...); \
				open -g -a Docker >/dev/null 2>&1 || open -a Docker >/dev/null 2>&1 || true; \
				started=1; \
			elif [ "$(CONTAINER_ENGINE)" = "podman" ] && command -v podman >/dev/null 2>&1; then \
				$(call log_info,$(CONTAINER_ENGINE),Starting podman machine...); \
				podman machine start >/dev/null 2>&1 || true; \
				started=1; \
			fi; \
		elif command -v systemctl >/dev/null 2>&1; then \
			systemctl --user start $(CONTAINER_ENGINE) >/dev/null 2>&1 || sudo systemctl start $(CONTAINER_ENGINE) >/dev/null 2>&1 || true; \
			started=1; \
		fi; \
		if [ "$$started" = "1" ]; then \
			$(call log_info,$(CONTAINER_ENGINE),Waiting for $(CONTAINER_ENGINE) API socket (up to 30s)...); \
			for i in $$(seq 1 30); do \
				if $(CONTAINER_ENGINE) info >/dev/null 2>&1; then \
					$(call log_pass,$(CONTAINER_ENGINE),Daemon is ready); \
					exit 0; \
				fi; \
				sleep 1; \
			done; \
		fi; \
		$(call log_error,$(CONTAINER_ENGINE),Cannot connect to $(CONTAINER_ENGINE) daemon.); \
		sock="$$($(CONTAINER_ENGINE) context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || echo unknown)"; \
		printf "  $(YELLOW)Active endpoint: %s$(RESET)\n" "$$sock"; \
		printf "  $(YELLOW)Please ensure OrbStack, Docker Desktop, or the $(CONTAINER_ENGINE) daemon is running.$(RESET)\n"; \
		if [ "$$(uname -s)" = "Darwin" ]; then \
			printf "  $(YELLOW)Tip: On macOS run 'open -a OrbStack' or 'open -a Docker' or check 'docker context ls'.$(RESET)\n"; \
		fi; \
		exit 1; \
	fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  USER COMMANDS  (the ones you run every day)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## conf    Interactive one-time setup, kept to the fewest questions that
##         actually need a human: container engine and UI language are
##         auto-detected and only asked about when detection genuinely can't
##         tell; picking a model is ONE path (local: a model name, cloud: an
##         API key) with everything else — network mode, agent backend —
##         decided for you. Persists to .yummi.conf so `make build`/
##         `make start`/`make brain-run`/etc. never need those flags again.
##         Re-run anytime to change any of it.
conf:
	@$(call log_section,Yummi Configuration)
	@detected=""; detected_label=""; \
	if [ "$$(uname -s)" = "Darwin" ]; then \
		ctx="$$(docker context show 2>/dev/null || true)"; \
		if [ "$$ctx" = "orbstack" ] || [ -d "/Applications/OrbStack.app" ]; then detected=1; detected_label="OrbStack"; \
		elif [ "$$ctx" = "desktop-linux" ] || [ -d "/Applications/Docker.app" ]; then detected=2; detected_label="Docker Desktop"; \
		elif command -v podman >/dev/null 2>&1; then detected=3; detected_label="Podman"; \
		fi; \
	else \
		if command -v podman >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; then detected=3; detected_label="Podman"; \
		elif command -v docker >/dev/null 2>&1; then detected=4; detected_label="Linux native Docker Engine"; \
		fi; \
	fi; \
	if [ -n "$$detected" ]; then \
		case "$$detected" in \
			1) engine=docker; mode=secure ;; \
			2) engine=docker; mode=insecure-bridge ;; \
			3) engine=podman; mode=insecure-bridge ;; \
			4) engine=docker; mode=secure ;; \
		esac; \
		printf "$(GREEN)[PASS]$(RESET) $(BOLD)conf$(RESET) $(DIM)Container engine: %s (detected)$(RESET)\n" "$$detected_label"; \
	else \
		printf "$(BOLD)How are you running containers on this machine?$(RESET)$(DIM) (nothing detected automatically)$(RESET)\n\n"; \
		printf "  $(CYAN)1)$(RESET) OrbStack                    $(DIM)recommended — no known trade-offs$(RESET)\n"; \
		printf "  $(CYAN)2)$(RESET) Docker Desktop\n"; \
		printf "  $(CYAN)3)$(RESET) Podman\n"; \
		printf "  $(CYAN)4)$(RESET) Linux native Docker Engine   $(DIM)no VM, no known bug$(RESET)\n\n"; \
		printf "$(BOLD)Choice [1-4]: $(RESET)"; \
		read choice; \
		case "$$choice" in \
			1) engine=docker; mode=secure ;; \
			2) engine=docker; mode=insecure-bridge ;; \
			3) engine=podman; mode=insecure-bridge ;; \
			4) engine=docker; mode=secure ;; \
			*) printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Invalid choice '%s' — expected 1-4.$(RESET)\n" "$$choice"; exit 1 ;; \
		esac; \
	fi; \
	if [ "$$mode" = "insecure-bridge" ]; then \
		$(MAKE) _check-network-mode CONTAINER_ENGINE=$$engine YUMMI_NETWORK_MODE=insecure-bridge || exit 1; \
	fi; \
	lang_detected=en; \
	case "$$LANG" in tr_*|*.tr|tr) lang_detected=tr ;; esac; \
	printf "\n$(BOLD)UI display language?$(RESET) $(DIM)(build-time only — changing it later needs 'make conf' + a rebuild)$(RESET)\n\n"; \
	printf "  $(CYAN)1)$(RESET) English\n"; \
	printf "  $(CYAN)2)$(RESET) Türkçe\n\n"; \
	if [ "$$lang_detected" = "tr" ]; then dl=2; else dl=1; fi; \
	printf "$(BOLD)Choice [1-2, blank=%s]: $(RESET)" "$$dl"; \
	read langchoice; \
	if [ -z "$$langchoice" ]; then langchoice="$$dl"; fi; \
	case "$$langchoice" in \
		1) lang=en ;; \
		2) lang=tr ;; \
		*) printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Invalid choice '%s' — expected 1-2.$(RESET)\n" "$$langchoice"; exit 1 ;; \
	esac; \
	backend=eino; model=""; einoprovider=""; einobaseurl=""; einomodel=""; einoapikey=""; \
	printf "\n$(BOLD)Local model or a cloud model?$(RESET)\n\n"; \
	printf "  $(CYAN)1)$(RESET) Local   $(DIM)— runs on your own machine via Ollama; just needs a model name$(RESET)\n"; \
	printf "  $(CYAN)2)$(RESET) Cloud   $(DIM)— OpenRouter/OpenAI/any frontier model; just needs an API key$(RESET)\n"; \
	printf "  $(CYAN)3)$(RESET) Custom endpoint   $(DIM)— a different OpenAI-compatible server (LM Studio, vLLM, ...)$(RESET)\n\n"; \
	printf "$(BOLD)Choice [1-3]: $(RESET)"; \
	read modeltypechoice; \
	case "$$modeltypechoice" in \
		1) printf "\n$(BOLD)Ollama model name$(RESET) $(DIM)(a 20B-class model is fine for trying the chat flow, but is NOT\n"; \
		   printf "reliable for autonomous multi-step tasks — see persona/README.md. For real autonomous use, this\n"; \
		   printf "needs to be 120B+, e.g. gpt-oss:120b.)$(RESET)\n"; \
		   printf "Model tag [blank = gpt-oss:120b]: "; \
		   read model; \
		   if [ -z "$$model" ]; then model=gpt-oss:120b; fi ;; \
		2) printf "\n$(BOLD)OpenRouter API key$(RESET) $(DIM)(input hidden as you type; stored in gitignored .yummi.conf, never committed)$(RESET): "; \
		   read -s einoapikey; printf "\n"; \
		   if [ -z "$$einoapikey" ]; then printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)API key cannot be empty.$(RESET)\n"; exit 1; fi; \
		   einoprovider=openrouter; einobaseurl="https://openrouter.ai/api/v1"; \
		   printf "\n$(BOLD)Model name$(RESET) $(DIM)[blank = deepseek/deepseek-chat-v3.1 — confirmed working end to end against\n"; \
		   printf "a real exploit chain; e.g. anthropic/claude-3.5-sonnet, openai/gpt-4o for something else]: $(RESET)"; \
		   read einomodel; \
		   if [ -z "$$einomodel" ]; then einomodel="deepseek/deepseek-chat-v3.1"; fi ;; \
		3) printf "\n$(BOLD)Base URL$(RESET) $(DIM)(include /v1 or whatever path prefix the server needs, e.g.\n"; \
		   printf "http://host.docker.internal:1234/v1 for LM Studio)$(RESET): "; \
		   read einobaseurl; \
		   if [ -z "$$einobaseurl" ]; then printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Base URL cannot be empty.$(RESET)\n"; exit 1; fi; \
		   printf "\n$(BOLD)Model name$(RESET): "; \
		   read einomodel; \
		   if [ -z "$$einomodel" ]; then printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Model name cannot be empty.$(RESET)\n"; exit 1; fi; \
		   printf "\n$(BOLD)API key$(RESET) $(DIM)(optional — press Enter if the server doesn't need one; hidden as you type)$(RESET): "; \
		   read -s einoapikey; printf "\n"; \
		   einoprovider=openai ;; \
		*) printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Invalid choice '%s' — expected 1-3.$(RESET)\n" "$$modeltypechoice"; exit 1 ;; \
	esac; \
	printf "\n$(BOLD)Set up the optional LangGraph brain too?$(RESET) $(DIM)(a separate Python agent, independent of the\n"; \
	printf "choice above — see brain/README.md. Skip if you only want the Go orchestrator's own agent.)$(RESET)\n"; \
	printf "Set it up now? [y/N]: "; \
	read setup_brain; \
	brainbaseurl=""; brainmodel=""; brainapikey=""; \
	if [ "$$setup_brain" = "y" ] || [ "$$setup_brain" = "Y" ]; then \
		if [ -n "$$einoapikey" ] && [ "$$einoprovider" = "openrouter" ]; then \
			brainbaseurl="$$einobaseurl"; brainmodel="$$einomodel"; brainapikey="$$einoapikey"; \
			printf "$(DIM)Reusing the OpenRouter key and model from above.$(RESET)\n"; \
		else \
			printf "\n$(BOLD)OpenRouter API key for the brain$(RESET) $(DIM)(hidden as you type; a different provider/endpoint\n"; \
			printf "can be set by hand afterward — see .yummi.conf.example)$(RESET): "; \
			read -s brainapikey; printf "\n"; \
			if [ -z "$$brainapikey" ]; then printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)API key cannot be empty.$(RESET)\n"; exit 1; fi; \
			brainbaseurl="https://openrouter.ai/api/v1"; \
			printf "\n$(BOLD)Model name$(RESET) [blank = deepseek/deepseek-chat-v3.1]: "; \
			read brainmodel; \
			if [ -z "$$brainmodel" ]; then brainmodel="deepseek/deepseek-chat-v3.1"; fi; \
		fi; \
	fi; \
	{ \
		printf "CONTAINER_ENGINE=%s\n" "$$engine"; \
		printf "YUMMI_NETWORK_MODE=%s\n" "$$mode"; \
		printf "YUMMI_LANG=%s\n" "$$lang"; \
		printf "YUMMI_AGENT_BACKEND=%s\n" "$$backend"; \
		printf "YUMMI_OLLAMA_MODEL=%s\n" "$$model"; \
		if [ -n "$$einobaseurl" ]; then \
			printf "YUMMI_EINO_MODEL_BASE_URL=%s\n" "$$einobaseurl"; \
			printf "YUMMI_EINO_MODEL_NAME=%s\n" "$$einomodel"; \
			printf "YUMMI_EINO_MODEL_API_KEY=%s\n" "$$einoapikey"; \
			printf "YUMMI_EINO_PROVIDER=%s\n" "$$einoprovider"; \
		fi; \
		if [ -n "$$brainbaseurl" ]; then \
			printf "YUMMI_BRAIN_MODEL_BASE_URL=%s\n" "$$brainbaseurl"; \
			printf "YUMMI_BRAIN_MODEL_NAME=%s\n" "$$brainmodel"; \
			printf "YUMMI_BRAIN_MODEL_API_KEY=%s\n" "$$brainapikey"; \
		fi; \
	} > .yummi.conf; \
	printf "\n$(GREEN)[PASS]$(RESET) $(BOLD)conf$(RESET) $(DIM)Saved to .yummi.conf$(RESET)\n"; \
	printf "$(DIM)  engine=$$engine  network=$$mode  lang=$$lang  backend=$$backend$(RESET)\n"; \
	if [ -n "$$model" ]; then printf "$(DIM)  local_model=$$model$(RESET)\n"; fi; \
	if [ -n "$$einomodel" ]; then printf "$(DIM)  cloud=$$einoprovider/$$einomodel$(RESET)\n"; fi; \
	if [ -n "$$brainmodel" ]; then printf "$(DIM)  brain=$$brainmodel @ $$brainbaseurl$(RESET)\n"; fi; \
	printf "$(DIM)Every 'make build'/'make start'/'make brain-run'/etc. now uses this automatically. Run 'make conf' again to change it.$(RESET)\n\n"

## build   Build native binaries and UI (CLI + orchestrator + gateway + UI).
build: _check-deps $(BIN_DIR)/$(BINARY) $(UI_DIST_DIR)/index.html $(BIN_DIR)/$(ORCHESTRATOR_BINARY) $(GATEWAY_DIR)/target/release/yummi-gateway $(BIN_DIR)/yummi-sandbox-mcp
	@$(call log_section,Build Complete)
	@$(call log_pass,yummi,$(BIN_DIR)/$(BINARY))
	@$(call log_pass,yummi-orchestrator,$(BIN_DIR)/$(ORCHESTRATOR_BINARY))
	@$(call log_pass,gateway,$(GATEWAY_DIR)/target/release/yummi-gateway)
	@$(call log_pass,ui,$(UI_DIST_DIR))
	@printf "\n$(DIM)  version $(VERSION)  git $(GIT_SHA)  $(BUILD_DATE)$(RESET)\n"
	@printf "\n"

## start   Start Ollama + runtime services via compose (images built if
##         missing; pass BUILD=1 to force rebuild, or 'make rebase' after source edits).
##         UI is served at http://localhost:3000 — Yummi is web-managed only.
##         The sandbox is a single unified environment: the agent's exec
##         path and the browser Desktop tab (http://localhost:3000/desktop)
##         both run against it, no separate opt-in image/container.
##         Engine: CONTAINER_ENGINE=docker|podman (default docker).
##         Network trade-off: YUMMI_NETWORK_MODE=secure|insecure-bridge (default secure).
start: _runtime-deps _ollama-ensure
	@$(call log_section,Starting Services)
	@if ! $(CONTAINER_ENGINE) ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-ui$$'; then \
		if nc -z -w 1 127.0.0.1 3000 >/dev/null 2>&1; then \
			$(call log_warn,ports,Port 3000 is already in use by a host process outside Docker. Container port mapping may collide.); \
		fi; \
	fi
	@if ! $(CONTAINER_ENGINE) ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-orchestrator$$'; then \
		if nc -z -w 1 127.0.0.1 9080 >/dev/null 2>&1; then \
			$(call log_warn,ports,Port 9080 is already in use by a host process outside Docker. Container port mapping may collide.); \
		fi; \
	fi
	@need_build=0; \
	if [ "$$(echo $(BUILD))" = "1" ]; then \
		need_build=1; \
	else \
		for img in yummi/gateway:local yummi/sandbox:local yummi/orchestrator:local yummi/ui:local; do \
			if ! $(CONTAINER_ENGINE) image inspect "$$img" >/dev/null 2>&1; then \
				need_build=1; break; \
			fi; \
		done; \
	fi; \
	if [ "$$need_build" = "1" ]; then \
		$(call log_info,$(CONTAINER_ENGINE) compose,Building images (BUILD=1 or missing images)...); \
		set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) build gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to build images.); exit 1; }; \
		$(call log_pass,$(CONTAINER_ENGINE) compose,Images built successfully.); \
	else \
		$(call log_pass,$(CONTAINER_ENGINE) compose,Images already present — skipping build (use BUILD=1 or 'make rebase' to rebuild).); \
	fi
	@$(call log_info,$(CONTAINER_ENGINE) compose,Bringing up all services in background...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) up -d gateway sandbox orchestrator yummi-ui docker-proxy 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to start services.); exit 1; }
	@$(call log_pass,services,Running — UI on http://localhost:3000 — Desktop tab at http://localhost:3000/desktop)

# Docker startup needs no native Go/Rust/npm toolchains and must not launch
# host desktop applications when the Docker daemon is unavailable.
.PHONY: _runtime-deps
_runtime-deps:
	@command -v $(CONTAINER_ENGINE) >/dev/null || { printf 'Container engine unavailable\n'; exit 1; }
	@$(CONTAINER_ENGINE) info >/dev/null 2>&1 || { printf 'Container engine unavailable; start it explicitly\n'; exit 1; }

## rebase  Fast clean restart: down, rebuild images, up — Docker's OWN layer
##         cache does the work (go mod download / npm install layers are
##         skipped when go.sum/package-lock.json haven't changed), so a
##         source-only change rebuilds in seconds instead of minutes. Use
##         this instead of `make stop && make close && make clean && make
##         build && make start`: `make build` compiles native binaries to
##         ./bin and ui/dist that the Docker images never consume at all —
##         running it before `make start` is pure duplicate work, since
##         `make start` builds the images from source independently — and
##         neither `clean` nor `close` touch Docker's cache/images/volumes
##         in the first place, so that whole sequence buys nothing over this
##         one command except redoing the native build for no reason.
##         Never prunes Docker's build cache, images, or volumes (that's
##         what makes it fast) and never touches Ollama/local model
##         processes (unlike `close`) — reach for `make clean` or
##         `docker system prune` yourself if you actually need a
##         from-scratch rebuild.
rebase: _check-deps _container-ensure _ollama-ensure
	@$(call log_section,Rebase — fast clean restart)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Stopping containers (image cache untouched)...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) down --remove-orphans 2>&1 | sed 's/^/  /' || true
	@$(call log_info,$(CONTAINER_ENGINE) compose,Rebuilding images (Docker layer cache reused)...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) build gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to build images.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE) compose,Images built successfully.)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Bringing up all services in background...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) up -d gateway sandbox orchestrator yummi-ui docker-proxy 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to start services.); exit 1; }
	@$(call log_pass,services,Running — UI on http://localhost:3000)

## stop    Stop all runtime services
stop:
	@$(call log_section,Stopping Services)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Stopping all services...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) stop gateway sandbox orchestrator yummi-ui docker-proxy 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to stop services.); exit 1; }
	@$(call log_pass,services,All services stopped.)

## clean   Remove all build artifacts
clean:
	@$(call log_section,Clean)
	@$(call log_info,clean,Removing $(BIN_DIR)...)
	@rm -rf $(BIN_DIR)
	@$(call log_info,clean,Removing $(UI_DIST_DIR)...)
	@rm -rf $(UI_DIST_DIR)
	@$(call log_info,clean,Removing gateway release binary...)
	@rm -f $(GATEWAY_DIR)/target/release/yummi-gateway
	@$(call log_info,clean,Removing databases...)
	@find . -name "*.db" -not -path "*/.*" -not -path "./thirdy-party/*" -not -path "./node_modules/*" -type f -delete 2>/dev/null || true
	@$(call log_info,clean,Removing brain/ virtualenv and checkpoint databases...)
	@rm -rf brain/.venv
	@find brain -name "*.sqlite" -type f -delete 2>/dev/null || true
	@$(call log_pass,clean,Workspace is tidy.)

## force-restart  Stop services, kill memory hogs (ollama), and restart
force-restart: _check-deps _container-ensure
	@$(call log_section,Force Restart)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Stopping all services and removing orphans...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) down --remove-orphans 2>&1 | sed 's/^/  /' || true
	@$(call log_info,memory,Killing ollama to free VRAM...)
	@-killall -9 ollama 2>/dev/null || true
	@$(call log_info,memory,Killing orphaned ollama model runners...)
	@-killall -9 llama-server 2>/dev/null || true
	@$(call log_info,memory,Killing lingering yummi processes...)
	@-killall -9 yummi yummi-orchestrator 2>/dev/null || true
	@$(MAKE) _ollama-ensure
	@$(call log_info,$(CONTAINER_ENGINE) compose,Building images...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) build gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to build images.); exit 1; }
	@$(call log_info,$(CONTAINER_ENGINE) compose,Bringing up all services...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) up -d gateway sandbox orchestrator yummi-ui docker-proxy 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to start services.); exit 1; }
	@$(call log_pass,force-restart,Services forcibly restarted successfully.)

## close          Stop all services, Docker, LLM models, and free RAM
close:
	@$(call log_section,Close System)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Stopping all services...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) down --remove-orphans 2>&1 | sed 's/^/  /' || true
	@$(call log_info,memory,Stopping ollama service and freeing VRAM...)
	@brew services stop ollama 2>/dev/null || true
	@-killall -9 ollama 2>/dev/null || true
	@-killall -9 Ollama 2>/dev/null || true
	@$(call log_info,memory,Killing orphaned ollama model runners...)
	@-killall -9 llama-server 2>/dev/null || true
	@$(call log_info,memory,Killing lingering yummi processes...)
	@-killall -9 yummi yummi-orchestrator 2>/dev/null || true
	@$(call log_pass,close,All processes and models successfully stopped.)

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  DEVELOPER COMMANDS  (granular, fast feedback loops)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## test    Run all test suites (CLI + orchestrator + gateway + UI)
test: _check-deps _test-cli _test-orchestrator _test-gateway _test-ui _test-sandbox-mcp _test-brain
	@$(call log_section,All Tests Passed)
	@$(call log_pass,test,All suites completed successfully.)

## test-agent-live  End-to-end agentic loop against the REAL model + sandbox container
test-agent-live: _check-deps _container-ensure
	@$(call log_section,Agent Live E2E)
	@$(MAKE) _ollama-ensure OLLAMA_MODEL=$(LIVE_TEST_MODEL)
	@if ! $(CONTAINER_ENGINE) ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-sandbox$$'; then \
		$(call log_warn,agent live,yummi-sandbox container is NOT running — this run will SILENTLY SKIP the real test); \
		$(call log_warn,agent live,rather than actually exercising the model + sandbox path. Run 'make start' first.); \
	fi
	@$(call log_info,go test,Running real $(LIVE_TEST_MODEL) + sandbox container...)
	@# -count=1 disables go test's result cache — without it, a second run with
	@# nothing source-changed (e.g. after 'docker stop yummi-sandbox') silently
	@# replays yesterday's cached PASS instead of re-executing at all, since Go's
	@# cache doesn't know this test's outcome depends on live container state.
	@set -o pipefail; cd orchestrator && YUMMI_LIVE=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(LIVE_TEST_MODEL) \
		go test -count=1 ./internal/agent/ -run TestRunnerLive -v -timeout 15m 2>&1 | tee /tmp/yummi-test-agent-live.out | sed 's/^/  /' || { $(call log_error,agent live,Test failed.); exit 1; }
	@if grep -q '^--- SKIP' /tmp/yummi-test-agent-live.out; then \
		$(call log_warn,agent live,Test was SKIPPED, not actually run against real infra — see the SKIP reason above.); \
	else \
		$(call log_pass,agent live e2e,Test passed.); \
	fi
	@rm -f /tmp/yummi-test-agent-live.out

## test-agent-eval  Assertion-bearing multi-scenario quality gate against the REAL model + sandbox
test-agent-eval: _check-deps _container-ensure
	@$(call log_section,Agent Quality Evaluation)
	@$(MAKE) _ollama-ensure OLLAMA_MODEL=$(LIVE_TEST_MODEL)
	@if ! $(CONTAINER_ENGINE) ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-sandbox$$'; then \
		$(call log_warn,agent eval,yummi-sandbox container is NOT running — this run will SILENTLY SKIP the real test); \
		$(call log_warn,agent eval,rather than actually exercising the model + sandbox path. Run 'make start' first.); \
	fi
	@$(call log_info,go test,Running quality scenarios with $(LIVE_TEST_MODEL) + sandbox...)
	@set -o pipefail; cd orchestrator && YUMMI_EVAL=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(LIVE_TEST_MODEL) \
		go test -count=1 ./internal/agent/ -run TestLiveEval_Quality -v -timeout 45m 2>&1 | tee /tmp/yummi-test-agent-eval.out | sed 's/^/  /' || { $(call log_error,agent eval,Quality thresholds failed.); exit 1; }
	@if grep -q '^--- SKIP' /tmp/yummi-test-agent-eval.out; then \
		$(call log_warn,agent eval,Test was SKIPPED, not actually run against real infra — see the SKIP reason above.); \
	else \
		$(call log_pass,agent quality eval,All thresholds passed.); \
	fi
	@rm -f /tmp/yummi-test-agent-eval.out

## bench-target-up    Start the intentionally-vulnerable local benchmark target (outdated WordPress)
bench-target-up: _check-deps _container-ensure
	@$(call log_section,Benchmark Target)
	@$(COMPOSE) -f docker-compose.yml -f docker-compose.override.eval-target.yml \
		up -d gateway sandbox orchestrator docker-proxy eval-target-db eval-target || { $(call log_error,bench target,Failed to start.); exit 1; }
	@$(call log_pass,bench target,eval-target is up at 203.0.113.10 (see docker-compose.override.eval-target.yml).)

## bench-target-down  Stop and remove the benchmark target
bench-target-down:
	@$(COMPOSE) -f docker-compose.yml -f docker-compose.override.eval-target.yml \
		rm -sf eval-target eval-target-db || true
	@$(call log_pass,bench target,eval-target stopped.)

## lab-target-up       Start the two-stage exploitation lab (RCE + SUID privesc, see lab-target/)
lab-target-up: _check-deps _container-ensure
	@$(call log_section,Exploitation Lab)
	@$(COMPOSE) -f docker-compose.yml -f docker-compose.override.lab-target.yml \
		build lab-target || { $(call log_error,lab target,Failed to build.); exit 1; }
	@$(COMPOSE) -f docker-compose.yml -f docker-compose.override.lab-target.yml \
		up -d lab-target gateway || { $(call log_error,lab target,Failed to start.); exit 1; }
	@$(call log_pass,lab target,lab-target is up at 198.51.100.10 (see docker-compose.override.lab-target.yml).)

## lab-target-down     Stop and remove the exploitation lab
lab-target-down:
	@$(COMPOSE) -f docker-compose.yml -f docker-compose.override.lab-target.yml \
		rm -sf lab-target || true
	@$(call log_pass,lab target,lab-target stopped.)

## test-agent-benchmark  Prove the agent finds a REAL, known vulnerability on the local benchmark target
test-agent-benchmark: _check-deps _container-ensure
	@$(call log_section,Agent Vulnerability-Finding Benchmark)
	@$(MAKE) _ollama-ensure OLLAMA_MODEL=$(LIVE_TEST_MODEL)
	@if ! $(CONTAINER_ENGINE) ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-sandbox$$'; then \
		$(call log_warn,agent benchmark,yummi-sandbox container is NOT running — this run will SILENTLY SKIP the real test); \
		$(call log_warn,agent benchmark,Run 'make bench-target-up' first.); \
	fi
	@$(call log_info,go test,Running the vulnerability-finding benchmark with $(LIVE_TEST_MODEL) + sandbox + eval-target...)
	@set -o pipefail; cd orchestrator && YUMMI_BENCHMARK=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(LIVE_TEST_MODEL) \
		go test -count=1 ./internal/agent/ -run TestBenchmark_FindsRealVulnerability -v -timeout 15m 2>&1 | tee /tmp/yummi-test-agent-benchmark.out | sed 's/^/  /' || { $(call log_error,agent benchmark,Test failed.); exit 1; }
	@if grep -q '^--- SKIP' /tmp/yummi-test-agent-benchmark.out; then \
		$(call log_warn,agent benchmark,Test was SKIPPED, not actually run against real infra — see the SKIP reason above.); \
	else \
		$(call log_pass,agent benchmark,The agent found and correctly reported the real vulnerability.); \
	fi
	@rm -f /tmp/yummi-test-agent-benchmark.out

## lint    Run Go static analysis
lint: _check-deps
	@$(call log_section,Lint)
	@$(call log_info,golangci-lint,Running on root module...)
	@golangci-lint run || { $(call log_error,lint,Root module lint failed.); exit 1; }
	@$(call log_info,golangci-lint,Running on orchestrator module...)
	@cd orchestrator && golangci-lint run || { $(call log_error,lint,Orchestrator lint failed.); exit 1; }
	@$(call log_info,golangci-lint,Running on sandbox-mcp module...)
	@cd sandbox-mcp && golangci-lint run || { $(call log_error,lint,sandbox-mcp module lint failed.); exit 1; }
	@$(call log_pass,lint,No issues found.)

## vuln    Supply-chain vulnerability scan (govulncheck, all three Go modules)
vuln: _check-deps
	@$(call log_section,Vulnerability Scan)
	@$(call log_info,govulncheck,Scanning root module...)
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./... || { $(call log_error,vuln,Root module has known vulnerabilities.); exit 1; }
	@$(call log_info,govulncheck,Scanning orchestrator module...)
	@cd orchestrator && go run golang.org/x/vuln/cmd/govulncheck@latest ./... || { $(call log_error,vuln,Orchestrator module has known vulnerabilities.); exit 1; }
	@$(call log_info,govulncheck,Scanning sandbox-mcp module...)
	@cd sandbox-mcp && go run golang.org/x/vuln/cmd/govulncheck@latest ./... || { $(call log_error,vuln,sandbox-mcp module has known vulnerabilities.); exit 1; }
	@$(call log_pass,vuln,No known vulnerabilities found.)

## doctor  Run local environment diagnostics
doctor:
	@./bin/$(BINARY) --doctor 2>&1 || ./scripts/doctor.sh

## install Bootstrap local developer environment
install: _check-deps
	@$(call log_section,Install)
	@$(call log_info,install,Running scripts/install.sh...)
	@YUMMI_OLLAMA_MODEL=$(OLLAMA_MODEL) ./scripts/install.sh || { $(call log_error,install,Install script failed.); exit 1; }
	@$(call log_pass,install,Environment bootstrapped.)

## dev     Run local development bootstrap (hot reload)
dev: _check-deps
	@$(call log_info,dev,Starting dev environment...)
	@./scripts/dev.sh || { $(call log_error,dev,Dev script failed.); exit 1; }

# ── Granular build pieces (prefixed with _ to signal internal use) ─────────────

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

$(BIN_DIR)/$(BINARY): $(CLI_SRCS) | $(BIN_DIR)
	@$(call log_info,yummi CLI,Building version $(GIT_SHA)...)
	@go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CLI_PKG) || { $(call log_error,yummi CLI,Build failed.); exit 1; }
	@$(call log_pass,yummi CLI,Built successfully.)

$(BIN_DIR)/$(ORCHESTRATOR_BINARY): $(ORCHESTRATOR_SRCS) | $(BIN_DIR)
	@$(call log_info,yummi-orchestrator,Building version $(GIT_SHA)...)
	@cd orchestrator && go build -trimpath -o $(abspath $(BIN_DIR))/$(ORCHESTRATOR_BINARY) $(ORCHESTRATOR_PKG) || { $(call log_error,yummi-orchestrator,Build failed.); exit 1; }
	@$(call log_pass,yummi-orchestrator,Built successfully.)

$(BIN_DIR)/yummi-sandbox-mcp: $(SANDBOX_MCP_SRCS) | $(BIN_DIR)
	@$(call log_info,sandbox-mcp,Building yummi-sandbox-mcp...)
	@cd sandbox-mcp && go build -trimpath -o $(abspath $(BIN_DIR))/yummi-sandbox-mcp . || { $(call log_error,sandbox-mcp,Build failed.); exit 1; }
	@$(call log_pass,sandbox-mcp,Built successfully.)

$(GATEWAY_DIR)/target/release/yummi-gateway: $(GATEWAY_SRCS)
	@$(call log_info,gateway,Running cargo release build...)
	@set -o pipefail; cd $(GATEWAY_DIR) && cargo build --release 2>&1 | sed 's/^/  /' || { $(call log_error,gateway,Cargo build failed.); exit 1; }
	@$(call log_pass,gateway,Built successfully.)

$(UI_DIR)/node_modules: $(UI_DIR)/package.json $(UI_DIR)/package-lock.json
	@$(call log_info,UI,Installing dependencies...)
	@if [ -f "$(UI_DIR)/package-lock.json" ]; then cd $(UI_DIR) && npm ci --silent || { $(call log_error,UI,npm ci failed.); exit 1; }; \
	 else cd $(UI_DIR) && npm install --silent || { $(call log_error,UI,npm install failed.); exit 1; }; fi
	@touch -m $(UI_DIR)/node_modules
	@$(call log_pass,UI,Dependencies installed.)

$(UI_DIST_DIR)/index.html: $(UI_DIR)/node_modules $(UI_SRCS)
	@$(call log_info,UI,Running vite build...)
	@set -o pipefail; cd $(UI_DIR) && npm run build 2>&1 | sed 's/^/  /' || { $(call log_error,UI,Vite build failed.); exit 1; }
	@touch -m $(UI_DIST_DIR)/index.html
	@$(call log_pass,UI,Dist ready.)

_build-cli: $(BIN_DIR)/$(BINARY)
_build-orchestrator: $(BIN_DIR)/$(ORCHESTRATOR_BINARY)
_build-gateway: $(GATEWAY_DIR)/target/release/yummi-gateway
_build-sandbox-mcp: $(BIN_DIR)/yummi-sandbox-mcp
_build-ui: $(UI_DIST_DIR)/index.html

_test-cli:
	@$(call log_info,test,Running CLI and internal packages tests...)
	@set -o pipefail; go test ./cmd/... ./internal/... 2>&1 | sed 's/^/  /' || { $(call log_error,test,CLI tests failed.); exit 1; }
	@$(call log_pass,test,CLI tests passed.)

_test-orchestrator:
	@$(call log_info,test,Running orchestrator module tests...)
	@set -o pipefail; cd orchestrator && go test ./... 2>&1 | sed 's/^/  /' || { $(call log_error,test,Orchestrator tests failed.); exit 1; }
	@$(call log_pass,test,Orchestrator tests passed.)

_test-gateway:
	@$(call log_info,test,Running gateway Rust tests...)
	@set -o pipefail; cd $(GATEWAY_DIR) && cargo test 2>&1 | sed 's/^/  /' || { $(call log_error,test,Gateway tests failed.); exit 1; }
	@$(call log_pass,test,Gateway tests passed.)

_test-sandbox-mcp:
	@$(call log_info,test,Running sandbox-mcp tests inside isolated Kali Docker container...)
	@$(CONTAINER_ENGINE) build -f sandbox-mcp/Dockerfile.test -t yummi/sandbox-mcp-test:local .
	@$(CONTAINER_ENGINE) run --rm --pull never --network none --cap-drop ALL --security-opt no-new-privileges --read-only --tmpfs /tmp:rw,nosuid,nodev --tmpfs /home/pentester:rw,nosuid,nodev,mode=1777 yummi/sandbox-mcp-test:local -test.v -test.timeout=5m
	@$(call log_pass,test,sandbox-mcp tests passed.)

_test-ui: $(UI_DIR)/node_modules
	@$(call log_info,test,Running UI vitest...)
	@set -o pipefail; cd $(UI_DIR) && unset VITE_YUMMI_LANG; npm test 2>&1 | sed 's/^/  /' || { $(call log_error,test,UI tests failed.); exit 1; }
	@$(call log_pass,test,UI tests passed.)

_test-brain:
	@if ! command -v uv >/dev/null 2>&1; then \
		$(call log_warn,test,uv is not installed -- skipping brain tests (brain/ is an optional component). Install from https://docs.astral.sh/uv/getting-started/installation/); \
		exit 0; \
	fi
	@$(call log_info,test,Running brain (LangGraph) tests...)
	@set -o pipefail; cd brain && uv sync --quiet && uv run pytest tests/ 2>&1 | sed 's/^/  /' || { $(call log_error,test,Brain tests failed.); exit 1; }
	@$(call log_pass,test,Brain tests passed.)

# ── Ollama lifecycle (internal) ───────────────────────────────────────────────

_ollama-ensure:
	@if [ -n "$(YUMMI_EINO_MODEL_BASE_URL)" ] && [ "$(OLLAMA_MODEL)" != "$(LIVE_TEST_MODEL)" ]; then \
		$(call log_section,Model Backend Verification); \
		$(call log_pass,model,Cloud backend configured ($(if $(YUMMI_EINO_MODEL_NAME),$(YUMMI_EINO_MODEL_NAME),custom) via $(if $(YUMMI_EINO_PROVIDER),$(YUMMI_EINO_PROVIDER),cloud)) — local Ollama daemon not required.); \
	else \
		$(call log_section,Ollama Verification); \
		$(call log_info,ollama,Checking daemon at $(OLLAMA_URL)...); \
		if curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1; then \
			$(call log_pass,ollama,Daemon already running); \
		else \
			$(call log_info,ollama,Starting daemon...); \
			if command -v brew >/dev/null 2>&1 && brew list --formula ollama >/dev/null 2>&1; then \
				brew services start ollama >/dev/null 2>&1 || true; \
			elif command -v ollama >/dev/null 2>&1; then \
				mkdir -p $(HOME)/.yummi 2>/dev/null || true; \
				nohup ollama serve >$(HOME)/.yummi/ollama.log 2>&1 & \
			else \
				$(call log_error,ollama,Not found — install from https://ollama.com); \
				exit 1; \
			fi; \
			$(call log_info,ollama,Waiting for API (up to 30s)...); \
			for i in $$(seq 1 30); do \
				curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1 && break; \
				sleep 1; \
			done; \
			if ! curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1; then \
				$(call log_error,ollama,API did not respond in 30s. Check logs: $$HOME/.yummi/ollama.log); \
				exit 1; \
			fi; \
			$(call log_pass,ollama,Daemon ready); \
		fi; \
		$(call log_info,ollama,Checking for model $(OLLAMA_MODEL)...); \
		if ollama list 2>/dev/null | grep -q "^$(OLLAMA_MODEL)"; then \
			$(call log_pass,ollama,Model $(OLLAMA_MODEL) already present); \
		else \
			$(call log_info,ollama,Pulling $(OLLAMA_MODEL) — first run may take several minutes...); \
			ollama pull $(OLLAMA_MODEL) 2>&1 | sed 's/^/  /' || { $(call log_error,ollama,Failed to pull model.); exit 1; }; \
			$(call log_pass,ollama,Model $(OLLAMA_MODEL) ready); \
		fi; \
	fi

# ── Docker helpers ────────────────────────────────────────────────────────────

docker-build: _check-deps _container-ensure ## Build all container images (gateway + sandbox + orchestrator + ui). CONTAINER_ENGINE=docker|podman
	@$(call log_section,Docker Build)
	@$(call log_info,$(CONTAINER_ENGINE),Building gateway sandbox orchestrator yummi-ui...)
	@$(COMPOSE) $(COMPOSE_FILES) build gateway sandbox orchestrator yummi-ui || { $(call log_error,$(CONTAINER_ENGINE),Build failed.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE),Images built successfully.)

docker-orchestrator: _check-deps _container-ensure ## Build orchestrator image. CONTAINER_ENGINE=docker|podman
	@$(call log_section,Docker Build — orchestrator)
	@$(call log_info,$(CONTAINER_ENGINE),Building orchestrator...)
	@$(COMPOSE) $(COMPOSE_FILES) build orchestrator || { $(call log_error,$(CONTAINER_ENGINE),Build failed.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE),Orchestrator built.)

docker-ui: _check-deps _container-ensure ## Build yummi-ui image. CONTAINER_ENGINE=docker|podman
	@$(call log_section,Docker Build — yummi-ui)
	@$(call log_info,$(CONTAINER_ENGINE),Building yummi-ui...)
	@$(COMPOSE) $(COMPOSE_FILES) build yummi-ui || { $(call log_error,$(CONTAINER_ENGINE),Build failed.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE),UI image built.)

dev-ui: ## Start UI dev server (Vite HMR on :3000)
	@$(call log_section,Dev — UI)
	@$(call log_info,dev,Starting npm run dev...)
	@cd $(UI_DIR) && npm run dev

dev-orchestrator: ## Run orchestrator locally (port 9080)
	@$(call log_section,Dev — orchestrator)
	@$(call log_info,dev,Starting orchestrator...)
	@cd orchestrator && go run ./cmd/orchestrator

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  BRAIN  (brain/ — the LangGraph agent, a separate Python process; see
#  brain/README.md. Deliberately NOT part of _check-deps: the core Go/Docker
#  workflow must keep working for anyone who never touches the brain — these
#  targets gate on their own _check-brain-deps instead.)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

_check-brain-deps:
	@$(call log_section,Brain Dependency Validation)
	@command -v uv >/dev/null 2>&1 || { $(call log_error,Dependencies,uv is not installed. Install from https://docs.astral.sh/uv/getting-started/installation/); exit 1; }
	@$(call log_pass,Dependencies,uv is installed.)
	@if [ -z "$$YUMMI_BRAIN_MODEL_API_KEY" ]; then \
		$(call log_error,brain,YUMMI_BRAIN_MODEL_API_KEY is not set -- run 'make conf' and opt into brain setup or set it directly: make brain-run YUMMI_BRAIN_MODEL_API_KEY=sk-... GOAL='...'); \
		exit 1; \
	fi
	@$(call log_pass,Dependencies,YUMMI_BRAIN_MODEL_API_KEY is set.)

## brain-install  Install the LangGraph brain's Python dependencies (uv sync)
brain-install: _check-brain-deps
	@$(call log_section,Brain — Install)
	@$(call log_info,uv,Syncing brain/ dependencies...)
	@set -o pipefail; cd brain && uv sync 2>&1 | sed 's/^/  /' || { $(call log_error,brain,uv sync failed.); exit 1; }
	@$(call log_pass,brain,Dependencies installed.)

## brain-run      Run the LangGraph brain against a goal — requires
##                `make start` already running (orchestrator + gateway +
##                sandbox). Usage: make brain-run GOAL="203.0.113.10 ... 1) ... 2) ..."
brain-run: _check-brain-deps
	@$(call log_section,Brain — Run)
	@if [ -z "$(GOAL)" ]; then \
		$(call log_error,brain,No goal given. Usage: make brain-run GOAL='your goal here'); \
		exit 1; \
	fi
	@if ! curl -sf http://127.0.0.1:9080/api/v1/system/status >/dev/null 2>&1; then \
		$(call log_warn,brain,Orchestrator is not reachable at 127.0.0.1:9080 — this run will fail immediately.); \
		$(call log_warn,brain,Run 'make start' first.); \
	fi
	@cd brain && uv run yummi-brain "$(GOAL)"

## brain-mcp      Run ONLY the agent-facing MCP server standalone (stdio) —
##                for connecting an external MCP client (Claude Desktop,
##                Cursor, Windsurf, ...) instead of this repo's own
##                LangGraph brain. Does not need YUMMI_BRAIN_MODEL_API_KEY:
##                the external client brings its own model.
brain-mcp:
	@command -v uv >/dev/null 2>&1 || { $(call log_error,Dependencies,uv is not installed. Install from https://docs.astral.sh/uv/getting-started/installation/); exit 1; }
	@cd brain && uv run yummi-mcp-server

## app       Alias for 'start' — kept for anyone with the old habit memorized.
app: start

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  HELP
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

help:
	@printf "\n$(BOLD)$(CYAN)  YUMMI$(RESET)  $(DIM)$(VERSION) · $(GIT_SHA)$(RESET)\n"
	@printf "$(DIM)  Privacy-first AI pentest conductor$(RESET)\n\n"
	@printf "$(BOLD)  User commands$(RESET)\n"
	@printf "  $(CYAN)make conf$(RESET)        Pick your container engine setup once (interactive, persists to .yummi.conf)\n"
	@printf "  $(CYAN)make build$(RESET)       Build native binaries and UI (CLI + orchestrator + gateway + UI)\n"
	@printf "  $(CYAN)make start$(RESET)       Start runtime services — UI on http://localhost:3000\n"
	@printf "  $(CYAN)make rebase$(RESET)      Fast clean restart after a source change — Docker's own layer cache is reused\n"
	@printf "  $(CYAN)make app$(RESET)         Alias for 'start'\n"
	@printf "  $(CYAN)make stop$(RESET)        Stop runtime services\n"
	@printf "  $(CYAN)make clean$(RESET)       Remove all build artifacts\n"
	@printf "  $(CYAN)make force-restart$(RESET) Stop services, free RAM, and restart\n"
	@printf "  $(CYAN)make close$(RESET)         Stop all services, LLM models, and free RAM\n"
	@printf "\n$(BOLD)  Engine / network variables$(RESET)  $(DIM)(see docs/installation.md for per-engine caveats)$(RESET)\n"
	@printf "  $(DIM)Prefer 'make conf' to set these once instead of passing them every time.$(RESET)\n"
	@printf "  $(CYAN)CONTAINER_ENGINE=docker|podman$(RESET)  $(DIM)default docker — e.g. make start CONTAINER_ENGINE=podman$(RESET)\n"
	@printf "  $(CYAN)YUMMI_NETWORK_MODE=secure|insecure-bridge$(RESET)  $(DIM)default secure — insecure-bridge drops yummi_internal's\n"
	@printf "    isolation as an engine-bug workaround, and requires typing a confirmation. Prefer OrbStack instead.$(RESET)\n"
	@printf "  $(CYAN)YUMMI_LANG=en|tr$(RESET)  $(DIM)default en — build-time UI language, baked into the UI at build time (no runtime switch)$(RESET)\n"
	@printf "  $(CYAN)YUMMI_AGENT_BACKEND=graph|eino$(RESET)  $(DIM)default graph — which agentic engine to run (no rebuild needed to change)$(RESET)\n"
	@printf "  $(CYAN)YUMMI_EINO_MODEL_BASE_URL/_NAME/_API_KEY/_PROVIDER/_NUM_CTX$(RESET)  $(DIM)optional — point ONLY the eino backend's\n"
	@printf "    ReAct loop at a different OpenAI-compatible server (local LM Studio, or a cloud provider like OpenRouter/OpenAI with\n"
	@printf "    _API_KEY set), independent of the conductor's own Ollama config. 'make conf' sets all of these interactively.$(RESET)\n"
	@printf "\n$(BOLD)  Brain$(RESET)  $(DIM)(brain/ — a separate Python/LangGraph agent driving Yummi over MCP; see brain/README.md)$(RESET)\n"
	@printf "  $(CYAN)make brain-install$(RESET)  Install brain/'s Python dependencies (uv sync)\n"
	@printf "  $(CYAN)make brain-run$(RESET)      Run the brain against a goal — GOAL=\"...\" required, orchestrator must be running\n"
	@printf "  $(CYAN)make brain-mcp$(RESET)      Run only the agent-facing MCP server, for an external MCP client (Claude Desktop, Cursor, ...)\n"
	@printf "  $(DIM)'make conf' can set up YUMMI_BRAIN_MODEL_BASE_URL/_NAME/_API_KEY interactively (masked key entry) — or set them directly.$(RESET)\n"
	@printf "\n$(BOLD)  Developer commands$(RESET)\n"
	@printf "  $(CYAN)make test$(RESET)        Run all test suites\n"
	@printf "  $(CYAN)make test-agent-live$(RESET) Agentic loop e2e vs. real model + sandbox\n"
	@printf "  $(CYAN)make bench-target-up$(RESET) Start the intentionally-vulnerable local benchmark target\n"
	@printf "  $(CYAN)make test-agent-benchmark$(RESET) Prove the agent finds a REAL vulnerability on it\n"
	@printf "  $(CYAN)make lab-target-up$(RESET)   Start the two-stage exploitation lab (RCE + SUID privesc)\n"
	@printf "  $(CYAN)make lab-target-down$(RESET) Stop and remove the exploitation lab\n"
	@printf "  $(CYAN)make lint$(RESET)        Static analysis (golangci-lint)\n"
	@printf "  $(CYAN)make vuln$(RESET)        Supply-chain vuln scan (govulncheck)\n"
	@printf "  $(CYAN)make doctor$(RESET)      Environment diagnostics\n"
	@printf "  $(CYAN)make install$(RESET)     Bootstrap dev environment\n"
	@printf "  $(CYAN)make dev$(RESET)         Hot-reload dev mode\n"
	@printf "\n$(BOLD)  Granular build$(RESET)  $(DIM)(fast feedback loops)$(RESET)\n"
	@printf "  $(DIM)make _build-cli$(RESET)   yummi CLI only\n"
	@printf "  $(DIM)make _build-ui$(RESET)    React/Vite UI only\n"
	@printf "  $(DIM)make _build-orchestrator$(RESET)    orchestrator server binary only\n"
	@printf "  $(DIM)make _build-gateway$(RESET) gateway Rust binary only\n"
	@printf "\n$(BOLD)  Granular test$(RESET)\n"
	@printf "  $(DIM)make _test-cli$(RESET)    CLI + internal packages\n"
	@printf "  $(DIM)make _test-orchestrator$(RESET)     orchestrator module\n"
	@printf "  $(DIM)make _test-gateway$(RESET)  gateway Rust\n"
	@printf "  $(DIM)make _test-ui$(RESET)     UI vitest\n"
	@printf "\n"
