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
OLLAMA_MODEL="${YUMMI_OLLAMA_MODEL:-gpt-oss:20b}"

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

# ── Trust-boundary canary ─────────────────────────────────────────────────────
# On some Docker Desktop versions, traffic forwarded between two custom bridge
# networks — exactly gateway's job, moving packets between yummi_internal and
# yummi_external — is silently dropped by Docker Desktop's own VM networking
# layer whenever the source network has internal:true set (see
# docs/installation.md's Docker Desktop VM networking callout). gateway's own
# nftables rules are correct in this failure mode and never see the problem —
# the sandbox just has no working egress even after an explicit allow rule,
# which otherwise looks like a confusing, unexplained hang to the operator.
# Only meaningful once the stack is actually running.
if docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-gateway$' \
   && docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^yummi-sandbox$'; then
  ORCH_URL="${YUMMI_ORCHESTRATOR_URL:-http://127.0.0.1:9080}"
  if curl -fsS --connect-timeout 3 "${ORCH_URL}/api/v1/system/status" >/dev/null 2>&1; then
    # A per-target allow rule (what this canary adds) is DELIBERATELY inert at
    # the kernel level while egress mode is "block" — block means "no external
    # egress, no exceptions," and that's enforced by omitting the
    # @allowed_targets accept rule entirely from the generated ruleset (see
    # gateway/src/network/nftables.rs's build_ruleset), not by ignoring the
    # rule some other way. Since "block" is the fail-closed default (and what
    # every restart resets to), the canary must switch to "allow" for its own
    # test and then restore whatever mode it found — otherwise this check
    # would misreport a perfectly healthy engine as hitting the Docker
    # Desktop bug, when the real cause is simply the normal default mode.
    prior_mode=$(curl -fsS "${ORCH_URL}/api/v1/gateway/egress-mode" 2>/dev/null | sed -n 's/.*"mode":"\([^"]*\)".*/\1/p')
    curl -fsS -X POST "${ORCH_URL}/api/v1/gateway/egress-mode" \
      -H "Content-Type: application/json" -d '{"mode":"allow"}' >/dev/null 2>&1 || true
    canary_resp=$(curl -fsS -X POST "${ORCH_URL}/api/v1/gateway/rules" \
      -H "Content-Type: application/json" \
      -d '{"network":"1.1.1.1/32","ttl_secs":30,"reason":"doctor trust boundary canary check"}' 2>/dev/null || true)
    rule_id=""
    if command -v jq >/dev/null 2>&1; then
      rule_id=$(echo "${canary_resp}" | jq -r '.rule_id // empty' 2>/dev/null || true)
    else
      rule_id=$(echo "${canary_resp}" | sed -n 's/.*"rule_id":"\([^"]*\)".*/\1/p')
    fi
    if [[ -n "${rule_id}" ]]; then
      # A freshly-added allow rule takes a few seconds to actually propagate
      # (nftables application + VM-layer route/ARP warm-up) — measured ~3-5s
      # on a healthy OrbStack setup. A single immediate attempt would false-
      # positive on that normal latency, so retry across a generous window
      # before concluding forwarding is actually broken, not just not-yet-live.
      reachable=0
      for _attempt in 1 2 3 4 5 6; do
        if docker exec yummi-sandbox curl -fsS -m 3 -o /dev/null https://1.1.1.1 >/dev/null 2>&1; then
          reachable=1
          break
        fi
        sleep 2
      done
      if [[ ${reachable} -eq 1 ]]; then
        echo "[ok] sandbox egress reaches an explicitly-allowed external target (trust boundary forwarding works)"
      else
        echo "[fail] gateway allowed 1.1.1.1 but the sandbox still could not reach it after ~15s of retries."
        echo "       This matches a known Docker Desktop VM networking bug — traffic between the"
        echo "       internal/external bridge networks is silently dropped, even though gateway's"
        echo "       own nftables rules are correct (this is NOT a Yummi bug)."
        echo "       Fix: switch to OrbStack (https://orbstack.dev) — confirmed working end-to-end —"
        echo "       or use native Linux Docker Engine. See docs/installation.md for the full writeup."
        status=1
      fi
      curl -fsS -X DELETE "${ORCH_URL}/api/v1/gateway/rules/${rule_id}" >/dev/null 2>&1 || true
    else
      echo "[warn] could not add a temporary gateway rule for the trust-boundary canary check — skipping"
    fi
    # Restore whatever egress mode was active before this check touched it —
    # this canary must never be the reason a stack is left less locked-down
    # than the operator had it. Defaults to the fail-closed "block" if the
    # prior mode couldn't be read for some reason.
    curl -fsS -X POST "${ORCH_URL}/api/v1/gateway/egress-mode" \
      -H "Content-Type: application/json" -d "{\"mode\":\"${prior_mode:-block}\"}" >/dev/null 2>&1 || true
  else
    echo "[warn] orchestrator API not reachable at ${ORCH_URL} — skipping trust-boundary canary check"
  fi
else
  echo "[info] gateway/sandbox not running — skipping trust-boundary canary check (run 'make start' first, then 'make doctor' again to verify forwarding)"
fi

if [[ ${status} -eq 0 ]]; then
  echo "doctor: healthy"
else
  echo "doctor: issues found"
fi

exit ${status}
