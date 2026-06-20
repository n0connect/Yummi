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

define log_step
	@printf "  $(CYAN)▶$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_ok
	@printf "  $(GREEN)✓$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_warn
	@printf "  $(YELLOW)⚠$(RESET) $(BOLD)$(1)$(RESET) $(DIM)$(2)$(RESET)\n"
endef
define log_section
	@printf "\n$(BOLD)$(CYAN)  ══ $(1) ══$(RESET)\n"
endef

# ── Phony declarations ────────────────────────────────────────────────────────
.PHONY: help \
	build start stop clean force-restart force-stop close \
	_build-cli _build-ui _build-orchestrator _build-gateway _build-ui-image \
	test test-agent-live lint doctor install dev dev-ui dev-orchestrator \
	_test-cli _test-orchestrator _test-gateway _test-ui \
	docker-build docker-orchestrator docker-ui \
	_ollama-ensure

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  USER COMMANDS  (the ones you run every day)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## build   Build native binaries (CLI + orchestrator + gateway). UI is built via `make _build-ui` or docker.
build: _build-cli _build-orchestrator _build-gateway
	$(call log_section,Build Complete)
	$(call log_ok,yummi,$(BIN_DIR)/$(BINARY))
	$(call log_ok,yummi-orchestrator,$(BIN_DIR)/$(ORCHESTRATOR_BINARY))
	$(call log_ok,gateway,$(GATEWAY_DIR)/target/release/yummi-gateway)
	@printf "\n$(DIM)  version $(VERSION)  git $(GIT_SHA)  $(BUILD_DATE)$(RESET)\n"
	$(call log_warn,UI,not built by this target — run 'make _build-ui' or 'make docker-ui')
	@printf "\n"

## start   Start Ollama + runtime services via docker compose
start: _ollama-ensure
	$(call log_section,Starting Services)
	$(call log_step,docker compose,bringing up all services)
	@set -o pipefail; docker compose up -d gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /'
	$(call log_ok,services,running — UI on http://localhost:3000)

## stop    Stop all runtime services
stop:
	$(call log_section,Stopping Services)
	$(call log_step,docker compose,stopping all services)
	@set -o pipefail; docker compose stop gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /'
	$(call log_ok,services,stopped)

## clean   Remove all build artifacts
clean:
	$(call log_section,Clean)
	$(call log_step,removing,$(BIN_DIR))
	@rm -rf $(BIN_DIR)
	$(call log_step,removing,$(UI_DIST_DIR))
	@rm -rf $(UI_DIST_DIR)
	$(call log_ok,clean,workspace is tidy)

## force-restart  Stop services, kill memory hogs (ollama), and restart
force-restart:
	$(call log_section,Force Restart)
	$(call log_step,docker compose,stopping all services)
	@set -o pipefail; docker compose down --remove-orphans 2>&1 | sed 's/^/  /'
	$(call log_step,memory,killing ollama to free VRAM)
	@-killall -9 ollama 2>/dev/null || true
	$(call log_step,memory,killing lingering yummi processes)
	@-killall -9 yummi yummi-orchestrator 2>/dev/null || true
	@$(MAKE) _ollama-ensure
	$(call log_step,docker compose,bringing up all services)
	@set -o pipefail; docker compose up -d gateway sandbox orchestrator yummi-ui 2>&1 | sed 's/^/  /'
	$(call log_ok,force-restart,services restarted)

## close          Stop all services, Docker, LLM models, and free RAM
close:
	$(call log_section,Close System)
	$(call log_step,docker compose,stopping all services)
	@set -o pipefail; docker compose down --remove-orphans 2>&1 | sed 's/^/  /'
	$(call log_step,memory,stopping ollama service and freeing VRAM)
	@brew services stop ollama 2>/dev/null || true
	@-killall -9 ollama 2>/dev/null || true
	@-killall -9 Ollama 2>/dev/null || true
	$(call log_step,memory,killing lingering yummi processes)
	@-killall -9 yummi yummi-orchestrator 2>/dev/null || true
	$(call log_ok,close,all processes and models successfully stopped)

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  DEVELOPER COMMANDS  (granular, fast feedback loops)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

## test    Run all test suites (CLI + orchestrator + gateway + UI)
test: _test-cli _test-orchestrator _test-gateway _test-ui
	$(call log_section,All Tests Passed)

## test-agent-live  End-to-end agentic loop against the REAL model + sandbox container
# Exercises the full tag-protocol pipeline (plan → steps → verify → memory →
# finish) with real qwen3:8b and the live sandbox Kali container. Ensures Ollama is
# up; the test self-skips if the sandbox container is not running (start it first
# with `make start`). Bypasses gateway (host has no socket) and targets 127.0.0.1.
test-agent-live: _ollama-ensure
	$(call log_section,Agent Live E2E)
	$(call log_step,go test,real $(OLLAMA_MODEL) + sandbox container)
	@set -o pipefail; cd orchestrator && YUMMI_LIVE=1 YUMMI_OLLAMA_URL=$(OLLAMA_URL) YUMMI_OLLAMA_MODEL=$(OLLAMA_MODEL) \
		go test ./internal/agent/ -run TestRunnerLive -v -timeout 15m 2>&1 | sed 's/^/  /'
	$(call log_ok,agent live e2e,done)

## lint    Run Go static analysis
lint:
	$(call log_section,Lint)
	$(call log_step,golangci-lint,root module)
	@golangci-lint run
	$(call log_step,golangci-lint,orchestrator module)
	@cd orchestrator && golangci-lint run
	$(call log_ok,lint,no issues)

## doctor  Run local environment diagnostics
doctor:
	@./bin/$(BINARY) --doctor 2>&1 || ./scripts/doctor.sh

## install Bootstrap local developer environment
install:
	$(call log_section,Install)
	@./scripts/install.sh

## dev     Run local development bootstrap (hot reload)
dev:
	@./scripts/dev.sh

# ── Granular build pieces (prefixed with _ to signal internal use) ─────────────

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

_build-cli: | $(BIN_DIR)
	$(call log_step,yummi CLI,$(GIT_SHA))
	@go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CLI_PKG)
	$(call log_ok,yummi,built)

