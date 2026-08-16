package gateway

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// This file tests send()/roundTrip()'s retry behavior directly, using real
// Unix-socket listeners under Go's control (rather than StartTestServer,
// which always answers every request) so each test can script exactly the
// failure mode it needs: a not-yet-listening socket, a connection that's
// accepted and read but then dropped before responding, and so on.

// shortSocketPath returns a socket path in a short-named temp dir the client
// can dial into, kept under the OS's sun_path length limit (~104 bytes on
// macOS) — mirrors the reasoning in testserver.go's StartTestServer.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ygwr")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "g.sock")
}

// readFrame reads one length-prefixed JSON request frame off conn.
func readFrame(conn net.Conn) (Request, error) {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return Request{}, err
	}
	n := binary.BigEndian.Uint32(lenBuf)
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return Request{}, err
	}
	var req Request
	err := json.Unmarshal(buf, &req)
	return req, err
}

// writeFrame writes one length-prefixed JSON response frame to conn.
func writeFrame(conn net.Conn, resp Response) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	frame := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(b)))
	copy(frame[4:], b)
	_, err = conn.Write(frame)
	return err
}

// TestClientRetriesReadOnlyCallOnInitialDialFailure proves the headline
// retry story: gateway isn't listening yet (mid-restart, momentarily busy),
// and the client's bounded retry rides that out instead of propagating the
// first failed dial straight to the caller.
func TestClientRetriesReadOnlyCallOnInitialDialFailure(t *testing.T) {
	socket := shortSocketPath(t) // nothing listening yet: first dial(s) must fail

	go func() {
		time.Sleep(60 * time.Millisecond)
		ln, err := net.Listen("unix", socket)
		if err != nil {
			return
		}
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := readFrame(conn); err != nil {
			return
		}
		_ = writeFrame(conn, Response{Type: "pong"})
	}()

	c := NewClient(socket)
	start := time.Now()
	if err := c.Ping(); err != nil {
		t.Fatalf("expected the retry to ride out the startup delay, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("expected the call to have waited through at least one retry backoff before succeeding, only took %v", elapsed)
	}
}

// TestClientRetriesMutatingCallOnInitialDialFailure proves a mutating call
// (AddTemporaryRule) is ALSO retried on a bare dial failure: that's the one
// failure mode send() can prove never reached gateway, so it's safe to retry
// even though the request mutates gateway's rule state.
func TestClientRetriesMutatingCallOnInitialDialFailure(t *testing.T) {
	socket := shortSocketPath(t)

	go func() {
		time.Sleep(60 * time.Millisecond)
		ln, err := net.Listen("unix", socket)
		if err != nil {
			return
		}
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := readFrame(conn); err != nil {
			return
		}
		_ = writeFrame(conn, Response{Type: "rule_added", RuleID: "temp-1", ExpiresAt: 1700})
	}()

	c := NewClient(socket)
	ruleID, expiresAt, _, err := c.AddTemporaryRule("10.0.0.5/32", 600, "agent execution")
	if err != nil {
		t.Fatalf("expected the retry to ride out the startup delay, got: %v", err)
	}
	if ruleID != "temp-1" || expiresAt != 1700 {
		t.Fatalf("unexpected result: ruleID=%s expiresAt=%d", ruleID, expiresAt)
	}
}

// TestClientDoesNotRetryMutatingCallAfterDispatch is the safety-critical
// case: gateway accepts the connection and reads the FULL request (so
// AddTemporaryRule may already have been applied server-side) but dies
// before writing a response. The client must surface that failure as-is —
// retrying here risks adding the same nftables rule twice.
func TestClientDoesNotRetryMutatingCallAfterDispatch(t *testing.T) {
	socket := shortSocketPath(t)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var attempts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			_, _ = readFrame(conn) // fully consume the request, then drop the connection
			_ = conn.Close()
		}
	}()

	c := NewClient(socket)
	_, _, _, err = c.AddTemporaryRule("10.0.0.5/32", 600, "agent execution")
	if err == nil {
		t.Fatal("expected an error: gateway dropped the connection after reading the request")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt for a mutating call once dispatched, got %d", got)
	}
}

// TestClientRetriesReadOnlyCallAfterDispatchFailure is the read-only mirror
// of the above: because a duplicate read has no side effect, the client
// keeps retrying even after the request was fully sent and the response was
// lost, succeeding once gateway (scripted here to fail twice, then answer)
// finally responds.
func TestClientRetriesReadOnlyCallAfterDispatchFailure(t *testing.T) {
	socket := shortSocketPath(t)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var attempts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n := attempts.Add(1)
			if _, err := readFrame(conn); err != nil {
				_ = conn.Close()
				continue
			}
			if n < maxSendAttempts {
				_ = conn.Close() // drop the connection without responding
				continue
			}
			_ = writeFrame(conn, Response{Type: "pong"})
			_ = conn.Close()
		}
	}()

	c := NewClient(socket)
	if err := c.Ping(); err != nil {
		t.Fatalf("expected the final retry to succeed, got: %v", err)
	}
	if got := attempts.Load(); got != maxSendAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", maxSendAttempts, got)
	}
}

// TestClientExhaustsRetriesOnPersistentFailure proves the retry is bounded:
// even a read-only call gives up after maxSendAttempts and surfaces the
// error, rather than retrying forever.
func TestClientExhaustsRetriesOnPersistentFailure(t *testing.T) {
	socket := shortSocketPath(t)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var attempts atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			_, _ = readFrame(conn)
			_ = conn.Close() // never respond
		}
	}()

	c := NewClient(socket)
	if err := c.Ping(); err == nil {
		t.Fatal("expected an error after exhausting all retries")
	}
	if got := attempts.Load(); got != maxSendAttempts {
		t.Fatalf("expected exactly %d attempts, got %d", maxSendAttempts, got)
	}
}

// TestClientDoesNotRetryGatewayErrorResponse proves a well-formed
// application-level rejection from gateway (Type=="error") is surfaced
// immediately: it's a deterministic answer, not a transport failure, so
// retrying it would just burn time (and an idempotent call's retry budget)
// for the same result.
func TestClientDoesNotRetryGatewayErrorResponse(t *testing.T) {
	var attempts atomic.Int32
	socket := StartTestServer(t, false, func(req Request) Response {
		attempts.Add(1)
		return Response{Type: "error", Message: "denied by policy"}
	})

	c := NewClient(socket)
	if err := c.Ping(); err == nil {
		t.Fatal("expected gateway error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected exactly 1 attempt for a deterministic gateway error, got %d", got)
	}
}
