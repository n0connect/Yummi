package agent

import (
	"math"
	"time"
)

// RetryConfig defines exponential backoff parameters for LLM call retries.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	Multiplier  float64
	MaxDelay    time.Duration
}

// defaultRetryConfig is used for all LLM step-call retries.
var defaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   500 * time.Millisecond,
	Multiplier:  2.0,
	MaxDelay:    30 * time.Second,
}

// RetryContext tracks retry state across nodes for a single failure episode.
type RetryContext struct {
	Attempts int    `json:"attempts"`
	Caller   string `json:"caller"` // node that triggered the retry
}

// Delay returns the backoff duration for the given attempt number (1-based).
func (rc RetryConfig) Delay(attempt int) time.Duration {
	if attempt <= 1 {
		return rc.BaseDelay
	}
	d := float64(rc.BaseDelay) * math.Pow(rc.Multiplier, float64(attempt-1))
	if d > float64(rc.MaxDelay) {
		d = float64(rc.MaxDelay)
	}
	return time.Duration(d)
}

// Exhausted reports whether all retry attempts have been consumed.
func (rc *RetryContext) Exhausted(cfg RetryConfig) bool {
	return rc.Attempts >= cfg.MaxAttempts
}
