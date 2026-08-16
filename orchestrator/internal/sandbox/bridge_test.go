package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewBridge(t *testing.T) {
	b := NewBridge("")
	if b.container != defaultContainer {
		t.Fatalf("expected %s, got %s", defaultContainer, b.container)
	}
	b2 := NewBridge("custom")
	if b2.container != "custom" {
		t.Fatalf("expected custom, got %s", b2.container)
	}
}

// TestSetTimeout proves the zero-guard: real pentest tooling needs the
// timeout raised well past the default, but a config parse failure landing
// on the zero value must never silently turn every command instant-fail.
func TestSetTimeout(t *testing.T) {
	b := NewBridge("test-container")
	if b.timeout != defaultTimeout {
		t.Fatalf("expected default timeout %v, got %v", defaultTimeout, b.timeout)
	}
	b.SetTimeout(30 * time.Minute)
	if b.timeout != 30*time.Minute {
		t.Fatalf("expected overridden timeout 30m, got %v", b.timeout)
	}
	b.SetTimeout(0)
	if b.timeout != 30*time.Minute {
		t.Fatalf("SetTimeout(0) must be a no-op, got %v", b.timeout)
	}
	b.SetTimeout(-5 * time.Second)
	if b.timeout != 30*time.Minute {
		t.Fatalf("SetTimeout(negative) must be a no-op, got %v", b.timeout)
	}
}

func TestCappedBuffer(t *testing.T) {
	b := newCappedBuffer(50)
	if n, err := b.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("first write = (%d, %v), want (5, nil)", n, err)
	}
	long := strings.Repeat("x", 100)
	if n, err := b.Write([]byte(long)); err != nil || n != len(long) {
		t.Fatalf("overflow write = (%d, %v), want (%d, nil)", n, err, len(long))
	}
	if b.buf.Len() != 50 {
		t.Fatalf("retained %d bytes, want hard cap 50", b.buf.Len())
	}
	if !strings.HasSuffix(b.String(), truncationMark) {
		t.Fatalf("result should end with truncation marker: %q", b.String())
	}
}

func TestExecEmptyCommand(t *testing.T) {
	b := NewBridge("test-container")
	_, err := b.Exec(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// Integration test — only runs when sandbox container is available.
func TestExecIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	b := NewBridge("yummi-sandbox")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test ping
	if err := b.Ping(ctx); err != nil {
		t.Skipf("sandbox container not available: %v", err)
	}

	// Test command execution
	result, err := b.Exec(ctx, "nmap --version")
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Nmap") {
		t.Fatalf("expected nmap version output, got: %s", result.Stdout)
	}
	if result.Duration <= 0 {
		t.Fatal("duration should be positive")
	}
}

// ─── streamWriter unit tests (no docker required) ──────────────────────────

// TestStreamWriterTapsWritesInOrder proves the tap is purely additive: the
// underlying cappedBuffer accumulates exactly as it would on its own, and
// onChunk observes each Write's bytes, in order, unmodified.
func TestStreamWriterTapsWritesInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	w := &streamWriter{
		cappedBuffer: newCappedBuffer(1024),
		source:       "stdout",
		onChunk: func(source string, chunk []byte) {
			mu.Lock()
			defer mu.Unlock()
			if source != "stdout" {
				t.Errorf("onChunk source = %q, want stdout", source)
			}
			got = append(got, string(chunk))
		},
	}
	for _, s := range []string{"hello ", "cruel ", "world"} {
		if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("write(%q) = (%d, %v), want (%d, nil)", s, n, err, len(s))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, "") != "hello cruel world" {
		t.Fatalf("onChunk saw %v, want the writes in order", got)
	}
	if w.String() != "hello cruel world" {
		t.Fatalf("underlying buffer = %q, want %q", w.String(), "hello cruel world")
	}
}

// TestStreamWriterStopsChunksOnceTruncated proves streaming respects the
// exact same cap the persisted buffer respects: once a write pushes the
// buffer past its limit, no further onChunk calls happen — the live
// side-channel can never grow past what the authoritative output already
// bounds itself to.
func TestStreamWriterStopsChunksOnceTruncated(t *testing.T) {
	var chunks [][]byte
	w := &streamWriter{
		cappedBuffer: newCappedBuffer(10),
		source:       "stdout",
		onChunk: func(_ string, chunk []byte) {
			chunks = append(chunks, chunk)
		},
	}
	if _, err := w.Write([]byte("hello")); err != nil { // 5 bytes, under the 10-byte cap
		t.Fatalf("write: %v", err)
	}
	if _, err := w.Write([]byte("world!!!!!")); err != nil { // pushes past the cap
		t.Fatalf("write: %v", err)
	}
	if _, err := w.Write([]byte("more")); err != nil { // already truncated — must not emit
		t.Fatalf("write: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected exactly 2 onChunk calls (the writes before truncation), got %d: %q", len(chunks), chunks)
	}
	// The second write crosses the cap mid-write: cappedBuffer only keeps
	// "world" (5 of the 10 bytes — the buffer had exactly 5 bytes of
	// headroom left after "hello"). onChunk must receive only that accepted
	// prefix, never the dropped tail — the live side-channel must not show
	// the operator bytes the persisted/final output never actually kept.
	if string(chunks[0]) != "hello" || string(chunks[1]) != "world" {
		t.Fatalf("unexpected chunk contents: %q", chunks)
	}
	if !w.truncated {
		t.Fatal("expected the underlying cappedBuffer to be marked truncated")
	}
}

