package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunCaptured_Success(t *testing.T) {
	result, _, err := runCaptured(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("runCaptured: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "hello") {
		t.Fatalf("expected output containing 'hello', got %+v", result.Content)
	}
}

func TestRunCaptured_NonZeroExit(t *testing.T) {
	result, _, err := runCaptured(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err != nil {
		t.Fatalf("runCaptured returned a Go error for a normal non-zero exit: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for a non-zero exit")
	}
}

func TestRunCaptured_LaunchFailure(t *testing.T) {
	result, _, err := runCaptured(context.Background(), "yummi-definitely-not-a-real-binary")
	if err != nil {
		t.Fatalf("runCaptured returned a Go error for a launch failure: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true when the binary does not exist")
	}
}

// TestRunCapturedEnv_OverridesVar proves runCapturedEnv actually layers the
// extra env on top of the inherited environment (not replacing it) — the
// regression this exists for: wpscanHandler needs HOME redirected to a
// writable path without losing PATH/etc, which a bare os.Setenv would also
// achieve but unsafely, racing across this server's concurrent tool calls.
func TestRunCapturedEnv_OverridesVar(t *testing.T) {
	t.Setenv("YUMMI_TEST_MARKER", "inherited")
	result, _, err := runCapturedEnv(context.Background(), []string{"HOME=/tmp/overridden"}, "sh", "-c", "echo HOME=$HOME MARKER=$YUMMI_TEST_MARKER")
	if err != nil {
		t.Fatalf("runCapturedEnv: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "HOME=/tmp/overridden") {
		t.Fatalf("expected overridden HOME, got %+v", result.Content)
	}
	if !strings.Contains(text.Text, "MARKER=inherited") {
		t.Fatalf("expected inherited env to survive alongside the override, got %+v", result.Content)
	}
}

// TestRunCapturedToFile_SavesExactlyWhatWasCaptured proves the saved file's
// content matches the text the model itself reads back — no re-derivation
// from the underlying tool's own -o/-f convention, no risk of the two
// diverging (see runCapturedToFile's doc comment for why that matters).
func TestRunCapturedToFile_SavesExactlyWhatWasCaptured(t *testing.T) {
	home := t.TempDir()
	sandboxHome = home
	t.Cleanup(func() { sandboxHome = "/home/pentester" })
	path := filepath.Join(home, "nested", "output.txt")
	result, _, err := runCapturedToFile(context.Background(), path, "echo", "sub1.example.com")
	if err != nil {
		t.Fatalf("runCapturedToFile: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %+v", result.Content[0])
	}
	if !strings.Contains(text.Text, "output saved to "+path) {
		t.Fatalf("expected a save confirmation naming %s, got %q", path, text.Text)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file at %s to exist: %v", path, err)
	}
	if !strings.Contains(string(saved), "sub1.example.com") {
		t.Fatalf("saved file missing the tool's own output, got %q", string(saved))
	}
	if strings.Contains(string(saved), "output saved to") {
		t.Fatalf("saved file should hold only the tool's own output, not the confirmation note appended afterward, got %q", string(saved))
	}
}

func TestRunCapturedToFile_EmptyPathIsNoop(t *testing.T) {
	result, _, err := runCapturedToFile(context.Background(), "", "echo", "hello")
	if err != nil {
		t.Fatalf("runCapturedToFile: %v", err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "hello\n" {
		t.Fatalf("expected plain runCaptured behavior with no save note, got %+v", result.Content)
	}
}

func TestRunCapturedToFile_RelativePathRejected(t *testing.T) {
	result, _, err := runCapturedToFile(context.Background(), "relative/path.txt", "echo", "hello")
	if err != nil {
		t.Fatalf("runCapturedToFile: %v", err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "must be an absolute path") {
		t.Fatalf("expected an absolute-path complaint, got %+v", result.Content)
	}
	if result.IsError {
		t.Fatal("a failed convenience save must not turn the tool's own successful run into an error result")
	}
}

// TestSaveNote_RejectsPathOutsideSandboxHome is the regression test for a
// real arbitrary-file-write finding: output_file is a value the model
// chooses (and can be steered by hostile content in scanned-target
// responses), so a path outside the pentester user's own home must be
// refused rather than written.
func TestSaveNote_RejectsPathOutsideSandboxHome(t *testing.T) {
	home := t.TempDir()
	sandboxHome = home
	t.Cleanup(func() { sandboxHome = "/home/pentester" })

	outside := filepath.Join(filepath.Dir(home), "escaped.txt")
	note := saveNote(outside, "pwned")
	if !strings.Contains(note, "must be under "+home) {
		t.Fatalf("expected an outside-sandboxHome rejection, got %q", note)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written outside sandboxHome, stat err = %v", err)
	}
}

// TestSaveNote_RejectsDotDotEscape covers the ../ traversal shape directly
// (as opposed to a plain sibling path), since that's the most likely form an
// injected or malformed output_file value would actually take.
func TestSaveNote_RejectsDotDotEscape(t *testing.T) {
	home := t.TempDir()
	sandboxHome = home
	t.Cleanup(func() { sandboxHome = "/home/pentester" })

	traversal := filepath.Join(home, "..", "escaped.txt")
	note := saveNote(traversal, "pwned")
	if !strings.Contains(note, "must be under "+home) {
		t.Fatalf("expected a traversal rejection, got %q", note)
	}
}

// TestSaveNote_RejectsSymlinkEscape covers a pre-existing symlink under
// sandboxHome that points outside it — the check against the raw path alone
// would pass, so saveNote must re-resolve the parent through the filesystem
// before writing.
func TestSaveNote_RejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	outsideDir := t.TempDir()
	sandboxHome = home
	t.Cleanup(func() { sandboxHome = "/home/pentester" })

	link := filepath.Join(home, "escape-link")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	target := filepath.Join(link, "output.txt")
	note := saveNote(target, "pwned")
	if !strings.Contains(note, "resolves outside "+home) {
		t.Fatalf("expected a symlink-escape rejection, got %q", note)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "output.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written through the symlink, stat err = %v", err)
	}
}

func TestCappedBuffer_Truncates(t *testing.T) {
	b := newCappedBuffer(10)
	_, _ = b.Write([]byte("0123456789ABCDEF"))
	if !strings.HasSuffix(b.String(), truncationMark) {
		t.Fatalf("expected truncation marker, got %q", b.String())
	}
	if len(b.String()) != 10+len(truncationMark) {
		t.Fatalf("expected capped length, got %d", len(b.String()))
	}
}
