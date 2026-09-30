package api

import (
	"testing"
	"time"
)

func TestEventHubReplaysOrderedSessionStateAndSkipsCosmeticEvents(t *testing.T) {
	h := newEventHubCore("epoch-1", 8)
	h.publish("task.status", "task.running", 1, "s1", "", map[string]string{"id": "a"}, true)
	h.publish("chat.stream", "chat.delta", 2, "s1", "t1", map[string]string{"delta": "x"}, false)
	h.publish("task.status", "task.running", 3, "s2", "", map[string]string{"id": "b"}, true)
	h.publish("agent.activity", "", 4, "s1", "", map[string]string{"id": "c"}, true)

	hello, replay, id, _ := h.subscribe("s1", "epoch-1", 0, 4)
	defer h.unsubscribe(id)
	if hello.ResyncRequired {
		t.Fatal("unexpected resync requirement")
	}
	if hello.LatestSequence != 4 {
		t.Fatalf("latest sequence = %d, want 4", hello.LatestSequence)
	}
	if len(replay) != 2 || replay[0].Sequence != 1 || replay[1].Sequence != 4 {
		t.Fatalf("unexpected replay: %+v", replay)
	}
}

func TestEventHubRequiresSnapshotWhenEpochOrReplayWindowIsLost(t *testing.T) {
	h := newEventHubCore("epoch-2", 2)
	h.publish("task.status", "task.running", 1, "s1", "", nil, true)
	h.publish("task.status", "task.running", 2, "s1", "", nil, true)
	h.publish("task.status", "task.running", 3, "s1", "", nil, true)

	hello, replay, id, _ := h.subscribe("s1", "epoch-2", 0, 4)
	h.unsubscribe(id)
	if !hello.ResyncRequired || len(replay) != 0 {
		t.Fatalf("lost replay window must require snapshot: hello=%+v replay=%+v", hello, replay)
	}

	hello, _, id, _ = h.subscribe("s1", "old-epoch", 3, 4)
	h.unsubscribe(id)
	if !hello.ResyncRequired {
		t.Fatal("epoch mismatch must require snapshot")
	}
}

func TestEventHubRequiresSnapshotWhenOtherSessionsEvictCursor(t *testing.T) {
	h := newEventHubCore("epoch-cross-session", 2)
	h.publish("task.status", "task.running", 1, "quiet-session", "", nil, true)
	h.publish("task.status", "task.running", 2, "busy-session", "", nil, true)
	h.publish("task.status", "task.paused", 3, "busy-session", "", nil, true)

	hello, replay, id, _ := h.subscribe("quiet-session", "epoch-cross-session", 0, 4)
	h.unsubscribe(id)
	if !hello.ResyncRequired || len(replay) != 0 {
		t.Fatalf("global eviction must require snapshot: hello=%+v replay=%+v", hello, replay)
	}
}

func TestEventHubDisconnectsSlowSubscriberInsteadOfSilentlyDroppingState(t *testing.T) {
	h := newEventHubCore("epoch-3", 8)
	_, _, id, live := h.subscribe("s1", "", 0, 1)
	defer h.unsubscribe(id)

	h.publish("task.status", "task.running", 1, "s1", "", nil, true)
	h.publish("task.status", "task.paused", 2, "s1", "", nil, true)
	<-live // buffered first event
	select {
	case _, ok := <-live:
		if ok {
			t.Fatal("overflowed subscriber remained open; the missing event would be silent")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("overflowed subscriber remained open; the missing event was silently dropped")
	}
}
