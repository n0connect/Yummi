package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestScreenshotTriage_RequiresAbsoluteTarget(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "screenshot_triage",
		Arguments: map[string]any{
			"target":     "relative/hosts.txt",
			"output_dir": "/home/pentester/work/shots",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for a relative target path")
	}
}

func TestScreenshotTriage_RejectsOutputDirOutsideSandboxHome(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })

	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "screenshot_triage",
		Arguments: map[string]any{
			"target":     filepath.Join(dir, "hosts.txt"),
			"output_dir": "/etc/shots",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for an output_dir outside sandboxHome")
	}
}

func TestConfineDir(t *testing.T) {
	dir := t.TempDir()
	sandboxHome = dir
	t.Cleanup(func() { sandboxHome = "/home/pentester" })

	if _, reason := confineDir("relative/shots"); reason == "" {
		t.Fatal("expected rejection for a relative path")
	}
	if _, reason := confineDir("/outside/shots"); reason == "" {
		t.Fatal("expected rejection for a path outside sandboxHome")
	}

	target := filepath.Join(dir, "work", "example.com", "shots")
	clean, reason := confineDir(target)
	if reason != "" {
		t.Fatalf("expected a confined path inside sandboxHome to be accepted, got reason: %s", reason)
	}
	if clean != target {
		t.Fatalf("clean = %q, want %q", clean, target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("expected confineDir to create the directory: err=%v", err)
	}
}

func TestListScreenshotFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.png", "not a real png, just bytes")
	writeTestFile(t, dir, "b.jpg", "x")
	writeTestFile(t, dir, "notes.txt", "ignore me")
	sub := filepath.Join(dir, "nested")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestFile(t, sub, "c.jpeg", "y")

	got := listScreenshotFiles(dir)
	want := []string{"a.png", "b.jpg", filepath.Join("nested", "c.jpeg")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestListScreenshotFiles_EmptyDirReturnsNil(t *testing.T) {
	dir := t.TempDir()
	if got := listScreenshotFiles(dir); len(got) != 0 {
		t.Fatalf("expected no files, got %v", got)
	}
}
