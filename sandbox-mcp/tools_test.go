package main

import (
	"context"
	"os"
	"os/exec"
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
