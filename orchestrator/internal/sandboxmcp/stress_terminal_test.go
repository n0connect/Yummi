package sandboxmcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func stressNonce() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// stress_terminal_test.go pushes the terminal-session layer well past normal
// use, against the REAL running yummi-sandbox container — the same
// liveContainerUp self-skip as live_terminal_check_test.go. Where that file
// proves the feature WORKS, this file tries hard to break it: same-session
// collisions, rapid back-to-back commands with zero pacing, high session
// counts, large output, session churn, and timeout/recovery. Each subtest
// cleans up its own sessions.

func newLiveClient(t *testing.T) *Client {
	t.Helper()
	liveContainerUp(t)
	c := NewClient("yummi-sandbox")
	// Each Client lazily opens its own persistent `docker exec -i
	// yummi-sandbox-mcp` process on first call. Without closing it, running
	// the full live+stress suite in one `go test` invocation accumulates one
	// such process PER TEST FUNCTION, all left running — under real load
	// this reproducibly exhausted something in the docker-exec/container
	// stack badly enough that a LATER test's docker exec never got a clean
	// JSON-RPC stream at all (a stray non-JSON error line broke the very
	// first decode). Every test must clean up its own client.
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func openStress(t *testing.T, c *Client, ctx context.Context, label string) string {
	t.Helper()
	res, err := c.CallTool(ctx, "term_open", map[string]any{"label": label})
	if err != nil || res.IsError {
		t.Fatalf("term_open(%s) failed: err=%v res=%v", label, err, res)
	}
	t.Cleanup(func() { _, _ = c.CallTool(context.Background(), "term_close", map[string]any{"id": label}) })
	return label
}

// --- 1. Same-session concurrent collision -----------------------------
//
// Two goroutines fire DIFFERENT term_run calls at the SAME session id truly
// concurrently. Each term_run is internally atomic (sendLines writes its
// three sentinel lines in one call), but nothing previously stopped two
// separate term_run calls targeting the same session from being in flight
// at once — their writes could still interleave inside the shell. This
// tries to catch that.
func TestLive_Stress_SameSessionConcurrentCollision(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := openStress(t, c, ctx, "stress_collide_"+stressNonce())

	const n = 4
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			marker := fmt.Sprintf("COLLIDE_MARK_%d", i)
			res, err := c.CallTool(ctx, "term_run", map[string]any{
				"id": id, "command": "echo " + marker, "timeout_secs": 15,
			})
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = textOf(res)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: term_run error: %v", i, errs[i])
		}
		want := fmt.Sprintf("COLLIDE_MARK_%d", i)
		if !strings.Contains(results[i], want) {
			t.Errorf("goroutine %d: expected %q in its OWN result, got: %s", i, want, results[i])
		}
		if !strings.Contains(results[i], "exit=0") {
			t.Errorf("goroutine %d: expected exit=0, got: %s", i, results[i])
		}
		// Cross-contamination check: this result must not carry another
		// goroutine's marker glued onto the same line (the corruption
		// pattern found in the original bug).
		for j := 0; j < n; j++ {
			if j == i {
				continue
			}
			other := fmt.Sprintf("COLLIDE_MARK_%d", j)
			if strings.Contains(results[i], other) {
				t.Errorf("goroutine %d's result contains goroutine %d's marker (cross-talk): %s", i, j, results[i])
			}
		}
	}
}

// --- 2. Rapid back-to-back commands on ONE session, zero pacing -------
func TestLive_Stress_RapidSequentialSameSession(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	id := openStress(t, c, ctx, "stress_rapid_"+stressNonce())

	const n = 20
	for i := 0; i < n; i++ {
		res, err := c.CallTool(ctx, "term_run", map[string]any{
			"id": id, "command": fmt.Sprintf("echo RAPID_%d_$((%d*3))", i, i), "timeout_secs": 10,
		})
		if err != nil {
			t.Fatalf("iter %d: term_run error: %v", i, err)
		}
		out := textOf(res)
		want := fmt.Sprintf("RAPID_%d_%d", i, i*3)
		if !strings.Contains(out, want) {
			t.Fatalf("iter %d: expected %q, got: %s", i, want, out)
		}
		if !strings.Contains(out, "exit=0") {
			t.Fatalf("iter %d: expected exit=0, got: %s", i, out)
		}
	}
}

