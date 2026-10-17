// Package dockerproxy exposes only non-root execution in one fixed sandbox.
// It never forwards a general Docker API or runs host-side commands.
package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/controlaccess"
)

const (
	maxBody        = 128 << 10
	maxExecs       = 4096
	execLifetime   = 24 * time.Hour
	completedGrace = 2 * time.Minute
	maxInspectBody = 1 << 20
)

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+/`)
var execID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Config struct {
	Socket    string
	Container string
	// Peers must be individual IP addresses or single-address CIDRs.
	Peers []string
}

type issuedExec struct {
	expires   time.Time
	tty       bool
	completed bool
	active    int
}

type Broker struct {
	container string
	peers     controlaccess.Peers
	proxy     httputil.ReverseProxy
	mu        sync.Mutex
	issued    map[string]issuedExec
	pending   int
}

func New(cfg Config) (*Broker, error) {
	if !filepath.IsAbs(cfg.Socket) || strings.IndexByte(cfg.Socket, 0) >= 0 {
		return nil, errors.New("docker socket must be an absolute Unix socket path")
	}
	if !containerName.MatchString(cfg.Container) {
		return nil, errors.New("a valid fixed sandbox container name is required")
	}
	if len(cfg.Peers) == 0 {
		return nil, errors.New("at least one exact control peer is required")
	}
	peers, err := controlaccess.ParsePeers(strings.Join(cfg.Peers, ","))
	if err != nil {
		return nil, err
	}
	b := &Broker{container: cfg.Container, peers: peers, issued: make(map[string]issuedExec)}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", cfg.Socket)
	}, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32}
	b.proxy = httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: "docker"})
			pr.Out.Host = "docker"
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Docker request failed", http.StatusBadGateway)
		},
		FlushInterval: -1,
	}
	return b, nil
}

func denied(w http.ResponseWriter) {
	// Do not drain an unauthorized peer's slow or unbounded request body.
	w.Header().Set("Connection", "close")
	http.Error(w, "Docker operation is not permitted", http.StatusForbidden)
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !b.peers.Allows(r.RemoteAddr) {
		denied(w)
		return
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.ContentLength != 0 || len(r.TransferEncoding) > 0) {
		denied(w)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		denied(w)
		return
	}
	// Never normalize ambiguous paths into an allowed Docker operation.
	path := r.URL.Path
	if r.URL.EscapedPath() != path || strings.Contains(path, "//") || strings.Contains(path, "..") || strings.Contains(path, "\\") {
		denied(w)
		return
	}
	if versionPrefix.MatchString(path) {
		path = versionPrefix.ReplaceAllString(path, "/")
	}
	if ((r.Method == http.MethodGet || r.Method == http.MethodHead) && path == "/_ping" || r.Method == http.MethodGet && path == "/version") && r.URL.RawQuery == "" {
		b.proxy.ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodGet && path == "/containers/"+b.container+"/json" && validQuery(query, map[string]bool{"size": true}) {
		b.proxy.ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodPost && path == "/containers/"+b.container+"/exec" && r.URL.RawQuery == "" {
		data, err := readBody(w, r)
		if err != nil {
			denied(w)
			return
		}
		cfg, err := validateCreate(data)
		if err != nil {
			denied(w)
			return
		}
		if !b.reserve() {
			http.Error(w, "sandbox exec registry is full", http.StatusTooManyRequests)
			return
		}
		defer func() { b.mu.Lock(); b.pending--; b.mu.Unlock() }()
		replaceBody(r, cfg)
		proxy := b.proxy
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode != http.StatusCreated {
				return nil
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
			_ = resp.Body.Close()
			if err != nil || len(data) > 8192 {
				return errors.New("invalid Docker exec response")
			}
			var result struct {
				ID string `json:"Id"`
			}
			if err := json.Unmarshal(data, &result); err != nil || !execID.MatchString(result.ID) {
				return errors.New("invalid Docker exec ID")
			}
			b.mu.Lock()
			b.issued[result.ID] = issuedExec{expires: time.Now().Add(execLifetime), tty: cfg.Tty}
			b.mu.Unlock()
			resp.Body = io.NopCloser(bytes.NewReader(data))
			resp.ContentLength = int64(len(data))
			return nil
		}
		proxy.ServeHTTP(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "exec" || !execID.MatchString(parts[1]) {
		denied(w)
		return
	}
	issued, ok := b.lookup(parts[1])
	if !ok {
		denied(w)
		return
	}
	proxy := b.proxy
	switch {
	case r.Method == http.MethodGet && parts[2] == "json" && r.URL.RawQuery == "":
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode != http.StatusOK {
				return nil
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, maxInspectBody+1))
			_ = resp.Body.Close()
			if err != nil || len(data) > maxInspectBody {
				return errors.New("invalid Docker exec inspection")
			}
			var state struct {
				ID      string
				Running *bool
			}
			if err := json.Unmarshal(data, &state); err != nil || state.ID != parts[1] || state.Running == nil {
				return errors.New("invalid Docker exec inspection state")
			}
			if !*state.Running {
				b.markCompleted(parts[1])
			}
			resp.Body = io.NopCloser(bytes.NewReader(data))
			resp.ContentLength = int64(len(data))
			return nil
		}
	case r.Method == http.MethodPost && parts[2] == "start" && r.URL.RawQuery == "":
		data, err := readBody(w, r)
		if err != nil {
			denied(w)
			return
		}
		var start struct {
			Detach      bool
			Tty         bool
			ConsoleSize *[2]uint `json:",omitempty"`
		}
		if err := strictObject(data, &start, "Detach", "Tty", "ConsoleSize"); err != nil || start.Tty != issued.tty {
			denied(w)
			return
		}
		replaceBody(r, start)
		b.pin(parts[1])
		defer b.unpin(parts[1])
	case r.Method == http.MethodPost && parts[2] == "resize" && validResize(query):
		if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			denied(w)
			return
		}
	default:
		denied(w)
		return
	}
	proxy.ServeHTTP(w, r)
}

func (b *Broker) pruneLocked() {
	now := time.Now()
	for id, item := range b.issued {
		if item.active == 0 && !now.Before(item.expires) {
			delete(b.issued, id)
		}
	}
}
func (b *Broker) reserve() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	if len(b.issued)+b.pending >= maxExecs {
		return false
	}
	b.pending++
	return true
}
func (b *Broker) lookup(id string) (issuedExec, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	value, ok := b.issued[id]
	if ok && !value.completed {
		value.expires = time.Now().Add(execLifetime)
		b.issued[id] = value
	}
	return value, ok
}

func (b *Broker) markCompleted(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item, ok := b.issued[id]; ok && !item.completed {
		item.completed = true
		item.expires = time.Now().Add(completedGrace)
		b.issued[id] = item
	}
}
func (b *Broker) pin(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item, ok := b.issued[id]; ok {
		item.active++
		item.completed = false
		item.expires = time.Now().Add(execLifetime)
		b.issued[id] = item
	}
}
func (b *Broker) unpin(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item, ok := b.issued[id]; ok {
		if item.active > 0 {
			item.active--
		}
		if !item.completed {
			item.expires = time.Now().Add(execLifetime)
		}
		b.issued[id] = item
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	// Bound slow clients while reading JSON, then clear the deadline before a
	// successful exec-start upgrades into a long-lived bidirectional stream.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
	defer func() { _ = r.Body.Close() }()
	if r.ContentLength > maxBody {
		w.Header().Set("Connection", "close")
		return nil, errors.New("request too large")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if len(data) > maxBody {
		return nil, errors.New("request too large")
	}
	return data, err
}
func replaceBody(r *http.Request, value any) {
	data, _ := json.Marshal(value)
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.TransferEncoding = nil
	r.Header.Del("Transfer-Encoding")
	r.Header.Set("Content-Type", "application/json")
}

// Keep the body bound even for direct validation calls.
func strictObject(data []byte, out any, allowed ...string) error {
	if len(data) > maxBody {
		return errors.New("request too large")
	}
	return controlaccess.DecodeObject(data, out, allowed...)
}

type createRequest struct {
	// Debian Docker CLI includes Detach in exec-create, even for attached exec.
	Detach       bool
	AttachStdin  bool
	AttachStdout bool
	AttachStderr bool
	ConsoleSize  *[2]uint `json:",omitempty"`
	DetachKeys   string
	Env          []string
	Cmd          []string
	Tty          bool
	WorkingDir   string
	User         string
	Privileged   bool
}

func validateCreate(data []byte) (createRequest, error) {
	var cfg createRequest
	if err := strictObject(data, &cfg, "Detach", "AttachStdin", "AttachStdout", "AttachStderr", "ConsoleSize", "DetachKeys", "Env", "Cmd", "Tty", "WorkingDir", "User", "Privileged"); err != nil {
		return cfg, err
	}
	if cfg.User != "1000:1000" || cfg.Privileged || cfg.Detach || len(cfg.Cmd) == 0 || cfg.Cmd[0] == "" {
		return cfg, errors.New("only non-root sandbox execution is permitted")
	}
	return cfg, nil
}
func validQuery(q url.Values, booleans map[string]bool) bool {
	for key, values := range q {
		if !booleans[key] || len(values) != 1 || (values[0] != "true" && values[0] != "false" && values[0] != "0" && values[0] != "1") {
			return false
		}
	}
	return true
}
func validResize(q url.Values) bool {
	if len(q) != 2 {
		return false
	}
	for _, key := range []string{"h", "w"} {
		if len(q[key]) != 1 {
			return false
		}
		n, err := strconv.Atoi(q.Get(key))
		if err != nil || n < 1 || n > 10000 {
			return false
		}
	}
	return true
}
