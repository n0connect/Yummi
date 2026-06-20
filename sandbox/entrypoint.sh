#!/usr/bin/env bash
# sandbox entrypoint — routes all external traffic through gateway (fail-closed gateway).
# gateway is guaranteed healthy before this container starts (docker-compose depends_on).
set -euo pipefail
IFS=$'\n\t'

GATEWAY_GW="172.20.0.2"

echo "[sandbox] routing external traffic through gateway (${GATEWAY_GW})..."

# Replace the default route so all non-subnet traffic goes through gateway.
# gateway's nftables FORWARD chain will drop it unless the operator has approved
# the target via orchestrator's approval flow.
ip route replace default via "${GATEWAY_GW}"

echo "[sandbox] default gateway → ${GATEWAY_GW} (fail-closed)"
echo "[sandbox] ready — $(nmap --version 2>&1 | head -1)"

exec sleep infinity
