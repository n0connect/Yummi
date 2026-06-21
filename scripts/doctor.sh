#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

YUMMI_HOME="${YUMMI_HOME:-${HOME}/.yummi}"
status=0

check_cmd() {
  local name="$1"
  if command -v "$name" >/dev/null 2>&1; then
    echo "[ok] ${name} found"
  else
    echo "[fail] ${name} not found"
    status=1
  fi
}

check_cmd docker

if docker compose version >/dev/null 2>&1; then
  echo "[ok] docker compose available"
else
  echo "[fail] docker compose unavailable"
  status=1
fi

if docker info >/dev/null 2>&1; then
  echo "[ok] docker daemon reachable"
else
  echo "[fail] docker daemon unreachable"
  status=1
fi

arch="$(uname -m)"
if [[ "${arch}" == "arm64" || "${arch}" == "aarch64" ]]; then
  echo "[ok] architecture ${arch}"
else
  echo "[warn] architecture ${arch} (ARM64 is recommended)"
fi

for d in "${YUMMI_HOME}" "${YUMMI_HOME}/wordlists" "${YUMMI_HOME}/workspace" "${YUMMI_HOME}/payloads" "${YUMMI_HOME}/logs"; do
  if [[ -d "${d}" ]]; then
    echo "[ok] ${d}"
  else
    echo "[fail] missing ${d}"
    status=1
  fi
done


# ── Ollama single-model checks ────────────────────────────────────────────────
OLLAMA_URL="${YUMMI_OLLAMA_URL:-http://localhost:11434}"
OLLAMA_MODEL="${YUMMI_OLLAMA_MODEL:-qwen3:8b}"

if curl -fsS --connect-timeout 3 "${OLLAMA_URL}/api/tags" >/dev/null 2>&1; then
  echo "[ok] ollama reachable at ${OLLAMA_URL}"

  # Check if the configured model is available locally
  if curl -fsS --connect-timeout 3 "${OLLAMA_URL}/api/tags" 2>/dev/null | grep -q "\"${OLLAMA_MODEL}\""; then
    echo "[ok] model ${OLLAMA_MODEL} available"
  else
    echo "[warn] model ${OLLAMA_MODEL} not found — run: ollama pull ${OLLAMA_MODEL}"
    status=1
  fi

  # Check how many models are currently loaded (requires jq)
  if command -v jq >/dev/null 2>&1; then
    loaded_count=$(curl -fsS --connect-timeout 3 "${OLLAMA_URL}/api/ps" 2>/dev/null \
      | jq -r '.models | length' 2>/dev/null || echo "0")
    if [[ "${loaded_count}" -gt 1 ]]; then
      echo "[warn] ${loaded_count} models loaded simultaneously — set OLLAMA_MAX_LOADED_MODELS=1 to prevent VRAM contention"
    elif [[ "${loaded_count}" -eq 1 ]]; then
      loaded_name=$(curl -fsS --connect-timeout 3 "${OLLAMA_URL}/api/ps" 2>/dev/null \
        | jq -r '.models[0].name' 2>/dev/null || echo "unknown")
      echo "[ok] ${loaded_name} loaded in memory (single model)"
    fi
  fi
else
  echo "[warn] ollama not reachable at ${OLLAMA_URL} — start Ollama or set YUMMI_OLLAMA_URL"
fi

# Recommend OLLAMA_MAX_LOADED_MODELS=1 if not already set
if [[ "${OLLAMA_MAX_LOADED_MODELS:-}" != "1" ]]; then
  echo "[warn] OLLAMA_MAX_LOADED_MODELS is not set to 1 — multiple models may load into VRAM"
  echo "       macOS : launchctl setenv OLLAMA_MAX_LOADED_MODELS 1 && killall Ollama && open -a Ollama"
  echo "       Linux : add 'Environment=OLLAMA_MAX_LOADED_MODELS=1' to the Ollama systemd service"
fi

if [[ ${status} -eq 0 ]]; then
  echo "doctor: healthy"
else
  echo "doctor: issues found"
fi

exit ${status}
