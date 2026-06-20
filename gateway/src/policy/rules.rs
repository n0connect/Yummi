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
}

/// Action to take when a rule matches.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum RuleAction {
    Allow,
    Block,
}

impl PolicyRule {
    /// Check if this rule has expired.
    pub fn is_expired(&self, now_unix: i64) -> bool {
        self.expires_at.map_or(false, |exp| now_unix > exp)
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
        };

        assert!(!rule.is_expired(999999));
    }
}