// --- 3. High concurrency scale-up, repeated for statistical confidence -
func TestLive_Stress_HighConcurrencyScaleUp(t *testing.T) {
	c := newLiveClient(t)
	const sessions = 12
	const rounds = 3

	for round := 0; round < rounds; round++ {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			ids := make([]string, sessions)
			var wg sync.WaitGroup
			for i := 0; i < sessions; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					label := fmt.Sprintf("stress_scale_r%d_%d_%s", round, i, stressNonce())
					res, err := c.CallTool(ctx, "term_open", map[string]any{"label": label})
					if err != nil || res.IsError {
						t.Errorf("round %d: term_open[%d] failed: err=%v isError=%v text=%s", round, i, err, res != nil && res.IsError, textOf(res))
						return
					}
					ids[i] = label
				}(i)
			}
			wg.Wait()
			defer func() {
				for _, id := range ids {
					if id != "" {
						_, _ = c.CallTool(context.Background(), "term_close", map[string]any{"id": id})
					}
				}
			}()

			wg = sync.WaitGroup{}
			for i := 0; i < sessions; i++ {
				if ids[i] == "" {
					continue
				}
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					want := fmt.Sprintf("SCALE_R%d_S%d", round, i)
					res, err := c.CallTool(ctx, "term_run", map[string]any{
						"id": ids[i], "command": "echo " + want, "timeout_secs": 20,
					})
					if err != nil {
						t.Errorf("round %d: term_run[%d] error: %v", round, i, err)
						return
					}
					out := textOf(res)
					if !strings.Contains(out, want) || !strings.Contains(out, "exit=0") {
						t.Errorf("round %d session %d: bad result: %s", round, i, out)
					}
				}(i)
			}
			wg.Wait()
		}()
	}
}

// --- 4. Large output correctness ---------------------------------------
func TestLive_Stress_LargeOutput(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := openStress(t, c, ctx, "stress_large_"+stressNonce())

	const lineCount = 3000
	res, err := c.CallTool(ctx, "term_run", map[string]any{
		"id": id, "command": fmt.Sprintf("seq 1 %d", lineCount), "timeout_secs": 20,
	})
	if err != nil {
		t.Fatalf("seq run: %v", err)
	}
	out := textOf(res)
	if !strings.Contains(out, "exit=0") {
		t.Fatalf("expected exit=0: %s", out[:min(200, len(out))])
	}
	if !strings.Contains(out, "\n1\n") && !strings.HasPrefix(strings.TrimSpace(out), "1") {
		t.Errorf("expected first seq line '1' present")
	}
	lastLine := strconv.Itoa(lineCount)
	if !strings.Contains(out, lastLine) {
		t.Errorf("expected last seq line %q present in term_run output", lastLine)
	}

	// term_read tail default (200 lines) must NOT contain the first line
	// (seq 1) since 3000 lines far exceeds the 200-line default tail.
	res, err = c.CallTool(ctx, "term_read", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("term_read tail: %v", err)
	}
	tailOut := textOf(res)
	if strings.Contains(tailOut, "\n1\n") {
		t.Errorf("tail read unexpectedly contains the very first line — default tail window may not be bounding correctly")
	}
	if !strings.Contains(tailOut, lastLine) {
		t.Errorf("tail read should contain the last seq line %q, got tail: %s", lastLine, tailOut[max(0, len(tailOut)-300):])
	}

	// term_read full=true must contain the first line.
	res, err = c.CallTool(ctx, "term_read", map[string]any{"id": id, "full": true})
	if err != nil {
		t.Fatalf("term_read full: %v", err)
	}
	fullOut := textOf(res)
	if !strings.Contains(fullOut, lastLine) {
		t.Errorf("full read missing last line %q", lastLine)
	}
}

// --- 5. Session churn: rapid open/close cycles -------------------------
func TestLive_Stress_SessionChurn(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const iterations = 15
	for i := 0; i < iterations; i++ {
		label := fmt.Sprintf("stress_churn_%d_%s", i, stressNonce())
		res, err := c.CallTool(ctx, "term_open", map[string]any{"label": label})
		if err != nil || res.IsError {
			t.Fatalf("iter %d: open failed: err=%v res=%v", i, err, res)
		}
		want := fmt.Sprintf("CHURN_%d", i)
		res, err = c.CallTool(ctx, "term_run", map[string]any{"id": label, "command": "echo " + want, "timeout_secs": 10})
		if err != nil {
			t.Fatalf("iter %d: run failed: %v", i, err)
		}
		if !strings.Contains(textOf(res), want) {
			t.Fatalf("iter %d: missing marker: %s", i, textOf(res))
		}
		res, err = c.CallTool(ctx, "term_close", map[string]any{"id": label})
		if err != nil || res.IsError {
			t.Fatalf("iter %d: close failed: err=%v res=%v", i, err, res)
		}
		// Using it after close must fail cleanly, not hang or corrupt state.
		res, err = c.CallTool(ctx, "term_run", map[string]any{"id": label, "command": "echo x", "timeout_secs": 5})
		if err != nil {
			t.Fatalf("iter %d: post-close call errored at transport level: %v", i, err)
		}
		if !res.IsError {
			t.Fatalf("iter %d: expected an error result running against a closed session, got success: %s", i, textOf(res))
		}
	}
}

