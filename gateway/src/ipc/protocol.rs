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
}

/// Response from gateway to orchestrator.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum IpcResponse {
    /// Result of a scope check.
    ScopeResult { ip: String, in_scope: bool },
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
    },
    /// Success response for rule removal.
    RuleRemoved { rule_id: String },
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
        };
        let json = serde_json::to_string(&resp).unwrap();
        assert!(json.contains("scope_result"));
        assert!(json.contains("true"));
    }

    #[test]
    fn test_ping_pong() {
        let req = IpcRequest::Ping;
        let json = serde_json::to_string(&req).unwrap();
        assert!(json.contains("ping"));
    }
}
