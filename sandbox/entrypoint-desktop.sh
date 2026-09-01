#!/usr/bin/env bash
# sandbox-desktop entrypoint — same fail-closed routing as the headless
# sandbox's entrypoint.sh, then brings up a full XFCE desktop reached via
# noVNC (websockify bridges a WebSocket to the VNC server). Runs as root
# initially (route setup + ownership fixups, matching the headless
# entrypoint's own pattern), then drops to the pentester user for the
# desktop session itself — nothing pentest-facing ever runs as root here.
set -euo pipefail
IFS=$'\n\t'

GATEWAY_GW="172.20.0.2"
DISPLAY_NUM="1"
VNC_PORT="590${DISPLAY_NUM}"
WS_PORT="6080"

echo "[sandbox-desktop] routing external traffic through gateway (${GATEWAY_GW})..."
ip route replace default via "${GATEWAY_GW}"
echo "[sandbox-desktop] default gateway → ${GATEWAY_GW} (fail-closed)"

# XDG_RUNTIME_DIR is required by dbus/XFCE for their own runtime sockets —
# created here (root) so ownership is correct before anything drops to the
# pentester user. Needs CAP_DAC_OVERRIDE (mkdir/write past normal
# permission checks), CAP_CHOWN, and CAP_FOWNER (chmod on a path not
# currently owned by the calling process) — see docker-compose.yml's
# cap_add list and its comment for why cap_drop ALL means even root needs
# these spelled out explicitly.
mkdir -p /run/user/1000
chown 1000:1000 /run/user/1000
chmod 0700 /run/user/1000

# This image is NOT read-only (see Dockerfile.desktop's own comment and
# docs/superpowers/specs/2026-09-01-browser-linux-pwnbox-design.md) — XFCE,
# dbus, and VNC all write scattered session state (~/.cache, ~/.config,
# ~/.vnc, fontconfig cache) that isn't worth chasing path-by-path for a
# first working version. Explicit, documented relaxation, not an oversight.
mkdir -p /home/pentester/.vnc
chown -R 1000:1000 /home/pentester

echo "[sandbox-desktop] starting Xtigervnc on :${DISPLAY_NUM}..."
# -SecurityTypes None: no VNC password. The only path in is orchestrator's
# own /ws/desktop proxy (never a published host port), matching the
# terminal bridge's identical no-extra-auth model — this is Yummi's
# established local-only single-operator trust boundary, not a new
# exception carved out for the desktop.
su - pentester -c "
  export DISPLAY=:${DISPLAY_NUM}
  export XDG_RUNTIME_DIR=/run/user/1000
  Xtigervnc :${DISPLAY_NUM} -geometry 1600x900 -depth 24 -SecurityTypes None -localhost no &
"

# Give Xvnc a moment to bind before anything tries to connect to :DISPLAY.
for i in $(seq 1 20); do
  if [ -e "/tmp/.X11-unix/X${DISPLAY_NUM}" ]; then break; fi
  sleep 0.5
done

echo "[sandbox-desktop] starting XFCE session..."
su - pentester -c "
  export DISPLAY=:${DISPLAY_NUM}
  export XDG_RUNTIME_DIR=/run/user/1000
  dbus-launch --exit-with-session xfce4-session > /tmp/xfce4-session.log 2>&1 &
"

echo "[sandbox-desktop] starting websockify (ws ${WS_PORT} -> vnc ${VNC_PORT})..."
echo "[sandbox-desktop] ready — $(nmap --version 2>&1 | head -1)"

# The bridge IS the container's liveness signal: if this dies, the
# container dies, exactly like the headless sandbox's `exec sleep infinity`
# ties liveness to nothing failing — here it's tied to something concrete.
exec websockify --web=/usr/share/novnc "${WS_PORT}" "localhost:${VNC_PORT}"
