package executor

import (
	"bytes"
	"encoding/json"
	"io"
	"net/netip"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	dec := json.NewDecoder(bytes.NewBufferString(content.Text))
	dec.DisallowUnknownFields()
	if dec.Decode(&report) != nil || dec.Decode(new(any)) != io.EOF || report.Kind != "egress_approval_required" || len(report.IPs) == 0 || len(report.IPs) > 8 {
		return nil
	}
	targetIP, targetErr := netip.ParseAddr(report.Target)
	if targetErr != nil && (!researchApprovalHost.MatchString(report.Target) || strings.Contains(report.Target, "..")) {
		return nil
	}
	seen := map[netip.Addr]bool{}
	for _, raw := range report.IPs {
		ip, err := netip.ParseAddr(raw)
		if err != nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
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
