package gateway

// Request mirrors gateway/src/ipc/protocol.rs IpcRequest.
// Must serialize identically to the Rust types.
type Request struct {
	Type        string  `json:"type"`
	IP          string  `json:"ip,omitempty"`
	Network     string  `json:"network,omitempty"`
	TTLSecs     uint64  `json:"ttl_secs,omitempty"`
	Description *string `json:"description,omitempty"`
	RuleID      string  `json:"rule_id,omitempty"`
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
}

// StatusResponse is a structured gateway status.
type StatusResponse struct {
	Level       int    `json:"level"`
	RulesActive bool   `json:"rules_active"`
	UptimeSecs  uint64 `json:"uptime_secs"`
}
