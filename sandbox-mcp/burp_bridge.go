package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// burp_bridge.go exposes Burp Suite's own official MCP extension
// (github.com/PortSwigger/mcp-server) as two sandbox-mcp tools, rather than
// reimplementing any of Burp's tool surface locally. PortSwigger ships a
// prebuilt stdio<->SSE proxy (mcp-proxy-all.jar) specifically so another
// program can reach the extension's SSE server (default
// http://127.0.0.1:9876) as a plain stdio MCP server — this file execs that
// proxy as a child process and bridges to it as an MCP *client*, the same
// shape sandbox-mcp itself presents to the orchestrator.
//
// The extension itself (burp-mcp-all.jar, fetched into the image by
// sandbox/Dockerfile) still has to be loaded into a running Burp Suite
// instance before the SSE server exists at all. PortSwigger's own docs
// describe only the manual Extensions-tab flow (Add > Java > select the
// jar > Next) for this — there's no documented CLI flag or config-file
// mechanism to pre-register an extension non-interactively on any Burp
// edition. So this bridge launches Burp itself (attached to the sandbox's
// always-on virtual desktop, the same DISPLAY browser_rod.go's Chromium
// uses) but makes no attempt to script that one dialog; if the MCP port
// never comes up, the error below tells the operator exactly what to click,
// once, through the sandbox desktop. That's a one-time step per container
// lifetime, not per call — once loaded, every later burp_* call just
// reconnects.
const (
	burpMCPHost           = "127.0.0.1"
	defaultBurpMCPPort    = "9876" // extension's documented default; override via YUMMI_BURP_MCP_PORT if changed in Burp's own MCP tab
	burpExtensionJarPath  = "/opt/burp-mcp/burp-mcp-all.jar"
	burpProxyJarPath      = "/opt/burp-mcp/mcp-proxy-all.jar"
	burpLaunchTimeout     = 45 * time.Second
	burpHealthCheckBudget = 2 * time.Second
)

func burpMCPPort() string {
	if p := strings.TrimSpace(os.Getenv("YUMMI_BURP_MCP_PORT")); p != "" {
		return p
	}
	return defaultBurpMCPPort
}

func burpMCPAddr() string {
	return net.JoinHostPort(burpMCPHost, burpMCPPort())
}

type burpBridge struct {
	mu        sync.Mutex
	client    *mcp.ClientSession
	instr     string
	burpAlive bool // true only between a successful launchBurpLocked and its process actually exiting
}

var globalBurp = &burpBridge{}

// session returns a live ClientSession to Burp's MCP extension, launching
// Burp Suite and/or the stdio proxy as needed. Serializes the whole
// ensure-and-connect sequence under b.mu: Burp is one shared GUI process for
// the whole sandbox, so a second concurrent caller should wait for the first
// launch rather than race it into starting a duplicate.
func (b *burpBridge) session(ctx context.Context) (*mcp.ClientSession, error) {
	if err := requireSandboxRuntime(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.client != nil {
		healthCtx, cancel := context.WithTimeout(ctx, burpHealthCheckBudget)
		_, err := b.client.ListTools(healthCtx, nil)
		cancel()
		if err == nil {
			return b.client, nil
		}
		_ = b.client.Close()
		b.client = nil
	}

	if !burpPortReachable(500 * time.Millisecond) {
		if err := b.launchBurpLocked(); err != nil {
			return nil, err
		}
		if !waitForBurpPort(ctx, burpLaunchTimeout) {
			return nil, fmt.Errorf(
				"Burp Suite's MCP extension is not responding on %s after %s. "+
					"On a fresh container this is expected: open the sandbox desktop, "+
					"and in Burp go to Extensions > Add > Extension type: Java > select "+
					"%s > Next. This is a one-time step for this container's lifetime — "+
					"every burp_* call after that loads automatically. "+
					"If Burp itself never opened, check that burpsuite is on PATH and "+
					"the sandbox desktop (DISPLAY=%s) is up",
				burpMCPAddr(), burpLaunchTimeout, burpExtensionJarPath, displayOrDefault())
		}
	}

	// A fresh, independent context for the persistent proxy connection:
	// this must outlive the single tool call that happens to trigger it,
	// so it is deliberately not ctx (which the MCP server may cancel the
	// moment this handler returns).
	cmd := exec.Command("java", "-jar", burpProxyJarPath, "--sse-url", "http://"+burpMCPAddr())
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "yummi-sandbox-burp-bridge", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start Burp MCP stdio proxy (%s): %w", burpProxyJarPath, err)
	}
	b.client = session
	if init := session.InitializeResult(); init != nil {
		b.instr = init.Instructions
	}
	return session, nil
}

