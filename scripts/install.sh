#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

YUMMI_HOME="${YUMMI_HOME:-${HOME}/.yummi}"
mkdir -p "${YUMMI_HOME}/wordlists" "${YUMMI_HOME}/workspace" "${YUMMI_HOME}/payloads" "${YUMMI_HOME}/logs"

if [[ ! -f "${YUMMI_HOME}/config.yaml" ]]; then
  cat > "${YUMMI_HOME}/config.yaml" <<'CFG'
version: 1
runtime:
  compose_file: docker-compose.yml
  sandbox_service: sandbox
  preferred_shell: /bin/bash
CFG
fi

if [[ ! -f "${YUMMI_HOME}/wordlists/.README" ]]; then
  cat > "${YUMMI_HOME}/wordlists/.README" <<'TXT'
Place your custom wordlists in this directory.
This directory is mounted read-only into the sandbox at /wordlists.
TXT
fi

if [[ ! -f "${YUMMI_HOME}/payloads/.README" ]]; then
  cat > "${YUMMI_HOME}/payloads/.README" <<'TXT'
Place payload collections (e.g., PayloadsAllTheThings subsets) here.
This directory is mounted read-only into the sandbox at /payloads.
TXT
fi

docker compose build sandbox

# ── Install UI dependencies ──────────────────────────────────────────────────
echo "Installing UI dependencies..."
if [ -d "ui" ]; then
  (cd ui && npm install)
fi

# ── Ollama + model ──────────────────────────────────────────────────────────
if ! command -v ollama &>/dev/null; then
  echo "Ollama not found. Installing..."
  if [[ "$(uname)" == "Darwin" ]]; then
    if command -v brew &>/dev/null; then
      brew install ollama
    else
      curl -fsSL https://ollama.com/install.sh | sh
    fi
  else
    curl -fsSL https://ollama.com/install.sh | sh
  fi
fi

OLLAMA_MODEL="${YUMMI_OLLAMA_MODEL:-qwen3:8b}"
if ! ollama list 2>/dev/null | grep -q "${OLLAMA_MODEL%%:*}"; then
  echo "Pulling ${OLLAMA_MODEL} (this may take a few minutes)..."
  ollama pull "${OLLAMA_MODEL}"
else
  echo "Ollama model ${OLLAMA_MODEL} already present."
fi

echo "Yummi install complete. Home: ${YUMMI_HOME}"
