package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func auditPage(html string) string {
	return "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(html))
}
func TestAuditWaitMsFreshSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	globalBrowser.mu.Lock()
	old := globalBrowser.driver
	globalBrowser.driver = d
	globalBrowser.mu.Unlock()
	defer func() { globalBrowser.mu.Lock(); globalBrowser.driver = old; globalBrowser.mu.Unlock() }()
	res, _, err := browserOpenHandler(ctx, nil, browserOpenArgs{URL: auditPage(`<html><body><button>Loading</button><script>addEventListener('load',()=>setTimeout(()=>{document.querySelector('button').textContent='SPA Ready'},500))</script></body></html>`), WaitMs: 1200})
	if err != nil || res.IsError {
		t.Fatalf("open: %v %+v", err, res)
	}
	var out string
	for _, c := range res.Content {
		if v, ok := c.(*mcp.TextContent); ok {
			out += v.Text
		}
	}
	if !strings.Contains(out, "SPA Ready") {
		t.Fatalf("wait_ms returned pre-wait snapshot: %s", out)
	}
}
func TestAuditAriaLiveDivSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	info, err := d.Open(ctx, OpenParams{URL: auditPage(`<html><body><div aria-live="polite">Live result visible</div><button>Submit</button></body></html>`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.InitialSnapshot.TreeText, "Live result visible") {
		t.Fatalf("plain aria-live DIV omitted: %s", info.InitialSnapshot.TreeText)
	}
}
func TestAuditClickCurrentVisibleStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	_, err := d.Open(ctx, OpenParams{URL: auditPage(`<html><body><p role="status" style="display:none">Hidden stale status</p><button id="submit" onclick="document.getElementById('result').textContent='Submitted result'">Submit</button><p role="status" id="result"></p></body></html>`)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.Interact(ctx, InteractParams{Action: "click", Selector: "#submit"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "Submitted result") || strings.Contains(result.Message, "Hidden stale status") {
		t.Fatalf("incorrect status observation: %s", result.Message)
	}
}
func TestAuditNetworkIdleWaitsForFetch(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		w.Write([]byte("Fetched Ready"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><button>Loading fetch</button><script>fetch('/slow').then(r=>r.text()).then(s=>document.querySelector('button').textContent=s)</script></body></html>`))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go server.Serve(l)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	info, err := d.Open(ctx, OpenParams{URL: "http://" + l.Addr().String() + "/", WaitUntil: "networkidle"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.InitialSnapshot.TreeText, "Fetched Ready") {
		t.Fatalf("networkidle returned while fetch pending: %s", info.InitialSnapshot.TreeText)
	}
}

// Cancellation during the requested settling period must not return success.
func TestBrowserOpenWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	if _, err := d.Open(ctx, OpenParams{URL: auditPage(`<button>Ready</button>`)}); err != nil {
		t.Fatal(err)
	}
	globalBrowser.mu.Lock()
	old := globalBrowser.driver
	globalBrowser.driver = d
	globalBrowser.mu.Unlock()
	defer func() { globalBrowser.mu.Lock(); globalBrowser.driver = old; globalBrowser.mu.Unlock() }()
	waitCtx, waitCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer waitCancel()
	res, _, err := browserOpenHandler(waitCtx, nil, browserOpenArgs{URL: auditPage(`<button>Loading</button>`), WaitMs: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("canceled wait returned success: %+v", res)
	}
}

func TestBrowserClickObservesAsyncChangedStatusAndBoundsText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	html := `<html><body>` + strings.Repeat(`<p role="status">Earlier result</p>`, 6) + `<div aria-hidden="true"><p role="status">Invisible stale result</p></div><button id="submit" onclick="setTimeout(()=>{document.getElementById('result').textContent='Current result '+ 'x'.repeat(2000)+'Tail sentinel'},100)">Submit</button><div aria-live="polite" id="result"></div></body></html>`
	if _, err := d.Open(ctx, OpenParams{URL: auditPage(html)}); err != nil {
		t.Fatal(err)
	}
	result, err := d.Interact(ctx, InteractParams{Action: "click", Selector: "#submit"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "Current result") || strings.Contains(result.Message, "Earlier result") || strings.Contains(result.Message, "Invisible stale") || strings.Contains(result.Message, "Tail sentinel") || len(result.Message) > 1500 {
		t.Fatalf("incorrect or unbounded current status: %s", result.Message)
	}
}

func TestBrowserNetworkIdleCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/pending", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><button>Pending</button><script>fetch('/pending')</script></body></html>`))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := NewRodDriver()
	defer d.Close()
	if _, err := d.Open(ctx, OpenParams{URL: auditPage(`<button>Ready</button>`)}); err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer waitCancel()
	_, err = d.Open(waitCtx, OpenParams{URL: "http://" + listener.Addr().String() + "/", WaitUntil: "networkidle"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("network wait must return context deadline: %v", err)
	}
}
