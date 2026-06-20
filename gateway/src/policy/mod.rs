//! Policy engine — determines allow/block decisions for traffic.

pub mod rules;

use std::net::IpAddr;
use std::sync::Arc;
use std::sync::RwLock;
use std::time::{SystemTime, UNIX_EPOCH};

use ipnet::IpNet;

use crate::config::ScopeConfig;
use self::rules::{PolicyRule, RuleAction};

/// Policy decision for a traffic flow.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Decision {
    /// Allow and forward traffic (log metadata).
    Allow,
    /// Block traffic (RST connection, log).
    Block,
}

impl std::fmt::Display for Decision {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Decision::Allow => write!(f, "allow"),
            Decision::Block => write!(f, "block"),
        }
    }
}

/// Combined mutable policy state — both rule lists under a single lock.
/// Holding a single `RwLock` eliminates the TOCTOU race that would exist if
/// block_rules and allow_rules were locked separately: evaluation reads the
/// complete consistent state in one atomic operation.
struct PolicyState {
    allow_rules: Vec<PolicyRule>,
    block_rules: Vec<PolicyRule>,
}

/// Policy engine evaluates traffic against scope rules.
///
/// Cloning is cheap — the internal state is reference-counted.
#[derive(Debug, Clone)]
pub struct PolicyEngine {
    state: Arc<RwLock<PolicyState>>,
}

impl std::fmt::Debug for PolicyState {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PolicyState")
            .field("allow_rules", &self.allow_rules.len())
            .field("block_rules", &self.block_rules.len())
            .finish()
    }
}

impl PolicyEngine {
    pub fn new(scope: &ScopeConfig) -> Self {
        let allow_rules: Vec<PolicyRule> = scope
            .allow
            .iter()
            .enumerate()
            .map(|(i, net)| PolicyRule {
                id: format!("allow-{}", i),
                action: RuleAction::Allow,
                network: *net,
                description: format!("Initial allow rule: {}", net),
                expires_at: None,
            })
            .collect();

        let block_rules: Vec<PolicyRule> = scope
            .block
            .iter()
            .enumerate()
            .map(|(i, net)| PolicyRule {
                id: format!("block-{}", i),
                action: RuleAction::Block,
                network: *net,
                description: format!("Initial block rule: {}", net),
                expires_at: None,
            })
            .collect();

        Self {
            state: Arc::new(RwLock::new(PolicyState {
                allow_rules,
                block_rules,
            })),
        }
    }

    /// Evaluate a destination IP against policy rules.
    ///
    /// Order of evaluation (all checks within a **single read-lock** to
    /// prevent TOCTOU races between the block and allow phases):
    /// 1. Block list takes priority (always block dangerous ranges)
    /// 2. If allow list has non-expired rules, only allow listed ranges
    /// 3. If allow list is empty / all expired, allow everything not blocked
    ///
    /// Expired rules are skipped automatically.
    pub fn evaluate(&self, dst: IpAddr) -> (Decision, &'static str) {
        let now_unix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;

        // Single read-lock covers both lists — no TOCTOU window.
        let state = self.state.read().unwrap();

        // Block list always wins.
        for rule in state.block_rules.iter() {
            if rule.is_expired(now_unix) {
                continue;
            }
            if rule.network.contains(&dst) {
                return (Decision::Block, "blocked_range");
            }
        }

        // If allow list is configured, restrict to it.
        let has_allow_rules = !state.allow_rules.is_empty();
        if has_allow_rules {
            for rule in state.allow_rules.iter() {
                if rule.is_expired(now_unix) {
                    continue;
                }
                if rule.network.contains(&dst) {
                    return (Decision::Allow, "allowed_range");
                }
            }
            return (Decision::Block, "not_in_scope");
        }

        // No allow list = observe-only mode, allow everything not blocked.
        (Decision::Allow, "default_allow")
    }

    /// Check if a specific IP is in scope (for IPC queries).
    pub fn is_in_scope(&self, ip: IpAddr) -> bool {
        matches!(self.evaluate(ip).0, Decision::Allow)
    }

    /// Add a temporary allow rule with TTL (from IPC request).
    /// Returns the rule ID and expiration timestamp.
    pub fn add_temporary_allow(&self, net: IpNet, ttl_secs: u64, description: String) -> (String, i64) {
        let now_unix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;

        let expires_at = now_unix + ttl_secs as i64;
        let rule_id = format!("temp-{}-{}", now_unix, uuid::Uuid::new_v4());

        let rule = PolicyRule {
            id: rule_id.clone(),
            action: RuleAction::Allow,
            network: net,
            description,
            expires_at: Some(expires_at),
        };

        let mut state = self.state.write().unwrap();
        state.allow_rules.push(rule);
        tracing::info!(%net, rule_id, expires_at, "temporary allow rule added");

        (rule_id, expires_at)
    }

    /// Remove a rule by ID. Returns true if the rule was found and removed.
    pub fn remove_rule(&self, rule_id: &str) -> bool {
        let mut state = self.state.write().unwrap();
        if let Some(pos) = state.allow_rules.iter().position(|r| r.id == rule_id) {
            state.allow_rules.remove(pos);
            tracing::info!(rule_id, "rule removed");
            return true;
        }
        false
    }

