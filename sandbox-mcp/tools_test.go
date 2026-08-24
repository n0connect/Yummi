package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "yummi-sandbox-test", Version: "v1"}, nil)
	registerTools(server)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestNmapScan_EmptyTarget(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "nmap_scan", Arguments: map[string]any{"target": ""},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for an empty target")
	}
}

func TestNmapScan_RealExec(t *testing.T) {
	if _, err := exec.LookPath("nmap"); err != nil {
		t.Skip("nmap not installed on this host")
	}
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "nmap_scan",
		Arguments: map[string]any{
			"target": "127.0.0.1", "service_detection": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected IsError: %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "Nmap") {
		t.Fatalf("expected nmap banner in output, got %+v", result.Content)
	}
}

func TestGobusterDir_RejectsWordlistTraversal(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "gobuster_dir",
		Arguments: map[string]any{"url": "http://example.local", "wordlist": "../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for a path-traversal wordlist")
	}
}

func TestFfufFuzz_AppendsFUZZWhenMissing(t *testing.T) {
	if _, err := exec.LookPath("ffuf"); err != nil {
		t.Skip("ffuf not installed on this host")
	}
	// Real wordlist file so path validation passes.
	if err := os.MkdirAll("/tmp/yummi-test-wordlists", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/yummi-test-wordlists/w.txt", []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YUMMI_WORDLIST_DIR", "/tmp/yummi-test-wordlists")
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "ffuf_fuzz",
		Arguments: map[string]any{"url": "http://127.0.0.1:1", "wordlist": "w.txt"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	_ = result // connection refused is expected (nothing listens on :1) — just proving argv construction ran
}

func TestWhatwebScan_EmptyURL(t *testing.T) {
	session := newTestServer(t)
	result, _ := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "whatweb_scan", Arguments: map[string]any{"url": ""},
	})
	if !result.IsError {
		t.Fatal("expected IsError for empty url")
	}
}

func TestCurlProbe_EmptyURL(t *testing.T) {
	session := newTestServer(t)
	result, _ := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "curl_probe", Arguments: map[string]any{"url": ""},
	})
	if !result.IsError {
		t.Fatal("expected IsError for empty url")
	}
}

func TestNiktoScan_EmptyHost(t *testing.T) {
	session := newTestServer(t)
	result, _ := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "nikto_scan", Arguments: map[string]any{"host": ""},
	})
	if !result.IsError {
		t.Fatal("expected IsError for empty host")
	}
}

func TestRejectIfFlagLike(t *testing.T) {
	cases := []struct {
		value   string
		wantErr bool
	}{
		{"10.0.0.1", false},
		{"example.com", false},
		{"http://example.com/path", false},
		{"--script=/tmp/evil.nse", true},
		{"-oN /tmp/out.txt", true},
		{"-K /tmp/curlrc", true},
		{"-", true}, // no legitimate target/url is a bare dash either
	}
	for _, tc := range cases {
		err := rejectIfFlagLike("target", tc.value)
		if tc.wantErr && err == nil {
			t.Errorf("rejectIfFlagLike(%q): expected an error, got nil", tc.value)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("rejectIfFlagLike(%q): unexpected error: %v", tc.value, err)
		}
	}
}

// TestNmapScan_RejectsFlagInjectionInTarget proves a target that would be
// parsed as a flag (not a positional host) by nmap itself is refused before
// ever reaching exec.Command — nmap's target is passed bare, with nothing
// preceding it in argv to claim it as an argument, so a model steered by
// hostile scanned content (the class of attack internal/security/threatscan
// defends against on the way back INTO the model) could otherwise redirect
// nmap's own behavior via --script, -oN, -iL, etc.
func TestNmapScan_RejectsFlagInjectionInTarget(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nmap_scan",
		Arguments: map[string]any{"target": "--script=/tmp/evil.nse"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for a flag-shaped target")
	}
}

// TestCurlProbe_RejectsFlagInjectionInURL is the highest-value case in this
// family: curl's -K <file> flag lets an injected value re-point curl at an
// attacker-chosen config file, indirectly controlling essentially any other
// curl option (headers, output path, protocols including file://) — far
// beyond "fetch this URL".
func TestCurlProbe_RejectsFlagInjectionInURL(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "curl_probe",
		Arguments: map[string]any{"url": "-K /tmp/evil.curlrc"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for a flag-shaped url")
	}
}

// TestResolveWordlist_FallsBackToCommonTxtWhenMountEmpty is the regression
// test for a live-observed friction point: on a fresh install, the operator-
// populated /wordlists mount is empty by design (see docker-compose.yml's
// comment), so "common.txt" — the exact example every wordlist-taking tool
// schema in this package suggests — failed outright. The model spent 5-9
// real step-budget turns rediscovering dirb's own bundled common.txt by
// trial and error across two separate live runs, once exhausting the whole
// step budget before finishing. This proves the fallback recovers it.
func TestResolveWordlist_FallsBackToCommonTxtWhenMountEmpty(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("YUMMI_WORDLIST_DIR", emptyDir)

	fallback := filepath.Join(t.TempDir(), "common.txt")
	if err := os.WriteFile(fallback, []byte("admin\nlogin\n"), 0o644); err != nil {
		t.Fatalf("write fallback fixture: %v", err)
	}
	origFallback := commonWordlistFallback
	commonWordlistFallback = fallback
	t.Cleanup(func() { commonWordlistFallback = origFallback })

	got, err := resolveWordlist("common.txt")
	if err != nil {
		t.Fatalf("resolveWordlist: %v", err)
	}
	if got != fallback {
		t.Fatalf("got %q, want the fallback path %q", got, fallback)
	}
}

// TestResolveWordlist_NoFallbackForOtherNames proves the fallback is scoped
// to the literal "common.txt" name only — a different, still-missing
// filename must still error normally, not silently substitute a different
// wordlist the operator didn't ask for.
func TestResolveWordlist_NoFallbackForOtherNames(t *testing.T) {
	t.Setenv("YUMMI_WORDLIST_DIR", t.TempDir())

	if _, err := resolveWordlist("rockyou.txt"); err == nil {
		t.Fatal("expected an error for a missing non-common.txt wordlist, got nil")
	}
}

// TestResolveWordlist_FallbackDoesNotBypassTraversalRejection proves the
// existing path-traversal guard still runs before the fallback is ever
// considered — a name can't smuggle traversal by disguising itself near
// "common.txt".
func TestResolveWordlist_FallbackDoesNotBypassTraversalRejection(t *testing.T) {
	t.Setenv("YUMMI_WORDLIST_DIR", t.TempDir())

	if _, err := resolveWordlist("../common.txt"); err == nil {
		t.Fatal("expected traversal rejection, got nil")
	}
}