// launchBurpLocked starts burpsuite if it isn't already running under this
// bridge's management. Caller must hold b.mu.
func (b *burpBridge) launchBurpLocked() error {
	if b.burpAlive {
		return nil
	}
	binPath, err := exec.LookPath("burpsuite")
	if err != nil {
		return fmt.Errorf("sandbox Burp Suite missing: %w", err)
	}
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "DISPLAY="+displayOrDefault())
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch burpsuite: %w", err)
	}
	b.burpAlive = true
	go func() {
		_ = cmd.Wait()
		b.mu.Lock()
		b.burpAlive = false
		b.mu.Unlock()
	}()
	return nil
}

func displayOrDefault() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	return ":1" // default TigerVNC display in sandbox, matching browser_rod.go
}

func burpPortReachable(timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", burpMCPAddr(), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForBurpPort(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if burpPortReachable(500 * time.Millisecond) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(1 * time.Second):
		}
	}
	return false
}

// ── burp_list_tools ──────────────────────────────────────────────────────────

func burpListToolsHandler(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	session, err := globalBurp.session(ctx)
	if err != nil {
		return errResult(err.Error())
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return errResult(fmt.Sprintf("burp_list_tools failed: %v", err))
	}
	var sb strings.Builder
	globalBurp.mu.Lock()
	instr := globalBurp.instr
	globalBurp.mu.Unlock()
	if instr != "" {
		sb.WriteString(instr)
		sb.WriteString("\n\n")
	}
	fmt.Fprintf(&sb, "Burp MCP extension tools (%d):\n", len(result.Tools))
	for _, t := range result.Tools {
		fmt.Fprintf(&sb, "- %s: %s\n", t.Name, t.Description)
	}
	if len(result.Tools) == 0 {
		sb.WriteString("(none reported — check the extension's own MCP tab settings in Burp)\n")
	}
	return textResult(sb.String())
}

// ── burp_call_tool ───────────────────────────────────────────────────────────

type burpCallToolArgs struct {
	ToolName  string         `json:"tool_name" jsonschema:"the exact Burp MCP tool name, from burp_list_tools"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"arguments for the tool, matching the schema burp_list_tools reports for it"`
}

func burpCallToolHandler(ctx context.Context, _ *mcp.CallToolRequest, args burpCallToolArgs) (*mcp.CallToolResult, any, error) {
	name := strings.TrimSpace(args.ToolName)
	if name == "" {
		return errResult("tool_name is required (see burp_list_tools)")
	}
	session, err := globalBurp.session(ctx)
	if err != nil {
		return errResult(err.Error())
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args.Arguments})
	if err != nil {
		return errResult(fmt.Sprintf("burp tool %q failed: %v", name, err))
	}
	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
			sb.WriteString("\n")
		}
	}
	if sb.Len() == 0 {
		if b, mErr := json.Marshal(result); mErr == nil {
			sb.Write(b)
		}
	}
	if result.IsError {
		return errResult(sb.String())
	}
	return textResult(sb.String())
}

func registerBurpTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "burp_list_tools",
		Description: "List the tools currently exposed by Burp Suite's own MCP extension (github.com/PortSwigger/mcp-server) — proxy history, Repeater, Scanner, Organizer, etc, depending on the Burp edition and the extension's own MCP-tab settings. Launches Burp Suite on the sandbox desktop on first use; on a fresh container this fails once with instructions to load the extension through the desktop (a one-time step), then works from then on.",
	}, burpListToolsHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "burp_call_tool",
		Description: "Call one tool exposed by Burp Suite's MCP extension by name. Call burp_list_tools first for the current tool names and their expected arguments — Burp's own tool surface varies by edition and by the extension's MCP-tab settings, so nothing here is hardcoded to one fixed list.",
	}, burpCallToolHandler)
}
