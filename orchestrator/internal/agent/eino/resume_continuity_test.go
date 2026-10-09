package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// resume_continuity_test.go reproduces the live-observed "brain reset" after a
// budget grant: the resumed run recorded prior evidence into the opening
// message, but the premature-FINISH guard then rebuilt the opening message
// from the goal alone (buildIncompleteGoalsMessage), dropping that evidence.
// The model then re-ran nmap/whatweb from zero.
//
// The fake model finishes immediately on every call — the exact behavior seen
// live right after a resume — and records each request body so the test can
// check what context the model was actually given on every attempt.

type recordingModel struct {
	mu     sync.Mutex
	bodies []string
}

func (m *recordingModel) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.bodies = append(m.bodies, string(b))
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "1", "object": "chat.completion", "model": "test",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant", "content": "", "tool_calls": []map[string]any{{
					"id": "call_f", "type": "function",
					"function": map[string]any{"name": "finish", "arguments": `{"summary":"done"}`},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	})
}

func (m *recordingModel) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

// TestResumedRunKeepsPriorEvidenceThroughEveryAttempt seeds a paused task
// with real prior command evidence in the durable activity log, resumes it
// with a granted budget, and lets the model finish immediately. Every model
// request — including any nudge or re-run the guard triggers — must still
// carry the prior evidence. A request that drops it is the reset the operator
// observed.
func TestResumedRunKeepsPriorEvidenceThroughEveryAttempt(t *testing.T) {
	store := openTestStore(t)
	task, err := store.CreateSubtask("sess-resume", "Fully compromise the target at 198.51.100.10. 1) Enumerate the target and identify the web server. 2) Exploit a vulnerability to gain code execution. 3) Establish a persistent foothold. 4) Escalate privileges to root. 5) Read both user.txt and root.txt.", 66, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}
	// Prior work done before the budget pause, recorded durably the same way
	// the live pipeline does (ActCommand Detail = command, ActCommandDone Detail = result).
	store.RecordActivity(agent.ActivityEvent{SessionID: "sess-resume", TaskID: task.ID, Kind: agent.ActCommand,
		Summary: "initial recon", Detail: "nmap -sV -p 80 198.51.100.10"})
	store.RecordActivity(agent.ActivityEvent{SessionID: "sess-resume", TaskID: task.ID, Kind: agent.ActCommandDone,
		Summary: "result", Detail: "80/tcp open http Apache httpd 2.4.49"})
	store.RecordActivity(agent.ActivityEvent{SessionID: "sess-resume", TaskID: task.ID, Kind: agent.ActCommand,
		Summary: "privesc", Detail: "/tmp/simple_exploit.sh id"})
	store.RecordActivity(agent.ActivityEvent{SessionID: "sess-resume", TaskID: task.ID, Kind: agent.ActCommandDone,
		Summary: "result", Detail: "uid=1(daemon) gid=1(daemon)"})

	// Put the task into the exact state a budget pause leaves it in.
	if err := store.Transition(task.ID, agent.StatusPlanned, nil); err != nil {
		t.Fatalf("to planned: %v", err)
	}
	if err := store.Transition(task.ID, agent.StatusRunning, nil); err != nil {
		t.Fatalf("to running: %v", err)
	}
	if err := store.Transition(task.ID, agent.StatusPaused, func(tk *agent.Task) {
		tk.Question = budgetPauseQuestion(66)
	}); err != nil {
		t.Fatalf("to paused: %v", err)
	}

	model := &recordingModel{}
	srv := httptest.NewServer(http.HandlerFunc(model.handler))
	defer srv.Close()

	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		StepBudget: 66,
	})
	// The operator's answer to the budget pause: grant 10 more steps.
	r.mu.Lock()
	r.resumeMsg[task.ID] = "10"
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tk, _ := store.Get(task.ID)
	if err := r.Run(ctx, &tk); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := model.requests()
	if len(reqs) != 1 {
		t.Fatalf("a resumed run with prior evidence must not be nudged back to square one — got %d model requests", len(reqs))
	}
	if len(reqs) == 0 {
		t.Fatal("the model was never called on the resumed run")
	}
	for i, body := range reqs {
		if !strings.Contains(body, "nmap -sV -p 80 198.51.100.10") {
			t.Fatalf("model request %d lost the prior command evidence — this is the reset the operator saw.\nbody (truncated): %.400s", i+1, body)
		}
		if !strings.Contains(body, "Apache httpd 2.4.49") {
			t.Fatalf("model request %d lost the prior command RESULT — the model cannot build on what it already found.", i+1)
		}
	}
}

