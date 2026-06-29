// Package gateway implements the client for communicating with gateway over Unix socket.
package gateway

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Client connects to gateway IPC socket.
type Client struct {
	socketPath string
	timeout    time.Duration
	tlsConfig  *tls.Config
}

// NewClient creates a new gateway client.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		timeout:    5 * time.Second,
	}
}

// WithTLS returns a new Client that wraps each connection with the given
// TLS config. The server name must match the certificate used by gateway.
func (c *Client) WithTLS(cfg *tls.Config) *Client {
	copy := *c
	copy.tlsConfig = cfg
	return &copy
}

// CheckScope asks gateway if an IP is in scope.
func (c *Client) CheckScope(ip string) (bool, error) {
	resp, err := c.send(Request{
		Type: "check_scope",
		IP:   ip,
	})
	if err != nil {
		return false, err
	}
	return resp.InScope, nil
}

// GetEgressMode returns the gateway's current egress mode ("block"|"allow"|"ask").
func (c *Client) GetEgressMode() (string, error) {
	resp, err := c.send(Request{Type: "get_egress_mode"})
	if err != nil {
		return "", err
	}
	if resp.Type != "egress_mode" {
		return "", fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return resp.Mode, nil
}

// SetEgressMode sets the gateway's egress mode. mode must be one of
// "block"/"allow"/"ask"; an invalid value is rejected client-side before the
// socket round-trip. Returns the mode the gateway reports it is now in.
func (c *Client) SetEgressMode(mode string) (string, error) {
	if !validEgressMode(mode) {
		return "", fmt.Errorf("invalid egress mode %q: must be one of block, allow, ask", mode)
	}
	resp, err := c.send(Request{Type: "set_egress_mode", Mode: mode})
	if err != nil {
		return "", err
	}
	if resp.Type != "egress_mode" {
		return "", fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return resp.Mode, nil
}

// CheckEgress asks gateway for the egress verdict on an IP, returning one of
// "allow"|"block"|"needs_approval". It sends the same check_scope request as
// CheckScope but reads the richer verdict field. If the gateway is older and
// returns no verdict, it degrades safely by mapping in_scope→"allow"/"block".
func (c *Client) CheckEgress(ip string) (string, error) {
	resp, err := c.send(Request{Type: "check_scope", IP: ip})
	if err != nil {
		return "", err
	}
	if resp.Verdict != "" {
		return resp.Verdict, nil
	}
	// Fail-safe fallback for an older gateway that doesn't send a verdict.
	if resp.InScope {
		return "allow", nil
	}
	return "block", nil
}

func validEgressMode(mode string) bool {
	switch mode {
	case "block", "allow", "ask":
		return true
	default:
		return false
	}
}

// GetStatus retrieves gateway status.
func (c *Client) GetStatus() (*StatusResponse, error) {
	resp, err := c.send(Request{Type: "get_status"})
	if err != nil {
		return nil, err
	}
	return &StatusResponse{
		Level:       resp.Level,
		RulesActive: resp.RulesActive,
		UptimeSecs:  resp.UptimeSecs,
	}, nil
}

// Ping checks if gateway is alive.
func (c *Client) Ping() error {
	resp, err := c.send(Request{Type: "ping"})
	if err != nil {
		return err
	}
	if resp.Type != "pong" {
		return fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return nil
}

// AddTemporaryRule adds a temporary allow rule with TTL. The returned
// kernelWarning is non-empty when the gateway's policy engine accepted the
// rule but its nftables kernel-level unlock failed — the call still succeeds
// (the policy state is consistent), but the caller should surface the
// warning since sandbox traffic to the target is still actually dropped.
func (c *Client) AddTemporaryRule(network string, ttlSecs uint64, description string) (ruleID string, expiresAt int64, kernelWarning string, err error) {
	desc := &description
	resp, err := c.send(Request{
		Type:        "add_temporary_rule",
		Network:     network,
		TTLSecs:     ttlSecs,
		Description: desc,
	})
	if err != nil {
		return "", 0, "", err
	}
	if resp.Type != "rule_added" {
		return "", 0, "", fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return resp.RuleID, resp.ExpiresAt, resp.KernelWarning, nil
}

// RemoveRule removes a rule by ID.
func (c *Client) RemoveRule(ruleID string) error {
	resp, err := c.send(Request{
		Type:   "remove_rule",
		RuleID: ruleID,
	})
	if err != nil {
		return err
	}
	if resp.Type != "rule_removed" {
		return fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return nil
}

// ListRules returns all currently active policy rules.
func (c *Client) ListRules() ([]RuleView, error) {
	resp, err := c.send(Request{Type: "list_rules"})
	if err != nil {
		return nil, err
	}
	return resp.Rules, nil
}

// GetAuditLog returns the most recent `limit` audit rows, oldest first, plus
// whether the tamper-evident hash chain currently verifies.
func (c *Client) GetAuditLog(limit int) ([]AuditRowView, bool, error) {
	resp, err := c.send(Request{Type: "get_audit_log", Limit: limit})
	if err != nil {
		return nil, false, err
	}
	return resp.AuditRows, resp.ChainVerified, nil
}

// AddManualRule adds an operator-issued temporary allow rule, distinct from
// AddTemporaryRule (used by the agent/executor path). reason is mandatory —
// the gateway rejects an empty reason, but we also fail fast client-side to
// avoid an unnecessary round trip.
func (c *Client) AddManualRule(network string, ttlSecs uint64, reason string) (ruleID string, expiresAt int64, kernelWarning string, err error) {
	if strings.TrimSpace(reason) == "" {
		return "", 0, "", fmt.Errorf("reason is required for manual rules")
	}
	resp, err := c.send(Request{
		Type:    "add_manual_rule",
		Network: network,
		TTLSecs: ttlSecs,
		Reason:  reason,
	})
	if err != nil {
		return "", 0, "", err
	}
	if resp.Type != "rule_added" {
		return "", 0, "", fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return resp.RuleID, resp.ExpiresAt, resp.KernelWarning, nil
}

// FingerprintConfig reports the gateway's current fingerprint-masking settings.
type FingerprintConfig struct {
	TTLNormalize     *uint8 `json:"ttl_normalize"`
	MACRandomization bool   `json:"mac_randomization"`
}

// GetFingerprintConfig returns the gateway's current fingerprint-masking configuration.
func (c *Client) GetFingerprintConfig() (*FingerprintConfig, error) {
	resp, err := c.send(Request{Type: "get_fingerprint_config"})
	if err != nil {
		return nil, err
	}
	return &FingerprintConfig{
		TTLNormalize:     resp.TTLNormalize,
		MACRandomization: resp.MACRandomization,
	}, nil
}

func (c *Client) send(req Request) (*Response, error) {
	rawConn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("connect to gateway: %w", err)
	}

	// Optionally upgrade to mTLS.
	var conn = rawConn
	if c.tlsConfig != nil {
		tlsConn := tls.Client(rawConn, c.tlsConfig)
		if err := tlsConn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("set deadline: %w", err)
		}
		if err := tlsConn.Handshake(); err != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("mTLS handshake: %w", err)
		}
		conn = tlsConn
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	// Write length-prefixed JSON
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if err := writeAll(conn, lenBuf); err != nil {
		return nil, fmt.Errorf("write length: %w", err)
	}
	if err := writeAll(conn, data); err != nil {
		return nil, fmt.Errorf("write payload: %w", err)
	}

	// Read response
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, fmt.Errorf("read response length: %w", err)
	}
	respLen := binary.BigEndian.Uint32(lenBuf)
	if respLen > 1048576 {
		return nil, fmt.Errorf("response too large: %d bytes", respLen)
	}

	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(respBuf, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Type == "error" {
		return nil, fmt.Errorf("gateway error: %s", resp.Message)
	}

	return &resp, nil
}

func writeAll(conn net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}
