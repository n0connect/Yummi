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

// reconnect replaces c.session, but ONLY if it is still the exact session
// pointer the caller observed failing (bad). Multiple goroutines can share
// one Client concurrently — Eino's ToolsNode genuinely executes several
// tool calls in parallel within one model turn (compose.ToolsNode's own
// worker-goroutine fan-out), so several in-flight CallTool calls can be
// sharing this one persistent docker-exec-i session at once. Stress-testing
// the terminal-session feature with real concurrency (N goroutines each
// opening/driving a session) reproducibly showed one goroutine's transient
// transport error triggering a reconnect that tore the shared connection
// out from under every OTHER goroutine's still-healthy in-flight call —
// each of those then saw a spurious EOF too and reconnected AGAIN
// redundantly, and the original bad session was never closed (leaking its
// docker-exec-i process every single time). The "already exists" failures
// this surfaced on term_open were a symptom, not the disease: a goroutine
// whose first attempt genuinely succeeded server-side, but whose response
// got lost when a DIFFERENT goroutine's reconnect killed the connection out
// from under it, retried the same (non-idempotent) call and collided with
// its own already-created session.
//
// Fix: only the goroutine whose bad session pointer still matches the
// CURRENT c.session is responsible for reconnecting (and it Closes the old
// session first, so the process is never leaked); every other goroutine
// that races in here with an already-superseded bad pointer just reuses
// whatever is current — recreated by someone else, or never actually replaced
// if this goroutine was the only one affected.
func (c *Client) reconnect(ctx context.Context, bad *mcp.ClientSession) (*mcp.ClientSession, error) {
	c.mu.Lock()
	if c.session != bad {
		// Someone else already replaced it (or it was never actually the
		// live one when we observed the failure) — reuse the current
		// session instead of tearing down a connection other calls may
		// still be healthily using.
		current := c.session
		c.mu.Unlock()
		if current != nil {
			return current, nil
		}
		// Current is nil too (e.g. a concurrent reconnect itself just
		// failed) — fall through to try creating one ourselves.
		return c.ensureSession(ctx)
	}
	// We are the one responsible: close the dead session (don't leak its
	// docker-exec-i process) and connect a fresh one before anyone else can
	// observe c.session as non-nil and skip past this branch.
	if bad != nil {
		_ = bad.Close()
	}
	session, err := c.mcpClient.Connect(ctx, c.newTransport(ctx), nil)
	if err != nil {
		c.session = nil
		c.mu.Unlock()
		return nil, err
	}
	c.session = session
	c.mu.Unlock()
	return session, nil
}

// withReconnect runs call against the current session and, if it fails,
// drops the (possibly dead) session and retries exactly once against a
// freshly reconnected one — shared by CallTool and ListTools, which
// otherwise duplicated this retry-after-reconnect shape identically.
//
// If ctx is already past its deadline when the first call fails — the
// realistic case for a slow tool (gau's historical-URL fetch against a
// live domain has been observed taking 90s+ depending on external OSINT
// service responsiveness) that simply ran out of the caller's time budget
// — a second attempt is doomed before it starts: ensureSession's own
// Connect(ctx, ...) would fail immediately on the same expired ctx. Retrying
// anyway produced a misleading "reconnect failed: context deadline exceeded"
// that reads as a broken connection when the real story is "the call itself
// timed out" — confirmed live against gau_fetch. Surface the original error
// directly in that case instead of masking it behind a doomed reconnect.
func withReconnect[T any](c *Client, ctx context.Context, call func(*mcp.ClientSession) (T, error)) (T, error) {
	var zero T
	session, err := c.ensureSession(ctx)
	if err != nil {
		return zero, err
	}
	result, err := call(session)
	if err == nil {
		return result, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return zero, fmt.Errorf("sandboxmcp: call did not complete before context was done: %w", err)
	}

	session, err = c.reconnect(ctx, session)
	if err != nil {
		return zero, fmt.Errorf("sandboxmcp: reconnect failed: %w", err)
	}
	return call(session)
}

// CallTool invokes a tool on the sandbox-resident server, transparently
// reconnecting once if the persistent session has died (sandbox restarted,
// process crashed) since the last call.
func (c *Client) CallTool(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	return withReconnect(c, ctx, func(session *mcp.ClientSession) (*mcp.CallToolResult, error) {
		return session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	})
}

// ListTools returns every tool the sandbox-resident server has registered —
// the discovery half of the search-then-dispatch pattern (CallTool is the
// dispatch half). The catalog is small enough that pagination is never
// exercised in practice, but the underlying protocol supports it via
// ListToolsResult.NextCursor if that changes.
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	return withReconnect(c, ctx, func(session *mcp.ClientSession) ([]*mcp.Tool, error) {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			return nil, err
		}
		return result.Tools, nil
	})
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
