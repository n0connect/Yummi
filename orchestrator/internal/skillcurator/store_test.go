package skillcurator

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"

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
