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
	Type                   string `json:"type"`
	IP                     string `json:"ip,omitempty"`
	InScope                bool   `json:"in_scope,omitempty"`
	Level                  int    `json:"level,omitempty"`
	RulesActive            bool   `json:"rules_active,omitempty"`
	UptimeSecs             uint64 `json:"uptime_secs,omitempty"`
	PolicyKernelConsistent bool   `json:"policy_kernel_consistent,omitempty"`
	EnforcementGeneration  uint64 `json:"enforcement_generation,omitempty"`
	LastReconcileError     string `json:"last_reconcile_error,omitempty"`
	Message                string `json:"message,omitempty"`
	RuleID                 string `json:"rule_id,omitempty"`
	ExpiresAt              int64  `json:"expires_at,omitempty"`
	// KernelWarning is a legacy compatibility field. Current gateways commit
	// policy only after nftables succeeds and return Error on any mismatch.
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
	Level                  int    `json:"level"`
	RulesActive            bool   `json:"rules_active"`
	UptimeSecs             uint64 `json:"uptime_secs"`
	PolicyKernelConsistent bool   `json:"policy_kernel_consistent"`
	EnforcementGeneration  uint64 `json:"enforcement_generation"`
	LastReconcileError     string `json:"last_reconcile_error,omitempty"`
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