_build-ui:
	$(call log_step,UI,install dependencies)
	@if [ -f "$(UI_DIR)/package-lock.json" ]; then cd $(UI_DIR) && npm ci --silent; \
	 else cd $(UI_DIR) && npm install --silent; fi
	$(call log_step,UI,vite build)
	@set -o pipefail; cd $(UI_DIR) && npm run build 2>&1 | sed 's/^/  /'
	$(call log_ok,UI,dist ready)

_build-ui-image: _build-ui
	$(call log_step,yummi-ui,docker build)
	@docker build -t yummi/ui:local ./$(UI_DIR) 2>&1 | sed 's/^/  /'
	$(call log_ok,yummi-ui,image built)

_build-orchestrator: | $(BIN_DIR)
	$(call log_step,yummi-orchestrator,$(GIT_SHA))
	@cd orchestrator && go build -trimpath -o ../$(BIN_DIR)/$(ORCHESTRATOR_BINARY) $(ORCHESTRATOR_PKG)
	$(call log_ok,yummi-orchestrator,built)

_build-gateway:
	$(call log_step,gateway,cargo release)
	@set -o pipefail; cd $(GATEWAY_DIR) && cargo build --release 2>&1 | sed 's/^/  /'
	$(call log_ok,gateway,built)

_test-cli:
	$(call log_step,test,CLI + internal packages)
	@set -o pipefail; go test ./cmd/... ./internal/... 2>&1 | sed 's/^/  /'

_test-orchestrator:
	$(call log_step,test,orchestrator module)
	@set -o pipefail; cd orchestrator && go test ./... 2>&1 | sed 's/^/  /'

_test-gateway:
	$(call log_step,test,gateway Rust)
	@set -o pipefail; cd $(GATEWAY_DIR) && cargo test 2>&1 | sed 's/^/  /'

_test-ui:
	$(call log_step,UI,install dependencies)
	@if [ -f "$(UI_DIR)/package-lock.json" ]; then cd $(UI_DIR) && npm ci --silent; \
	 else cd $(UI_DIR) && npm install --silent; fi
	$(call log_step,test,UI vitest)
	@set -o pipefail; cd $(UI_DIR) && npm test 2>&1 | sed 's/^/  /'

