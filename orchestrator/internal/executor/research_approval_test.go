package executor

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestResearchApprovalBoundary(t *testing.T) {
	valid := `{"kind":"egress_approval_required","target":"www.openssh.com","ips":["1.1.1.1"]}`
	for _, tc := range []struct {
		tool, text    string
		isError, want bool
	}{
		{"web_fetch", valid, true, true},
		{"web_search", valid, true, true},
		{"web_fetch", valid, false, false},
		{"browser_eval", valid, true, false},
		{"web_fetch", `{"kind":"egress_approval_required","target":"localhost","ips":["127.0.0.1"]}`, true, false},
		{"web_fetch", `{"kind":"egress_approval_required","target":"1.1.1.1","ips":["8.8.8.8"]}`, true, false},
		{"web_fetch", `{"kind":"egress_approval_required","target":"https://example.com","ips":["1.1.1.1"]}`, true, false},
		{"web_fetch", valid + `{}`, true, false},
		{"web_fetch", `{"kind":"egress_approval_required","target":"example.com","ips":["172.20.0.3"]}`, true, false},
	} {
		r := &mcp.CallToolResult{IsError: tc.isError, Content: []mcp.Content{&mcp.TextContent{Text: tc.text}}}
		got := researchApproval(tc.tool, r)
		if (got != nil) != tc.want {
			t.Errorf("tool=%s error=%v report=%s: got %v", tc.tool, tc.isError, tc.text, got)
		}
	}
}
