package sandboxmcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestPingServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	return server
}

func TestClient_CallTool_RoundTrip(t *testing.T) {
	server := newTestPingServer()
	c := newClient(func(ctx context.Context) mcp.Transport {
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		go func() { _, _ = server.Connect(ctx, serverTransport, nil) }()
		return clientTransport
	})

	result, err := c.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "pong" {
		t.Fatalf("unexpected content: %+v", result.Content)
	}
}

func TestClient_CallTool_ReconnectsAfterSessionDeath(t *testing.T) {
	server := newTestPingServer()
	c := newClient(func(ctx context.Context) mcp.Transport {
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		go func() { _, _ = server.Connect(ctx, serverTransport, nil) }()
		return clientTransport
	})

	if _, err := c.CallTool(context.Background(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Simulate the sandbox-side process dying out from under the client.
	c.mu.Lock()
	_ = c.session.Close()
	c.mu.Unlock()

	result, err := c.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("call after simulated death: %v", err)
	}
	text := result.Content[0].(*mcp.TextContent)
	if text.Text != "pong" {
		t.Fatalf("got %q, want pong", text.Text)
	}
}

func TestClient_ListTools_ReturnsCatalog(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "replies pong"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes input"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})

	c := newClient(func(ctx context.Context) mcp.Transport {
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		go func() { _, _ = server.Connect(ctx, serverTransport, nil) }()
		return clientTransport
	})

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2: %+v", len(tools), tools)
	}
	names := map[string]string{}
	for _, tool := range tools {
		names[tool.Name] = tool.Description
	}
	if names["ping"] != "replies pong" || names["echo"] != "echoes input" {
		t.Fatalf("unexpected tool set: %+v", names)
	}
}
