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
# XFCE session start AND the wallpaper branding below run in the SAME `su`
# shell invocation, sharing the SAME D-Bus session bus — critical, and the
# reason an earlier version of this silently never applied the wallpaper:
# `dbus-launch --exit-with-session CMD` exports DBUS_SESSION_BUS_ADDRESS
# only into CMD's own environment, not back into this script, so a
# SEPARATE later `su -c "..."` invocation (a fresh shell, fresh D-Bus
# session) could never see xfce4-desktop's real xfconf properties — its
# own `xfconf-query -c xfce4-desktop -l` came back nearly empty every
# time, live-confirmed. `eval $(dbus-launch --sh-syntax)` instead sets the
# session bus address in THIS shell explicitly, so everything after it —
# xfce4-session itself and the wallpaper xfconf-query calls — talks to the
# one real session bus.
su - pentester -c "
  export DISPLAY=:${DISPLAY_NUM}
  export XDG_RUNTIME_DIR=/run/user/1000
  eval \$(dbus-launch --sh-syntax)
  export DBUS_SESSION_BUS_ADDRESS DBUS_SESSION_BUS_PID
  xfce4-session > /tmp/xfce4-session.log 2>&1 &

  # Applies the Yummi-branded wallpaper once xfwm4/Xtigervnc's RandR output
  # is up (xrandr -q succeeding is the real readiness signal — xfce4-desktop
  # itself never pre-populates these xfconf properties at all on a fresh
  # profile, confirmed live: -l on the xfce4-desktop channel came back
  # with nothing under /backdrop even long after the desktop was fully
  # rendered, so discovering an EXISTING property to overwrite never found
  # one — the earlier version of this loop that tried that starved out
  # every time). -n creates the property outright instead. 'VNC-0' is
  # TigerVNC's own fixed RandR output name (confirmed via xrandr -q in this
  # exact image), not a guess. Backgrounded and entirely best-effort: a
  # branding step failing must never take down the desktop session itself.
  (
    for i in \$(seq 1 30); do
      if xrandr -q > /dev/null 2>&1; then
        xfconf-query -c xfce4-desktop -p /backdrop/screen0/monitorVNC-0/workspace0/last-image -n -t string -s /usr/share/backgrounds/yummi/wallpaper.png 2>/dev/null || true
        xfconf-query -c xfce4-desktop -p /backdrop/screen0/monitorVNC-0/workspace0/image-style -n -t int -s 5 2>/dev/null || true
        xfdesktop --reload > /dev/null 2>&1 || true
        break
      fi
      sleep 1
    done
  ) &
  wait
" > /tmp/xfce-shell.log 2>&1 &

echo "[sandbox-desktop] starting websockify (ws ${WS_PORT} -> vnc ${VNC_PORT})..."
echo "[sandbox-desktop] ready — $(nmap --version 2>&1 | head -1)"

# The bridge IS the container's liveness signal: if this dies, the
# container dies, exactly like the headless sandbox's `exec sleep infinity`
# ties liveness to nothing failing — here it's tied to something concrete.
exec websockify --web=/usr/share/novnc "${WS_PORT}" "localhost:${VNC_PORT}"
