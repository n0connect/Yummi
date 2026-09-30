package sandboxmcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestPingServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "slow"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		select {
		case <-time.After(2 * time.Second):
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "eventually"}}}, nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
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

// TestClient_CallTool_ExpiredContextSurfacesOriginalError confirms a real
// bug found live: when a slow-but-otherwise-fine tool call (gau_fetch
// against a real domain has been observed taking 90s+ depending on external
// OSINT service responsiveness) simply outlives the caller's context,
// withReconnect used to retry ensureSession against that SAME already-expired
// context — a doomed second attempt whose failure ("reconnect failed:
// context deadline exceeded") reads as a broken connection, masking the real
// story that the call itself just ran out of time. The caller should see the
// original timeout, not a confusing reconnect failure.
func TestClient_CallTool_ExpiredContextSurfacesOriginalError(t *testing.T) {
	server := newTestPingServer()
	c := newClient(func(ctx context.Context) mcp.Transport {
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		go func() { _, _ = server.Connect(ctx, serverTransport, nil) }()
		return clientTransport
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.CallTool(ctx, "slow", nil)
	if err == nil {
		t.Fatal("expected an error from a call that outlives its context")
	}
	if strings.Contains(err.Error(), "reconnect failed") {
		t.Fatalf("error should surface the original timeout, not a doomed reconnect attempt: %v", err)
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