// --- 6. Timeout path + recovery -----------------------------------------
func TestLive_Stress_TimeoutThenRecover(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := openStress(t, c, ctx, "stress_timeout_"+stressNonce())

	res, err := c.CallTool(ctx, "term_run", map[string]any{
		"id": id, "command": "sleep 5 && echo SLOW_DONE", "timeout_secs": 1,
	})
	if err != nil {
		t.Fatalf("timeout run: %v", err)
	}
	out := textOf(res)
	if !strings.Contains(out, "still running") {
		t.Fatalf("expected a 'still running' partial-timeout response, got: %s", out)
	}
	if strings.Contains(out, "exit=") {
		t.Errorf("a timed-out (not yet finished) run should not report an exit code: %s", out)
	}

	// The session must still be usable: waiting for the command's eventual
	// completion, then running a fresh command afterward, must both work.
	res, err = c.CallTool(ctx, "term_wait", map[string]any{"id": id, "pattern": "SLOW_DONE", "timeout_secs": 10})
	if err != nil {
		t.Fatalf("term_wait after timeout: %v", err)
	}
	if !strings.Contains(textOf(res), "matched pattern") {
		t.Fatalf("expected term_wait to eventually observe completion: %s", textOf(res))
	}
	res, err = c.CallTool(ctx, "term_run", map[string]any{"id": id, "command": "echo RECOVERED", "timeout_secs": 10})
	if err != nil {
		t.Fatalf("post-timeout recovery run: %v", err)
	}
	if !strings.Contains(textOf(res), "RECOVERED") || !strings.Contains(textOf(res), "exit=0") {
		t.Fatalf("session did not recover cleanly after a timeout: %s", textOf(res))
	}
}

// --- 7. Concurrent full lifecycles (open->run->wait->keys->read->close) -
func TestLive_Stress_ConcurrentFullLifecycles(t *testing.T) {
	c := newLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const n = 8
	var wg sync.WaitGroup
	fails := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			label := fmt.Sprintf("stress_lifecycle_%d_%s", i, stressNonce())
			res, err := c.CallTool(ctx, "term_open", map[string]any{"label": label})
			if err != nil || res.IsError {
				fails[i] = fmt.Sprintf("open: err=%v res=%v", err, res)
				return
			}
			defer func() { _, _ = c.CallTool(context.Background(), "term_close", map[string]any{"id": label}) }()

			marker := fmt.Sprintf("LIFECYCLE_%d", i)
			if _, err := c.CallTool(ctx, "term_run", map[string]any{
				"id": label, "command": "sleep 1 && echo " + marker, "wait": false,
			}); err != nil {
				fails[i] = fmt.Sprintf("fire-and-return: %v", err)
				return
			}
			res, err = c.CallTool(ctx, "term_wait", map[string]any{"id": label, "pattern": marker, "timeout_secs": 10})
			if err != nil || !strings.Contains(textOf(res), "matched pattern") {
				fails[i] = fmt.Sprintf("term_wait: err=%v out=%s", err, textOf(res))
				return
			}
			if _, err := c.CallTool(ctx, "term_run", map[string]any{"id": label, "command": "sleep 10", "wait": false}); err != nil {
				fails[i] = fmt.Sprintf("start long sleep: %v", err)
				return
			}
			time.Sleep(200 * time.Millisecond)
			if _, err := c.CallTool(ctx, "term_keys", map[string]any{"id": label, "keys": "C-c"}); err != nil {
				fails[i] = fmt.Sprintf("C-c: %v", err)
				return
			}
			res, err = c.CallTool(ctx, "term_run", map[string]any{"id": label, "command": "echo DONE_" + strconv.Itoa(i), "timeout_secs": 10})
			if err != nil || !strings.Contains(textOf(res), "DONE_"+strconv.Itoa(i)) {
				fails[i] = fmt.Sprintf("post-interrupt run: err=%v out=%s", err, textOf(res))
				return
			}
			res, err = c.CallTool(ctx, "term_read", map[string]any{"id": label})
			if err != nil || !strings.Contains(textOf(res), "DONE_"+strconv.Itoa(i)) {
				fails[i] = fmt.Sprintf("term_read: err=%v out=%s", err, textOf(res))
				return
			}
		}(i)
	}
	wg.Wait()
	for i, f := range fails {
		if f != "" {
			t.Errorf("lifecycle %d failed: %s", i, f)
		}
	}
}
