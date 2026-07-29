package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordAndListActivity(t *testing.T) {
	s, _ := NewStore(openTestDB(t))
	s.RecordActivity(ActivityEvent{SessionID: "s1", TaskID: "t1", Kind: ActSkill, Summary: "web"})
	s.RecordActivity(ActivityEvent{SessionID: "s1", TaskID: "t1", Kind: ActMemory, Summary: "nmap recalled"})
	got := s.ListActivity("s1", 10)
	if len(got) != 2 {
		t.Fatalf("want 2 activity rows, got %d", len(got))
	}
	if got[0].ID == "" || got[0].Timestamp == 0 {
		t.Fatalf("id/timestamp not filled: %+v", got[0])
	}
}

func TestActivityPubSub(t *testing.T) {
	s, _ := NewStore(nil)
	id, ch, err := s.SubscribeActivity(8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer s.UnsubscribeActivity(id)
	s.RecordActivity(ActivityEvent{SessionID: "s1", TaskID: "t1", Kind: ActFinding, Summary: "redis"})
	select {
	case env := <-ch:
		if env.Event.Kind != ActFinding {
			t.Fatalf("wrong event: %+v", env.Event)
		}
	default:
		t.Fatal("no activity published")
	}
}

func TestActivityDetailNeverSerializesToClients(t *testing.T) {
	ev := ActivityEvent{
		ID: "a1", SessionID: "s1", TaskID: "t1", Kind: ActCommandDone,
		Summary: "Command result processed", Detail: "private rationale and raw output",
	}
	buf, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(buf), "private rationale") || strings.Contains(string(buf), "detail") {
		t.Fatalf("internal activity detail leaked into JSON: %s", buf)
	}
}
