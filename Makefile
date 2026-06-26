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

OLLAMA_MODEL ?= qwen3:8b
OLLAMA_URL   := http://localhost:11434

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
.PHONY: help \
	build start start-tui stop clean force-restart force-stop close \
	_check-deps _build-cli _build-ui _build-orchestrator _build-gateway _build-ui-image \
	test test-agent-live lint doctor install dev dev-ui dev-orchestrator \
	_test-cli _test-orchestrator _test-gateway _test-ui \
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
	@command -v docker >/dev/null 2>&1 || { $(call log_error,Dependencies,Docker is not installed. Install from https://docker.com/); exit 1; }
	@$(call log_pass,Dependencies,Docker is installed.)
	@command -v curl >/dev/null 2>&1 || { $(call log_error,Dependencies,curl is not installed. Please install it.); exit 1; }
	@$(call log_pass,Dependencies,curl is installed.)
	@$(call log_pass,System,All required cross-platform tools are present.)

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  USER COMMANDS  (the ones you run every day)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## build   Build native binaries and UI (CLI + orchestrator + gateway + UI).
build: _check-deps _build-cli _build-ui _build-orchestrator _build-gateway
	@$(call log_section,Build Complete)
	@$(call log_pass,yummi,$(BIN_DIR)/$(BINARY))
	@$(call log_pass,yummi-orchestrator,$(BIN_DIR)/$(ORCHESTRATOR_BINARY))
	@$(call log_pass,gateway,$(GATEWAY_DIR)/target/release/yummi-gateway)
	@$(call log_pass,ui,$(UI_DIST_DIR))
	@printf "\n$(DIM)  version $(VERSION)  git $(GIT_SHA)  $(BUILD_DATE)$(RESET)\n"
	@printf "\n"

## start   Start Ollama + runtime services via docker compose (always rebuilds
##         images first so source changes are never silently skipped)
start: _check-deps _ollama-ensure
	@$(call log_section,Starting Services)
	@$(call log_info,docker compose,Building docker images...)
	@set -o pipefail; docker compose build gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,docker compose,Failed to build images.); exit 1; }
	@$(call log_pass,docker compose,Images built successfully.)
	@$(call log_info,docker compose,Bringing up all services in background...)
	@set -o pipefail; docker compose up -d gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,docker compose,Failed to start services.); exit 1; }
	@$(call log_pass,services,Running — UI on http://localhost:3000)

## start-tui Start services and immediately launch the Terminal UI
start-tui: start
	@$(call log_section,Launching TUI)
	@$(call log_info,yummi,Starting terminal interface...)
	@./bin/$(BINARY) --tui

## stop    Stop all runtime services
stop:
	@$(call log_section,Stopping Services)
	@$(call log_info,docker compose,Stopping all services...)
	@set -o pipefail; docker compose stop gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,docker compose,Failed to stop services.); exit 1; }
	@$(call log_pass,services,All services stopped.)

## clean   Remove all build artifacts
clean:
	@$(call log_section,Clean)
	@$(call log_info,clean,Removing $(BIN_DIR)...)
	@rm -rf $(BIN_DIR)
	@$(call log_info,clean,Removing $(UI_DIST_DIR)...)
	@rm -rf $(UI_DIST_DIR)
	@$(call log_info,clean,Removing databases...)
	@find . -name "*.db" -type f -delete 2>/dev/null || true
	@$(call log_pass,clean,Workspace is tidy.)

## force-restart  Stop services, kill memory hogs (ollama), and restart
force-restart: _check-deps
	@$(call log_section,Force Restart)
	@$(call log_info,docker compose,Stopping all services and removing orphans...)
	@set -o pipefail; docker compose down --remove-orphans 2>&1 | sed 's/^/  /' || true
	@$(call log_info,memory,Killing ollama to free VRAM...)
	@-killall -9 ollama 2>/dev/null || true
	@$(call log_info,memory,Killing lingering yummi processes...)
	@-killall -9 yummi yummi-orchestrator 2>/dev/null || true
	@$(MAKE) _ollama-ensure
	@$(call log_info,docker compose,Building images...)
	@set -o pipefail; docker compose build gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,docker compose,Failed to build images.); exit 1; }
	@$(call log_info,docker compose,Bringing up all services...)
	@set -o pipefail; docker compose up -d gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /' || { $(call log_error,docker compose,Failed to start services.); exit 1; }
	@$(call log_pass,force-restart,Services forcibly restarted successfully.)

