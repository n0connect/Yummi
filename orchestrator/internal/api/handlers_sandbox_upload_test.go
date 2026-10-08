package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

func multipartUploadRequest(t *testing.T, filename, content string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandbox/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestHandleSandboxUploadNilBridgeIs503(t *testing.T) {
	req := multipartUploadRequest(t, "vpn.ovpn", "client\n")
	w := httptest.NewRecorder()
	HandleSandboxUpload(nil, nil).ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSandboxUploadMissingFileFieldIs400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandbox/upload", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	w := httptest.NewRecorder()
	HandleSandboxUpload(sandbox.NewBridge("unused"), nil).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing file field, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSandboxUploadRejectsPathTraversalFilename(t *testing.T) {
	req := multipartUploadRequest(t, "../../etc/cron.d/evil", "* * * * * root id\n")
	w := httptest.NewRecorder()
	HandleSandboxUpload(sandbox.NewBridge("unused"), nil).ServeHTTP(w, req)
	// filepath.Base reduces this to the harmless leaf name "evil" — not a
	// 400, but also never a write outside desktopDir either way (see
	// SanitizeUploadFilename's own tests for that guarantee). This test
	// exists to document that behavior, not to require rejection here.
	if w.Code != http.StatusBadRequest && w.Code != http.StatusBadGateway {
		t.Fatalf("expected a 4xx/502 (bad bridge, no live sandbox), got %d: %s", w.Code, w.Body.String())
	}
}

// SanitizeUploadFilename's own rejection branch (control bytes, empty-
// after-Base()) is NOT independently tested at this HTTP layer: hand-
// verified that every way to trigger it — a 0x00-0x1F or 0x7F control byte,
// or filename="" — is already rejected earlier by net/textproto's MIME
// header parsing or by http.Request.FormFile itself ("no such file" for an
// empty filename), never reaching this handler's own code at all. That
// rejection logic is fully covered directly in bridge_test.go's
// TestSanitizeUploadFilename instead, which calls it without going through
// net/http's stricter outer layer.

// Real filenames routinely have spaces and non-ASCII characters — must not
// be rejected just because they'd be dangerous in a SHELL string, since no
// shell is ever involved on this path.
func TestHandleSandboxUploadAcceptsRealisticFilenames(t *testing.T) {
	for _, name := range []string{"My VPN Config.ovpn", "şirket-vpn.ovpn", "a;rm -rf /"} {
		req := multipartUploadRequest(t, name, "x")
		w := httptest.NewRecorder()
		HandleSandboxUpload(sandbox.NewBridge("unused"), nil).ServeHTTP(w, req)
		// No live sandbox named "unused" — this only proves sanitize let it
		// through (502 from the docker exec failure), never a 400.
		if w.Code == http.StatusBadRequest {
			t.Fatalf("filename %q was wrongly rejected as invalid: %s", name, w.Body.String())
		}
	}
}

// Integration test — only runs when the real sandbox container is available
// (self-skips otherwise, matching internal/sandbox's own Integration tests).
func TestHandleSandboxUploadIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	b := sandbox.NewBridge("yummi-sandbox")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Ping(ctx); err != nil {
		t.Skipf("sandbox container not available: %v", err)
	}

	req := multipartUploadRequest(t, "yummi-handler-test-upload.txt", "hello from the API handler test\n")
	w := httptest.NewRecorder()
	HandleSandboxUpload(b, nil).ServeHTTP(w, req)
	defer func() {
		_, _ = b.Exec(context.Background(), "rm -f /home/pentester/Desktop/yummi-handler-test-upload.txt")
	}()

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	result, err := b.Exec(ctx, "cat /home/pentester/Desktop/yummi-handler-test-upload.txt")
	if err != nil {
		t.Fatalf("exec cat failed: %v", err)
	}
	if result.Stdout != "hello from the API handler test\n" {
		t.Fatalf("unexpected content: %q", result.Stdout)
	}
}
