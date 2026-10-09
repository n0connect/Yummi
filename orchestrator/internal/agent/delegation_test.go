package agent

import (
	"context"
	"testing"
	"time"
)

func TestDelegationCoordinatorBoundsConcurrency(t *testing.T) {
	c := NewDelegationCoordinator(1)
	id1, _, err := c.Begin(context.Background(), "parent", "recon", "first")
	if err != nil {
		t.Fatalf("Begin first: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, _, err := c.Begin(ctx, "parent", "web", "second"); err == nil {
		t.Fatal("second delegation exceeded the global concurrency quota")
	}
	c.Finish(id1, nil)

	id2, _, err := c.Begin(context.Background(), "parent", "web", "second")
	if err != nil {
		t.Fatalf("slot was not released after Finish: %v", err)
	}
	c.Finish(id2, nil)
}

func TestDelegationCoordinatorInterruptsOneRun(t *testing.T) {
	c := NewDelegationCoordinator(2)
	id, childCtx, err := c.Begin(context.Background(), "parent", "web", "inspect")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	c.BindTask(id, "child-task")
	if err := c.Interrupt(id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case <-childCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("interrupt did not cancel child context")
	}
	c.Finish(id, childCtx.Err())

	runs := c.List()
	if len(runs) != 1 || runs[0].TaskID != "child-task" || runs[0].Status != "interrupted" {
		t.Fatalf("unexpected lifecycle record: %+v", runs)
	}
}

func TestNewRunnerAppliesDelegationSafetyDefaults(t *testing.T) {
	store, _ := NewStore(nil)
	sub := NewRunner(store, nil, nil, nil, Config{})
	r := NewRunner(store, nil, nil, nil, Config{SubAgents: map[string]SubAgentRunner{"web": sub}})
	if r.cfg.MaxHandoffsPerStep != 4 || r.cfg.MaxConcurrentSubAgents != 3 || r.cfg.Delegations == nil {
		t.Fatalf("delegation defaults not applied: %+v", r.cfg)
	}
}
