package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPassiveReconBatch_RequiredFields(t *testing.T) {
	dir := t.TempDir()
	session := newTestServer(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"empty domain", map[string]any{"domain": "", "output_dir": dir}},
		{"empty output_dir", map[string]any{"domain": "example.com", "output_dir": ""}},
		{"relative output_dir", map[string]any{"domain": "example.com", "output_dir": "work"}},
		{"flag-shaped domain", map[string]any{"domain": "--evil", "output_dir": dir}},
		{"unknown source", map[string]any{"domain": "example.com", "output_dir": dir, "sources": []string{"nmap"}}},
	}
	for _, c := range cases {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "passive_recon_batch", Arguments: c.args})
		if err != nil {
			t.Fatalf("%s: CallTool: %v", c.name, err)
		}
		if !result.IsError {
			t.Errorf("%s: expected IsError", c.name)
		}
	}
}

func TestResolvePassiveReconSources(t *testing.T) {
	all, err := resolvePassiveReconSources(nil)
	if err != nil {
		t.Fatalf("resolvePassiveReconSources(nil): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("expected all 4 default sources, got %v", all)
	}

	subset, err := resolvePassiveReconSources([]string{"AMASS", "gau", "amass"})
	if err != nil {
		t.Fatalf("resolvePassiveReconSources: %v", err)
	}
	want := []string{"amass", "gau"}
	if len(subset) != len(want) {
		t.Fatalf("resolvePassiveReconSources case-fold/dedup = %v, want %v", subset, want)
	}
	for i := range want {
		if subset[i] != want[i] {
			t.Fatalf("resolvePassiveReconSources = %v, want %v", subset, want)
		}
	}

	if _, err := resolvePassiveReconSources([]string{"nikto"}); err == nil {
		t.Fatal("expected an error for an unknown source")
	}
}

// TestPassiveReconBatch_RunsRequestedSourcesConcurrentlyAndSavesEach uses a
// fake fast-failing environment (none of the four real binaries exist on
// this test host) to prove the batch still: runs every requested source
// (not just the first), reports each one's own outcome independently, and
// writes a subs_<source>.txt file for each — the launch-failure text itself
// still gets saved, proving the save-path runs regardless of exec success.
func TestPassiveReconBatch_RunsRequestedSourcesConcurrentlyAndSavesEach(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "passive_recon_batch",
		Arguments: map[string]any{
			"domain":     "example.com",
			"output_dir": dir,
			"sources":    []string{"subfinder", "gau"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %+v", result.Content[0])
	}
	for _, source := range []string{"subfinder", "gau"} {
		if !strings.Contains(text.Text, "=== "+source+" [") {
			t.Errorf("expected a section for %s in %q", source, text.Text)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "subs_"+source+".txt")); statErr != nil {
			t.Errorf("expected subs_%s.txt to be written: %v", source, statErr)
		}
	}
	// theharvester/amass were not requested — must not have run or saved.
	for _, source := range []string{"theharvester", "amass"} {
		if strings.Contains(text.Text, "=== "+source+" [") {
			t.Errorf("did not request %s but it appears in the result: %q", source, text.Text)
		}
	}
}
