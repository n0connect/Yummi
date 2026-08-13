// Package sandboxmcp is the orchestrator-side client for the MCP server that
// runs inside the Kali sandbox container. It holds one persistent
// *mcp.ClientSession reached via a long-lived `docker exec -i` — the same
// invocation shape internal/sandbox.Bridge.Exec already uses, just held open
// instead of one-shot-buffered. This package never touches the dormant
// internal/mcp client (host-spawning, unrelated) and never execs anything
// other than a fixed `docker` argv — see the invariant test in
// internal/security/invariants.
package sandboxmcp

import (
	"context"
	"fmt"
	"os/exec"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client maintains one persistent MCP session. newTransport is swappable so
// tests can inject an in-memory transport instead of spawning docker exec.
type Client struct {
	newTransport func(ctx context.Context) mcp.Transport
	mcpClient    *mcp.Client

	mu      sync.Mutex
	session *mcp.ClientSession
}

func newClient(newTransport func(ctx context.Context) mcp.Transport) *Client {
	return &Client{
		newTransport: newTransport,
		mcpClient:    mcp.NewClient(&mcp.Implementation{Name: "yummi-orchestrator", Version: "v1"}, nil),
	}
}

// NewClient builds a Client that reaches the sandbox-resident MCP server via
// `docker exec -i --user 1000:1000 <container> /usr/local/bin/yummi-sandbox-mcp`.
func NewClient(container string) *Client {
	return newClient(func(ctx context.Context) mcp.Transport {
		cmd := exec.CommandContext(ctx, "docker", "exec", "-i", "--user", "1000:1000",
			container, "/usr/local/bin/yummi-sandbox-mcp")
		return &mcp.CommandTransport{Command: cmd}
	})
}

func (c *Client) ensureSession(ctx context.Context) (*mcp.ClientSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session, nil
	}
	session, err := c.mcpClient.Connect(ctx, c.newTransport(ctx), nil)
	if err != nil {
		return nil, fmt.Errorf("sandboxmcp: connect: %w", err)
	}
	c.session = session
	return c.session, nil
}

// CallTool invokes a tool on the sandbox-resident server, transparently
// reconnecting once if the persistent session has died (sandbox restarted,
// process crashed) since the last call.
func (c *Client) CallTool(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err == nil {
		return result, nil
	}

	c.mu.Lock()
	c.session = nil
	c.mu.Unlock()
	session, err = c.ensureSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("sandboxmcp: reconnect failed: %w", err)
	}
	return session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
}

// ListTools returns every tool the sandbox-resident server has registered —
// the discovery half of the search-then-dispatch pattern (CallTool is the
// dispatch half). The catalog is small enough that pagination is never
// exercised in practice, but the underlying protocol supports it via
// ListToolsResult.NextCursor if that changes.
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err == nil {
		return result.Tools, nil
	}

	c.mu.Lock()
	c.session = nil
	c.mu.Unlock()
	session, err = c.ensureSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("sandboxmcp: reconnect failed: %w", err)
	}
	result, err = session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// Close terminates the persistent session, if any.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session = nil
	return err
}
