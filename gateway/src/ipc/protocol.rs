//! IPC protocol — message types for orchestrator ↔ gateway communication.
//!
//! Length-prefixed JSON over Unix domain socket.
//! These types must serialize identically to orchestrator/internal/gateway/protocol.go.

use serde::{Deserialize, Serialize};

/// Request from orchestrator to gateway.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum IpcRequest {
    /// Check if a destination IP is in scope.
    CheckScope { ip: String },
    /// Get gateway status.
    GetStatus,
    /// Health check.
    Ping,
    /// Add a temporary allow rule with TTL.
    AddTemporaryRule {
        /// Network CIDR (e.g., "192.168.1.0/24" or "10.0.0.5/32")
        network: String,
        /// Time-to-live in seconds (e.g., 600 = 10 minutes)
        ttl_secs: u64,
        /// Optional description for audit trail
        description: Option<String>,
    },
    /// Remove a rule by ID.
    RemoveRule {
        /// Rule ID returned from AddTemporaryRule
        rule_id: String,
    },
    /// List all active policy rules (read-only, no auth required).
    ListRules,
    /// Fetch the most recent N audit log rows plus a chain-verification result.
    GetAuditLog { limit: usize },
    /// Add a manually-operator-issued temporary allow rule. Distinct from
    /// AddTemporaryRule (used by the agent/executor path) so the gateway can
    /// hard-code `source: Manual` on the resulting PolicyRule without trusting
    /// any client-supplied label.
    AddManualRule {
        network: String,
        ttl_secs: u64,
        reason: String,
    },
    /// Read the gateway's current fingerprint-masking configuration.
    GetFingerprintConfig,
    /// Set the operator-controlled egress mode ("block"/"allow"/"ask").
    /// Write op — UID-gated; re-applies the nftables ruleset on change.
    SetEgressMode { mode: String },
    /// Read the current egress mode (read-only, no auth required).
    GetEgressMode,
    /// Name-level scope verdict for a hostname against the configured
    /// hostname_allow/hostname_block patterns (read-only). The orchestrator
    /// consults this before it resolves a name; the gateway cannot enforce it
    /// at the packet layer (it sees only IPs).
    CheckHostname { host: String },
    /// Read the configured hostname allow/block patterns (read-only).
    GetHostnamePolicy,
    /// Add a hostname pattern to the "allow" or "block" list at runtime.
    /// Write op — UID-gated. In-memory only (resets on restart).
    AddHostnameRule { list: String, pattern: String },
    /// Remove a hostname pattern from the "allow" or "block" list at runtime.
    /// Write op — UID-gated.
    RemoveHostnameRule { list: String, pattern: String },
}

/// Response from gateway to orchestrator.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum IpcResponse {
    /// Result of a scope check.
    ///
    /// `verdict` is the egress-mode-aware result ("allow"|"block"|"needs_approval").
    /// `in_scope` is kept for backward compatibility — `true` iff `verdict == "allow"`.
    ScopeResult {
        ip: String,
        in_scope: bool,
        verdict: String,
    },
    /// Current gateway status.
    Status {
        level: u8,
        rules_active: bool,
        uptime_secs: u64,
    },
    /// Pong response to Ping.
    Pong,
    /// Error response.
    Error { message: String },
    /// Success response for rule operations.
    RuleAdded {
        /// Unique rule ID for removal
        rule_id: String,
        /// Unix timestamp when rule will expire
        expires_at: i64,
        /// Set when the policy-engine rule was added but the kernel-level
        /// nftables unlock failed (add_allowed_target error) — the rule
        /// LOOKS active to future check_scope calls, but sandbox traffic to
        /// this target is still actually dropped at the kernel (fail-safe,
        /// not fail-open: nothing got MORE open than before). None when the
        /// kernel call succeeded or no live controller is attached.
        #[serde(default, skip_serializing_if = "Option::is_none")]
        kernel_warning: Option<String>,
    },
    /// Success response for rule removal.
    RuleRemoved { rule_id: String },
    /// Result of ListRules.
    RuleList { rules: Vec<RuleView> },
    /// Result of GetAuditLog.
    AuditLog {
        rows: Vec<AuditRowView>,
        chain_verified: bool,
    },
    /// Result of GetFingerprintConfig — the gateway's fingerprint-masking settings.
    FingerprintConfig {
        ttl_normalize: Option<u8>,
        mac_randomization: bool,
    },
    /// Current/updated egress mode ("block"|"allow"|"ask"). Returned by both
    /// GetEgressMode and a successful SetEgressMode.
    EgressMode { mode: String },
    /// Result of CheckHostname. `verdict` is "allow" (matched a whitelist),
    /// "block" (matched a blocklist, or a whitelist is set and the name didn't
    /// match), or "neutral" (no name restriction — defer to the IP-egress flow).
    HostnameResult { host: String, verdict: String },
    /// Result of GetHostnamePolicy — the configured patterns, for operator visibility.
    HostnamePolicy {
        allow: Vec<String>,
        block: Vec<String>,
    },
}

