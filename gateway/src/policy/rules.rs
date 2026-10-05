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
/// - `BlockAll` (default): complete fail-closed lockdown, no external sandbox egress
///   at all (drops all traffic regardless of active allow rules).
/// - `BlockExceptRules`: deny by default, but permits explicit active allow rules
///   (static allow list + dynamic temporary rules).
/// - `AllowAll`: all external egress permitted (blanket accept above the drop).
/// - `Ask`: each new external destination needs operator approval; already-opened
///   destinations (static allow / temporary rules) are permitted.
///
/// **Does NOT persist across gateway restarts.** A fresh gateway always resets to
/// `BlockAll`, which preserves the fail-closed invariant.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum EgressMode {
    #[serde(alias = "block")]
    BlockAll,
    #[serde(alias = "rules_only")]
    BlockExceptRules,
    #[serde(alias = "allow")]
    AllowAll,
    #[serde(alias = "ask_every_time")]
    Ask,
}

impl Default for EgressMode {
    fn default() -> Self {
        EgressMode::BlockAll
    }
}

impl EgressMode {
    /// Parse the operator-supplied mode string.
    /// Canonical modes: "block_all", "block_except_rules", "allow_all", "ask".
    /// Aliases: "block", "rules_only", "allow", "ask_every_time".
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "block_all" | "block" => Some(EgressMode::BlockAll),
            "block_except_rules" | "rules_only" => Some(EgressMode::BlockExceptRules),
            "allow_all" | "allow" => Some(EgressMode::AllowAll),
            "ask" | "ask_every_time" => Some(EgressMode::Ask),
            _ => None,
        }
    }

    /// Lowercase wire string, matching the serde representation.
    pub fn as_str(&self) -> &'static str {
        match self {
            EgressMode::BlockAll => "block_all",
            EgressMode::BlockExceptRules => "block_except_rules",
            EgressMode::AllowAll => "allow_all",
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
