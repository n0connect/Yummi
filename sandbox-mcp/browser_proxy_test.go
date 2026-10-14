package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestBrowserProxyStreamsPastConnectionDeadline(t *testing.T) {
	if !liveChromiumAvailable() {
		t.Skip("real sandbox required")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: first\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(16 * time.Second):
			_, _ = fmt.Fprint(w, "data: complete\n")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
		}
	})}
	go func() { _ = s.Serve(l) }()
	defer s.Close()
	p, err := newBrowserProxy()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse("http://" + p.address)
	transport := &http.Transport{Proxy: http.ProxyURL(u)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 22 * time.Second}
	started := time.Now()
	response, err := client.Get("http://" + l.Addr().String() + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: first\n" || time.Since(started) > 2*time.Second {
		t.Fatalf("stream was buffered: %q %v elapsed=%s", first, err, time.Since(started))
	}
	last, err := reader.ReadString('\n')
	if err != nil || last != "data: complete\n" {
		t.Fatalf("stream cancelled by connection timeout: %q %v", last, err)
	}
}

func TestBrowserProxyFailClosedBeforeConnect(t *testing.T) {
	if !liveChromiumAvailable() {
		t.Skip("real sandbox required")
	}
	p, err := newBrowserProxy()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// The networkless test container has no policy endpoint. Both plaintext
	// upgrade and TLS CONNECT must fail closed before any destination dial.
	for _, request := range []string{
		"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n",
		"GET http://example.com/ws HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n",
	} {
		conn, err := net.DialTimeout("tcp", p.address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprint(conn, request)
		line, err := bufio.NewReader(conn).ReadString('\n')
		_ = conn.Close()
		if err != nil || !strings.Contains(line, "403") {
			t.Fatalf("unguarded connection: %q %v", line, err)
		}
	}
}

func TestBrowserRealWebSocketAndLogIsolation(t *testing.T) {
	if !liveChromiumAvailable() {
		t.Skip("real sandbox Chromium required")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<title>Actual WebSocket QA</title><p>Live application</p>")
	})
	mux.Handle("/ws", websocket.Handler(func(c *websocket.Conn) {
		defer c.Close()
		var message string
		if websocket.Message.Receive(c, &message) == nil {
			_ = websocket.Message.Send(c, "echo:"+message)
		}
	}))
	s := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.Serve(l) }()
	defer s.Close()
	d := NewRodDriver()
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headless := true
	u := "http://" + l.Addr().String()
	if _, err = d.Open(ctx, OpenParams{URL: u, Session: "admin", Headless: &headless}); err != nil {
		t.Fatal(err)
	}
	result, err := d.Eval(ctx, fmt.Sprintf(`new Promise((resolve,reject)=>{const w=new WebSocket(%q);w.onopen=()=>w.send('real-qa');w.onmessage=e=>{w.close();resolve(e.data)};w.onerror=()=>reject(new Error('WebSocket failed'));setTimeout(()=>reject(new Error('WS timeout')),5000)})`, "ws://"+l.Addr().String()+"/ws"))
	if err != nil || result != "echo:real-qa" {
		t.Fatalf("real WS upgrade failed: %v %v", result, err)
	}
	_, _ = d.Eval(ctx, `console.log('admin-only-evidence')`)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		logs, _ := d.Logs(ctx, "admin-only-evidence", false)
		if logs.TotalEntries > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	adminLogs, _ := d.Logs(ctx, "admin-only-evidence", false)
	if adminLogs.TotalEntries == 0 {
		t.Fatal("real console event was not captured; isolation test would be vacuous")
	}
	if _, err = d.Open(ctx, OpenParams{URL: u, Session: "guest"}); err != nil {
		t.Fatal(err)
	}
	logs, _ := d.Logs(ctx, "admin-only-evidence", false)
	if logs.TotalEntries != 0 {
		t.Fatal("admin logs exposed to guest")
	}
	_ = d.Close()
	logs, _ = d.Logs(ctx, "", false)
	if logs.TotalEntries != 0 {
		t.Fatal("logs retained after reset")
	}
}
