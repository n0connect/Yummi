package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestConsolidateHosts_MergesDeduplicatesAndFiltersNoise(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })
	f1 := writeTestFile(t, dir, "subs_subfinder.txt", "www.example.com\nDEV.example.com\napi.example.com\n")
	f2 := writeTestFile(t, dir, "subs_amass.txt", "www.example.com\nSession Scope\n\nFQDN:\nexample.com\nnotexample.com\n")
	out := filepath.Join(dir, "union.txt")

	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "consolidate_hosts",
		Arguments: map[string]any{
			"input_files": []string{f1, f2},
			"domain":      "example.com",
			"output_file": out,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}
	got := strings.TrimSpace(string(data))
	want := "api.example.com\ndev.example.com\nexample.com\nwww.example.com"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "unique): 4") {
		t.Fatalf("expected summary to report 4 unique hosts, got %+v", result.Content)
	}
	// "notexample.com" and the amass banner lines are not *.example.com
	// hostnames — must be dropped, not just merged in as noise.
	if strings.Contains(got, "notexample.com") {
		t.Fatalf("notexample.com should have been filtered out as noise, got %q", got)
	}
}

func TestConsolidateHosts_MissingInputFileIsSkippedNotFatal(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })
	f1 := writeTestFile(t, dir, "real.txt", "www.example.com\n")
	out := filepath.Join(dir, "union.txt")

	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "consolidate_hosts",
		Arguments: map[string]any{
			"input_files": []string{f1, filepath.Join(dir, "does-not-exist.txt")},
			"domain":      "example.com",
			"output_file": out,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("a missing input file must not fail the whole call, got IsError with %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "does-not-exist.txt") {
		t.Fatalf("expected the missing file to be noted in the summary, got %+v", result.Content)
	}
}

func TestConsolidateHosts_HighConfidenceOnlyDropsNoiseLikeNames(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })
	f1 := writeTestFile(t, dir, "in.txt", "www.example.com\n142.example.com\nab.example.com\nsupport.example.com\n")
	out := filepath.Join(dir, "union.txt")

	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "consolidate_hosts",
		Arguments: map[string]any{
			"input_files":          []string{f1},
			"domain":               "example.com",
			"output_file":          out,
			"high_confidence_only": true,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}
	data, _ := os.ReadFile(out)
	got := string(data)
	for _, dropped := range []string{"142.example.com", "ab.example.com"} {
		if strings.Contains(got, dropped) {
			t.Errorf("high_confidence_only should have dropped %q, got %q", dropped, got)
		}
	}
	for _, kept := range []string{"www.example.com", "support.example.com"} {
		if !strings.Contains(got, kept) {
			t.Errorf("high_confidence_only should have kept %q, got %q", kept, got)
		}
	}
}

func TestConsolidateHosts_RequiredFieldsAndValidation(t *testing.T) {
	dir := t.TempDir()
	f1 := writeTestFile(t, dir, "in.txt", "www.example.com\n")
	session := newTestServer(t)
	cases := []struct {
		name string
		args map[string]any
	}{
		{"empty domain", map[string]any{"input_files": []string{f1}, "domain": "", "output_file": filepath.Join(dir, "o.txt")}},
		{"no input_files", map[string]any{"domain": "example.com", "output_file": filepath.Join(dir, "o.txt")}},
		{"empty output_file", map[string]any{"input_files": []string{f1}, "domain": "example.com", "output_file": ""}},
		{"relative output_file", map[string]any{"input_files": []string{f1}, "domain": "example.com", "output_file": "union.txt"}},
		{"flag-shaped domain", map[string]any{"input_files": []string{f1}, "domain": "--evil", "output_file": filepath.Join(dir, "o.txt")}},
	}
	for _, c := range cases {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "consolidate_hosts", Arguments: c.args})
		if err != nil {
			t.Fatalf("%s: CallTool: %v", c.name, err)
		}
		if !result.IsError {
			t.Errorf("%s: expected IsError", c.name)
		}
	}
}

func TestConsolidateHosts_MatchesApexAndIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })
	f1 := writeTestFile(t, dir, "in.txt", "EXAMPLE.COM\nWWW.EXAMPLE.COM\nwww.example.com\n")
	out := filepath.Join(dir, "union.txt")

	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "consolidate_hosts",
		Arguments: map[string]any{
			"input_files": []string{f1},
			"domain":      "example.com",
			"output_file": out,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}
	data, _ := os.ReadFile(out)
	got := strings.TrimSpace(string(data))
	want := "example.com\nwww.example.com"
	if got != want {
		t.Fatalf("output = %q, want %q (apex kept, case folded, deduplicated)", got, want)
	}
}