// TestStreamWriterChunkIsIndependentCopy guards against aliasing os/exec's
// reused read buffer: os/exec's stdout/stderr copy goroutines reuse the same
// backing array across Write calls, so onChunk must receive its own copy —
// otherwise a later Write would silently corrupt a chunk already handed to
// the callback.
func TestStreamWriterChunkIsIndependentCopy(t *testing.T) {
	var got []byte
	w := &streamWriter{
		cappedBuffer: newCappedBuffer(1024),
		source:       "stdout",
		onChunk:      func(_ string, chunk []byte) { got = chunk },
	}
	buf := []byte("hello")
	if _, err := w.Write(buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf[0] = 'X' // mutate the caller's slice after Write returns, as a reused pipe buffer would
	if string(got) != "hello" {
		t.Fatalf("onChunk's chunk was aliased to the caller's buffer: got %q, want %q", got, "hello")
	}
}

// TestStreamWriterNilOnChunk proves a nil callback (Exec's own usage, via
// ExecStream(ctx, cmd, nil)) is a safe no-op, not a panic.
func TestStreamWriterNilOnChunk(t *testing.T) {
	w := &streamWriter{cappedBuffer: newCappedBuffer(1024), source: "stdout"}
	if _, err := w.Write([]byte("fine")); err != nil {
		t.Fatalf("write with nil onChunk: %v", err)
	}
	if w.String() != "fine" {
		t.Fatalf("buffer = %q, want %q", w.String(), "fine")
	}
}

// ─── ExecStream integration tests (real docker, real container) ───────────

// startTestSandboxContainer starts a standalone container from the same
// yummi/sandbox:local image the real deployment uses (bash + uid 1000
// pentester user), so ExecStream can be exercised against a real
// `docker exec`, never a mock. The entrypoint is overridden to a bare sleep:
// the image's real entrypoint.sh does `ip route replace` for gateway
// routing, which needs NET_ADMIN and a live gateway container, neither of
// which a unit test provides — skipping it changes nothing ExecStream itself
// exercises (docker exec --user 1000:1000 <container> /bin/bash -lc <cmd>).
// Skips the test (not a failure) when docker or the image isn't available
// locally, matching TestExecIntegration's own skip discipline above.
func startTestSandboxContainer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "image", "inspect", "yummi/sandbox:local").Run(); err != nil {
		t.Skip("yummi/sandbox:local image not available locally (build it with `make docker-build` or `make _build-sandbox-mcp` + docker build)")
	}
	name := fmt.Sprintf("yummi-sandbox-bridge-test-%d", time.Now().UnixNano())
	runArgs := []string{"run", "-d", "--rm", "--name", name, "--entrypoint", "/bin/sleep", "yummi/sandbox:local", "infinity"}
	if out, err := exec.Command("docker", runArgs...).CombinedOutput(); err != nil {
		t.Skipf("failed to start test sandbox container: %v (%s)", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("docker", "exec", name, "true").Run(); err == nil {
			return name
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("test sandbox container did not become ready in time")
	return ""
}

// TestExecStreamDeliversMultipleChunks proves ExecStream taps real docker
// exec output as it arrives — a command that sleeps between lines must
// produce more than one onChunk call — and that concatenating every observed
// chunk reproduces exactly the same bytes as the final, authoritative
// Result.Stdout/Stderr the (unmodified) capped-buffer accounting produces.
func TestExecStreamDeliversMultipleChunks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	container := startTestSandboxContainer(t)

	b := NewBridge(container)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var mu sync.Mutex
	type chunk struct{ source, data string }
	var chunks []chunk
	onChunk := func(source string, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		chunks = append(chunks, chunk{source, string(data)})
	}

	cmd := `for i in 1 2 3; do printf 'line%s\n' "$i"; sleep 0.1; done; printf 'err-line\n' 1>&2`
	result, err := b.ExecStream(ctx, cmd, onChunk)
	if err != nil {
		t.Fatalf("ExecStream failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", result.ExitCode, result.Stderr)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(chunks) < 2 {
		t.Fatalf("expected multiple streamed chunks for a multi-line, sleep-interleaved command, got %d: %+v", len(chunks), chunks)
	}
	var gotStdout, gotStderr strings.Builder
	for _, c := range chunks {
		switch c.source {
		case "stdout":
			gotStdout.WriteString(c.data)
		case "stderr":
			gotStderr.WriteString(c.data)
		default:
			t.Fatalf("unexpected chunk source %q", c.source)
		}
	}
	if gotStdout.String() != result.Stdout {
		t.Fatalf("streamed stdout %q does not match final Result.Stdout %q", gotStdout.String(), result.Stdout)
	}
	if gotStderr.String() != result.Stderr {
		t.Fatalf("streamed stderr %q does not match final Result.Stderr %q", gotStderr.String(), result.Stderr)
	}
}

// TestExecDelegatesToExecStream proves Exec is genuinely a thin wrapper
// (same container, same invocation shape, same result) rather than a
// parallel implementation that could drift from ExecStream over time.
func TestExecDelegatesToExecStream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	container := startTestSandboxContainer(t)
	b := NewBridge(container)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := b.Exec(ctx, "echo plain-exec")
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "plain-exec" {
		t.Fatalf("Exec stdout = %q, want %q", result.Stdout, "plain-exec")
	}
}