## close          Stop all services, Docker, LLM models, and free RAM
close:
	@$(call log_section,Close System)
	@$(call log_info,docker compose,Stopping all services...)
	@set -o pipefail; docker compose down --remove-orphans 2>&1 | sed 's/^/  /' || true
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
test: _check-deps _test-cli _test-orchestrator _test-gateway _test-ui
	@$(call log_section,All Tests Passed)
	@$(call log_pass,test,All suites completed successfully.)

## test-agent-live  End-to-end agentic loop against the REAL model + sandbox container
test-agent-live: _check-deps _ollama-ensure
	@$(call log_section,Agent Live E2E)
	@$(call log_info,go test,Running real $(OLLAMA_MODEL) + sandbox container...)
	@set -o pipefail; cd orchestrator && YUMMI_LIVE=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(OLLAMA_MODEL) \
		go test ./internal/agent/ -run TestRunnerLive -v -timeout 15m 2>&1 | sed 's/^/  /' || { $(call log_error,agent live,Test failed.); exit 1; }
	@$(call log_pass,agent live e2e,Test passed.)

## lint    Run Go static analysis
lint: _check-deps
	@$(call log_section,Lint)
	@$(call log_info,golangci-lint,Running on root module...)
	@golangci-lint run || { $(call log_error,lint,Root module lint failed.); exit 1; }
	@$(call log_info,golangci-lint,Running on orchestrator module...)
	@cd orchestrator && golangci-lint run || { $(call log_error,lint,Orchestrator lint failed.); exit 1; }
	@$(call log_pass,lint,No issues found.)

## doctor  Run local environment diagnostics
doctor:
	@./bin/$(BINARY) --doctor 2>&1 || ./scripts/doctor.sh

## install Bootstrap local developer environment
install: _check-deps
	@$(call log_section,Install)
	@$(call log_info,install,Running scripts/install.sh...)
	@./scripts/install.sh || { $(call log_error,install,Install script failed.); exit 1; }
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

_build-ui-image: _build-ui
	@$(call log_info,yummi-ui,Building Docker image...)
	@docker build -t yummi/ui:local ./$(UI_DIR) 2>&1 | sed 's/^/  /' || { $(call log_error,yummi-ui,Docker build failed.); exit 1; }
	@$(call log_pass,yummi-ui,Image built.)

_build-orchestrator: | $(BIN_DIR)
	@$(call log_info,yummi-orchestrator,Building version $(GIT_SHA)...)
	@cd orchestrator && go build -trimpath -o ../$(BIN_DIR)/$(ORCHESTRATOR_BINARY) $(ORCHESTRATOR_PKG) || { $(call log_error,yummi-orchestrator,Build failed.); exit 1; }
	@$(call log_pass,yummi-orchestrator,Built successfully.)

_build-gateway:
	@$(call log_info,gateway,Running cargo release build...)
	@set -o pipefail; cd $(GATEWAY_DIR) && cargo build --release 2>&1 | sed 's/^/  /' || { $(call log_error,gateway,Cargo build failed.); exit 1; }
	@$(call log_pass,gateway,Built successfully.)

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

_test-ui:
	@$(call log_info,UI,Installing dependencies for tests...)
	@if [ -f "$(UI_DIR)/package-lock.json" ]; then cd $(UI_DIR) && npm ci --silent || { $(call log_error,UI,npm ci failed.); exit 1; }; \
	 else cd $(UI_DIR) && npm install --silent || { $(call log_error,UI,npm install failed.); exit 1; }; fi
	@$(call log_info,test,Running UI vitest...)
	@set -o pipefail; cd $(UI_DIR) && npm test 2>&1 | sed 's/^/  /' || { $(call log_error,test,UI tests failed.); exit 1; }
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

