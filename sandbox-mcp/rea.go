package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rea.go bridges sandbox-mcp to REA (https://github.com/morluto/rea) —
// "Reverse Engineer Anything", a binary/application analysis toolkit — via
// REA's own `rea --mcp` stdio MCP server, the same bridge-not-rewrap pattern
// burp_bridge.go uses for Burp Suite.
//
// REA's CLI (`rea analyze`, `rea inspect-analysis-view`, ...) looked like the
// obvious thing to exec.Command-wrap, and an earlier version of this file did
// exactly that — but real verification against a built image caught it
// guessing wrong in two different ways (a nonexistent --json flag; a fully
// invented JSON shape for inspect-analysis-view's positional argument, which
// is actually an opaque "workflow" document REA's own docs never show a full
// example of). REA's MCP mode sidesteps all of that: `rea --mcp` serves 229
// tools (analyze_function, inspect_analysis_view, open_binary, list_procedures,
// decompile, ... — confirmed via a live tools/list, not the docs) with real
// JSON schemas the agent can read directly, over the same session-based
// "retained Evidence" model its own MCP instructions describe (open a target,
// work with it across several calls, call close_binary when done — state
// that would not survive a fresh CLI process per call anyway). Wrapping that
// surface by hand, tool by tool, isn't something 229 hand-guessed schemas
// could ever track safely as REA evolves; a generic pass-through can.
//
// Unlike Burp, REA needs no running GUI/display and no one-time manual setup
// — `rea --mcp` is a plain child process, spawned fresh and reused for the
// sandbox-mcp process's lifetime (or until it stops responding, at which
// point the next call respawns it).

const reaHealthCheckBudget = 2 * time.Second

type reaBridge struct {
	mu     sync.Mutex
	client *mcp.ClientSession
	instr  string
}

var globalRea = &reaBridge{}

// session returns a live ClientSession to `rea --mcp`, spawning it if this
// is the first call or the previous child stopped responding.
func (r *reaBridge) session(ctx context.Context) (*mcp.ClientSession, error) {
	if err := requireSandboxRuntime(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.client != nil {
		healthCtx, cancel := context.WithTimeout(ctx, reaHealthCheckBudget)
		_, err := r.client.ListTools(healthCtx, nil)
		cancel()
		if err == nil {
			return r.client, nil
		}
		_ = r.client.Close()
		r.client = nil
	}

	if _, err := exec.LookPath("rea"); err != nil {
		return nil, fmt.Errorf("sandbox REA (rea) missing: %w", err)
	}
	// Independent of ctx: this connection must outlive the single tool call
	// that happens to trigger it — see burp_bridge.go's session() for the
	// same reasoning.
	client := mcp.NewClient(&mcp.Implementation{Name: "yummi-sandbox-rea-bridge", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: exec.Command("rea", "--mcp")}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start rea --mcp: %w", err)
	}
	r.client = session
	if init := session.InitializeResult(); init != nil {
		r.instr = init.Instructions
	}
	return session, nil
}

// ── rea_list_tools ───────────────────────────────────────────────────────────

type reaListToolsArgs struct {
	NameFilter string `json:"name_filter,omitempty" jsonschema:"optional case-insensitive substring to narrow the list (REA exposes ~230 tools across native binaries, Android/iOS, browser/JS, firmware, packet captures, and more — filter rather than scanning the whole catalog, e.g. 'android', 'frida', 'pe_')"`
}

func reaListToolsHandler(ctx context.Context, _ *mcp.CallToolRequest, args reaListToolsArgs) (*mcp.CallToolResult, any, error) {
	session, err := globalRea.session(ctx)
	if err != nil {
		return errResult(err.Error())
	}
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return errResult(fmt.Sprintf("rea_list_tools failed: %v", err))
	}
	filter := strings.ToLower(strings.TrimSpace(args.NameFilter))
	var sb strings.Builder
	globalRea.mu.Lock()
	instr := globalRea.instr
	globalRea.mu.Unlock()
	if instr != "" {
		sb.WriteString(instr)
		sb.WriteString("\n\n")
	}
	matched := 0
	for _, t := range result.Tools {
		if filter != "" && !strings.Contains(strings.ToLower(t.Name), filter) {
			continue
		}
		matched++
		fmt.Fprintf(&sb, "- %s: %s\n", t.Name, t.Description)
	}
	fmt.Fprintf(&sb, "\n(%d of %d tools shown", matched, len(result.Tools))
	if filter != "" {
		fmt.Fprintf(&sb, ", filtered by %q", args.NameFilter)
	}
	sb.WriteString(")\n")
	return textResult(sb.String())
}

// ── rea_call_tool ────────────────────────────────────────────────────────────

type reaCallToolArgs struct {
	ToolName  string         `json:"tool_name" jsonschema:"the exact REA tool name, from rea_list_tools"`
	Arguments map[string]any `json:"arguments,omitempty" jsonschema:"arguments for the tool, matching the schema rea_list_tools reports for it"`
}

func reaCallToolHandler(ctx context.Context, _ *mcp.CallToolRequest, args reaCallToolArgs) (*mcp.CallToolResult, any, error) {
	name := strings.TrimSpace(args.ToolName)
	if name == "" {
		return errResult("tool_name is required (see rea_list_tools)")
	}
	session, err := globalRea.session(ctx)
	if err != nil {
		return errResult(err.Error())
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args.Arguments})
	if err != nil {
		return errResult(fmt.Sprintf("rea tool %q failed: %v", name, err))
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

func registerReaTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "rea_list_tools",
		Description: "List REA's (morluto/rea) reverse-engineering tools: native ELF/PE/Mach-O binaries, Go/Rust/managed code, Android/iOS apps, browser/JS/Electron, firmware, packet captures, model artifacts, and more — plus REA's own usage guidance. Filter by name_filter since the full catalog is large. Full native decompilation needs a configured engine (Ghidra/Hopper/IDA, none installed by default); calls needing one fail with a clear provider_unavailable error rather than hanging.",
	}, reaListToolsHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "rea_call_tool",
		Description: "Call one REA tool by name. Call rea_list_tools first for the current tool names and their argument schemas — REA's surface is too large to hardcode here, and nothing here guesses at it.",
	}, reaCallToolHandler)
}
