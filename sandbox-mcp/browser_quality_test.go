package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Serve a real local application over TCP and exercise real Chromium. This
// does not replace a browser, network transport, provider or Yummi component.
func TestBrowserQualityIsolatedSessionsAndScreenshot(t *testing.T) {
	if !liveChromiumAvailable() {
		t.Skip("real sandbox Chromium required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.FileServer(http.Dir("/usr/local/share/yummi-qa")), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	u := "http://" + listener.Addr().String() + "/browser-qa.html"
	headless := true
	if _, err = d.Open(ctx, OpenParams{URL: u, Session: "admin", Headless: &headless}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := d.Snapshot(ctx, true, true, 4, "")
	if err != nil || !strings.Contains(snapshot.TreeText, `"Research query"`) {
		t.Fatalf("wrapped form label missing from accessible snapshot: %v %v", snapshot, err)
	}
	if _, err = d.Eval(ctx, `console.log("admin-private-token-12345"); localStorage.setItem('identity','admin'); document.cookie='role=admin; path=/'; 'saved'`); err != nil {
		t.Fatal(err)
	}
	var adminLogs *LogsResult
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if logs, err := d.Logs(ctx, "console", false); err == nil && logs.TotalEntries > 0 {
			adminLogs = logs
			break
		}
	}
	if adminLogs == nil || adminLogs.TotalEntries == 0 {
		t.Fatalf("admin logs missing")
	}
	if _, err = d.Open(ctx, OpenParams{URL: u, Session: "guest"}); err != nil {
		t.Fatal(err)
	}
	guestLogs, err := d.Logs(ctx, "console", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range guestLogs.Entries {
		if strings.Contains(entry.Text, "admin-private-token-12345") {
			t.Fatalf("cross-session log leak: %v", entry)
		}
	}
	guest, err := d.Eval(ctx, `JSON.stringify({identity:localStorage.getItem('identity'),cookies:document.cookie})`)
	if err != nil || strings.Contains(guest.(string), "admin") {
		t.Fatalf("session leaked: %v %v", guest, err)
	}
	tabs, err := d.Tabs(ctx, "list", 0, "")
	if err != nil || len(tabs.Tabs) != 1 {
		t.Fatalf("cross-session tabs visible: %v %v", tabs, err)
	}
	if _, err = d.Open(ctx, OpenParams{URL: u, Session: "admin"}); err != nil {
		t.Fatal(err)
	}
	admin, err := d.Eval(ctx, `localStorage.getItem('identity')`)
	if err != nil || admin != "admin" {
		t.Fatalf("session did not persist: %v %v", admin, err)
	}
	if _, err = d.Interact(ctx, InteractParams{Action: "set_viewport", Width: 375, Height: 812}); err != nil {
		t.Fatal(err)
	}
	shot, err := d.Screenshot(ctx, false, false, "/home/pentester/work/qa-mobile.png")
	if err != nil || shot.Width != 375 || shot.Height != 812 {
		t.Fatalf("incorrect screenshot dimensions: %v %v", shot, err)
	}
	if _, err = d.Storage(ctx, StorageParams{Action: "load_state", Path: "/etc/passwd"}); err == nil {
		t.Fatal("unconfined state read accepted")
	}
	missing, err := d.Open(ctx, OpenParams{URL: "http://" + listener.Addr().String() + "/missing-file.html", Session: "guest"})
	if err != nil || missing.HTTPStatus != 404 {
		t.Fatalf("HTTP error page misreported: %v %v", missing, err)
	}
	full := false
	if _, err = d.Open(ctx, OpenParams{URL: u, Headless: &full}); err == nil {
		t.Fatal("mode change silently accepted without reset")
	}
	if _, err = d.Session(ctx, "reset"); err != nil {
		t.Fatal(err)
	}
	status, err := d.Session(ctx, "status")
	if err != nil || status["running"] != false {
		t.Fatalf("reset failed: %v %v", status, err)
	}
	postResetLogs, err := d.Logs(ctx, "all", false)
	if err != nil || postResetLogs.TotalEntries != 0 {
		t.Fatalf("logs not cleared on reset: %v %v", postResetLogs, err)
	}
}

func TestBrowserQualityWebSocketHostnameEnforcement(t *testing.T) {
	if !liveChromiumAvailable() {
		t.Skip("real sandbox Chromium required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				key := r.Header.Get("Sec-WebSocket-Key")
				hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				accept := base64.StdEncoding.EncodeToString(hash[:])
				hj, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "hijack failed", 500)
					return
				}
				conn, bufrw, err := hj.Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n")
				bufrw.Flush()
				return
			}
			http.ServeFile(w, r, "/usr/local/share/yummi-qa/browser-qa.html")
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()

	port := strings.Split(listener.Addr().String(), ":")[1]
	u := "http://127.0.0.1:" + port + "/browser-qa.html"
	headless := true
	if _, err := d.Open(ctx, OpenParams{URL: u, Session: "ws-test", Headless: &headless}); err != nil {
		t.Fatal(err)
	}

	// Local loopback WebSocket connection succeeds
	res, err := d.Eval(ctx, `
		new Promise((resolve) => {
			try {
				const ws = new WebSocket("ws://127.0.0.1:`+port+`/ws");
				ws.onopen = () => { ws.close(); resolve("connected"); };
				ws.onerror = (e) => resolve("error");
				setTimeout(() => resolve("timeout"), 2000);
			} catch(e) {
				resolve("threw: " + e.message);
			}
		})
	`)
	if err != nil || res != "connected" {
		t.Fatalf("local loopback websocket connection failed: res=%v err=%v", res, err)
	}

	// External unauthorized destination WebSocket connection is blocked by client/policy
	blockedRes, err := d.Eval(ctx, `
		new Promise((resolve) => {
			try {
				const ws = new WebSocket("ws://blocked.invalid.target:9999/ws");
				ws.onopen = () => { ws.close(); resolve("connected"); };
				ws.onerror = (e) => resolve("blocked_or_failed");
				setTimeout(() => resolve("timeout"), 1500);
			} catch(e) {
				resolve("threw: " + e.message);
			}
		})
	`)
	if err != nil || blockedRes == "connected" {
		t.Fatalf("unauthorized external websocket should be blocked: res=%v err=%v", blockedRes, err)
	}
}
