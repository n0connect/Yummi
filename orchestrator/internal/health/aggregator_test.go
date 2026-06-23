package health

import (
	"context"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

func TestAggregatorBroadcastsOnRuleChange(t *testing.T) {
	a := New("", func(context.Context) bool { return true }, nil)

	subID, events, err := a.SubscribeGateway(4)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer a.UnsubscribeGateway(subID)

	expiresAt := int64(9999)
	a.diffAndPublishRules([]gateway.RuleView{
		{ID: "r1", Action: "allow", Network: "10.0.0.5/32", Source: "manual", ExpiresAt: &expiresAt},
	}, "block")

	select {
	case evt := <-events:
		if evt.Type != "gateway.rules_changed" {
			t.Fatalf("unexpected event type: %s", evt.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for gateway event")
	}
}

func TestAggregatorNoBroadcastWhenRulesUnchanged(t *testing.T) {
	a := New("", func(context.Context) bool { return true }, nil)

	subID, events, err := a.SubscribeGateway(4)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer a.UnsubscribeGateway(subID)

	expiresAt := int64(9999)
	snapshot := []gateway.RuleView{
		{ID: "r1", Action: "allow", Network: "10.0.0.5/32", Source: "manual", ExpiresAt: &expiresAt},
	}
	a.diffAndPublishRules(snapshot, "block")
	<-events // drain the first broadcast

	a.diffAndPublishRules(snapshot, "block") // identical snapshot + mode — no second event

	select {
	case evt := <-events:
		t.Fatalf("unexpected second event for unchanged snapshot: %+v", evt)
	case <-time.After(200 * time.Millisecond):
		// expected: no event
	}
}

func TestAggregatorBroadcastsOnEgressModeChange(t *testing.T) {
	a := New("", func(context.Context) bool { return true }, nil)

	subID, events, err := a.SubscribeGateway(4)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer a.UnsubscribeGateway(subID)

	expiresAt := int64(9999)
	snapshot := []gateway.RuleView{
		{ID: "r1", Action: "allow", Network: "10.0.0.5/32", Source: "manual", ExpiresAt: &expiresAt},
	}
	a.diffAndPublishRules(snapshot, "block")
	<-events // drain the first broadcast

	// Same rules, but mode flipped — should rebroadcast with the new mode.
	a.diffAndPublishRules(snapshot, "allow")

	select {
	case evt := <-events:
		if evt.Mode != "allow" {
			t.Fatalf("expected mode 'allow' in event, got %q", evt.Mode)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for egress-mode-change event")
	}
}
