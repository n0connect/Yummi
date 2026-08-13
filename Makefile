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
DESKTOP_DIR      := desktop
DESKTOP_TARGET_DIR := $(DESKTOP_DIR)/src-tauri/target
DESKTOP_APP      := $(DESKTOP_TARGET_DIR)/release/bundle/macos/Yummi.app

OLLAMA_MODEL    ?= $(if $(YUMMI_OLLAMA_MODEL),$(YUMMI_OLLAMA_MODEL),$(if $(filter lite,$(YUMMI_OLLAMA_TIER)),qwen3:4b,$(if $(filter standard,$(YUMMI_OLLAMA_TIER)),qwen3:8b,$(if $(filter expert,$(YUMMI_OLLAMA_TIER)),qwen3:32b,qwen3:14b))))
LIVE_TEST_MODEL ?= qwen3:8b
OLLAMA_URL      := http://localhost:11434
export YUMMI_OLLAMA_MODEL := $(OLLAMA_MODEL)

# ── Local configuration (written by `make conf`; gitignored, per-machine) ────
# Run `make conf` once to pick your container engine setup interactively; it
# persists CONTAINER_ENGINE / YUMMI_NETWORK_MODE here so you never have to
# pass them on the command line again. Command-line overrides (e.g.
# `make start CONTAINER_ENGINE=podman`) still win over this file. The `-`
# prefix means "don't complain if the file doesn't exist yet".
-include .yummi.conf

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
# interactive confirmation in `_check-network-mode` below. Only reach for
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
# VITE_YUMMI_LANG — the native path (_build-ui, desktop, dev-ui) via this
# exported env var, the Docker path (yummi-ui image) via docker-compose.yml's
# build arg, which reads the same YUMMI_LANG from the environment/.yummi.conf.
YUMMI_LANG ?= en
export VITE_YUMMI_LANG := $(YUMMI_LANG)

# YUMMI_AGENT_BACKEND: graph (default) or eino — which agentic engine the
# orchestrator uses. graph is the tag-protocol state machine (best for
# small/local models); eino is native-tool-calling (best for larger models
# with reliable function-calling). Runtime env var, read by main.go — unlike
# YUMMI_LANG this does not need a UI rebuild, just exported before `make start`.
YUMMI_AGENT_BACKEND ?= graph
export YUMMI_AGENT_BACKEND

VERSION    ?= v0.1.0
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
define log_section
printf "\n$(BOLD)$(CYAN)[INFO] ══ $(1) ══$(RESET)\n"
endef

