// Package health collects periodic system metrics into a ring buffer.
package health

import (
	"context"
	"net/http"
	"runtime"
	"sync"
	"time"
)

const (
	ringSize = 720 // 1 hour at 5 s intervals
	interval = 5 * time.Second
)

// MetricSample is a single point-in-time health snapshot.
type MetricSample struct {
	Timestamp   int64   `json:"ts"` // Unix seconds
	HeapMB      float64 `json:"heap_mb"`
	Goroutines  int     `json:"goroutines"`
	OllamaLatMs int64   `json:"ollama_lat_ms"` // -1 if unavailable
	SandboxUp   bool    `json:"sandbox_up"`
	GatewayUp   bool    `json:"gateway_up"`
	OllamaUp    bool    `json:"ollama_up"`
}

// Pinger checks whether a dependency is reachable. Returning true means healthy.
type Pinger func(ctx context.Context) bool

// Aggregator collects metrics every 5 s and stores them in a fixed-size ring buffer.
// Create with New, then call Start in a goroutine.
type Aggregator struct {
	mu    sync.RWMutex
	buf   [ringSize]MetricSample
	head  int // next write index
	count int // samples stored (capped at ringSize)

	ollamaURL   string
	sandboxPing Pinger
	gatewayPing Pinger
	httpClient  *http.Client
}

// New creates an Aggregator. Pass nil pingers to mark those services as always down.
func New(ollamaURL string, sandboxPing, gatewayPing Pinger) *Aggregator {
	return &Aggregator{
		ollamaURL:   ollamaURL,
		sandboxPing: sandboxPing,
		gatewayPing: gatewayPing,
		httpClient:  &http.Client{Timeout: 2 * time.Second},
	}
}

// Start collects metrics on every tick until ctx is cancelled.
func (a *Aggregator) Start(ctx context.Context) {
	a.collect(ctx) // immediate first sample
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.collect(ctx)
		}
	}
}

// Samples returns the last n samples ordered oldest→newest.
// If n <= 0 or n exceeds available data, all stored samples are returned.
func (a *Aggregator) Samples(n int) []MetricSample {
	a.mu.RLock()
	defer a.mu.RUnlock()

	available := min(a.count, ringSize)
	if available == 0 {
		return nil
	}
	if n <= 0 || n > available {
		n = available
	}

	out := make([]MetricSample, n)
	// head-1 is the most recent; walk back n steps from there
	start := ((a.head-n)%ringSize + ringSize) % ringSize
	for i := 0; i < n; i++ {
		out[i] = a.buf[(start+i)%ringSize]
	}
	return out
}

// Latest returns the single most recent sample, or zero value if empty.
func (a *Aggregator) Latest() MetricSample {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.count == 0 {
		return MetricSample{}
	}
	return a.buf[(a.head-1+ringSize)%ringSize]
}

func (a *Aggregator) collect(ctx context.Context) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	sample := MetricSample{
		Timestamp:  time.Now().Unix(),
		HeapMB:     float64(ms.HeapAlloc) / 1024 / 1024,
		Goroutines: runtime.NumGoroutine(),
	}

	// Ping Ollama
	if a.ollamaURL != "" {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		t0 := time.Now()
		req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, a.ollamaURL+"/api/tags", nil)
		if err == nil {
			if resp, err := a.httpClient.Do(req); err == nil {
				_ = resp.Body.Close()
				sample.OllamaLatMs = time.Since(t0).Milliseconds()
				sample.OllamaUp = true
			} else {
				sample.OllamaLatMs = -1
			}
		}
	} else {
		sample.OllamaLatMs = -1
	}

	// Ping sandbox and gateway with tight timeouts
	if a.sandboxPing != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		sample.SandboxUp = a.sandboxPing(pingCtx)
		cancel()
	}
	if a.gatewayPing != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		sample.GatewayUp = a.gatewayPing(pingCtx)
		cancel()
	}

	a.mu.Lock()
	a.buf[a.head] = sample
	a.head = (a.head + 1) % ringSize
	if a.count < ringSize {
		a.count++
	}
	a.mu.Unlock()
}
