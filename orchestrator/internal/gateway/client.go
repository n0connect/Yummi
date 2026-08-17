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

// CheckHostname asks gateway for the name-level verdict on a hostname against
// the configured hostname_allow/hostname_block patterns, returning one of
// "allow"|"block"|"neutral". An older gateway that doesn't implement it returns
// no verdict; we degrade to "neutral" (no name restriction) so the IP-egress
// flow — which is fail-closed on its own — remains the authority.
func (c *Client) CheckHostname(host string) (string, error) {
	resp, err := c.send(Request{Type: "check_hostname", Host: host})
	if err != nil {
		return "", err
	}
	switch resp.Verdict {
	case "allow", "block", "neutral":
		return resp.Verdict, nil
	default:
		return "neutral", nil
	}
}

// HostnamePolicy is the configured hostname allow/block pattern lists.
type HostnamePolicy struct {
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

// GetHostnamePolicy returns the gateway's configured hostname allow/block
// patterns, for operator visibility.
func (c *Client) GetHostnamePolicy() (*HostnamePolicy, error) {
	resp, err := c.send(Request{Type: "get_hostname_policy"})
	if err != nil {
		return nil, err
	}
	return &HostnamePolicy{Allow: resp.Allow, Block: resp.Block}, nil
}

// AddHostnameRule adds a pattern to the "allow" or "block" hostname list at
// runtime (in-memory; resets on restart). Returns the updated policy.
func (c *Client) AddHostnameRule(list, pattern string) (*HostnamePolicy, error) {
	resp, err := c.send(Request{Type: "add_hostname_rule", List: list, Pattern: pattern})
	if err != nil {
		return nil, err
	}
	if resp.Type == "error" {
		return nil, fmt.Errorf("gateway: %s", resp.Message)
	}
	return &HostnamePolicy{Allow: resp.Allow, Block: resp.Block}, nil
}

// RemoveHostnameRule removes a pattern from the "allow" or "block" hostname
// list at runtime. Returns the updated policy.
func (c *Client) RemoveHostnameRule(list, pattern string) (*HostnamePolicy, error) {
	resp, err := c.send(Request{Type: "remove_hostname_rule", List: list, Pattern: pattern})
	if err != nil {
		return nil, err
	}
	if resp.Type == "error" {
		return nil, fmt.Errorf("gateway: %s", resp.Message)
	}
	return &HostnamePolicy{Allow: resp.Allow, Block: resp.Block}, nil
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

// RemoveRule removes a rule by ID. The returned kernelWarning is non-empty
// when the gateway's policy engine removed the rule but its nftables
// kernel-level revoke failed — the call still succeeds (the policy state is
// consistent), but the caller should surface the warning since sandbox may
// still be able to reach the target until its original TTL expires.
func (c *Client) RemoveRule(ruleID string) (kernelWarning string, err error) {
	resp, err := c.send(Request{
		Type:   "remove_rule",
		RuleID: ruleID,
	})
	if err != nil {
		return "", err
	}
	if resp.Type != "rule_removed" {
		return "", fmt.Errorf("unexpected response: %s", resp.Type)
	}
	return resp.KernelWarning, nil
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

// idempotentRequestTypes are gateway IPC calls that only read state — safe to
// retry after ANY transient transport failure (dial, write, or read), since a
// duplicate read has no side effect. Every other request type mutates
// gateway's policy/rule/mode state and is retried far more conservatively —
// see send()'s dispatched check below.
var idempotentRequestTypes = map[string]bool{
	"check_scope":            true,
	"check_hostname":         true,
	"get_status":             true,
	"ping":                   true,
	"get_hostname_policy":    true,
	"list_rules":             true,
	"get_audit_log":          true,
	"get_fingerprint_config": true,
	"get_egress_mode":        true,
}

const (
	// maxSendAttempts bounds the total number of attempts (first try plus
	// retries) a single logical send() makes.
	maxSendAttempts = 3
	// retryBaseDelay is the backoff unit between attempts, scaled linearly by
	// attempt number (40ms, 80ms, ...). gateway is a co-located daemon reached
	// over a Unix domain socket, not a remote service — a transient failure
	// here (mid-restart, a momentarily full accept backlog) resolves in
	// milliseconds, so there's no reason for the seconds-scale exponential
	// backoff a networked/remote client would need.
	retryBaseDelay = 40 * time.Millisecond
)

// send performs one logical gateway IPC call, retrying transient transport
// failures within tight, deliberately conservative bounds.
//
// Every attempt dials a fresh connection — see roundTrip's doc comment for
// why this client does not keep a persistent connection open across calls.
//
// Retry eligibility depends on whether the request could have already
// mutated gateway's state:
//   - Read-only request types (idempotentRequestTypes) are retried on any
//     transport failure: dial, write, or read. A duplicate read is harmless.
//   - Mutating request types (add_temporary_rule, remove_rule,
//     set_egress_mode, ...) are retried ONLY when roundTrip proves the
//     request bytes were never dispatched onto the wire — i.e. the failure
//     happened during dial or the mTLS handshake, before writing a single
//     byte of the request frame. Once a write has been attempted, even one
//     that itself returned an error, the request is treated as possibly
//     delivered (net.Conn's Write can push a byte prefix into the kernel send
//     queue before a deadline/error cuts it off — an ambiguous partial write)
//     and is never retried, to avoid double-applying a mutation gateway may
//     have already received and started processing.
//
// A well-formed gateway response with Type=="error" (e.g. "malformed
// hostname pattern") is a deterministic application-level rejection, not a
// transport failure — retrying it would just get the same answer, so it is
// surfaced immediately without consuming a retry.
func (c *Client) send(req Request) (*Response, error) {
	idempotent := idempotentRequestTypes[req.Type]

	var resp *Response
	var dispatched bool
	var err error

	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		resp, dispatched, err = c.roundTrip(req)
		if err == nil {
			break
		}

		retryable := idempotent || !dispatched
		if !retryable || attempt == maxSendAttempts {
			return nil, err
		}
		time.Sleep(retryBaseDelay * time.Duration(attempt))
	}

	if resp.Type == "error" {
		return nil, fmt.Errorf("gateway error: %s", resp.Message)
	}
	return resp, nil
}

// roundTrip performs exactly one dial-write-read cycle against gateway's IPC
// socket.
//
// Connection reuse: this client deliberately dials a fresh connection per
// call rather than keeping one persistent connection open with
// reconnect-on-error. Two reasons, in order of weight:
//  1. A single *Client is shared process-wide (orchestrator wires one
//     instance into the executor, the API handlers, and the health
//     aggregator — see cmd/orchestrator/main.go) and called concurrently from
//     many goroutines. A persistent connection would need either a mutex
//     serializing every call through it (turning concurrent gateway checks
//     into a queue — a real regression, since a slow mutating call would
//     block an unrelated health ping or scope check behind it) or a small
//     connection pool (health-checking idle conns, handling a half-closed
//     peer, redialing under load) — genuine complexity for a daemon that is
//     always co-located on the same host.
//  2. Unix domain sockets are cheap to dial: no DNS, no TCP handshake, just a
//     local connect(2) syscall. The cost this would save is negligible next
//     to gateway's own per-request work (nftables lookups, a hash-chained
//     SQLite audit write).
//
// If gateway call volume or latency ever becomes a measured problem, revisit
// this — but a fresh dial per call is not the bottleneck today, and the
// concurrency-serialization cost of naive reuse would likely cost more than
// it saves.
//
// dispatched reports whether the request frame may have reached gateway: it
// is false only when the connection could not even be established (dial or
// TLS-handshake failure) — the one case send() can prove gateway never saw
// the request, and so the only case a mutating call is retried on. It flips
// to true the moment the first write of the request frame is attempted,
// whether or not that write itself returned an error (see send()'s doc
// comment for why).
func (c *Client) roundTrip(req Request) (resp *Response, dispatched bool, err error) {
	rawConn, err := net.DialTimeout("unix", c.socketPath, c.timeout)
	if err != nil {
		return nil, false, fmt.Errorf("connect to gateway: %w", err)
	}

	// Optionally upgrade to mTLS.
	var conn = rawConn
	if c.tlsConfig != nil {
		tlsConn := tls.Client(rawConn, c.tlsConfig)
		if err := tlsConn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
			_ = rawConn.Close()
			return nil, false, fmt.Errorf("set deadline: %w", err)
		}
		if err := tlsConn.Handshake(); err != nil {
			_ = rawConn.Close()
			return nil, false, fmt.Errorf("mTLS handshake: %w", err)
		}
		conn = tlsConn
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, false, fmt.Errorf("set deadline: %w", err)
	}

	// Write length-prefixed JSON
	data, err := json.Marshal(req)
	if err != nil {
		return nil, false, fmt.Errorf("marshal request: %w", err)
	}

	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))

	// From this point on the request is considered dispatched: bytes may
	// already be in flight even if a Write call below returns an error.
	dispatched = true
	if err := writeAll(conn, lenBuf); err != nil {
		return nil, true, fmt.Errorf("write length: %w", err)
	}
	if err := writeAll(conn, data); err != nil {
		return nil, true, fmt.Errorf("write payload: %w", err)
	}

	// Read response
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, true, fmt.Errorf("read response length: %w", err)
	}
	respLen := binary.BigEndian.Uint32(lenBuf)
	if respLen > 1048576 {
		return nil, true, fmt.Errorf("response too large: %d bytes", respLen)
	}

	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return nil, true, fmt.Errorf("read response: %w", err)
	}

	var out Response
	if err := json.Unmarshal(respBuf, &out); err != nil {
		return nil, true, fmt.Errorf("unmarshal response: %w", err)
	}

	return &out, true, nil
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
