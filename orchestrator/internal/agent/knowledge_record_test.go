package agent

import (
	"context"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

func TestRecordFindingsWrapsFreetext(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	recordTaskFindings(context.Background(), ks, "sess1", "task1",
		[]string{"redis open on 6379"}, nil)

	got := ks.ListFindings("sess1")
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	if got[0].Severity != "info" || got[0].Source != "agent" || got[0].TaskID != "task1" {
		t.Fatalf("freetext wrap wrong: %+v", got[0])
	}
	if got[0].Title == "" {
		t.Fatal("title should carry the freetext")
	}
}

func TestRecordFindingsUsesStructuredDetail(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	detail := []FindingDetail{{Severity: "high", Title: "RCE", Description: "unauth", Evidence: "id=root"}}
	recordTaskFindings(context.Background(), ks, "sess1", "task1", nil, detail)

	got := ks.ListFindings("sess1")
	if len(got) != 1 || got[0].Severity != "high" || got[0].Title != "RCE" {
		t.Fatalf("structured detail not used: %+v", got)
	}
}

func TestRecordFindingsNilStoreIsNoop(t *testing.T) {
	// Nil knowledge store must not panic (degraded mode).
	recordTaskFindings(context.Background(), nil, "s", "t", []string{"x"}, nil)
}
