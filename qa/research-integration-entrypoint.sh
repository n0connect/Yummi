#!/bin/sh
set -eu
# Disposable --network none container only. No host mounts or Docker socket.
# Operator executor is UID0; CommandTransport explicitly starts installed MCP as
# UID/GID1000 with an empty supplementary group list, matching production roles.
ip addr add 172.20.0.3/32 dev lo
mkdir -p /run/yummi /var/lib/yummi/gateway
sed -i 's@/run/yummi/gateway.sock@/run/yummi/research-gateway.sock@' /etc/yummi/research-gateway.yaml
printf true > /tmp/yummi_web_search_state
chmod 0644 /tmp/yummi_web_search_state
/usr/local/bin/yummi-gateway --config /etc/yummi/research-gateway.yaml >/tmp/research-gateway.log 2>&1 &
gateway_pid=$!
trap 'kill "$gateway_pid" 2>/dev/null || true' EXIT INT TERM
n=0
while [ ! -S /run/yummi/research-gateway.sock ]; do
 n=$((n + 1))
 if [ "$n" -gt 100 ] || ! kill -0 "$gateway_pid" 2>/dev/null; then
  cat /tmp/research-gateway.log
  exit 1
 fi
 sleep .05
done
run_agent=0
if [ "$#" -eq 0 ]; then
 run_agent=1
 set -- -test.run '^TestResearchApprovalRealSandboxIntegration$' -test.v -test.timeout=90s
fi
/usr/local/bin/research-integration.test "$@"
if [ "$run_agent" -eq 1 ]; then
 /usr/local/bin/agent-approval-integration.test -test.run '^TestApprovedMCPInstalledSandboxIntegration$' -test.v -test.timeout=30s
fi
