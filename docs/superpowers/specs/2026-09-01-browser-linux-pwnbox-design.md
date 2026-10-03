# Browser-accessible Linux (Pwnbox-style) — engineering assessment

**Date:** 2026-09-01
**Status:** BUILT AND LIVE-VERIFIED — both stages. Stage 1 (persistent
tmux-backed terminal) and Stage 2 (full noVNC/XFCE desktop) are implemented,
committed, and confirmed working end to end in a real browser: a live XFCE
session rendered, a mouse click opened a real xfce4-terminal showing the
sandbox's actual hostname. See the "operator drives, AI assists" direction
doc and this repo's commit history (`feat(terminal): tmux-backed persistent
sessions`, `feat(desktop): Stage 2`) for the implementation. The assessment
below is kept as-is for the reasoning; treat its "nothing built yet" framing
as historical.

**Update 2026-09-04 — unified into a single container.** Stage 2 originally
shipped as a separate opt-in `sandbox-desktop` container (`make
start-desktop`), so the agent's exec path and the GUI ran in two different
environments with two different tool sets. That's now retired: `sandbox/
Dockerfile` and `sandbox/entrypoint.sh` are the union of both (headless tool
set + XFCE/VNC/Burp/Wireshark), the compose service is just `sandbox`
(container `yummi-sandbox`, no `profiles: ["desktop"]` gate), and `make
start` brings it up unconditionally — the Desktop tab is reachable at
http://localhost:3000/desktop immediately, no second build/target. Zero
environmental difference between what the agent can reach and what the
operator sees in the browser: same filesystem, same tools, same container.
Traded away: the read-only rootfs the headless variant had (XFCE/VNC need
writable session state) — the network trust boundary (gateway's fail-closed
nftables) is completely unaffected, since it never depended on which
container held the tools.

**Update 2026-10-03 — VPN, Kerberos, revshell, wordlist ergonomics.** Four
operational improvements were added to reduce engagement friction to zero:
- **`openvpn` + `/dev/net/tun`**: `entrypoint.sh` creates the TUN device at
  startup (as root, before the privilege drop to pentester). `openvpn --config
  lab.ovpn` in a `term_run(wait=false)` + `term_wait("Initialization Sequence
  Completed")` pair brings up `tun0` — HackTheBox, TryHackMe, Proving Grounds,
  and client lab connectivity from inside the sandbox, all still routed through
  the gateway's fail-closed nftables boundary.
- **`krb5-user`**: `kinit`/`klist`/`kdestroy` for native Kerberos ticket
  management. Enables Pass-the-Ticket, PKINIT, and full Kerberoasting/AS-REP
  Roasting workflows natively alongside `certipy`, `impacket`, and `netexec`.
- **`revshell` CLI**: `/usr/local/bin/revshell [PORT]` auto-detects LHOST from
  `tun0`/`eth0` and prints copy-paste payloads for Bash, Python3, Netcat,
  Socat, PHP, Perl, Ruby, PowerShell, plus listener starters — no external
  lookup required.
- **Wordlist shortcut variables**: `$ROCKYOU`, `$COMMON_TXT`, `$BIG_TXT`,
  `$MEDIUM_TXT`, `$USERNAMES`, `$PASSWORDS_TOP` exported in `.bashrc` — both
  operator and agent reference common wordlist paths without spelling out the
  full `/usr/share/seclists/...` path.
- **`metasploit-framework`**: Full Metasploit suite (`msfconsole`, `msfvenom`,
  meterpreter payload generation and multi-handlers) baked in with desktop launcher.
- **`nishang`**: Offensive PowerShell framework symlinked into user home and Desktop
  for post-exploitation against Windows targets.
- **`kerbrute`**: Rapid Go-based Kerberos user enumeration and password spraying.
- **Environment Identity (`yummi-pwnbox`)**: Dedicated container hostname
  `yummi-pwnbox`, tactical prompt, custom interactive greeting MOTD, and
  branded desktop wallpaper (`YUMMI PWNBOX: Autonomous AI Offensive Operations & Tactical Range`).


## The ask

Turn Yummi's sandbox into a browser-accessible full Linux the way HackTheBox
(Pwnbox) and Hackviser expose a cloud Kali/Parrot desktop through the
browser. The operator's words: *"if we pull this off, Yummi becomes exactly
what we want."*

## What those platforms actually are

HTB Pwnbox = a real Kali/Parrot Linux with a graphical desktop (XFCE),
reached through the browser via **noVNC** (VNC streamed over a WebSocket) or
Apache Guacamole. The operator gets GUI tools (Burp, Wireshark, Ghidra, a
browser), a file manager, and multiple terminals — a machine they live in.

## What Yummi's sandbox is today

Headless Kali · read-only root filesystem · non-root (UID 1000) ·
`no-new-privileges` · **passive** (no persistent process — every command is a
fresh `docker exec`). The Bash tab is a single xterm.js web terminal over a
PTY WebSocket. This posture is not incidental — it's most of what the
security docs actually claim.

## The honest tension (must not be glossed over)

A full graphical desktop is the **opposite of passive**: a long-running VNC
session, a display server, a window manager, GUI apps that need a writable
home for config/cache. It directly relaxes three properties the security
model currently states: passive-sandbox, read-only-rootfs, and
single-`docker-exec`-entry.

**What it does NOT touch — and this is the saving grace:** the network trust
boundary. Whether the operator drives via headless `docker exec`, a web
terminal, or a full VNC desktop, **every packet still exits through the
gateway's nftables policy.** Pwnbox-ification changes the *interaction
surface inside the box*, not the *network boundary around it*. Fail-closed
egress survives intact. What grows is the container-internal attack surface
and writable state — real, but bounded, and orthogonal to the thing that
makes Yummi Yummi.

## Recommended path — two stages, because 80% of the feeling costs 20% of the risk

### Stage 1 — persistent, richer web terminal (low risk, buildable now)

Make the Bash tab feel like *the operator's box*, not a throwaway shell:
- A **tmux-backed session** inside the sandbox so a reconnect resumes exactly
  where they left off (survives WS drops, tab switches, refreshes).
- **Multiple terminals** (tmux windows/panes, or multiple PTY tabs in the UI).
- Optional lightweight **file browser** over a read endpoint.

Keeps the security posture almost entirely intact — still terminal-only,
still one `docker exec` entry, still crosses the gateway. This alone closes
most of the "bare shell, cut off from everything" complaint, especially
paired with the operator-briefing companion already shipped.

### Stage 2 — optional graphical desktop via noVNC (opt-in, documented tradeoff)

The true Pwnbox. A **separate sandbox image variant / compose profile**
(`sandbox-desktop`) carrying XFCE + TigerVNC + noVNC, so:
- The **default deployment stays the hardened headless one** — nobody gets
  the relaxed posture unless they opt in.
- The desktop variant documents *exactly* which properties it relaxes
  (writable home, persistent process tree, display server) and which it
  keeps (the gateway boundary, non-root, `no-new-privileges`).
- The UI gains a "Desktop" surface that embeds the noVNC canvas the same way
  the Bash tab embeds xterm.js.

### Why this is also the answer to the operator's original RE-sandbox idea

Reverse-engineering tooling (Ghidra, a GUI debugger, a disassembler) *cannot*
be driven from a headless `docker exec` — it needs a desktop. So the Pwnbox
direction and the operator's original "separate RE environment" idea are the
**same architectural fork**: a richer interactive (and for RE, graphical)
environment the operator inhabits. A dedicated `sandbox-desktop` profile is
naturally where an isolated RE environment lives — a detonated binary in a
graphical RE box, still behind the same gateway, still unable to reach the
network except through fail-closed rules. Stage 2 and the RE-environment fork
should be designed as one thing, not two.

## Decision needed from the operator

- Stage 1 is unambiguously safe and wanted — can start immediately.
- Stage 2 trades documented security properties for the full Pwnbox
  experience. That is a security-expert's call (the operator's), not a
  default to bake in silently. The gateway boundary is preserved either way.