# ── Phony declarations ────────────────────────────────────────────────────────
.PHONY: help conf \
	build start start-tui stop clean force-restart close \
	_check-deps _check-network-mode _build-cli _build-ui _build-orchestrator _build-gateway _build-sandbox-mcp \
	test test-agent-live test-agent-eval lint vuln doctor install dev dev-ui dev-orchestrator \
	app desktop desktop-dev _check-desktop-deps _desktop-deps \
	_test-cli _test-orchestrator _test-gateway _test-ui _test-sandbox-mcp \
	docker-build docker-orchestrator docker-ui \
	_ollama-ensure

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  DEPENDENCY GUARD
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
_check-deps:
	@$(call log_section,Dependency Validation)
	@$(call log_info,System,Checking required cross-platform tools...)
	@command -v go >/dev/null 2>&1 || { $(call log_error,Dependencies,Go is not installed. Install from https://golang.org/dl/); exit 1; }
	@$(call log_pass,Dependencies,Go is installed.)
	@command -v cargo >/dev/null 2>&1 || { $(call log_error,Dependencies,Rust/Cargo is not installed. Install from https://rustup.rs/); exit 1; }
	@$(call log_pass,Dependencies,Rust/Cargo is installed.)
	@command -v npm >/dev/null 2>&1 || { $(call log_error,Dependencies,Node.js/npm is not installed. Install from https://nodejs.org/); exit 1; }
	@$(call log_pass,Dependencies,Node.js/npm is installed.)
	@command -v $(CONTAINER_ENGINE) >/dev/null 2>&1 || { $(call log_error,Dependencies,$(CONTAINER_ENGINE) is not installed. Install Docker from https://docker.com/ or Podman from https://podman.io/); exit 1; }
	@$(call log_pass,Dependencies,$(CONTAINER_ENGINE) is installed.)
	@$(CONTAINER_ENGINE) compose version >/dev/null 2>&1 || { $(call log_error,Dependencies,$(CONTAINER_ENGINE) compose is not available. Docker needs the Compose v2 plugin; Podman needs docker-compose on PATH — see docs/installation.md); exit 1; }
	@$(call log_pass,Dependencies,$(CONTAINER_ENGINE) compose is available.)
	@command -v curl >/dev/null 2>&1 || { $(call log_error,Dependencies,curl is not installed. Please install it.); exit 1; }
	@$(call log_pass,Dependencies,curl is installed.)
	@$(call log_pass,System,All required cross-platform tools are present.)

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  NETWORK-MODE GUARD  (opt-in security trade-off, never silent)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
_check-network-mode:
ifeq ($(YUMMI_NETWORK_MODE),insecure-bridge)
	@printf "\n$(RED)$(BOLD)⚠️  YUMMI_NETWORK_MODE=insecure-bridge$(RESET)\n"
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
#  USER COMMANDS  (the ones you run every day)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## conf    Pick your container engine setup and UI language once (interactive);
##         persists CONTAINER_ENGINE / YUMMI_NETWORK_MODE / YUMMI_LANG to
##         .yummi.conf so `make build`/`make start` never need those flags
##         again. Re-run anytime to change your choice.
conf:
	@$(call log_section,Yummi Configuration)
	@printf "$(BOLD)How are you running containers on this machine?$(RESET)\n\n"; \
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
	if [ "$$mode" = "insecure-bridge" ]; then \
		$(MAKE) _check-network-mode CONTAINER_ENGINE=$$engine YUMMI_NETWORK_MODE=insecure-bridge || exit 1; \
	fi; \
	printf "\n$(BOLD)UI display language?$(RESET) $(DIM)(build-time only — changing it later needs 'make conf' + a rebuild)$(RESET)\n\n"; \
	printf "  $(CYAN)1)$(RESET) English\n"; \
	printf "  $(CYAN)2)$(RESET) Türkçe\n\n"; \
	printf "$(BOLD)Choice [1-2]: $(RESET)"; \
	read langchoice; \
	case "$$langchoice" in \
		1) lang=en ;; \
		2) lang=tr ;; \
		*) printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Invalid choice '%s' — expected 1-2.$(RESET)\n" "$$langchoice"; exit 1 ;; \
	esac; \
	printf "\n$(BOLD)Agent backend?$(RESET) $(DIM)(which model you'll run — can be changed later without a rebuild)$(RESET)\n\n"; \
	printf "  $(CYAN)1)$(RESET) Graph $(DIM)— small/local models (default)$(RESET)\n"; \
	printf "  $(CYAN)2)$(RESET) Eino $(DIM)— larger models with reliable native tool-calling$(RESET)\n\n"; \
	printf "$(BOLD)Choice [1-2]: $(RESET)"; \
	read backendchoice; \
	case "$$backendchoice" in \
		1) backend=graph ;; \
		2) backend=eino ;; \
		*) printf "$(RED)[ERROR]$(RESET) $(BOLD)conf$(RESET) $(DIM)Invalid choice '%s' — expected 1-2.$(RESET)\n" "$$backendchoice"; exit 1 ;; \
	esac; \
	printf "CONTAINER_ENGINE=%s\n" "$$engine" > .yummi.conf; \
	printf "YUMMI_NETWORK_MODE=%s\n" "$$mode" >> .yummi.conf; \
	printf "YUMMI_LANG=%s\n" "$$lang" >> .yummi.conf; \
	printf "YUMMI_AGENT_BACKEND=%s\n" "$$backend" >> .yummi.conf; \
	printf "\n$(GREEN)[PASS]$(RESET) $(BOLD)conf$(RESET) $(DIM)Saved to .yummi.conf — CONTAINER_ENGINE=$$engine YUMMI_NETWORK_MODE=$$mode YUMMI_LANG=$$lang YUMMI_AGENT_BACKEND=$$backend$(RESET)\n"; \
	printf "$(DIM)Every 'make build'/'make start'/etc. now uses this automatically. Run 'make conf' again to change it.$(RESET)\n\n"

## build   Build native binaries and UI (CLI + orchestrator + gateway + UI).
build: _check-deps _build-cli _build-ui _build-orchestrator _build-gateway _build-sandbox-mcp
	@$(call log_section,Build Complete)
	@$(call log_pass,yummi,$(BIN_DIR)/$(BINARY))
	@$(call log_pass,yummi-orchestrator,$(BIN_DIR)/$(ORCHESTRATOR_BINARY))
	@$(call log_pass,gateway,$(GATEWAY_DIR)/target/release/yummi-gateway)
	@$(call log_pass,ui,$(UI_DIST_DIR))
	@printf "\n$(DIM)  version $(VERSION)  git $(GIT_SHA)  $(BUILD_DATE)$(RESET)\n"
	@printf "\n"

## start   Start Ollama + runtime services via compose (always rebuilds
##         images first so source changes are never silently skipped), then
##         launches the native Tauri console directly — no browser tab to
##         open by hand. Engine: CONTAINER_ENGINE=docker|podman (default docker).
##         Network trade-off: YUMMI_NETWORK_MODE=secure|insecure-bridge (default secure).
##         Headless/CI/server: NO_DESKTOP=1 skips the desktop launch and
##         leaves http://localhost:3000 as the only surface.
start: _check-deps _check-network-mode _ollama-ensure
	@$(call log_section,Starting Services)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Building images...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) build gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to build images.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE) compose,Images built successfully.)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Bringing up all services in background...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) up -d gateway sandbox orchestrator yummi-ui docker-proxy 2>&1 | sed 's/^/  /' || { $(call log_error,$(CONTAINER_ENGINE) compose,Failed to start services.); exit 1; }
	@$(call log_pass,services,Running — UI on http://localhost:3000)
ifneq ($(NO_DESKTOP),1)
	@$(MAKE) desktop-dev
endif

## start-tui Start services (headless) and immediately launch the Terminal UI.
##           Always passes NO_DESKTOP=1 to 'start' — the terminal UI and the
##           native desktop console are alternative operator surfaces, not
##           something to launch both of from one command.
start-tui:
	@$(MAKE) start NO_DESKTOP=1
	@$(call log_section,Launching TUI)
	@$(call log_info,yummi,Starting terminal interface...)
	@./bin/$(BINARY) --tui

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
	@$(call log_info,clean,Removing $(DESKTOP_TARGET_DIR)...)
	@rm -rf $(DESKTOP_TARGET_DIR)
	@$(call log_info,clean,Removing databases...)
	@find . -name "*.db" -type f -delete 2>/dev/null || true
	@$(call log_pass,clean,Workspace is tidy.)

## force-restart  Stop services, kill memory hogs (ollama), and restart
force-restart: _check-deps _check-network-mode
	@$(call log_section,Force Restart)
	@$(call log_info,$(CONTAINER_ENGINE) compose,Stopping all services and removing orphans...)
	@set -o pipefail; $(COMPOSE) $(COMPOSE_FILES) down --remove-orphans 2>&1 | sed 's/^/  /' || true
	@$(call log_info,memory,Killing ollama to free VRAM...)
	@-killall -9 ollama 2>/dev/null || true
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
	@$(call log_info,memory,Killing lingering yummi processes...)
	@-killall -9 yummi yummi-orchestrator 2>/dev/null || true
	@$(call log_pass,close,All processes and models successfully stopped.)

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  DEVELOPER COMMANDS  (granular, fast feedback loops)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## test    Run all test suites (CLI + orchestrator + gateway + UI)
test: _check-deps _test-cli _test-orchestrator _test-gateway _test-ui _test-sandbox-mcp
	@$(call log_section,All Tests Passed)
	@$(call log_pass,test,All suites completed successfully.)

## test-agent-live  End-to-end agentic loop against the REAL model + sandbox container
test-agent-live: _check-deps
	@$(call log_section,Agent Live E2E)
	@$(MAKE) _ollama-ensure OLLAMA_MODEL=$(LIVE_TEST_MODEL)
	@$(call log_info,go test,Running real $(LIVE_TEST_MODEL) + sandbox container...)
	@set -o pipefail; cd orchestrator && YUMMI_LIVE=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(LIVE_TEST_MODEL) \
		go test ./internal/agent/ -run TestRunnerLive -v -timeout 15m 2>&1 | sed 's/^/  /' || { $(call log_error,agent live,Test failed.); exit 1; }
	@$(call log_pass,agent live e2e,Test passed.)

## test-agent-eval  Assertion-bearing multi-scenario quality gate against the REAL model + sandbox
test-agent-eval: _check-deps
	@$(call log_section,Agent Quality Evaluation)
	@$(MAKE) _ollama-ensure OLLAMA_MODEL=$(LIVE_TEST_MODEL)
	@$(call log_info,go test,Running quality scenarios with $(LIVE_TEST_MODEL) + sandbox...)
	@set -o pipefail; cd orchestrator && YUMMI_EVAL=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(LIVE_TEST_MODEL) \
		go test ./internal/agent/ -run TestLiveEval_Quality -v -timeout 45m 2>&1 | sed 's/^/  /' || { $(call log_error,agent eval,Quality thresholds failed.); exit 1; }
	@$(call log_pass,agent quality eval,All thresholds passed.)

## lint    Run Go static analysis
lint: _check-deps
	@$(call log_section,Lint)
	@$(call log_info,golangci-lint,Running on root module...)
	@golangci-lint run || { $(call log_error,lint,Root module lint failed.); exit 1; }
	@$(call log_info,golangci-lint,Running on orchestrator module...)
	@cd orchestrator && golangci-lint run || { $(call log_error,lint,Orchestrator lint failed.); exit 1; }
	@$(call log_pass,lint,No issues found.)

## vuln    Supply-chain vulnerability scan (govulncheck, both Go modules)
vuln: _check-deps
	@$(call log_section,Vulnerability Scan)
	@$(call log_info,govulncheck,Scanning root module...)
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./... || { $(call log_error,vuln,Root module has known vulnerabilities.); exit 1; }
	@$(call log_info,govulncheck,Scanning orchestrator module...)
	@cd orchestrator && go run golang.org/x/vuln/cmd/govulncheck@latest ./... || { $(call log_error,vuln,Orchestrator module has known vulnerabilities.); exit 1; }
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

_build-cli: | $(BIN_DIR)
	@$(call log_info,yummi CLI,Building version $(GIT_SHA)...)
	@go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CLI_PKG) || { $(call log_error,yummi CLI,Build failed.); exit 1; }
	@$(call log_pass,yummi CLI,Built successfully.)

_build-ui:
	@$(call log_info,UI,Installing dependencies...)
	@if [ -f "$(UI_DIR)/package-lock.json" ]; then cd $(UI_DIR) && npm ci --silent || { $(call log_error,UI,npm ci failed.); exit 1; }; \
	 else cd $(UI_DIR) && npm install --silent || { $(call log_error,UI,npm install failed.); exit 1; }; fi
	@$(call log_pass,UI,Dependencies installed.)
	@$(call log_info,UI,Running vite build...)
	@set -o pipefail; cd $(UI_DIR) && npm run build 2>&1 | sed 's/^/  /' || { $(call log_error,UI,Vite build failed.); exit 1; }
	@$(call log_pass,UI,Dist ready.)

_build-orchestrator: | $(BIN_DIR)
	@$(call log_info,yummi-orchestrator,Building version $(GIT_SHA)...)
	@cd orchestrator && go build -trimpath -o ../$(BIN_DIR)/$(ORCHESTRATOR_BINARY) $(ORCHESTRATOR_PKG) || { $(call log_error,yummi-orchestrator,Build failed.); exit 1; }
	@$(call log_pass,yummi-orchestrator,Built successfully.)

_build-gateway:
	@$(call log_info,gateway,Running cargo release build...)
	@set -o pipefail; cd $(GATEWAY_DIR) && cargo build --release 2>&1 | sed 's/^/  /' || { $(call log_error,gateway,Cargo build failed.); exit 1; }
	@$(call log_pass,gateway,Built successfully.)

_build-sandbox-mcp: | $(BIN_DIR)
	@$(call log_info,sandbox-mcp,Building yummi-sandbox-mcp...)
	@cd sandbox-mcp && go build -trimpath -o ../$(BIN_DIR)/yummi-sandbox-mcp . || { $(call log_error,sandbox-mcp,Build failed.); exit 1; }
	@$(call log_pass,sandbox-mcp,Built successfully.)

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
	@$(call log_info,test,Running sandbox-mcp module tests...)
	@set -o pipefail; cd sandbox-mcp && go test ./... 2>&1 | sed 's/^/  /' || { $(call log_error,test,sandbox-mcp tests failed.); exit 1; }
	@$(call log_pass,test,sandbox-mcp tests passed.)

_test-ui:
	@$(call log_info,UI,Installing dependencies for tests...)
	@if [ -f "$(UI_DIR)/package-lock.json" ]; then cd $(UI_DIR) && npm ci --silent || { $(call log_error,UI,npm ci failed.); exit 1; }; \
	 else cd $(UI_DIR) && npm install --silent || { $(call log_error,UI,npm install failed.); exit 1; }; fi
	@$(call log_info,test,Running UI vitest...)
	@set -o pipefail; cd $(UI_DIR) && unset VITE_YUMMI_LANG && npm test 2>&1 | sed 's/^/  /' || { $(call log_error,test,UI tests failed.); exit 1; }
	@$(call log_pass,test,UI tests passed.)

# ── Ollama lifecycle (internal) ───────────────────────────────────────────────

_ollama-ensure:
	@$(call log_section,Ollama Verification)
	@$(call log_info,ollama,Checking daemon at $(OLLAMA_URL)...)
	@if curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1; then \
		$(call log_pass,ollama,Daemon already running); \
	else \
		$(call log_info,ollama,Starting daemon...); \
		if command -v brew >/dev/null 2>&1 && brew list --formula ollama >/dev/null 2>&1; then \
			brew services start ollama >/dev/null 2>&1 || true; \
		elif command -v ollama >/dev/null 2>&1; then \
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
	fi
	@$(call log_info,ollama,Checking for model $(OLLAMA_MODEL)...)
	@if ollama list 2>/dev/null | grep -q "^$(OLLAMA_MODEL)"; then \
		$(call log_pass,ollama,Model $(OLLAMA_MODEL) already present); \
	else \
		$(call log_info,ollama,Pulling $(OLLAMA_MODEL) — first run may take several minutes...); \
		ollama pull $(OLLAMA_MODEL) 2>&1 | sed 's/^/  /' || { $(call log_error,ollama,Failed to pull model.); exit 1; }; \
		$(call log_pass,ollama,Model $(OLLAMA_MODEL) ready); \
	fi

# ── Docker helpers ────────────────────────────────────────────────────────────

docker-build: ## Build all container images (gateway + sandbox + orchestrator + ui). CONTAINER_ENGINE=docker|podman
	@$(call log_section,Docker Build)
	@$(call log_info,$(CONTAINER_ENGINE),Building gateway sandbox orchestrator yummi-ui...)
	@$(COMPOSE) $(COMPOSE_FILES) build gateway sandbox orchestrator yummi-ui || { $(call log_error,$(CONTAINER_ENGINE),Build failed.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE),Images built successfully.)

docker-orchestrator: ## Build orchestrator image. CONTAINER_ENGINE=docker|podman
	@$(call log_section,Docker Build — orchestrator)
	@$(call log_info,$(CONTAINER_ENGINE),Building orchestrator...)
	@$(COMPOSE) $(COMPOSE_FILES) build orchestrator || { $(call log_error,$(CONTAINER_ENGINE),Build failed.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE),Orchestrator built.)

docker-ui: ## Build yummi-ui image. CONTAINER_ENGINE=docker|podman
	@$(call log_section,Docker Build — yummi-ui)
	@$(call log_info,$(CONTAINER_ENGINE),Building yummi-ui...)
	@$(COMPOSE) $(COMPOSE_FILES) build yummi-ui || { $(call log_error,$(CONTAINER_ENGINE),Build failed.); exit 1; }
	@$(call log_pass,$(CONTAINER_ENGINE),UI image built.)

dev-ui: ## Start UI dev server (Vite HMR on :3000)
	@$(call log_section,Dev — UI)
	@$(call log_info,dev,Starting npm run dev...)
	@cd $(UI_DIR) && npm run dev

dev-orchestrator: ## Run orchestrator orchestrator locally (port 9080)
	@$(call log_section,Dev — orchestrator)
	@$(call log_info,dev,Starting orchestrator...)
	@cd orchestrator && go run ./cmd/orchestrator

_check-desktop-deps:
	@command -v cargo >/dev/null 2>&1 || { $(call log_error,desktop,Rust/Cargo is not installed. Install from https://rustup.rs/); exit 1; }
	@command -v npm >/dev/null 2>&1 || { $(call log_error,desktop,Node.js/npm is not installed. Install from https://nodejs.org/); exit 1; }

_desktop-deps: _check-desktop-deps
	@if [ ! -x "$(DESKTOP_DIR)/node_modules/.bin/tauri" ] || [ "$(DESKTOP_DIR)/package-lock.json" -nt "$(DESKTOP_DIR)/node_modules/.package-lock.json" ]; then \
		$(call log_info,desktop,Installing locked Tauri CLI dependencies...); \
		cd $(DESKTOP_DIR) && npm ci --silent || { $(call log_error,desktop,npm ci failed.); exit 1; }; \
	else \
		$(call log_pass,desktop,Tauri CLI dependencies are current.); \
	fi

## app       Alias for 'start' — kept for anyone with the old habit memorized.
##           'start' has launched the native desktop console directly since
##           the workflow merged; this no longer does anything extra on top.
app: start

## desktop   Build the native Tauri operator console (runtime stack stays separate).
desktop: _desktop-deps
	@$(call log_section,Desktop Build)
	@$(call log_info,desktop,Building native operator console...)
	@cd $(DESKTOP_DIR) && npm run build || { $(call log_error,desktop,Tauri build failed.); exit 1; }
	@$(call log_pass,desktop,Native Yummi bundle is ready at $(DESKTOP_APP).)

## desktop-dev  Launch the native Tauri console on its own — 'make start' already
##              calls this automatically; use this directly only to reattach the
##              console without rebuilding/restarting the already-running stack.
desktop-dev: _desktop-deps
	@$(call log_section,Desktop Development)
	@$(call log_info,desktop,Launching native operator console...)
	@cd $(DESKTOP_DIR) && npm run dev

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  HELP
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

help:
	@printf "\n$(BOLD)$(CYAN)  YUMMI$(RESET)  $(DIM)$(VERSION) · $(GIT_SHA)$(RESET)\n"
	@printf "$(DIM)  Privacy-first AI pentest conductor$(RESET)\n\n"
	@printf "$(BOLD)  User commands$(RESET)\n"
	@printf "  $(CYAN)make conf$(RESET)        Pick your container engine setup once (interactive, persists to .yummi.conf)\n"
	@printf "  $(CYAN)make build$(RESET)       Build native binaries and UI (CLI + orchestrator + gateway + UI)\n"
	@printf "  $(CYAN)make start$(RESET)       Start runtime services AND launch the native desktop console (NO_DESKTOP=1 to skip)\n"
	@printf "  $(CYAN)make app$(RESET)         Alias for 'start'\n"
	@printf "  $(CYAN)make desktop-dev$(RESET) Reattach the desktop console without restarting the stack\n"
	@printf "  $(CYAN)make desktop$(RESET)     Build a native Tauri application bundle\n"
	@printf "  $(CYAN)make start-tui$(RESET)   Start runtime services (headless) and launch Terminal UI\n"
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
	@printf "  $(CYAN)NO_DESKTOP=1$(RESET)  $(DIM)default unset — skip launching the desktop console from 'make start' (headless/CI/server)$(RESET)\n"
	@printf "\n$(BOLD)  Developer commands$(RESET)\n"
	@printf "  $(CYAN)make test$(RESET)        Run all test suites\n"
	@printf "  $(CYAN)make test-agent-live$(RESET) Agentic loop e2e vs. real model + sandbox\n"
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
