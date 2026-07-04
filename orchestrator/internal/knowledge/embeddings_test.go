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

// TestNearestTacticsPrefersWorkedOverHigherRawSimilarityFailed proves semantic
// ranking carries the same "a proven approach beats a dead end" preference
// ListTactics/tacticScore already apply to the structured path — otherwise
// switching from StructuredRetrieval to SemanticRetrieval for its cross-tool
// generalization would silently regress ranking quality along another axis: a
// tactic recorded "failed" that merely happens to be textually closer to the
// query could outrank one recorded "worked" that helped less literally.
func TestNearestTacticsPrefersWorkedOverHigherRawSimilarityFailed(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		ctx := context.Background()

		if err := s.RecordTactic(ctx, "curl 10.0.0.1/admin", "failed", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}
		if err := s.RecordTactic(ctx, "gobuster dir -u 10.0.0.1", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}

		tacs := s.ListTactics("", 10)
		if len(tacs) < 2 {
			// ListTactics is tool-scoped; fetch each explicitly instead.
			tacs = append(s.ListTactics("curl", 10), s.ListTactics("gobuster", 10)...)
		}
		var failedID, workedID string
		for _, tac := range tacs {
			switch tac.Outcome {
			case "failed":
				failedID = tac.ID
			case "worked":
				workedID = tac.ID
			}
		}
		if failedID == "" || workedID == "" {
			t.Fatalf("expected both tactics recorded, got %+v", tacs)
		}

		// Query vector is a PERFECT match for the failed tactic (cosine=1.0) and
		// a WORSE match for the worked one (cosine=0.9) — pure cosine ranking
		// would put the failed one first.
		if err := s.SaveEmbedding(ctx, "procedural_memory", failedID, "m", []float32{1, 0}); err != nil {
			t.Fatalf("save embedding: %v", err)
		}
		if err := s.SaveEmbedding(ctx, "procedural_memory", workedID, "m", []float32{0.9, 0.43589}); err != nil {
			t.Fatalf("save embedding: %v", err)
		}

		got, err := s.NearestTactics(ctx, "m", []float32{1, 0}, "", 2)
		if err != nil {
			t.Fatalf("NearestTactics: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 results, got %d", len(got))
		}
		if got[0].Outcome != "worked" {
			t.Fatalf("expected the WORKED tactic to rank first despite lower raw cosine similarity, got top=%q (outcome=%s)",
				got[0].CommandPattern, got[0].Outcome)
		}
	}
}

// TestNearestTacticsExcludesLowSimilarityNoise is a regression test for a
// live-observed cross-session contamination bug: with the embedding model
// running (nomic-embed-text) and semantic retrieval active, an OSINT task
// whose only commands were dig/whois/curl had completely unrelated tactics
// from OTHER concurrently-running sessions (`ps aux`, `cat /etc/passwd`,
// `nmap -sT <TARGET>`) injected into its MEMORY prompt block. Root cause:
// NearestTactics (called with tool="" for semantic retrieval's cross-tool
// generalization) always returns the top `limit` candidates by cosine
// similarity, with NO floor — so once there aren't enough genuinely relevant
// matches, it happily pads the result with whatever is left, however
// unrelated. A short query like a bare tool name ("dig") embeds with little
// semantic signal, so "closest available" can still be nearly orthogonal.
// NearestTactics must drop candidates below a minimum similarity instead of
// force-filling the limit with noise.
func TestNearestTacticsExcludesLowSimilarityNoise(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		ctx := context.Background()

		if err := s.RecordTactic(ctx, "dig example.com +nocmd", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}
		if err := s.RecordTactic(ctx, "cat /etc/passwd && cat /etc/group && ls -la /home", "worked", nil, ""); err != nil {
			t.Fatalf("record: %v", err)
		}

		tacs := append(s.ListTactics("dig", 10), s.ListTactics("cat", 10)...)
		var digID, catID string
		for _, tac := range tacs {
			switch tac.Tool {
			case "dig":
				digID = tac.ID
			case "cat":
				catID = tac.ID
			}
		}
		if digID == "" || catID == "" {
			t.Fatalf("expected both tactics recorded, got %+v", tacs)
		}

		// Query vector matches the "dig" tactic closely; the "cat" tactic is
		// nearly orthogonal (unrelated) — cosine ~0.1.
		if err := s.SaveEmbedding(ctx, "procedural_memory", digID, "m", []float32{1, 0}); err != nil {
			t.Fatalf("save embedding: %v", err)
		}
		if err := s.SaveEmbedding(ctx, "procedural_memory", catID, "m", []float32{0.1, 0.995}); err != nil {
			t.Fatalf("save embedding: %v", err)
		}

		// Ask for 2 results (limit=2) — without a similarity floor, both would
		// come back even though "cat" is essentially unrelated noise.
		got, err := s.NearestTactics(ctx, "m", []float32{1, 0}, "", 2)
		if err != nil {
			t.Fatalf("NearestTactics: %v", err)
		}
		for _, tac := range got {
			if tac.Tool == "cat" {
				t.Fatalf("expected the near-orthogonal 'cat' tactic to be excluded as noise, got it in results: %+v", got)
			}
		}
		if len(got) != 1 || got[0].Tool != "dig" {
			t.Fatalf("expected exactly the relevant 'dig' tactic, got %+v", got)
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
