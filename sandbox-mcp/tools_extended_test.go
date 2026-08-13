package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExtendedTools_EmptyRequiredFieldsRejected(t *testing.T) {
	session := newTestServer(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"amass_enum", map[string]any{"domain": ""}},
		{"subfinder_enum", map[string]any{"domain": ""}},
		{"theharvester_scan", map[string]any{"domain": ""}},
		{"dnsenum_scan", map[string]any{"domain": ""}},
		{"enum4linux_scan", map[string]any{"target": ""}},
		{"onesixtyone_scan", map[string]any{"target": ""}},
		{"wafw00f_scan", map[string]any{"url": ""}},
		{"wpscan_scan", map[string]any{"url": ""}},
		{"nuclei_scan", map[string]any{"target": ""}},
		{"sqlmap_scan", map[string]any{"url": ""}},
		{"dirb_scan", map[string]any{"url": ""}},
		{"hydra_bruteforce", map[string]any{"target": "", "service": "", "pass_list": "x"}},
		{"medusa_bruteforce", map[string]any{"target": "", "module": "", "pass_list": "x"}},
	}
	for _, c := range cases {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s: CallTool: %v", c.tool, err)
		}
		if !result.IsError {
			t.Errorf("%s: expected IsError for empty required fields", c.tool)
		}
	}
}

func TestHydraBruteforce_RequiresUserOrUserList(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hydra_bruteforce",
		Arguments: map[string]any{"target": "10.0.0.5", "service": "ssh", "pass_list": "common.txt"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError when neither user nor user_list is given")
	}
}

func TestHydraBruteforce_RejectsWordlistTraversal(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "hydra_bruteforce",
		Arguments: map[string]any{"target": "10.0.0.5", "service": "ssh", "user": "admin", "pass_list": "../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for a path-traversal pass_list")
	}
}