/// Read-only view of a policy rule, returned by ListRules.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RuleView {
    pub id: String,
    pub action: String, // "allow" | "block"
    pub network: String,
    pub description: String,
    pub expires_at: Option<i64>,
    pub source: String, // "config" | "agent" | "manual"
}

/// Read-only view of one audit log row, returned by GetAuditLog.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AuditRowView {
    pub id: i64,
    pub timestamp: i64,
    pub src_ip: String,
    pub dst_ip: String,
    pub decision: String,
    pub reason: String,
}

/// Decision event streamed to subscribers.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecisionEvent {
    pub timestamp_ns: i64,
    pub session_id: Option<String>,
    pub src_ip: String,
    pub dst_ip: String,
    pub src_port: Option<u16>,
    pub dst_port: Option<u16>,
    pub protocol: String,
    pub decision: String,
    pub reason: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_request_serialization() {
        let req = IpcRequest::CheckScope {
            ip: "10.0.0.1".to_string(),
        };
        let json = serde_json::to_string(&req).unwrap();
        assert!(json.contains("check_scope"));
        assert!(json.contains("10.0.0.1"));

        let parsed: IpcRequest = serde_json::from_str(&json).unwrap();
        match parsed {
            IpcRequest::CheckScope { ip } => assert_eq!(ip, "10.0.0.1"),
            _ => panic!("wrong variant"),
        }
    }

    #[test]
    fn test_response_serialization() {
        let resp = IpcResponse::ScopeResult {
            ip: "10.0.0.1".to_string(),
            in_scope: true,
            verdict: "allow".to_string(),
        };
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("scope_result"));
        assert!(json.contains("true"));
        assert!(json.contains("allow"));
    }

    #[test]
    fn test_ping_pong() {
        let req = IpcRequest::Ping;
        let json = serde_json::to_string(&req).unwrap();
        assert!(json.contains("ping"));
    }
}

#[cfg(test)]
mod gateway_mgmt_tests {
    use super::*;

    #[test]
    fn test_list_rules_request_serializes() {
        let req = IpcRequest::ListRules;
        let json = serde_json::to_string(&req).unwrap();
        assert_eq!(json, r#"{"type":"list_rules"}"#);
    }

    #[test]
    fn test_add_manual_rule_request_round_trips() {
        let req = IpcRequest::AddManualRule {
            network: "10.0.0.5/32".to_string(),
            ttl_secs: 600,
            reason: "emergency access for finding F-12".to_string(),
        };
        let json = serde_json::to_string(&req).unwrap();
        let back: IpcRequest = serde_json::from_str(&json).unwrap();
        match back {
            IpcRequest::AddManualRule {
                network,
                ttl_secs,
                reason,
            } => {
                assert_eq!(network, "10.0.0.5/32");
                assert_eq!(ttl_secs, 600);
                assert_eq!(reason, "emergency access for finding F-12");
            }
            _ => panic!("wrong variant"),
        }
    }

    #[test]
    fn test_rule_list_response_serializes() {
        let resp = IpcResponse::RuleList {
            rules: vec![RuleView {
                id: "temp-1".to_string(),
                action: "allow".to_string(),
                network: "10.0.0.0/24".to_string(),
                description: "[manual] test".to_string(),
                expires_at: Some(1000),
                source: "manual".to_string(),
            }],
        };
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("\"source\":\"manual\""));
    }
}
