package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnqueueLifecycle(t *testing.T) {
	store := NewStore()
	rec, err := store.Enqueue("s1", "p1", "nmap -sV -Pn 10.10.10.10", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if rec.Status != StatusQueued {
		t.Fatalf("expected queued, got %s", rec.Status)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, ok := store.Get(rec.ID)
		if !ok {
			t.Fatalf("record disappeared: %s", rec.ID)
		}
		if got.Status == StatusCompleted {
			if got.Output == "" {
				t.Fatal("expected output on completed stub execution")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution did not complete in time, last status: %s", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestListBySession(t *testing.T) {
	store := NewStore()
	if _, err := store.Enqueue("s1", "p1", "nmap 10.10.10.10", "manual_terminal"); err != nil {
		t.Fatalf("enqueue s1 #1: %v", err)
	}
	if _, err := store.Enqueue("s2", "p2", "nmap 10.10.10.11", "manual_terminal"); err != nil {
		t.Fatalf("enqueue s2: %v", err)
	}
	if _, err := store.Enqueue("s1", "p3", "nmap 10.10.10.12", "manual_terminal"); err != nil {
		t.Fatalf("enqueue s1 #2: %v", err)
	}

	list := store.ListBySession("s1", 10)
	if len(list) != 2 {
		t.Fatalf("expected 2 records for s1, got %d", len(list))
	}
}

func TestWaitForTerminal(t *testing.T) {
	store := NewStore()
	rec, err := store.Enqueue("s1", "p1", "nmap -sV -Pn 10.10.10.10", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	got, err := store.WaitForTerminal(rec.ID, time.Second)
	if err != nil {
		t.Fatalf("wait for terminal: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %s", got.Status)
	}
}

func TestWaitForTerminalMissing(t *testing.T) {
	store := NewStore()
	if _, err := store.WaitForTerminal("missing-id", 20*time.Millisecond); err == nil {
		t.Fatal("expected missing execution error")
	} else if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("expected ErrExecutionNotFound, got %v", err)
	}
}

func TestCancelExecution(t *testing.T) {
	store := NewStore()
	rec, err := store.Enqueue("s1", "p1", "nmap -sV -Pn 10.10.10.10", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	canceled, err := store.Cancel(rec.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.Status != StatusCanceled {
		t.Fatalf("expected canceled status, got %s", canceled.Status)
	}

	got, err := store.WaitForTerminal(rec.ID, time.Second)
	if err != nil {
		t.Fatalf("wait terminal after cancel: %v", err)
	}
	if got.Status != StatusCanceled {
		t.Fatalf("expected canceled terminal status, got %s", got.Status)
	}
}

func TestCancelRunningExecutionCancelsProcessor(t *testing.T) {
	store := NewStore()
	started := make(chan struct{})
	finished := make(chan error, 1)
	store.SetProcessor(func(ctx context.Context, rec Record) (string, error) {
		close(started)
		<-ctx.Done()
		finished <- ctx.Err()
		return "", ctx.Err()
	})

	rec, err := store.Enqueue("s1", "p1", "nmap -sV -Pn 10.10.10.10", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("processor did not start")
	}

	canceled, err := store.Cancel(rec.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceled.Status != StatusCanceled {
		t.Fatalf("expected canceled status, got %s", canceled.Status)
	}

	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected processor context canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("processor context was not canceled")
	}

	got, err := store.WaitForTerminal(rec.ID, time.Second)
	if err != nil {
		t.Fatalf("wait terminal after cancel: %v", err)
	}
	if got.Status != StatusCanceled {
		t.Fatalf("expected canceled terminal status, got %s", got.Status)
	}
}

func TestCancelCompletedExecutionFails(t *testing.T) {
	store := NewStore()
	rec, err := store.Enqueue("s1", "p1", "nmap -sV -Pn 10.10.10.10", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := store.WaitForTerminal(rec.ID, 2*time.Second); err != nil {
		t.Fatalf("wait for completion: %v", err)
	}
	if _, err := store.Cancel(rec.ID); err == nil {
		t.Fatal("expected cancel on completed execution to fail")
	} else if !errors.Is(err, ErrExecutionTerminal) {
		t.Fatalf("expected ErrExecutionTerminal, got %v", err)
	}
}

func TestSubscribeReceivesLifecycleEvents(t *testing.T) {
	store := NewStore()
	store.processDelay = 20 * time.Millisecond

	subID, events, err := store.Subscribe(16)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer store.Unsubscribe(subID)

	rec, err := store.Enqueue("s1", "p1", "nmap -sV -Pn 10.10.10.10", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	seen := map[string]bool{}
	sawLifecycle := func() bool {
		return seen["execution.queued"] && seen["execution.running"] && seen["execution.completed"]
	}
	for !sawLifecycle() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for events, got: %+v", seen)
		}
		select {
		case evt := <-events:
			if evt.Record.ID != rec.ID {
				continue
			}
			seen[evt.Type] = true
		case <-time.After(20 * time.Millisecond):
		}
	}
}