// A long multi-task session must not push a task's own evidence out of the
// window. The old path read the session-wide ListActivity (capped at 500) and
// filtered by task afterwards, so a busy session silently lost early evidence.
func TestResumeEvidenceSurvivesBusySessionWindow(t *testing.T) {
	store := openTestStore(t)
	mine, _ := store.CreateSubtask("sess-busy", "our goal", 20, "")
	other, _ := store.CreateSubtask("sess-busy", "another task", 20, "")
	// Noise first, then OUR evidence as the most recent events: the session-wide
	// ListActivity read is oldest-first and capped at 500, so a busy session
	// would cut exactly the newest evidence — the work a resume most needs.
	for range 600 {
		store.RecordActivity(agent.ActivityEvent{SessionID: "sess-busy", TaskID: other.ID, Kind: agent.ActCommand, Detail: "noise"})
		store.RecordActivity(agent.ActivityEvent{SessionID: "sess-busy", TaskID: other.ID, Kind: agent.ActCommandDone, Detail: "noise result"})
	}
	store.RecordActivity(agent.ActivityEvent{SessionID: "sess-busy", TaskID: mine.ID, Kind: agent.ActCommand, Detail: "nmap -sV 198.51.100.10"})
	store.RecordActivity(agent.ActivityEvent{SessionID: "sess-busy", TaskID: mine.ID, Kind: agent.ActCommandDone, Detail: "80/tcp open http"})
	pairs, omitted := resumeEvidence(store, mine.ID)
	if len(pairs) != 1 || pairs[0].Command != "nmap -sV 198.51.100.10" || omitted != 0 {
		t.Fatalf("newest evidence for our task was lost in a busy session: pairs=%+v omitted=%d", pairs, omitted)
	}
}

// Over the digest limit, the head (early topology) and the recent tail are
// kept, and the omitted middle is counted in the rendered digest — never
// silently cut.
func TestResumeEvidenceBoundsKeepHeadAndTailAndCountsOmitted(t *testing.T) {
	store := openTestStore(t)
	task, _ := store.CreateSubtask("sess-bound", "goal", 99, "")
	for i := range 30 {
		store.RecordActivity(agent.ActivityEvent{SessionID: "sess-bound", TaskID: task.ID, Kind: agent.ActCommand, Detail: fmt.Sprintf("cmd-%02d", i)})
		store.RecordActivity(agent.ActivityEvent{SessionID: "sess-bound", TaskID: task.ID, Kind: agent.ActCommandDone, Detail: fmt.Sprintf("res-%02d", i)})
	}
	pairs, omitted := resumeEvidence(store, task.ID)
	if len(pairs) != resumeEvidenceDigestLimit {
		t.Fatalf("want %d kept pairs, got %d", resumeEvidenceDigestLimit, len(pairs))
	}
	if pairs[0].Command != "cmd-00" || pairs[resumeEvidenceHeadPairs-1].Command != "cmd-02" {
		t.Fatalf("head (early topology) not kept: first=%q", pairs[0].Command)
	}
	if last := pairs[len(pairs)-1].Command; last != "cmd-29" {
		t.Fatalf("most recent command not kept: last=%q", last)
	}
	if omitted != 30-resumeEvidenceDigestLimit {
		t.Fatalf("omitted = %d, want %d", omitted, 30-resumeEvidenceDigestLimit)
	}
	if !strings.Contains(renderEvidenceDigest(pairs, omitted), "middle commands omitted for length") {
		t.Fatal("the omission must be stated in the rendered digest")
	}
}
