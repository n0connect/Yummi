//! Policy rule types and evaluation helpers.

use ipnet::IpNet;
use serde::{Deserialize, Serialize};

/// A named policy rule for audit trail.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PolicyRule {
    pub id: String,
    pub action: RuleAction,
    pub network: IpNet,
    pub description: String,
    /// Rule expiration (Unix timestamp). None = permanent.
    pub expires_at: Option<i64>,
    pub source: RuleSource,
}

/// Action to take when a rule matches.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum RuleAction {
    Allow,
    Block,
}

/// Operator-controlled, gateway-enforced master egress switch that sits *above*
/// the per-IP scope allow/block list:
///
/// - `Block` (default): no external sandbox egress at all (fail-closed).
/// - `Allow`: all external egress permitted (blanket accept above the drop).
/// - `Ask`: each new external destination needs operator approval; already-opened
///   destinations (static allow / temporary rules) are permitted.
///
/// **Does NOT persist across gateway restarts.** A fresh gateway always resets to
/// `Block`, which preserves the fail-closed invariant: a restarted gateway with no
/// operator action permits no external egress.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum EgressMode {
    Block,
    Allow,
    Ask,
}

impl EgressMode {
    /// Parse the operator-supplied mode string ("block"/"allow"/"ask").
    /// Returns `None` for any other value so the caller can reject it.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "block" => Some(EgressMode::Block),
            "allow" => Some(EgressMode::Allow),
            "ask" => Some(EgressMode::Ask),
            _ => None,
        }
    }

    /// Lowercase wire string, matching the serde representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            EgressMode::Block => "block",
            EgressMode::Allow => "allow",
            EgressMode::Ask => "ask",
        }
    }
}

impl std::fmt::Display for EgressMode {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Where a temporary rule originated — drives the audit trail and the
/// management UI's "source" badge. Config = loaded from gateway's own YAML
/// config at startup (PolicyEngine::new). Agent = added automatically by the
/// executor on approved command execution (add_temporary_allow). Manual =
/// added by an operator through the gateway management UI, independent of
/// the approval flow (add_manual_allow).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum RuleSource {
    Config,
    Agent,
    Manual,
}

impl PolicyRule {
    /// Check if this rule has expired.
    pub fn is_expired(&self, now_unix: i64) -> bool {
        self.expires_at.is_some_and(|exp| now_unix > exp)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_rule_expiration() {
        let rule = PolicyRule {
            id: "test-1".to_string(),
            action: RuleAction::Allow,
            network: "10.0.0.0/8".parse().unwrap(),
            description: "test rule".to_string(),
            expires_at: Some(1000),
            source: RuleSource::Agent,
        };

        assert!(!rule.is_expired(500));
        assert!(rule.is_expired(1500));
    }

    #[test]
    fn test_permanent_rule() {
        let rule = PolicyRule {
            id: "perm-1".to_string(),
            action: RuleAction::Block,
            network: "127.0.0.0/8".parse().unwrap(),
            description: "block localhost".to_string(),
            expires_at: None,
            source: RuleSource::Agent,
        };

        assert!(!rule.is_expired(999999));
    }
}