# ── Ollama lifecycle (internal) ───────────────────────────────────────────────

# _ollama-ensure: starts the Ollama daemon if not running, then pulls the model
# if it isn't already present. Used by `start` and `force-restart`.
_ollama-ensure:
	$(call log_section,Ollama)
	@if curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1; then \
		printf "  $(GREEN)✓$(RESET) $(BOLD)ollama$(RESET) $(DIM)daemon already running$(RESET)\n"; \
	else \
		printf "  $(CYAN)▶$(RESET) $(BOLD)ollama$(RESET) $(DIM)starting daemon...$(RESET)\n"; \
		if command -v brew >/dev/null 2>&1 && brew list --formula ollama >/dev/null 2>&1; then \
			brew services start ollama >/dev/null 2>&1 || true; \
		elif command -v ollama >/dev/null 2>&1; then \
			nohup ollama serve >$(HOME)/.yummi/ollama.log 2>&1 & \
		else \
			printf "  $(RED)✗$(RESET) ollama not found — install from https://ollama.com\n"; \
			exit 1; \
		fi; \
		printf "  $(CYAN)▶$(RESET) $(BOLD)ollama$(RESET) $(DIM)waiting for API (up to 30s)...$(RESET)\n"; \
		for i in $$(seq 1 30); do \
			curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1 && break; \
			sleep 1; \
		done; \
		if ! curl -sf $(OLLAMA_URL)/ >/dev/null 2>&1; then \
			printf "  $(RED)✗$(RESET) Ollama API did not respond in 30s. Check logs: $$HOME/.yummi/ollama.log\n"; \
			exit 1; \
		fi; \
		printf "  $(GREEN)✓$(RESET) $(BOLD)ollama$(RESET) $(DIM)daemon ready$(RESET)\n"; \
	fi
	@if ollama list 2>/dev/null | grep -q "^$(OLLAMA_MODEL)"; then \
		printf "  $(GREEN)✓$(RESET) $(BOLD)ollama$(RESET) $(DIM)$(OLLAMA_MODEL) already present$(RESET)\n"; \
	else \
		printf "  $(CYAN)▶$(RESET) $(BOLD)ollama pull$(RESET) $(DIM)$(OLLAMA_MODEL) — first run may take several minutes$(RESET)\n"; \
		ollama pull $(OLLAMA_MODEL) 2>&1 | sed 's/^/  /'; \
		printf "  $(GREEN)✓$(RESET) $(BOLD)ollama$(RESET) $(DIM)$(OLLAMA_MODEL) ready$(RESET)\n"; \
	fi

# ── Docker helpers ────────────────────────────────────────────────────────────

docker-build: ## Build all Docker images (gateway + sandbox + orchestrator + ui)
	$(call log_section,Docker Build)
	@docker compose build gateway sandbox orchestrator yummi-ui

docker-orchestrator: ## Build orchestrator Docker image
	$(call log_section,Docker Build — orchestrator)
	@docker compose build orchestrator

docker-ui: ## Build yummi-ui Docker image
	$(call log_section,Docker Build — yummi-ui)
	@docker compose build yummi-ui

dev-ui: ## Start UI dev server (Vite HMR on :3000)
	$(call log_section,Dev — UI)
	@cd $(UI_DIR) && npm run dev

dev-orchestrator: ## Run orchestrator orchestrator locally (port 9080)
	$(call log_section,Dev — orchestrator)
	@cd orchestrator && go run ./cmd/orchestrator

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  HELP
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

help:
	@printf "\n$(BOLD)$(CYAN)  YUMMI$(RESET)  $(DIM)$(VERSION) · $(GIT_SHA)$(RESET)\n"
	@printf "$(DIM)  Privacy-first AI pentest conductor$(RESET)\n\n"
	@printf "$(BOLD)  User commands$(RESET)\n"
	@printf "  $(CYAN)make build$(RESET)       Build native binaries (CLI + orchestrator + gateway; UI separate)\n"
	@printf "  $(CYAN)make start$(RESET)       Start runtime services (docker compose)\n"
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
