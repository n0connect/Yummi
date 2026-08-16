package agent

import (
	"context"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
)

// TestStreamContract_FullTaskEmitsUIEventSequence is the LLM↔Agent communication
// contract test that guards what the web UI consumes over /ws/events.
//
// It drives a real LLM-scripted task (real llm.Client → fake Ollama HTTP server,
// real Store/Runner/Execer — NOT a mock of any Yummi component) through an
// approval gate to completion, while subscribed to BOTH the task-status and
// agent-activity pub/sub channels — exactly the two streams HandleEventsWS
// forwards to clients. It then asserts the event payloads carry everything a
// client needs to render the agent: the plan, live status transitions, the
// pending-approval fields, and the legible activity timeline.
//
// If the agent loop ever stops emitting one of these, the UI silently loses the
// ability to surface it — which is the class of bug this test exists to catch.
func TestStreamContract_FullTaskEmitsUIEventSequence(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	es := realShellStore(t)
	validator := conductor.NewValidator() // real risk classification → RequiresApproval

	// Subscribe BEFORE creating the task so we observe the planning event too —
	// this mirrors a real client, which opens its WS before any task starts.
	taskSubID, taskCh, err := store.Subscribe(128)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer store.Unsubscribe(taskSubID)
	actSubID, actCh, err := store.SubscribeActivity(128)
	if err != nil {
		t.Fatalf("SubscribeActivity: %v", err)
	}
	defer store.UnsubscribeActivity(actSubID)

	ollama := newFakeOllama(t,
		// 1. planning call
		`{"steps":[{"id":"s1","description":"identify the user"}]}`,
		// 2. step → medium-risk command (forces the approval gate)
		"[RUN_COMMAND]\n{\"command\":\"echo enumerating\",\"step_id\":\"s1\",\"risk_level\":\"medium\",\"findings\":[\"recon started\"]}",
		// 3. finish
		"[FINISH]\n{\"summary\":\"done\",\"findings\":[\"shell confirmed\"]}",
	)

	cfg := fastRetryCfg()
	cfg.ApprovalMode = ModeAskPermission // gate every command on the operator
	runner := NewRunner(store, ollama, validator, NewStoreExecer(es, 5*time.Second), cfg)

	task, err := runner.CreateAndPlan(context.Background(), "stream-session", "recon the host")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := runner.Start(task.ID, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Operator approves the gate exactly as the API's HandleApproveTask does.
	pending := waitForStatus(t, store, task.ID, StatusAwaitingApproval, 5*time.Second)
	if err := runner.Approve(task.ID, pending.PendingProposalID, true, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	waitForStatus(t, store, task.ID, StatusCompleted, 5*time.Second)

	// Let the final events settle into the buffered channels, then drain both.
	time.Sleep(100 * time.Millisecond)
	taskEvents := drainTask(taskCh)
	actEvents := drainActivity(actCh)

	// ── Assert the task.status contract ──────────────────────────────────────
	seenStatus := map[TaskStatus]Task{}
	for _, e := range taskEvents {
		if e.Task.ID == task.ID {
			seenStatus[e.Task.Status] = e.Task
		}
	}
	for _, want := range []TaskStatus{
		StatusPlanning, StatusPlanned, StatusRunning, StatusAwaitingApproval, StatusCompleted,
	} {
		if _, ok := seenStatus[want]; !ok {
			t.Fatalf("task.status stream missing %q; got %v", want, statusKeys(seenStatus))
		}
	}

	// The `planned` event must carry the plan — this is what the UI renders so
	// the operator can see what the LLM decided to do.
	if got := seenStatus[StatusPlanned]; len(got.Plan) != 1 {
		t.Fatalf("planned event must carry the 1-step plan, got %d steps", len(got.Plan))
	}

	// The `awaiting_approval` event must be self-sufficient for the approve
	// action: it carries the proposal id, command, and risk.
	gate := seenStatus[StatusAwaitingApproval]
	if gate.PendingProposalID == "" || gate.PendingCommand != "echo enumerating" || gate.PendingRisk != "medium" {
		t.Fatalf("awaiting_approval event missing pending fields: id=%q cmd=%q risk=%q",
			gate.PendingProposalID, gate.PendingCommand, gate.PendingRisk)
	}

	// The terminal `completed` event must carry the summary + findings.
	if done := seenStatus[StatusCompleted]; done.Summary == "" || len(done.Findings) == 0 {
		t.Fatalf("completed event must carry summary+findings, got summary=%q findings=%v",
			done.Summary, done.Findings)
	}

	// ── Assert the agent.activity timeline contract ──────────────────────────
	seenKind := map[ActivityKind]bool{}
	for _, e := range actEvents {
		seenKind[e.Event.Kind] = true
	}
	for _, want := range []ActivityKind{ActPlan, ActFinish} {
		if !seenKind[want] {
			t.Fatalf("agent.activity stream missing kind %q; got %v", want, kindKeys(seenKind))
		}
	}
}

// drainTask non-blockingly collects all buffered task events.
func drainTask(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

// drainActivity non-blockingly collects all buffered activity envelopes.
func drainActivity(ch <-chan ActivityEnvelope) []ActivityEnvelope {
	var out []ActivityEnvelope
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func statusKeys(m map[TaskStatus]Task) []TaskStatus {
	out := make([]TaskStatus, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func kindKeys(m map[ActivityKind]bool) []ActivityKind {
	out := make([]ActivityKind, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
