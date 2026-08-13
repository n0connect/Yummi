package skillcurator

import (
	"context"
	"testing"
	"time"
)

func TestWorkerIsBoundedAndShutdownAware(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	chat := func(ctx context.Context, _, _ string) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return `{"name":"queued-skill","triggers":["queued"],"knowledge":"queued workflow"}`, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	store, _ := NewStore(nil)
	worker := NewWorker(NewCurator(store, chat, nil), 1, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	if !worker.Submit(sampleTrajectory()) {
		t.Fatal("first submission unexpectedly rejected")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin queued trajectory")
	}
	if !worker.Submit(sampleTrajectory()) {
		t.Fatal("queue capacity should accept one waiting trajectory")
	}
	if worker.Submit(sampleTrajectory()) {
		t.Fatal("bounded queue accepted work beyond capacity")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := worker.Wait(waitCtx); err != nil {
		t.Fatalf("worker did not stop with parent context: %v", err)
	}
	close(release)
}
