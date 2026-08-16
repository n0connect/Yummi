package skillcurator

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newStores(t *testing.T) []*Store {
	t.Helper()
	memStore, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore(nil): %v", err)
	}
	dbStore, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore(db): %v", err)
	}
	return []*Store{memStore, dbStore}
}

func samplePending(id string) PendingSkill {
	return PendingSkill{
		ID: id, Status: "pending", Origin: "auto",
		TaskID: "t1", SessionID: "s1", Name: "recon-sweep",
		DraftYAML: "name: recon-sweep\n", Trajectory: `{"observations":[]}`,
	}
}

func TestPutAndGetRoundtrip(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		if err := s.Put(ctx, samplePending("p1")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := s.Get(ctx, "p1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !ok {
			t.Fatal("expected row to be found")
		}
		if got.Name != "recon-sweep" || got.Status != "pending" || got.Origin != "auto" {
			t.Fatalf("unexpected row: %+v", got)
		}
	}
}

func TestGetMissingReturnsNotFound(t *testing.T) {
	for _, s := range newStores(t) {
		_, ok, err := s.Get(context.Background(), "nope")
		if err != nil {
			t.Fatalf("expected no error for a missing id, got %v", err)
		}
		if ok {
			t.Fatal("expected ok=false for a missing id")
		}
	}
}

func TestListFiltersByStatus(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_ = s.Put(ctx, samplePending("p1"))
		p2 := samplePending("p2")
		p2.Status = "rejected"
		_ = s.Put(ctx, p2)

		pending, err := s.List(ctx, "pending")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(pending) != 1 || pending[0].ID != "p1" {
			t.Fatalf("expected only p1 pending, got %+v", pending)
		}

		rejected, err := s.List(ctx, "rejected")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(rejected) != 1 || rejected[0].ID != "p2" {
			t.Fatalf("expected only p2 rejected, got %+v", rejected)
		}
	}
}

func TestResolveApprove(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_ = s.Put(ctx, samplePending("p1"))

		got, err := s.Resolve(ctx, "p1", true, "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Status != "approved" {
			t.Fatalf("expected status=approved, got %+v", got)
		}
		if got.ResolvedAt == 0 {
			t.Fatal("expected ResolvedAt to be set")
		}
	}
}

func TestResolveReject(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_ = s.Put(ctx, samplePending("p1"))

		got, err := s.Resolve(ctx, "p1", false, "looked risky")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Status != "rejected" || got.RejectReason != "looked risky" {
			t.Fatalf("unexpected row after reject: %+v", got)
		}
	}
}

func TestResolveMissingIDIsError(t *testing.T) {
	for _, s := range newStores(t) {
		_, err := s.Resolve(context.Background(), "nope", true, "")
		if err == nil {
			t.Fatal("expected an error resolving a nonexistent id")
		}
	}
}

func TestResolveAlreadyResolvedIsError(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_ = s.Put(ctx, samplePending("p1"))
		if _, err := s.Resolve(ctx, "p1", true, ""); err != nil {
			t.Fatalf("first resolve: %v", err)
		}
		if _, err := s.Resolve(ctx, "p1", true, ""); err == nil {
			t.Fatal("expected an error resolving an already-resolved id")
		}
	}
}

// TestResolveConcurrentCallsOnlyOneSucceeds proves Resolve's pending->resolved
// transition is atomic: N concurrent Resolve calls on the same row must
// produce exactly one success, not a duplicate approval (which would let a
// racing REST handler write the skill file / hot-add to the registry twice).
func TestResolveConcurrentCallsOnlyOneSucceeds(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_ = s.Put(ctx, samplePending("p1"))

		const n = 20
		var successes int32
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.Resolve(ctx, "p1", true, ""); err == nil {
					atomic.AddInt32(&successes, 1)
				}
			}()
		}
		wg.Wait()

		if successes != 1 {
			t.Fatalf("expected exactly 1 successful resolve out of %d concurrent calls, got %d", n, successes)
		}
	}
}

func TestActivationClaimSurvivesRestartAsRetryablePending(t *testing.T) {
	db := openTestDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, samplePending("p1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.ClaimActivation(ctx, "p1"); err != nil {
		t.Fatalf("ClaimActivation: %v", err)
	}

	reloaded, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok, err := reloaded.Get(ctx, "p1")
	if err != nil || !ok || got.Status != "pending" || !strings.Contains(got.RejectReason, "interrupted") {
		t.Fatalf("activating row was not recovered safely: got=%+v ok=%v err=%v", got, ok, err)
	}
}

// recvEvent waits for one event on ch, failing the test if none arrives
// within a bounded time — used both to assert an event was published and,
// just as importantly, to catch a regression back to the deadlock described
// below: publish() takes s.mu.RLock(), so any exported method that still
// held s.mu.Lock() (e.g. via a bare `defer s.mu.Unlock()` spanning the whole
// function) across a call to publish would hang forever the moment a
// subscriber existed. A wall-clock timeout turns that hang into a failed
// test instead of a stuck CI job.
func recvEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case evt := <-ch:
		return evt
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a skill event — possible deadlock in Store.publish")
		return Event{}
	}
}

