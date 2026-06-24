package agent

import (
	"context"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

func TestRememberPersistsContext(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	ks, _ := knowledge.NewStore(nil)
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"map code"}]}`,
		`[REMEMBER]
{"key":"auth_map","value":"login.go -> session.go -> db.go"}`,
		`[FINISH]
{"summary":"mapped"}`,
	)
	cfg := fastRetryCfg()
	cfg.Knowledge = ks
	r := NewRunner(store, ollama, nil, NewStoreExecer(es, 5*time.Second), cfg)
	task, _ := r.CreateAndPlan(context.Background(), "s1", "map the code")
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("run: %v", err)
	}
	ctxs := ks.ListContexts("s1")
	if len(ctxs) != 1 || ctxs[0].Key != "auth_map" {
		t.Fatalf("context not persisted: %+v", ctxs)
	}
	// A context_saved activity event must have been emitted.
	var sawContext bool
	for _, a := range store.ListActivity("s1", 100) {
		if a.Kind == ActContext {
			sawContext = true
		}
	}
	if !sawContext {
		t.Fatal("context_saved activity not emitted")
	}
}
