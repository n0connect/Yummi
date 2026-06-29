package tui

import (
	"encoding/json"
	"math/rand"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// Reconnect backoff bounds for the /ws/events stream. A healthy connection
// resets the curve to baseBackoff; repeated dial failures grow it geometrically
// up to maxBackoff so a downed orchestrator isn't hammered every 1.5s forever.
const (
	baseBackoff = 500 * time.Millisecond
	maxBackoff  = 30 * time.Second
)

// nextBackoff doubles cur, clamped to maxBackoff. Pure + deterministic so the
// escalation curve is unit-tested without sleeping.
func nextBackoff(cur time.Duration) time.Duration {
	n := cur * 2
	if n > maxBackoff {
		return maxBackoff
	}
	return n
}

// jittered adds up to ±20% random jitter so many reconnecting clients don't
// retry in lockstep (thundering herd).
func jittered(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := time.Duration(rand.Int63n(int64(d) / 5)) // up to 20%
	if rand.Intn(2) == 0 {
		return d - delta
	}
	return d + delta
}

// ── Bubble Tea messages delivered from the /ws/events stream ──────────────────

type execMsg struct {
	Command string
	Status  string
	Output  string
	Error   string
	// SanitizedOutput/SanitizedError/Banner are the ANSI-stripped, threat-scan-banner-prefixed
	// copies used when feeding this result back into the conductor — see analyzeOutput in
	// app.go. Never the raw fields: those may carry target-controlled escape sequences.
	SanitizedOutput string
	SanitizedError  string
	Banner          string
	Mode            string
}

// PlanStep mirrors the orchestrator's agent.PlanStep JSON (the fields the TUI
// renders). The TUI is in the root module and cannot import the orchestrator
// module, so it decodes the wire shape locally.
type PlanStep struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type taskMsg struct {
	ID             string
	Status         string
	Goal           string
	Summary        string
	Findings       []string
	Plan           []PlanStep
	StepsUsed      int
	StepBudget     int
	PendingCommand string
	PendingPropID  string
	Question       string
	LastError      string
}

type activityMsg struct {
	Kind    string
	Summary string
	Parent  string
}

type wsErrMsg struct{ note string }

// ── Wire decode shapes (subset of the orchestrator payloads) ─────────────────

type wsEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type wireExec struct {
	Command         string `json:"command"`
	Status          string `json:"status"`
	Output          string `json:"output"`
	Error           string `json:"error"`
	SanitizedOutput string `json:"sanitized_output"`
	SanitizedError  string `json:"sanitized_error"`
	SecurityBanner  string `json:"security_banner"`
	Mode            string `json:"mode"`
}

type wireTask struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	Goal              string     `json:"goal"`
	Summary           string     `json:"summary"`
	Findings          []string   `json:"findings"`
	Plan              []PlanStep `json:"plan"`
	StepsUsed         int        `json:"steps_used"`
	StepBudget        int        `json:"step_budget"`
	PendingCommand    string     `json:"pending_command"`
	PendingProposalID string     `json:"pending_proposal_id"`
	Question          string     `json:"question"`
	LastError         string     `json:"last_error"`
}

type wireActivity struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	Parent  string `json:"parent_task_id"`
}

// decodeFrame turns one raw websocket JSON frame into a Bubble Tea message, or
// nil if the frame is irrelevant/unparseable. Exported-free; tested directly.
func decodeFrame(raw []byte) tea.Msg {
	var env wsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	switch env.Type {
	case "execution.status":
		var e wireExec
		if json.Unmarshal(env.Data, &e) != nil {
			return nil
		}
		return execMsg{
			Command: e.Command, Status: e.Status, Output: e.Output, Error: e.Error,
			SanitizedOutput: e.SanitizedOutput, SanitizedError: e.SanitizedError,
			Banner: e.SecurityBanner, Mode: e.Mode,
		}
	case "task.status":
		var t wireTask
		if json.Unmarshal(env.Data, &t) != nil {
			return nil
		}
		return taskMsg{
			ID: t.ID, Status: t.Status, Goal: t.Goal, Summary: t.Summary, Findings: t.Findings,
			Plan: t.Plan, StepsUsed: t.StepsUsed, StepBudget: t.StepBudget,
			PendingCommand: t.PendingCommand, PendingPropID: t.PendingProposalID,
			Question: t.Question, LastError: t.LastError,
		}
	case "agent.activity":
		var a wireActivity
		if json.Unmarshal(env.Data, &a) != nil {
			return nil
		}
		return activityMsg(a)
	default:
		return nil
	}
}

// newStream opens /ws/events for the session and returns a channel of decoded
// Bubble Tea messages. It reconnects with jittered exponential backoff (reset on
// a healthy connection); no Origin header is sent so the orchestrator accepts it
// as a non-browser client.
func newStream(base, sessionID string) chan tea.Msg {
	ch := make(chan tea.Msg, 128)
	go func() {
		wsURL := toWS(base) + "/ws/events"
		if sessionID != "" {
			wsURL += "?session_id=" + url.QueryEscape(sessionID)
		}
		backoff := baseBackoff
		for {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				ch <- wsErrMsg{note: "stream disconnected, retrying…"}
				time.Sleep(jittered(backoff))
				backoff = nextBackoff(backoff) // escalate only on repeated dial failure
				continue
			}
			backoff = baseBackoff // a successful connection resets the curve
			for {
				_, raw, rerr := conn.ReadMessage()
				if rerr != nil {
					break
				}
				if msg := decodeFrame(raw); msg != nil {
					ch <- msg
				}
			}
			_ = conn.Close()
			time.Sleep(jittered(baseBackoff)) // brief pause before immediate reconnect
		}
	}()
	return ch
}

// waitForEvent yields the next streamed message as a Bubble Tea command.
func waitForEvent(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// toWS converts an http(s) base URL to its ws(s) form.
func toWS(base string) string {
	if strings.HasPrefix(base, "https://") {
		return "wss://" + strings.TrimPrefix(base, "https://")
	}
	return "ws://" + strings.TrimPrefix(base, "http://")
}
