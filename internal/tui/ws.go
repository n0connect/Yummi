package tui

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// ── Bubble Tea messages delivered from the /ws/events stream ──────────────────

type execMsg struct {
	Command string
	Status  string
	Output  string
	Error   string
}

type taskMsg struct {
	ID             string
	Status         string
	Summary        string
	Findings       []string
	PendingCommand string
	PendingPropID  string
	Question       string
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
	Command string `json:"command"`
	Status  string `json:"status"`
	Output  string `json:"output"`
	Error   string `json:"error"`
}

type wireTask struct {
	ID                string   `json:"id"`
	Status            string   `json:"status"`
	Summary           string   `json:"summary"`
	Findings          []string `json:"findings"`
	PendingCommand    string   `json:"pending_command"`
	PendingProposalID string   `json:"pending_proposal_id"`
	Question          string   `json:"question"`
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
		return execMsg(e)
	case "task.status":
		var t wireTask
		if json.Unmarshal(env.Data, &t) != nil {
			return nil
		}
		return taskMsg{
			ID: t.ID, Status: t.Status, Summary: t.Summary, Findings: t.Findings,
			PendingCommand: t.PendingCommand, PendingPropID: t.PendingProposalID, Question: t.Question,
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
// Bubble Tea messages. It reconnects with backoff; no Origin header is sent so
// the orchestrator accepts it as a non-browser client.
func newStream(base, sessionID string) chan tea.Msg {
	ch := make(chan tea.Msg, 128)
	go func() {
		wsURL := toWS(base) + "/ws/events"
		if sessionID != "" {
			wsURL += "?session_id=" + url.QueryEscape(sessionID)
		}
		for {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				ch <- wsErrMsg{note: "stream disconnected, retrying…"}
				time.Sleep(1500 * time.Millisecond)
				continue
			}
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
			time.Sleep(1500 * time.Millisecond)
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
