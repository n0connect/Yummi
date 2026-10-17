package executor

import (
	"net/netip"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n0connect/yummi/orchestrator/internal/controlaccess"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

var researchApprovalHost = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

// researchApproval recognizes only an explicit error emitted by the installed
// research tool. Successful/untrusted web content can never request approval.
// Pinned addresses came from sandbox DNS; do not resolve them on this host.
func researchApproval(tool string, result *mcp.CallToolResult) *execution.EgressApprovalRequiredError {
	if (tool != "web_search" && tool != "web_fetch") || result == nil || !result.IsError || len(result.Content) != 1 {
		return nil
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok || len(content.Text) > 4096 {
		return nil
	}
	var report struct {
		Kind   string   `json:"kind"`
		Target string   `json:"target"`
		IPs    []string `json:"ips"`
	}
	if controlaccess.DecodeObject([]byte(content.Text), &report, "kind", "target", "ips") != nil || report.Kind != "egress_approval_required" || len(report.IPs) == 0 || len(report.IPs) > 8 {
		return nil
	}
	targetIP, targetErr := netip.ParseAddr(report.Target)
	if targetErr != nil && (!researchApprovalHost.MatchString(report.Target) || strings.Contains(report.Target, "..")) {
		return nil
	}
	seen := map[netip.Addr]bool{}
	for _, raw := range report.IPs {
		ip, err := netip.ParseAddr(raw)
		if err != nil || !publicResearchApprovalIP(ip) {
			return nil
		}
		if targetErr == nil && ip.Unmap() != targetIP.Unmap() {
			return nil
		}
		if seen[ip.Unmap()] {
			return nil
		}
		seen[ip.Unmap()] = true
	}
	return &execution.EgressApprovalRequiredError{Target: report.Target, IPs: report.IPs}
}

// Match the independently enforced sandbox research address boundary. In
// particular, global-unicast alone includes documentation, shared and NAT64
// ranges, which must never become an operator-approved research destination.
func publicResearchApprovalIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range researchReservedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var researchReservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"),
}