docker-build: ## Build all Docker images (gateway + sandbox + orchestrator + ui)
	@$(call log_section,Docker Build)
	@$(call log_info,docker,Building gateway sandbox orchestrator yummi-ui...)
	@docker compose build gateway sandbox orchestrator yummi-ui || { $(call log_error,docker,Build failed.); exit 1; }
	@$(call log_pass,docker,Images built successfully.)

docker-orchestrator: ## Build orchestrator Docker image
	@$(call log_section,Docker Build — orchestrator)
	@$(call log_info,docker,Building orchestrator...)
	@docker compose build orchestrator || { $(call log_error,docker,Build failed.); exit 1; }
	@$(call log_pass,docker,Orchestrator built.)

docker-ui: ## Build yummi-ui Docker image
	@$(call log_section,Docker Build — yummi-ui)
	@$(call log_info,docker,Building yummi-ui...)
	@docker compose build yummi-ui || { $(call log_error,docker,Build failed.); exit 1; }
	@$(call log_pass,docker,UI image built.)

dev-ui: ## Start UI dev server (Vite HMR on :3000)
	@$(call log_section,Dev — UI)
	@$(call log_info,dev,Starting npm run dev...)
	@cd $(UI_DIR) && npm run dev

dev-orchestrator: ## Run orchestrator orchestrator locally (port 9080)
	@$(call log_section,Dev — orchestrator)
	@$(call log_info,dev,Starting orchestrator...)
	@cd orchestrator && go run ./cmd/orchestrator

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  HELP
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

help:
	@printf "\n$(BOLD)$(CYAN)  YUMMI$(RESET)  $(DIM)$(VERSION) · $(GIT_SHA)$(RESET)\n"
	@printf "$(DIM)  Privacy-first AI pentest conductor$(RESET)\n\n"
	@printf "$(BOLD)  User commands$(RESET)\n"
	@printf "  $(CYAN)make build$(RESET)       Build native binaries and UI (CLI + orchestrator + gateway + UI)\n"
	@printf "  $(CYAN)make start$(RESET)       Start runtime services (docker compose)\n"
	@printf "  $(CYAN)make start-tui$(RESET)   Start runtime services and launch Terminal UI\n"
	@printf "  $(CYAN)make stop$(RESET)        Stop runtime services\n"
	@printf "  $(CYAN)make clean$(RESET)       Remove all build artifacts\n"
	@printf "  $(CYAN)make force-restart$(RESET) Stop services, free RAM, and restart\n"
	@printf "  $(CYAN)make close$(RESET)         Stop all services, LLM models, and free RAM\n"
	@printf "\n$(BOLD)  Developer commands$(RESET)\n"
	@printf "  $(CYAN)make test$(RESET)        Run all test suites\n"
	@printf "  $(CYAN)make test-agent-live$(RESET) Agentic loop e2e vs. real model + sandbox\n"
	@printf "  $(CYAN)make lint$(RESET)        Static analysis (golangci-lint)\n"
	@printf "  $(CYAN)make doctor$(RESET)      Environment diagnostics\n"
	@printf "  $(CYAN)make install$(RESET)     Bootstrap dev environment\n"
	@printf "  $(CYAN)make dev$(RESET)         Hot-reload dev mode\n"
	@printf "\n$(BOLD)  Granular build$(RESET)  $(DIM)(fast feedback loops)$(RESET)\n"
	@printf "  $(DIM)make _build-cli$(RESET)   yummi CLI only\n"
	@printf "  $(DIM)make _build-ui$(RESET)    React/Vite UI only\n"
	@printf "  $(DIM)make _build-orchestrator$(RESET)    orchestrator orchestrator + UI embed\n"
	@printf "  $(DIM)make _build-gateway$(RESET) gateway Rust binary only\n"
	@printf "\n$(BOLD)  Granular test$(RESET)\n"
	@printf "  $(DIM)make _test-cli$(RESET)    CLI + internal packages\n"
	@printf "  $(DIM)make _test-orchestrator$(RESET)     orchestrator module\n"
	@printf "  $(DIM)make _test-gateway$(RESET)  gateway Rust\n"
	@printf "  $(DIM)make _test-ui$(RESET)     UI vitest\n"
	@printf "\n"