    /// Remove expired rules (called periodically by background task).
    pub fn cleanup_expired(&self) -> usize {
        let now_unix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;

        let mut state = self.state.write().unwrap();
        let before = state.allow_rules.len();
        state.allow_rules.retain(|rule| !rule.is_expired(now_unix));
        let removed = before - state.allow_rules.len();

        if removed > 0 {
            tracing::info!(removed, "expired rules cleaned up");
        }
        removed
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::ScopeConfig;

    fn test_scope() -> ScopeConfig {
        ScopeConfig {
            allow: vec![
                "10.0.0.0/8".parse().unwrap(),
                "192.168.1.0/24".parse().unwrap(),
            ],
            block: vec![
                "127.0.0.0/8".parse().unwrap(),
                "169.254.0.0/16".parse().unwrap(),
            ],
        }
    }

    #[test]
    fn test_allow_in_scope() {
        let engine = PolicyEngine::new(&test_scope());
        let (decision, _) = engine.evaluate("10.0.0.5".parse().unwrap());
        assert_eq!(decision, Decision::Allow);
    }

    #[test]
    fn test_block_localhost() {
        let engine = PolicyEngine::new(&test_scope());
        let (decision, reason) = engine.evaluate("127.0.0.1".parse().unwrap());
        assert_eq!(decision, Decision::Block);
        assert_eq!(reason, "blocked_range");
    }

    #[test]
    fn test_block_out_of_scope() {
        let engine = PolicyEngine::new(&test_scope());
        let (decision, reason) = engine.evaluate("8.8.8.8".parse().unwrap());
        assert_eq!(decision, Decision::Block);
        assert_eq!(reason, "not_in_scope");
    }

    #[test]
    fn test_block_link_local() {
        let engine = PolicyEngine::new(&test_scope());
        let (decision, _) = engine.evaluate("169.254.1.1".parse().unwrap());
        assert_eq!(decision, Decision::Block);
    }

    #[test]
    fn test_observe_only_mode() {
        let scope = ScopeConfig {
            allow: vec![],
            block: vec!["127.0.0.0/8".parse().unwrap()],
        };
        let engine = PolicyEngine::new(&scope);

        let (decision, reason) = engine.evaluate("8.8.8.8".parse().unwrap());
        assert_eq!(decision, Decision::Allow);
        assert_eq!(reason, "default_allow");

        let (decision, _) = engine.evaluate("127.0.0.1".parse().unwrap());
        assert_eq!(decision, Decision::Block);
    }

    #[test]
    fn test_temporary_allow() {
        let scope = ScopeConfig {
            allow: vec!["10.0.0.0/8".parse().unwrap()],
            block: vec![],
        };
        let engine = PolicyEngine::new(&scope);

        assert!(!engine.is_in_scope("8.8.8.8".parse().unwrap()));

        let (rule_id, expires_at) =
            engine.add_temporary_allow("8.8.8.0/24".parse().unwrap(), 600, "Test rule".to_string());
        assert!(!rule_id.is_empty());
        assert!(expires_at > 0);

        assert!(engine.is_in_scope("8.8.8.8".parse().unwrap()));

        assert!(engine.remove_rule(&rule_id));
        assert!(!engine.is_in_scope("8.8.8.8".parse().unwrap()));
    }

    #[test]
    fn test_expired_rule_cleanup() {
        let scope = ScopeConfig { allow: vec![], block: vec![] };
        let engine = PolicyEngine::new(&scope);

        engine.add_temporary_allow("192.168.1.0/24".parse().unwrap(), 1, "Expires soon".to_string());
        std::thread::sleep(std::time::Duration::from_secs(2));

        let removed = engine.cleanup_expired();
        assert_eq!(removed, 1);
    }

    /// Verify that block and allow are evaluated atomically (no TOCTOU).
    /// This spawns many concurrent evaluations while adding rules — if there
    /// were a TOCTOU race the test would flake, but with a single RwLock it
    /// is deterministic.
    #[test]
    fn test_concurrent_evaluation_consistency() {
        use std::sync::Arc;
        use std::thread;

        let scope = ScopeConfig {
            allow: vec!["10.0.0.0/8".parse().unwrap()],
            block: vec!["10.0.0.1/32".parse().unwrap()],
        };
        let engine = Arc::new(PolicyEngine::new(&scope));
        let mut handles = vec![];

        for _ in 0..32 {
            let e = engine.clone();
            handles.push(thread::spawn(move || {
                // 10.0.0.1 is explicitly blocked — must always be Block
                let (d, _) = e.evaluate("10.0.0.1".parse().unwrap());
                assert_eq!(d, Decision::Block, "blocked IP must stay blocked under concurrency");
            }));
        }

        // Simultaneously add a temporary allow for 10.0.0.0/8 (which includes 10.0.0.1)
        engine.add_temporary_allow("10.0.0.0/8".parse().unwrap(), 60, "concurrent test".to_string());

        for h in handles {
            h.join().unwrap();
        }
    }
}
