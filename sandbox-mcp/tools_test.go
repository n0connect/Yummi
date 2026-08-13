package main

import (
	"context"
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
