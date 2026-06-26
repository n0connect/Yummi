package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client talks to the orchestrator REST API. Base is the orchestrator root
// (e.g. http://127.0.0.1:9080); all paths hang off /api/v1.
type Client struct {
	Base      string
	SessionID string
	http      *http.Client
}

// NewClient builds a client with an LLM-friendly timeout.
func NewClient(base string) *Client {
	return &Client{Base: base, http: &http.Client{Timeout: 90 * time.Second}}
}

// ── Wire types (match the real orchestrator contract) ────────────────────────

// Proposal is one suggested command from the conductor.
type Proposal struct {
	ID         string `json:"id"`
	Command    string `json:"command"`
	Rationale  string `json:"rationale"`
	RiskLevel  string `json:"risk_level"`
	TargetTool string `json:"target_tool"`
}

// ChatResult is the flattened, TUI-friendly result of a chat turn.
type ChatResult struct {
	Reply     string
	Intent    string
	Proposals []Proposal
	TaskID    string
	TaskGoal  string
}

// chatEnvelope mirrors the server response: {reply, session_id, data:{...}}.
type chatEnvelope struct {
	Reply     string `json:"reply"`
	SessionID string `json:"session_id"`
	Data      struct {
		Intent    string     `json:"intent"`
		Message   string     `json:"message"`
		Proposals []Proposal `json:"proposals"`
		TaskID    string     `json:"task_id"`
		TaskGoal  string     `json:"task_goal"`
	} `json:"data"`
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, path)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// CreateSession opens a session and stores its id. The server returns the
// Session object whose id field is the session id (NOT "session_id").
func (c *Client) CreateSession() error {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.postJSON(context.Background(), "/api/v1/sessions", map[string]string{"name": "TUI"}, &out); err != nil {
		return err
	}
	if out.ID == "" {
		return fmt.Errorf("session create returned empty id")
	}
	c.SessionID = out.ID
	return nil
}

// Chat sends a message to the conductor and returns the flattened result.
func (c *Client) Chat(msg string) (ChatResult, error) {
	if c.SessionID == "" {
		return ChatResult{}, fmt.Errorf("no active session")
	}
	var env chatEnvelope
	err := c.postJSON(context.Background(), "/api/v1/chat",
		map[string]string{"message": msg, "session_id": c.SessionID}, &env)
	if err != nil {
		return ChatResult{}, err
	}
	reply := env.Reply
	if reply == "" {
		reply = env.Data.Message
	}
	return ChatResult{
		Reply:     reply,
		Intent:    env.Data.Intent,
		Proposals: env.Data.Proposals,
		TaskID:    env.Data.TaskID,
		TaskGoal:  env.Data.TaskGoal,
	}, nil
}

// Approve runs a pending single-command proposal.
func (c *Client) Approve(proposalID string) error {
	return c.postJSON(context.Background(), "/api/v1/chat/approve",
		map[string]string{"session_id": c.SessionID, "proposal_id": proposalID}, nil)
}

// ApproveTask resolves an agent task's approval gate.
func (c *Client) ApproveTask(taskID, proposalID string, approved bool) error {
	reason := ""
	if !approved {
		reason = "operator rejected"
	}
	return c.postJSON(context.Background(), "/api/v1/tasks/"+taskID+"/approve",
		map[string]any{"proposal_id": proposalID, "approved": approved, "reason": reason}, nil)
}

// StopTask cancels a running/looping agent task.
func (c *Client) StopTask(taskID string) error {
	return c.postJSON(context.Background(), "/api/v1/tasks/"+taskID+"/stop", nil, nil)
}

// ResumeTask continues a paused agent task (the runner's Start accepts a paused
// task), letting the operator unblock an ask/stuck/timeout pause from the TUI.
func (c *Client) ResumeTask(taskID string) error {
	return c.postJSON(context.Background(), "/api/v1/tasks/"+taskID+"/start", nil, nil)
}

// ApprovalMode reads the agent's current approval mode (auto | ask_permission).
func (c *Client) ApprovalMode() (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.Base+"/api/v1/agent/mode", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Mode, nil
}

// SetApprovalMode switches the agent between "auto" and "ask_permission".
func (c *Client) SetApprovalMode(mode string) error {
	return c.postJSON(context.Background(), "/api/v1/agent/mode",
		map[string]string{"mode": mode}, nil)
}

// Module is one component health row (subset of the system status payload).
type Module struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Health fetches component status (best-effort; empty slice on any error).
func (c *Client) Health() []Module {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.Base+"/api/v1/system/status", nil)
	if err != nil {
		return nil
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Modules []Module `json:"modules"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	return out.Modules
}
