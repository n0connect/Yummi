package knowledge

import (
	"context"
	"database/sql"
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

func TestSaveEmbeddingRoundtripPrecision(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		vec := []float32{0.123456, -0.987654, 3.14159}
		if err := s.SaveEmbedding(context.Background(), "procedural_memory", "row-1", "test-model", vec); err != nil {
			t.Fatalf("SaveEmbedding: %v", err)
		}
		got, ok := s.getEmbedding("procedural_memory", "row-1", "test-model")
		if !ok {
			t.Fatal("expected embedding to be found")
		}
		if len(got) != len(vec) {
			t.Fatalf("expected %d dims, got %d", len(vec), len(got))
		}
		for i := range vec {
			if diff := got[i] - vec[i]; diff > 1e-6 || diff < -1e-6 {
				t.Fatalf("float32 precision not preserved at %d: want %v got %v", i, vec[i], got[i])
			}
		}
	}
}

func TestSaveEmbeddingUpsertsOnConflict(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		if err := s.SaveEmbedding(context.Background(), "procedural_memory", "row-1", "m", []float32{1, 0}); err != nil {
			t.Fatalf("save 1: %v", err)
		}
		if err := s.SaveEmbedding(context.Background(), "procedural_memory", "row-1", "m", []float32{0, 1}); err != nil {
			t.Fatalf("save 2 (should upsert): %v", err)
		}
		got, ok := s.getEmbedding("procedural_memory", "row-1", "m")
		if !ok || got[0] != 0 || got[1] != 1 {
			t.Fatalf("expected upsert to replace vector, got %v ok=%v", got, ok)
		}
	}
}

func TestCosineSimilarityKnownVectors(t *testing.T) {
	if got := cosineSimilarity([]float32{1, 0}, []float32{1, 0}); got < 0.999 || got > 1.001 {
		t.Errorf("identical vectors: expected ~1.0, got %v", got)
	}
	if got := cosineSimilarity([]float32{1, 0}, []float32{0, 1}); got < -0.001 || got > 0.001 {
		t.Errorf("orthogonal vectors: expected ~0.0, got %v", got)
	}
	if got := cosineSimilarity([]float32{1, 0}, []float32{-1, 0}); got < -1.001 || got > -0.999 {
		t.Errorf("opposite vectors: expected ~-1.0, got %v", got)
	}
}

func TestNearestTacticsOrdersBySimilarityNotInsertionOrder(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		ctx := context.Background()

		// Insert three tactics for the same tool, out of similarity order.
		if err := s.RecordTactic(ctx, "nmap -sV 10.0.0.1", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}
		if err := s.RecordTactic(ctx, "nmap -p- 10.0.0.1", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}
		if err := s.RecordTactic(ctx, "nmap --script vuln 10.0.0.1", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}

		tacs := s.ListTactics("nmap", 10)
		if len(tacs) != 3 {
			t.Fatalf("expected 3 tactics, got %d", len(tacs))
		}
		vectors := map[string][]float32{
			"nmap -sV <TARGET>":           {1, 0, 0},
			"nmap -p- <TARGET>":           {0, 1, 0},
			"nmap --script vuln <TARGET>": {0.9, 0.1, 0}, // closest to the query below
		}
		for _, tac := range tacs {
			v, ok := vectors[tac.CommandPattern]
			if !ok {
				t.Fatalf("unexpected command pattern %q", tac.CommandPattern)
			}
			if err := s.SaveEmbedding(ctx, "procedural_memory", tac.ID, "m", v); err != nil {
				t.Fatalf("save embedding: %v", err)
			}
		}

		// Query vector nearest to the "vuln" tactic's embedding.
		got, err := s.NearestTactics(ctx, "m", []float32{1, 0.05, 0}, "nmap", 2)
		if err != nil {
			t.Fatalf("NearestTactics: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 results (limit), got %d", len(got))
		}
		if got[0].CommandPattern != "nmap -sV <TARGET>" && got[0].CommandPattern != "nmap --script vuln <TARGET>" {
			t.Fatalf("expected the two closest vectors first, got top result %q", got[0].CommandPattern)
		}
		// The most-orthogonal one ("-p-", vector {0,1,0}) must not be first.
		if got[0].CommandPattern == "nmap -p- <TARGET>" {
			t.Fatalf("expected the least-similar tactic to be excluded/ranked last, got it first")
		}
	}
}

func TestNearestTacticsEmptyToolMatchesAcrossTools(t *testing.T) {
	// Semantic retrieval's whole value-add is cross-tool generalization: an
	// empty tool argument must NOT filter by tool at all, so a "masscan"
	// tactic can surface for a query embedded near an "nmap" tactic.
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		ctx := context.Background()

		if err := s.RecordTactic(ctx, "nmap -sV 10.0.0.1", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}
		if err := s.RecordTactic(ctx, "masscan -p1-65535 10.0.0.1", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}

		for _, tool := range []string{"nmap", "masscan"} {
			tacs := s.ListTactics(tool, 10)
			if len(tacs) != 1 {
				t.Fatalf("expected 1 tactic for %s, got %d", tool, len(tacs))
			}
			if err := s.SaveEmbedding(ctx, "procedural_memory", tacs[0].ID, "m", []float32{1, 0}); err != nil {
				t.Fatalf("save embedding: %v", err)
			}
		}

		got, err := s.NearestTactics(ctx, "m", []float32{1, 0}, "", 10)
		if err != nil {
			t.Fatalf("NearestTactics: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected both nmap and masscan tactics with tool=\"\" (no filter), got %d", len(got))
		}
	}
}

func TestNearestTacticsEmptyStoreReturnsEmptyNotError(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		got, err := s.NearestTactics(context.Background(), "m", []float32{1, 0}, "nmap", 5)
		if err != nil {
			t.Fatalf("expected no error on empty store, got %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty result, got %d", len(got))
		}
	}
}

// TestEmbeddingsTableAddedToExistingDB simulates a database created before the
// embeddings table existed, verifying NewStore adds it without error (plain
// CREATE TABLE IF NOT EXISTS — no data to migrate, unlike the findings
// provenance columns).
func TestEmbeddingsTableAddedToExistingDB(t *testing.T) {
	db := openTestDB(t)
	// Simulate a pre-embeddings DB: create only the findings table (minimal
	// old schema), then run NewStore, which must succeed and create embeddings.
	if _, err := db.Exec(`CREATE TABLE findings (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
		severity TEXT NOT NULL, title TEXT NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore on pre-embeddings DB: %v", err)
	}
	if err := s.SaveEmbedding(context.Background(), "procedural_memory", "x", "m", []float32{1}); err != nil {
		t.Fatalf("embeddings table not usable after migration: %v", err)
	}
}
