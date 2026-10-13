package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Browser-only TCP connection guard. Unlike CDP notification events, CONNECT
// and Upgrade requests can be refused BEFORE opening a destination socket.
// It runs inside the sandbox; kernel egress remains the gateway's authority.
type browserProxy struct {
	address     string
	server      *http.Server
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	closed      bool
	slots       chan struct{}
}

func newBrowserProxy() (*browserProxy, error) {
	if err := requireSandboxRuntime(); err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &browserProxy{address: l.Addr().String(), connections: make(map[net.Conn]struct{}), slots: make(chan struct{}, 128)}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 65536}
	go func() { _ = p.server.Serve(l) }()
	return p, nil
}

func (p *browserProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "browser connection limit", http.StatusServiceUnavailable)
		return
	}
	address := r.Host
	if r.Method != http.MethodConnect {
		if r.URL.Scheme != "http" || r.URL.User != nil {
			http.Error(w, "unsupported proxy request", http.StatusBadRequest)
			return
		}
		address = r.URL.Host
		if r.URL.Port() == "" {
			address = net.JoinHostPort(r.URL.Hostname(), "80")
		}
	}
	host, port, err := net.SplitHostPort(address)
	n, parseErr := strconv.Atoi(port)
	if err != nil || host == "" || parseErr != nil || n < 1 || n > 65535 {
		http.Error(w, "invalid destination", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		if err := researchHostnameAllowed(ctx, host); err != nil {
			http.Error(w, "gateway hostname policy denied or unavailable", http.StatusForbidden)
			return
		}
	}
	var addresses []string
	if strings.EqualFold(host, "localhost") {
		addresses = []string{"127.0.0.1"}
	} else if ip != nil {
		addresses = []string{ip.String()}
	} else {
		ips, err := resolveResearchIPs(ctx, host)
		if err != nil || len(ips) == 0 || len(ips) > 16 {
			http.Error(w, "sandbox DNS unavailable", http.StatusBadGateway)
			return
		}
		for _, resolved := range ips {
			addresses = append(addresses, resolved.String())
		}
	}
	if r.Method != http.MethodConnect && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		outReq := r.Clone(ctx)
		outReq.RequestURI = ""
		outReq.Header.Del("Proxy-Authorization")
		outReq.Header.Del("Proxy-Connection")

		transport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialAddresses(ctx, addresses, port)
			},
			ResponseHeaderTimeout: 30 * time.Second,
		}
		defer transport.CloseIdleConnections()

		resp, err := transport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, "gateway egress blocked or destination unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	upstream, err := dialAddresses(ctx, addresses, port)
	if err != nil || upstream == nil {
		http.Error(w, "gateway egress blocked or destination unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "connection upgrade unavailable", http.StatusInternalServerError)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.connections[client] = struct{}{}
	p.connections[upstream] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.connections, client)
		delete(p.connections, upstream)
		p.mu.Unlock()
	}()
	if r.Method == http.MethodConnect {
		_, err = fmt.Fprint(buffer, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if err != nil || buffer.Flush() != nil {
			return
		}
	} else {
		r.RequestURI = ""
		r.URL.Scheme = ""
		r.URL.Host = ""
		r.Header.Del("Proxy-Authorization")
		r.Header.Del("Proxy-Connection")
		_ = upstream.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if err = r.Write(upstream); err != nil {
			return
		}
		_ = upstream.SetWriteDeadline(time.Time{})
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, buffer); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

func dialAddresses(ctx context.Context, addresses []string, port string) (net.Conn, error) {
	var (
		upstream net.Conn
		err      error
	)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range addresses {
		upstream, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if err == nil {
			return upstream, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("no address available")
}

func (p *browserProxy) Close() error {
	p.mu.Lock()
	p.closed = true
	for connection := range p.connections {
		_ = connection.Close()
	}
	p.mu.Unlock()
	return p.server.Close()
}
