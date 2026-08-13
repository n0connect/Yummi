package agent

import "testing"

// TestRunnerSatisfiesTaskRunner is a compile-time-checked assertion: if
// *Runner's method set ever drifts from TaskRunner, this file fails to
// compile (not just this test failing) — the earliest possible signal.
func TestRunnerSatisfiesTaskRunner(t *testing.T) {
	var _ TaskRunner = (*Runner)(nil)
}
