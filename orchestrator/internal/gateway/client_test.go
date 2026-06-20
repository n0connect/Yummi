package gateway

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientGetStatus(t *testing.T) {
	socket := startTestServer(t, false, func(req Request) Response {
		if req.Type != "get_status" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{
			Type:        "status",
			Level:       2,
			RulesActive: true,
			UptimeSecs:  77,
		}
	})

	c := NewClient(socket)
	status, err := c.GetStatus()
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.Level != 2 || !status.RulesActive || status.UptimeSecs != 77 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestClientCheckScope(t *testing.T) {
	socket := startTestServer(t, false, func(req Request) Response {
		if req.Type != "check_scope" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{
			Type:    "scope_check_result",
			IP:      req.IP,
			InScope: req.IP == "10.0.0.5",
		}
	})

	c := NewClient(socket)
	ok, err := c.CheckScope("10.0.0.5")
	if err != nil {
		t.Fatalf("check scope: %v", err)
	}
	if !ok {
		t.Fatal("expected IP to be in scope")
	}
}

func TestClientPingChunkedResponse(t *testing.T) {
	socket := startTestServer(t, true, func(req Request) Response {
		if req.Type != "ping" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{Type: "pong"}
	})

	c := NewClient(socket)
	if err := c.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClientReturnsPStarError(t *testing.T) {
	socket := startTestServer(t, false, func(req Request) Response {
		return Response{Type: "error", Message: "denied by policy"}
	})

	c := NewClient(socket)
	err := c.Ping()
	if err == nil {
		t.Fatal("expected p* error")
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("expected policy error, got: %v", err)
	}
}

func startTestServer(t *testing.T, chunked bool, responder func(Request) Response) string {
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
	defer conn.Close()

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
