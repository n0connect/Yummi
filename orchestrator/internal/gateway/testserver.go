package gateway

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// StartTestServer starts a real Unix-socket listener that speaks the gateway
// wire protocol (length-prefixed JSON), driven by the provided handler. It is
// exported so other packages' tests can exercise code that talks to a
// *gateway.Client without faking gateway's own decision logic — the listener
// genuinely implements the wire framing; only the per-request response is
// supplied by the test.
//
// It lives in a non-_test.go file (rather than client_test.go) so that
// importing packages' test binaries — e.g. internal/api — can link against it;
// test-only symbols are not visible across package boundaries.
func StartTestServer(t *testing.T, chunked bool, responder func(Request) Response) string {
	t.Helper()

	// Short temp dir + short socket name: the unix socket path must stay under
	// the OS sun_path limit (~104 on macOS); t.TempDir()'s long test-name path
	// would overflow it.
	dir, err := os.MkdirTemp("", "ygw")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "g.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConnection(conn, chunked, responder)
		}
	}()

	return socketPath
}

func handleConnection(conn net.Conn, chunked bool, responder func(Request) Response) {
	defer func() { _ = conn.Close() }()

	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return
	}
	reqLen := binary.BigEndian.Uint32(lenBuf)
	if reqLen == 0 || reqLen > 1024*1024 {
		return
	}

	payload := make([]byte, reqLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return
	}

	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return
	}
	resp := responder(req)

	respBytes, err := json.Marshal(resp)
	if err != nil {
		return
	}

	frame := make([]byte, 4+len(respBytes))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(respBytes)))
	copy(frame[4:], respBytes)

	if chunked {
		for i := 0; i < len(frame); i++ {
			if _, err := conn.Write(frame[i : i+1]); err != nil {
				return
			}
		}
		return
	}
	_, _ = conn.Write(frame)
}