func TestPutPublishesPendingCreatedEvent(t *testing.T) {
	for _, s := range newStores(t) {
		_, ch, err := s.Subscribe(4)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if err := s.Put(context.Background(), samplePending("p1")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		evt := recvEvent(t, ch)
		if evt.Type != "skill.pending" || evt.Skill.ID != "p1" || evt.Skill.Name != "recon-sweep" {
			t.Fatalf("unexpected event: %+v", evt)
		}
	}
}

func TestResolvePublishesApprovedAndRejectedEvents(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_, ch, err := s.Subscribe(4)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		_ = s.Put(ctx, samplePending("p1"))
		recvEvent(t, ch) // the Put's own "skill.pending" event

		if _, err := s.Resolve(ctx, "p1", true, ""); err != nil {
			t.Fatalf("Resolve approve: %v", err)
		}
		if evt := recvEvent(t, ch); evt.Type != "skill.approved" || evt.Skill.ID != "p1" {
			t.Fatalf("unexpected approve event: %+v", evt)
		}

		_ = s.Put(ctx, samplePending("p2"))
		recvEvent(t, ch) // p2's own "skill.pending" event
		if _, err := s.Resolve(ctx, "p2", false, "too risky"); err != nil {
			t.Fatalf("Resolve reject: %v", err)
		}
		if evt := recvEvent(t, ch); evt.Type != "skill.rejected" || evt.Skill.RejectReason != "too risky" {
			t.Fatalf("unexpected reject event: %+v", evt)
		}
	}
}

// TestCompleteActivationPublishesEvent covers both branches: a successful
// activation publishes "skill.approved", and a failed one rolls the row back
// to "skill.pending" (with the failure reason attached) so a subscriber
// re-fetches and sees the draft return to the review queue instead of
// silently vanishing behind the transient "activating" state.
func TestCompleteActivationPublishesEvent(t *testing.T) {
	for _, s := range newStores(t) {
		ctx := context.Background()
		_, ch, err := s.Subscribe(4)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		_ = s.Put(ctx, samplePending("p1"))
		recvEvent(t, ch) // Put's event

		if _, err := s.ClaimActivation(ctx, "p1"); err != nil {
			t.Fatalf("ClaimActivation: %v", err)
		}
		// ClaimActivation deliberately does not publish (see its doc comment) —
		// prove that by completing successfully and observing exactly the
		// CompleteActivation event next, not a stray "activating" one.
		if _, err := s.CompleteActivation(ctx, "p1", nil); err != nil {
			t.Fatalf("CompleteActivation success: %v", err)
		}
		if evt := recvEvent(t, ch); evt.Type != "skill.approved" {
			t.Fatalf("unexpected event after successful activation: %+v", evt)
		}

		_ = s.Put(ctx, samplePending("p2"))
		recvEvent(t, ch)
		if _, err := s.ClaimActivation(ctx, "p2"); err != nil {
			t.Fatalf("ClaimActivation p2: %v", err)
		}
		if _, err := s.CompleteActivation(ctx, "p2", errors.New("disk full")); err != nil {
			t.Fatalf("CompleteActivation failure: %v", err)
		}
		evt := recvEvent(t, ch)
		if evt.Type != "skill.pending" || !strings.Contains(evt.Skill.RejectReason, "disk full") {
			t.Fatalf("unexpected event after failed activation: %+v", evt)
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	for _, s := range newStores(t) {
		id, ch, err := s.Subscribe(4)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		s.Unsubscribe(id)
		if err := s.Put(context.Background(), samplePending("p1")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		select {
		case evt, ok := <-ch:
			if ok {
				t.Fatalf("expected no event after unsubscribe, got %+v", evt)
			}
		case <-time.After(100 * time.Millisecond):
			// no delivery, as expected
		}
	}
}

// TestPublishDropsOnSlowSubscriberWithoutBlocking proves a full subscriber
// channel never blocks the write path (Put here) — publish's non-blocking
// send with a default-drop case is the only thing standing between a slow
// WS client and a stalled skill-drafting pipeline.
func TestPublishDropsOnSlowSubscriberWithoutBlocking(t *testing.T) {
	for _, s := range newStores(t) {
		_, ch, err := s.Subscribe(1) // buffer of 1: the first Put already fills it
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		ctx := context.Background()
		done := make(chan struct{})
		go func() {
			_ = s.Put(ctx, samplePending("p1"))
			_ = s.Put(ctx, samplePending("p2")) // channel is now full; must not block
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Put blocked on a full subscriber channel")
		}
		<-ch // drain the one buffered event so the goroutine's sends are accounted for
	}
}
