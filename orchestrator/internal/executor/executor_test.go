package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

func TestNewExecutor(t *testing.T) {
	e := New(nil, nil)
	if e == nil {
		t.Fatal("expected non-nil executor")
	}
	fn := e.ProcessFunc()
	if fn == nil {
		t.Fatal("expected non-nil process func")
	}
}

func TestHostNetwork(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"1.2.3.4", "1.2.3.4/32"},
		{"10.0.0.0/24", "10.0.0.0/24"},
		{"2001:db8::1", "2001:db8::1/128"},
		{"example.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := hostNetwork(tt.target); got != tt.want {
			t.Errorf("hostNetwork(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}
}

func TestProcessBlocksTargetedCommandWhenGatewayUnavailable(t *testing.T) {
	e := New(sandbox.NewBridge("unused-container"), nil)
	_, err := e.ProcessFunc()(context.Background(), execution.Record{
		ID:        "exec-1",
		SessionID: "s1",
		Command:   "nmap -sV 10.0.0.1",
	})
	if err == nil {
		t.Fatal("expected gateway unavailable error")
	}
	if !strings.Contains(err.Error(), "gateway scope check unavailable") {
		t.Fatalf("expected gateway unavailable error, got %v", err)
	}
}
