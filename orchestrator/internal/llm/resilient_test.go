package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// okServer is a REAL local HTTP server that answers /api/chat like Ollama does,
// echoing a fixed reply. Not a mock of any Yummi component — it stands in for the
// external Ollama service exactly as newFakeOllama does in the agent tests.
func okServer(t *testing.T, reply string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(reply)
		fmt.Fprintf(w, `{"message":{"role":"assistant","content":%s},"done":true}`, b)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "primary-model")
}

// downServer returns a client pointed at a real server that always 500s, plus a
// counter of how many requests reached it — used to assert the breaker stops
// hammering the primary.
func downServer(t *testing.T) (*Client, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "primary-model"), &hits
}

func TestResilientHealthyPassthrough(t *testing.T) {
	primary := okServer(t, "hello from primary")
	r := NewResilient(primary.Chat, nil, "primary-model", "", 3, time.Minute)

	out, err := r.Chat(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("healthy primary should not error: %v", err)
	}
	if out != "hello from primary" {
		t.Fatalf("want primary reply, got %q", out)
	}
	if st := r.Status(); st.CircuitOpen || st.ConsecFails != 0 || st.EMALatencyMs <= 0 {
		t.Fatalf("healthy status wrong: %+v", st)
	}
}

func TestResilientDowngradesToFallbackOnPrimaryFailure(t *testing.T) {
	down, _ := downServer(t)
	fallback := okServer(t, "served by lite tier")
	r := NewResilient(down.Chat, fallback.Chat, "expert-70b", "lite-4b", 3, time.Minute)

	out, err := r.Chat(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("with a fallback, a primary failure must NOT surface an error: %v", err)
	}
	if out != "served by lite tier" {
		t.Fatalf("expected fallback reply, got %q", out)
	}
}

func TestResilientOpensCircuitAndStopsHammeringPrimary(t *testing.T) {
	down, hits := downServer(t)
	fallback := okServer(t, "lite")
	// threshold 3, long cooldown so it stays open for the test.
	r := NewResilient(down.Chat, fallback.Chat, "expert", "lite", 3, time.Hour)

	// Drive 3 failures to trip the breaker; each downgrades to fallback.
	for i := 0; i < 3; i++ {
		if _, err := r.Chat(context.Background(), "s", "u"); err != nil {
			t.Fatalf("call %d should downgrade, not error: %v", i, err)
		}
	}
	if !r.Status().CircuitOpen {
		t.Fatalf("breaker should be open after %d failures", 3)
	}
	hitsAtOpen := atomic.LoadInt64(hits)

	// Further calls while open must skip the primary entirely (served by fallback).
	for i := 0; i < 5; i++ {
		if _, err := r.Chat(context.Background(), "s", "u"); err != nil {
			t.Fatalf("open-circuit call should be served by fallback: %v", err)
		}
	}
	if got := atomic.LoadInt64(hits); got != hitsAtOpen {
		t.Fatalf("primary was hit %d more times while circuit open; breaker not protecting it", got-hitsAtOpen)
	}
}

func TestResilientNoFallbackFailsFastWhenOpen(t *testing.T) {
	down, _ := downServer(t)
	r := NewResilient(down.Chat, nil, "primary", "", 2, time.Hour)

	// Two failures trip the breaker (no fallback → each returns the error).
	for i := 0; i < 2; i++ {
		if _, err := r.Chat(context.Background(), "s", "u"); err == nil {
			t.Fatalf("call %d without fallback should surface the error", i)
		}
	}
	// Now open: should fail fast with a circuit-open error.
	_, err := r.Chat(context.Background(), "s", "u")
	if err == nil {
		t.Fatalf("open circuit with no fallback should fail fast")
	}
	if st := r.Status(); !st.CircuitOpen {
		t.Fatalf("status should report open circuit, got %+v", st)
	}
}

func TestResilientHalfOpenRecovers(t *testing.T) {
	down, _ := downServer(t)
	r := NewResilient(down.Chat, nil, "primary", "", 1, 10*time.Millisecond)

	// One failure opens the breaker (threshold 1).
	if _, err := r.Chat(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected failure")
	}
	if !r.Status().CircuitOpen {
		t.Fatal("breaker should be open")
	}
	// After cooldown, the breaker half-opens and probes the primary again. Swap
	// the primary to a healthy backend to confirm recovery closes the breaker.
	healthy := okServer(t, "recovered")
	r.primary = healthy.Chat
	time.Sleep(15 * time.Millisecond)
	out, err := r.Chat(context.Background(), "s", "u")
	if err != nil || out != "recovered" {
		t.Fatalf("half-open probe should recover via primary, got %q err=%v", out, err)
	}
	if r.Status().CircuitOpen {
		t.Fatal("a healthy probe should close the breaker")
	}
}
