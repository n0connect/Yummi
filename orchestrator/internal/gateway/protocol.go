package gateway

// Request mirrors gateway/src/ipc/protocol.rs IpcRequest.
// Must serialize identically to the Rust types.
type Request struct {
	Type        string  `json:"type"`
	IP          string  `json:"ip,omitempty"`
	Host        string  `json:"host,omitempty"`
	Network     string  `json:"network,omitempty"`
	TTLSecs     uint64  `json:"ttl_secs,omitempty"`
	Description *string `json:"description,omitempty"`
	RuleID      string  `json:"rule_id,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Limit       int     `json:"limit,omitempty"`
	Mode        string  `json:"mode,omitempty"`
	List        string  `json:"list,omitempty"`
	Pattern     string  `json:"pattern,omitempty"`
}

// Response mirrors gateway/src/ipc/protocol.rs IpcResponse.
type Response struct {
	Type        string `json:"type"`
	IP          string `json:"ip,omitempty"`
	InScope     bool   `json:"in_scope,omitempty"`
	Level       int    `json:"level,omitempty"`
	RulesActive bool   `json:"rules_active,omitempty"`
	UptimeSecs  uint64 `json:"uptime_secs,omitempty"`
	Message     string `json:"message,omitempty"`
	RuleID      string `json:"rule_id,omitempty"`
	ExpiresAt   int64  `json:"expires_at,omitempty"`
	// KernelWarning is set when the gateway's policy engine added a rule but
	// the nftables kernel-level unlock failed — the rule looks active to
	// future scope checks, but traffic to the target is still actually
	// dropped (fail-safe, not fail-open). See ipc/protocol.rs RuleAdded.
	KernelWarning string         `json:"kernel_warning,omitempty"`
	Rules         []RuleView     `json:"rules,omitempty"`
	AuditRows     []AuditRowView `json:"rows,omitempty"`
	ChainVerified bool           `json:"chain_verified,omitempty"`

	TTLNormalize     *uint8 `json:"ttl_normalize,omitempty"`
	MACRandomization bool   `json:"mac_randomization,omitempty"`

	Mode    string `json:"mode,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Host    string `json:"host,omitempty"`

	// HostnamePolicy view (GetHostnamePolicy response).
	Allow []string `json:"allow,omitempty"`
	Block []string `json:"block,omitempty"`
}

// StatusResponse is a structured gateway status.
type StatusResponse struct {
	Level       int    `json:"level"`
	RulesActive bool   `json:"rules_active"`
	UptimeSecs  uint64 `json:"uptime_secs"`
}

// RuleView mirrors gateway/src/ipc/protocol.rs RuleView.
type RuleView struct {
	ID          string `json:"id"`
	Action      string `json:"action"`
	Network     string `json:"network"`
	Description string `json:"description"`
	ExpiresAt   *int64 `json:"expires_at"`
	Source      string `json:"source"`
}

// AuditRowView mirrors gateway/src/ipc/protocol.rs AuditRowView.
type AuditRowView struct {
	ID        int64  `json:"id"`
	Timestamp int64  `json:"timestamp"`
	SrcIP     string `json:"src_ip"`
	DstIP     string `json:"dst_ip"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
}
