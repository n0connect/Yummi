#!/bin/sh
# Adds the one route this box needs to ever reach the sandbox on its own
# initiative (a reverse shell, or anything else it connects out with) —
# see the Dockerfile's step 5 comment and
# docker-compose.override.lab-target.yml for why this exists and what sets
# SANDBOX_NET / LAB_GATEWAY_IP. Best-effort: a missing NET_ADMIN cap or an
# unset variable must never stop the web server from starting, since the
# RCE foothold itself doesn't depend on this route existing yet.
set -eu

if [ -n "${SANDBOX_NET:-}" ] && [ -n "${LAB_GATEWAY_IP:-}" ]; then
	if ! ip route add "$SANDBOX_NET" via "$LAB_GATEWAY_IP" 2>/tmp/route.err; then
		echo "lab-target entrypoint: could not add route to $SANDBOX_NET via $LAB_GATEWAY_IP (continuing without it):" >&2
		cat /tmp/route.err >&2
	fi
	rm -f /tmp/route.err
fi

exec "$@"
