package agent

import (
	"testing"
	"time"
)

func TestGateApprove(t *testing.T) {
	g := NewGate()
	ch := g.Request("task-1", "p1", "nmap -sV 192.168.1.5", "medium")

	go func() {
		time.Sleep(10 * time.Millisecond)
		if err := g.Resolve("task-1", "p1", true, ""); err != nil {
			t.Errorf("resolve: %v", err)
		}
	}()

	select {
	case v := <-ch:
		if !v.Approved {
			t.Fatal("expected approved verdict")
		}
	case <-time.After(time.Second):
		t.Fatal("verdict timeout")
	}
}

func TestGateDenyWithReason(t *testing.T) {
	g := NewGate()
	ch := g.Request("task-1", "p1", "cmd", "high")
	_ = g.Resolve("task-1", "p1", false, "too noisy")
	v := <-ch
	if v.Approved || v.Reason != "too noisy" {
		t.Fatalf("unexpected verdict: %+v", v)
	}
}

func TestGateResolveUnknown(t *testing.T) {
	g := NewGate()
	if err := g.Resolve("nope", "p1", true, ""); err == nil {
		t.Fatal("expected error for unknown pending approval")
	}
}

func TestGatePendingLookup(t *testing.T) {
	g := NewGate()
	_ = g.Request("task-1", "p1", "cmd", "medium")
	p, ok := g.Pending("task-1")
	if !ok || p.ProposalID != "p1" || p.Command != "cmd" {
		t.Fatalf("unexpected pending: %+v ok=%v", p, ok)
	}
	_ = g.Resolve("task-1", "p1", true, "")
	if _, ok := g.Pending("task-1"); ok {
		t.Fatal("pending should be cleared after resolve")
	}
}
