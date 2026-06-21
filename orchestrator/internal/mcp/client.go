// Package mcp is a minimal Model Context Protocol client.
//
// The MCP wire format is JSON-RPC 2.0 over stdio: each message is a single
// JSON object on one line terminated by \n. This is the stdio transport; we
// don't implement the HTTP/SSE transport here because orchestrator spawns the server
// as a child process and talks to it over pipes.
//
// The surface is intentionally small: open a server, call a method, close it.
// Tools like `list_tools` / `call_tool` ride on top of that primitive.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Request is a JSON-RPC 2.0 request envelope.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification — no ID, no response expected.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response envelope. Error is non-nil on failure.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is the structured JSON-RPC 2.0 error body.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

// Client speaks JSON-RPC 2.0 with a subprocess MCP server over its stdio
// pipes. One Client owns one server process; concurrent calls are safe.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu      sync.Mutex
	pending map[int64]chan *Response
	writeMu sync.Mutex

	nextID  atomic.Int64
	closed  atomic.Bool
	done    chan struct{}
	readErr error
}

// Options configures a Client.
type Options struct {
	// Command is the MCP server binary to spawn and its args. The first
	// element is the program, the rest are arguments.
	Command []string
	// Env lists "KEY=VAL" pairs added to the child process environment.
	Env []string
}

// NewClient spawns the server process and starts a background reader that
// demuxes responses by request ID.
func NewClient(opts Options) (*Client, error) {
	if len(opts.Command) == 0 {
		return nil, errors.New("mcp: no command to run")
	}
	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start: %w", err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan *Response),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Call invokes an MCP method and waits for the matching response or ctx done.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil || c.closed.Load() {
		return nil, errors.New("mcp: client closed")
	}

	id := c.nextID.Add(1)
	req := Request{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp: marshal params: %w", err)
		}
		req.Params = p
	}

	respCh := make(chan *Response, 1)
	c.mu.Lock()
	c.pending[id] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.writeJSON(req); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		if c.readErr != nil {
			return nil, fmt.Errorf("mcp: server stream ended: %w", c.readErr)
		}
		return nil, errors.New("mcp: server stream ended")
	case resp := <-respCh:
		if resp == nil {
			return nil, errors.New("mcp: nil response")
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (c *Client) Notify(method string, params any) error {
	if c == nil || c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	n := Notification{JSONRPC: "2.0", Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: marshal params: %w", err)
		}
		n.Params = p
	}
	return c.writeJSON(n)
}

// Close terminates the MCP server process and tears down pipes.
func (c *Client) Close() error {
	if c == nil || !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = c.stdin.Close()
	// Give the server a moment to exit cleanly on EOF, then force it.
	exited := make(chan error, 1)
	go func() { exited <- c.cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-time.After(500 * time.Millisecond):
		_ = c.cmd.Process.Kill()
		return <-exited
	}
}

// writeJSON serializes v and writes it as a single line to the server stdin.
func (c *Client) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mcp: marshal: %w", err)
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("mcp: write: %w", err)
	}
	return nil
}

// readLoop reads newline-delimited JSON responses and routes them to the
// waiting Call's channel by ID.
func (c *Client) readLoop() {
	defer close(c.done)
	r := bufio.NewReader(c.stdout)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if err != io.EOF {
				c.readErr = err
			}
			c.drainPending()
			return
		}
	}
}

func (c *Client) dispatch(line []byte) {
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		// Ignore malformed frames — the server may emit log lines on stderr
		// but if it puts garbage on stdout we don't panic the client.
		return
	}
	if resp.JSONRPC == "" && resp.ID == 0 && resp.Result == nil && resp.Error == nil {
		return // not a response frame
	}

	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- &resp:
	default:
	}
}

func (c *Client) drainPending() {
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}
