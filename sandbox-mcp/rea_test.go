package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReaCallTool_RequiresToolName(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "rea_call_tool", Arguments: map[string]any{"tool_name": ""},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for an empty tool_name")
	}
}

// REA, unlike Burp, needs no GUI/display — `rea --mcp` is a plain child
// process, so this can exercise the real bridge end to end wherever rea is
// actually installed (the built sandbox image; skipped elsewhere).

func TestReaListTools_RealExec(t *testing.T) {
	if _, err := exec.LookPath("rea"); err != nil {
		t.Skip("rea not installed on this host")
	}
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "rea_list_tools", Arguments: map[string]any{},
	})
	text := resultText(t, result, err)
	if !strings.Contains(text, "inspect_analysis_settings") {
		t.Fatalf("expected the real REA tool catalog, got %s", text)
	}
}

func TestReaListTools_RealExec_NameFilter(t *testing.T) {
	if _, err := exec.LookPath("rea"); err != nil {
		t.Skip("rea not installed on this host")
	}
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "rea_list_tools",
		Arguments: map[string]any{"name_filter": "adb"},
	})
	text := resultText(t, result, err)
	if !strings.Contains(text, "list_adb_devices") {
		t.Fatalf("expected the filtered catalog to include list_adb_devices, got %s", text)
	}
	if strings.Contains(text, "inspect_wasm_artifact") {
		t.Fatalf("name_filter=adb should have excluded unrelated tools, got %s", text)
	}
}

func TestReaCallTool_RealExec(t *testing.T) {
	if _, err := exec.LookPath("rea"); err != nil {
		t.Skip("rea not installed on this host")
	}
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "rea_call_tool",
		Arguments: map[string]any{
			"tool_name": "inspect_analysis_settings",
			"arguments": map[string]any{},
		},
	})
	text := resultText(t, result, err)
	if !strings.Contains(text, "setting") {
		t.Fatalf("expected a settings dump, got %s", text)
	}
}
