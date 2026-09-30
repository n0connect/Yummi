package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/n0connect/yummi/orchestrator/internal/sandboxmcp"
)

// mcpLister is the minimal surface search_pentest_tools needs from
// *sandboxmcp.Client — narrowed to an interface so tests can use a plain
// struct in place of a real docker-exec-backed client (same pattern as
// executor.sandboxMCPClient).
type mcpLister interface {
	ListTools(ctx context.Context) ([]*mcp.Tool, error)
}

// ── search_pentest_tools ─────────────────────────────────────────────────
//
// The discovery half of the search-then-dispatch pattern: rather than
// declaring every cataloged sandbox-mcp tool as its own individually-typed
// Eino function (which stops scaling once the catalog grows much past a
// handful — every tool's full JSON schema rides along on every single LLM
// call), the model can search the live catalog on demand and read back
// name + description + JSON schema as text, then call run_pentest_tool
// (below) with the tool name and args it decided on. nmap_scan and its five
// siblings stay individually typed regardless — they are the highest-value,
// most commonly used tools, and a dedicated schema gives the model API-level
// enforced argument shapes those six benefit most from.

type searchPentestToolsArgs struct {
	Query string `json:"query,omitempty"`
}

var searchPentestToolsInfo = &schema.ToolInfo{
	Name: "search_pentest_tools",
	Desc: "List the pentest tools available in the sandbox beyond the ones you already have dedicated functions for " +
		"(nmap_scan, whatweb_scan, curl_probe, gobuster_dir, ffuf_fuzz, nikto_scan). Returns each tool's name, " +
		"description, and parameter schema. Call this before run_pentest_tool if you're not sure a tool exists or " +
		"what arguments it takes. IMPORTANT: a tool listed here (e.g. nuclei_scan, sqlmap_scan) is NOT itself a " +
		"callable function — you cannot call it by name directly. Dispatch it through run_pentest_tool " +
		"(tool=\"<name>\", args={...matching its schema...}).",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"query": {Type: "string", Desc: "Optional keyword to filter by (matches tool name or description); omit to list everything", Required: false},
	}),
}

type searchPentestToolsTool struct {
	lister mcpLister
}

func newSearchPentestToolsTool(lister mcpLister) *searchPentestToolsTool {
	return &searchPentestToolsTool{lister: lister}
}

func (searchPentestToolsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return searchPentestToolsInfo, nil
}

func (st *searchPentestToolsTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args searchPentestToolsArgs
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return fmt.Sprintf("invalid tool call arguments: %v", err), nil
		}
	}
	tools, err := st.lister.ListTools(ctx)
	if err != nil {
		return "", fmt.Errorf("search_pentest_tools: list tools: %w", err)
	}

	query := strings.ToLower(strings.TrimSpace(args.Query))
	var out strings.Builder
	matched := 0
	for _, t := range tools {
		if query != "" && !strings.Contains(strings.ToLower(t.Name), query) && !strings.Contains(strings.ToLower(t.Description), query) {
			continue
		}
		matched++
		schemaJSON, _ := json.Marshal(t.InputSchema)
		fmt.Fprintf(&out, "%s: %s\n  schema: %s\n", t.Name, t.Description, string(schemaJSON))
	}
	if matched == 0 {
		return fmt.Sprintf("no sandbox tools matched %q", args.Query), nil
	}
	return out.String(), nil
}

// ── run_pentest_tool ─────────────────────────────────────────────────────
//
// The dispatch half: invokes any tool discovered via search_pentest_tools
// (or one of the individually-typed tools too, though those are more
// reliable called directly). Goes through the exact same
// validate/gate/approve/mcp-exec/verify pipeline as every individually-typed
// pentestTool via the shared runPentestCall helper — rationale/risk_level
// are top-level fields here, structurally separate from args, so they can
// never leak into the sandbox-mcp server's strict per-tool schema the way
// they briefly did for the typed tools (see the fix in pentest_tools.go).

type runPentestToolArgs struct {
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Rationale string         `json:"rationale"`
	RiskLevel string         `json:"risk_level"`
}

var runPentestToolInfo = &schema.ToolInfo{
	Name: "run_pentest_tool",
	Desc: "Run a sandbox pentest tool discovered via search_pentest_tools, using the exact argument names from its schema.",
	ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"tool":       {Type: "string", Desc: "The tool name, e.g. as returned by search_pentest_tools", Required: true},
		"args":       {Type: "object", Desc: "Arguments matching the tool's own schema (must include its target field)", Required: true},
		"rationale":  {Type: "string", Desc: "One sentence: why this command, right now", Required: true},
		"risk_level": {Type: "string", Desc: "Your own risk assessment: low, medium, high, or critical", Required: true},
	}),
}

type runPentestTool struct {
	deps pentestToolDeps
}

func newRunPentestTool(deps pentestToolDeps) *runPentestTool {
	return &runPentestTool{deps: deps}
}

func (runPentestTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return runPentestToolInfo, nil
}

func (rt *runPentestTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args runPentestToolArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return fmt.Sprintf("invalid tool call arguments: %v", err), nil
	}
	if strings.TrimSpace(args.Tool) == "" {
		return "tool is required", nil
	}
	if _, ok := sandboxmcp.Target(args.Tool, args.Args); !ok {
		return fmt.Sprintf("%s needs a non-empty target (call search_pentest_tools to check its schema)", args.Tool), nil
	}
	return runPentestCall(ctx, rt.deps, args.Tool, args.Args, args.Rationale, args.RiskLevel)
}
