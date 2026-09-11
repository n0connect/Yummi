package main

import (
	"context"
	"strings"
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
		{"snmpwalk_enum", map[string]any{"target": ""}},
		{"hydra_bruteforce", map[string]any{"target": "", "service": "", "pass_list": "x"}},
		{"medusa_bruteforce", map[string]any{"target": "", "module": "", "pass_list": "x"}},
		{"httpx_probe", map[string]any{"target": ""}},
		{"dnsx_resolve", map[string]any{"domain": ""}},
		{"alterx_mutate", map[string]any{"domain": ""}},
		{"gau_fetch", map[string]any{"domain": ""}},
		{"katana_crawl", map[string]any{"url": ""}},
		{"dalfox_scan", map[string]any{"url": ""}},
		{"checksec_audit", map[string]any{"file": ""}},
		{"radare2_disasm", map[string]any{"file": ""}},
		{"arjun_scan", map[string]any{"url": ""}},
		{"naabu_scan", map[string]any{"target": ""}},
		{"tlsx_scan", map[string]any{"target": ""}},
		{"cloud_enum_scan", map[string]any{"keyword": ""}},
		{"jwt_tool_inspect", map[string]any{"token": ""}},
		{"subzy_scan", map[string]any{"target": ""}},
		{"jsluice_scan", map[string]any{"url": ""}},
		{"graphql_cop_scan", map[string]any{"url": ""}},
		{"corsy_scan", map[string]any{"url": ""}},
		{"screenshot_triage", map[string]any{"target": "", "output_dir": ""}},
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

// TestExtendedTools_RejectsFlagInjection proves every field passed as a bare
// positional argument to its underlying binary (no preceding flag of its own
// claiming the next argv slot) is refused when it would be parsed as a flag
// instead of data — see rejectIfFlagLike's doc comment. Fields passed
// immediately after their own flag (e.g. "-d", domain) are excluded here:
// they are already safe regardless of content, since the flag consumes the
// next argv slot as its literal value no matter what it looks like.
func TestExtendedTools_RejectsFlagInjection(t *testing.T) {
	session := newTestServer(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"dnsenum_scan", map[string]any{"domain": "--dnsserver=evil"}},
		{"enum4linux_scan", map[string]any{"target": "--shares"}},
		{"onesixtyone_scan", map[string]any{"target": "-i /tmp/hosts"}},
		{"wafw00f_scan", map[string]any{"url": "-a"}},
		{"dirb_scan", map[string]any{"url": "-S"}},
		{"snmpwalk_enum", map[string]any{"target": "-r5"}},
		{"snmpwalk_enum", map[string]any{"target": "10.0.0.5", "oid": "-Cc"}},
		{"hydra_bruteforce", map[string]any{"target": "-e", "service": "ssh", "user": "admin", "pass_list": "x"}},
		{"hydra_bruteforce", map[string]any{"target": "10.0.0.5", "service": "-V", "user": "admin", "pass_list": "x"}},
		{"dnsx_resolve", map[string]any{"domain": "--json"}},
		{"alterx_mutate", map[string]any{"domain": "--enrich"}},
		{"gau_fetch", map[string]any{"domain": "--json"}},
		{"dalfox_scan", map[string]any{"url": "--silence"}},
		{"radare2_disasm", map[string]any{"file": "-w"}},
		{"cloud_enum_scan", map[string]any{"keyword": "-q"}},
		{"jwt_tool_inspect", map[string]any{"token": "-M"}},
		{"jsluice_scan", map[string]any{"url": "-f"}},
		{"graphql_cop_scan", map[string]any{"url": "-f"}},
	}
	for _, c := range cases {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s %v: CallTool: %v", c.tool, c.args, err)
		}
		if !result.IsError {
			t.Errorf("%s %v: expected IsError for a flag-shaped value", c.tool, c.args)
		}
	}
}

func TestNucleiScan_RejectsFlagInjectionOnTagsAndBareTarget(t *testing.T) {
	session := newTestServer(t)
	cases := []map[string]any{
		{"target": "-update-templates"},
		{"target": "https://example.com", "tags": "-update-templates"},
	}
	for _, args := range cases {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "nuclei_scan", Arguments: args})
		if err != nil {
			t.Fatalf("%v: CallTool: %v", args, err)
		}
		if !result.IsError {
			t.Errorf("%v: expected IsError for a flag-shaped value", args)
		}
	}
}

// TestNucleiScan_AbsolutePathTargetIsBulkMode proves an absolute-path target
// (rather than a URL/host) routes to -l instead of -u, without needing a
// second field — this is what keeps the tool's own "target" field always
// populated for the orchestrator's gateway scope check (sandboxmcp.Target),
// which fails closed on an empty target. Bulk mode is exercised indirectly
// here (no real nuclei binary in this test environment) via the flag-
// injection guard: an absolute path is never flag-shaped, so it must reach
// the non-error branch that actually execs, distinguishing it from
// TestExtendedTools_RejectsFlagInjection's bare-target coverage above.
func TestNucleiScan_AbsolutePathTargetIsBulkMode(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nuclei_scan",
		Arguments: map[string]any{"target": "/home/pentester/definitely-not-a-real-file.txt"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// The real nuclei binary isn't installed in this test environment, so
	// this exercises the launch-failure path — the point is that it reaches
	// exec at all (an absolute path is never rejected as flag-shaped),
	// unlike a genuinely flag-shaped target.
	if result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		if text != nil && strings.Contains(text.Text, "looks like a command-line flag") {
			t.Fatalf("an absolute path must not be rejected as flag-shaped: %s", text.Text)
		}
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
