package llm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ChatFunc is the minimal chat backend shape the agent loop depends on. Both
// *Client.Chat and another Resilient satisfy it, so backends compose.
type ChatFunc func(ctx context.Context, system, user string) (string, error)

// Resilient wraps a primary chat backend with a circuit breaker, exponential
// EMA latency tracking, and an OPTIONAL fallback backend (a smaller/cheaper
// model — e.g. lite tier qwen3:4b) used while the breaker is open or on an
// immediate primary failure.
//
// It deliberately keeps Yummi's privacy-first, single-host posture: there is no
// multi-cloud routing à la openclaude's smart_router. The only "route" is
// primary → local downgrade tier, so a heavy model that times out or OOMs
// degrades to a lighter LOCAL model instead of collapsing the whole agent loop.
//
// Resilient implements ChatFunc itself (via the Chat method), so it drops in
// anywhere the agent's LLM interface is expected — no change to the loop.
type Resilient struct {
	primary  ChatFunc
	fallback ChatFunc // nil → no downgrade; breaker fails fast + health-tracks only

	threshold int           // consecutive primary failures before opening the breaker
	cooldown  time.Duration // how long the breaker stays open before a half-open probe

	primaryName  string
	fallbackName string

	mu           sync.Mutex
	consecFail   int
	openUntil    time.Time
	emaLatencyMs float64
	samples      int
	lastErr      string
}

// ResilientStatus is a point-in-time snapshot for the health / monitoring
// surface (e.g. the orchestrator system-status endpoint).
type ResilientStatus struct {
	PrimaryModel  string  `json:"primary_model"`
	FallbackModel string  `json:"fallback_model,omitempty"`
	CircuitOpen   bool    `json:"circuit_open"`
	ConsecFails   int     `json:"consecutive_failures"`
	EMALatencyMs  float64 `json:"ema_latency_ms"`
	LastError     string  `json:"last_error,omitempty"`
}

// NewResilient builds a breaker around primary. fallback may be nil (no
// downgrade). threshold ≤ 0 defaults to 3; cooldown ≤ 0 defaults to 60s. The
// name args are for the status surface only.
func NewResilient(primary, fallback ChatFunc, primaryName, fallbackName string, threshold int, cooldown time.Duration) *Resilient {
	if threshold <= 0 {
		threshold = 3
	}
	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	return &Resilient{
		primary:      primary,
		fallback:     fallback,
		threshold:    threshold,
		cooldown:     cooldown,
		primaryName:  primaryName,
		fallbackName: fallbackName,
	}
}

// Chat routes a request through the breaker. While the breaker is open it serves
// from the fallback (if any); otherwise it tries the primary, tracks latency and
// failure streaks, opens the breaker after `threshold` consecutive failures, and
// downgrades the *current* call to the fallback when the primary errors.
//
// Context cancellation is never counted as a circuit failure (the caller, not
// the backend, ended the call) and is never downgraded — it propagates as-is.
func (r *Resilient) Chat(ctx context.Context, system, user string) (string, error) {
	r.mu.Lock()
	open := time.Now().Before(r.openUntil)
	r.mu.Unlock()

	if open {
		if r.fallback != nil {
			return r.fallback(ctx, system, user) // degrade to the lighter local model
		}
		return "", fmt.Errorf("llm circuit open (primary %q unhealthy): %s", r.primaryName, r.snapshotErr())
	}

	start := time.Now()
	out, err := r.primary(ctx, system, user)
	elapsed := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return out, err // caller cancelled — not a backend health signal
		}
		r.mu.Lock()
		r.consecFail++
		r.lastErr = err.Error()
		if r.consecFail >= r.threshold {
			r.openUntil = time.Now().Add(r.cooldown)
		}
		hasFallback := r.fallback != nil
		r.mu.Unlock()
		if hasFallback {
			return r.fallback(ctx, system, user) // immediate downgrade on this call
		}
		return "", err
	}

	r.mu.Lock()
	r.consecFail = 0
	r.openUntil = time.Time{} // a healthy response closes the breaker
	r.updateEMALocked(elapsed)
	r.mu.Unlock()
	return out, nil
}

// updateEMALocked folds a new latency sample into the exponential moving average
// (alpha 0.3, matching smart_router). Caller holds r.mu.
func (r *Resilient) updateEMALocked(d time.Duration) {
	ms := float64(d.Microseconds()) / 1000.0 // sub-ms precision (local calls are fast)
	const alpha = 0.3
	if r.samples == 0 {
		r.emaLatencyMs = ms
	} else {
		r.emaLatencyMs = alpha*ms + (1-alpha)*r.emaLatencyMs
	}
	r.samples++
}

func (r *Resilient) snapshotErr() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// Status returns a snapshot for monitoring.
func (r *Resilient) Status() ResilientStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ResilientStatus{
		PrimaryModel:  r.primaryName,
		FallbackModel: r.fallbackName,
		CircuitOpen:   time.Now().Before(r.openUntil),
		ConsecFails:   r.consecFail,
		EMALatencyMs:  r.emaLatencyMs,
		LastError:     r.lastErr,
	}
}
