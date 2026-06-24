package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// APIClient wraps interactions with the local Yummi orchestrator.
type APIClient struct {
	BaseURL    string
	SessionID  string
	HTTPClient *http.Client
}

type SessionCreateReq struct {
	TaskGoal string `json:"task_goal"`
}

type SessionCreateResp struct {
	SessionID string `json:"session_id"`
}

type ChatReq struct {
	Goal string `json:"goal"`
}

type ChatResp struct {
	Reply string `json:"reply"`
}

type NodeStatus struct {
	Status string `json:"status"`
}

func NewAPIClient(baseURL string) *APIClient {
	return &APIClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second, // Long timeout for LLM responses
		},
	}
}

func (c *APIClient) Ping() error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.BaseURL+"/api/v1/sandbox/status", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %d", resp.StatusCode)
	}
	return nil
}

func (c *APIClient) CreateSession() error {
	reqBody := SessionCreateReq{TaskGoal: "TUI Initialization"}
	b, _ := json.Marshal(reqBody)
	
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.BaseURL+"/api/v1/sessions", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	var out SessionCreateResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	c.SessionID = out.SessionID
	return nil
}

func (c *APIClient) SendChat(msg string) (string, error) {
	if c.SessionID == "" {
		return "", fmt.Errorf("no active session")
	}
	
	reqBody := ChatReq{Goal: msg}
	b, _ := json.Marshal(reqBody)
	
	u := fmt.Sprintf("%s/api/v1/sessions/%s/chat", c.BaseURL, c.SessionID)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	var out ChatResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Reply, nil
}
