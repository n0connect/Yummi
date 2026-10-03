# yummi-pwnbox workbench environment — copied to /etc/profile.d/yummi-pwnbox.sh.
#
# Lives here, not in ~/.bashrc: Kali's stock .bashrc returns early for
# non-interactive shells, and the agent runs every command as `bash -lc`
# (non-interactive). /etc/profile.d is sourced by login shells, so these
# reach agent commands AND the operator's tmux shells. Aliases do not expand
# in non-interactive shells, so anything the agent must use is a real binary.
#   Manifest for the model: /etc/yummi/pwnbox.md

export ROCKYOU='/usr/share/wordlists/rockyou.txt'
export COMMON_TXT='/usr/share/seclists/Discovery/Web-Content/common.txt'
export BIG_TXT='/usr/share/seclists/Discovery/Web-Content/big.txt'
export MEDIUM_TXT='/usr/share/seclists/Discovery/Web-Content/directory-list-2.3-medium.txt'
export USERNAMES='/usr/share/seclists/Usernames/top-usernames-shortlist.txt'
export PASSWORDS_TOP='/usr/share/seclists/Passwords/Common-Credentials/10k-most-common.txt'
