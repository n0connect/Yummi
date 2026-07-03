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

// getJSON issues a GET and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// getStatus issues a GET and reports only whether the server returned 2xx —
// used by SessionExists, where the body is irrelevant.
func (c *Client) getStatus(ctx context.Context, path string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
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

// SessionExists reports whether a session id is still known to the orchestrator
// (true on HTTP 200 from GET /api/v1/sessions/{id}).
func (c *Client) SessionExists(id string) bool {
	status, err := c.getStatus(context.Background(), "/api/v1/sessions/"+id)
	return err == nil && status == http.StatusOK
}

// EnsureSession reuses the session id persisted from a previous TUI launch
// (if the orchestrator still recognizes it) instead of always starting a
// brand-new, empty-history session. Conversation history lives in-memory on
// the conductor keyed by session id (see conductor.Engine.history), so a
// fresh id every launch meant the TUI's chat history never accumulated past
// a couple of turns — unlike the web UI, which reuses one long-lived session
// id across a whole browser tab — and the model's tone drifted noticeably
// between the two clients as a result, even though both hit the identical
// /api/v1/chat endpoint. Reusing the persisted id closes that gap.
func (c *Client) EnsureSession() error {
	if id := loadPersistedSessionID(); id != "" && c.SessionExists(id) {
		c.SessionID = id
		return nil
	}
	if err := c.CreateSession(); err != nil {
		return err
	}
	savePersistedSessionID(c.SessionID) // best-effort; a failed write just costs history continuity, not correctness
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

// ── Gateway (feature parity with the web UI's GATEWAY tab) ───────────────────

// GatewayRule mirrors gateway.RuleView (network, source, expiry) — only the
// fields the TUI's compact summary needs.
type GatewayRule struct {
	Network string `json:"network"`
	Source  string `json:"source"`
}

// GatewayStatus is the TUI-friendly flattening of egress-mode + rule count,
// the two facts an operator most needs at a glance without leaving the console.
type GatewayStatus struct {
	Mode   string // block | allow | ask
	Rules  int
	Status string // connected | unreachable | not_configured
}

// GatewaySnapshot fetches the egress mode and active-rule count in one call.
func (c *Client) GatewaySnapshot() (GatewayStatus, error) {
	var modeOut struct {
		Mode   string `json:"mode"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.getJSON(context.Background(), "/api/v1/gateway/egress-mode", &modeOut); err != nil {
		return GatewayStatus{}, err
	}
	var rulesOut struct {
		Rules  []GatewayRule `json:"rules"`
		Status string        `json:"status"`
	}
	if err := c.getJSON(context.Background(), "/api/v1/gateway/rules", &rulesOut); err != nil {
		return GatewayStatus{}, err
	}
	status := modeOut.Status
	if status == "" {
		status = rulesOut.Status
	}
	return GatewayStatus{Mode: modeOut.Mode, Rules: len(rulesOut.Rules), Status: status}, nil
}

// ── Pending skills (feature parity with the web UI's skills-approval panel) ──

// PendingSkill mirrors skillcurator.PendingSkill — only the fields the TUI
// needs to list and act on a draft.
type PendingSkill struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Name   string `json:"name"`
	TaskID string `json:"task_id"`
}

// PendingSkills lists auto-drafted skills awaiting operator approval.
func (c *Client) PendingSkills() ([]PendingSkill, error) {
	var out []PendingSkill
	if err := c.getJSON(context.Background(), "/api/v1/skills/pending", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApproveSkill promotes a pending draft to a live, loaded skill.
func (c *Client) ApproveSkill(id string) error {
	return c.postJSON(context.Background(), "/api/v1/skills/pending/"+id+"/approve", map[string]string{}, nil)
}

// RejectSkill discards a pending draft.
func (c *Client) RejectSkill(id string) error {
	return c.postJSON(context.Background(), "/api/v1/skills/pending/"+id+"/reject", map[string]string{}, nil)
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
