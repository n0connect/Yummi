//! Policy engine — determines allow/block decisions for traffic.

pub mod hostname;
pub mod rules;

use std::net::IpAddr;
use std::sync::Arc;
use std::sync::RwLock;
use std::time::{SystemTime, UNIX_EPOCH};

use ipnet::IpNet;

use crate::config::ScopeConfig;
use self::hostname::{HostnameList, HostnamePolicy, HostnameVerdict};
use self::rules::{EgressMode, PolicyRule, RuleAction, RuleSource};

/// Policy decision for a traffic flow.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Decision {
    /// Allow and forward traffic (log metadata).
    Allow,
    /// Block traffic (RST connection, log).
    Block,
}

/// Egress-mode-aware verdict for a destination. Unlike `Decision` (allow/block),
/// this carries the `NeedsApproval` state that `Ask` mode can produce.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EgressVerdict {
    Allow,
    Block,
    NeedsApproval,
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
    /// Operator-controlled master egress switch. Defaults to `Block`
    /// (fail-closed); does NOT persist across restarts.
    egress_mode: EgressMode,
    /// When the allow-list is empty, what does `evaluate()` decide for a
    /// non-blocked destination? `false` (the secure default, and the value in
    /// config/default.yaml) = deny-all / fail-closed; `true` = observe-only,
    /// allow everything not explicitly blocked. Sourced from
    /// `policy.default_allow` at startup.
    default_allow: bool,
}

/// Policy engine evaluates traffic against scope rules.
///
/// Cloning is cheap — the internal state is reference-counted.
#[derive(Debug, Clone)]
pub struct PolicyEngine {
    state: Arc<RwLock<PolicyState>>,
    /// Name-level allow/block patterns. Seeded from config and mutable at
    /// runtime (operator add/remove via IPC) — behind its own RwLock, separate
    /// from the IP rule state. Runtime edits are in-memory only (reset on
    /// restart, when config reloads).
    hostname: Arc<RwLock<HostnamePolicy>>,
}

impl std::fmt::Debug for PolicyState {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PolicyState")
            .field("allow_rules", &self.allow_rules.len())
            .field("block_rules", &self.block_rules.len())
            .finish()
    }
}

/// Address classes that are never a legitimate remote target, regardless of
/// any configured allow/block list. This is the single unconditional safety
/// floor in the system — orchestrator's conductor no longer performs an
/// equivalent check (see docs/superpowers/specs/2026-06-22-scope-centralization-design.md).
fn is_always_blocked(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v4) => {
            v4.is_loopback() || v4.is_multicast() || v4.is_link_local() || v4.is_unspecified()
        }
        IpAddr::V6(v6) => {
            v6.is_loopback()
                || v6.is_multicast()
                || v6.is_unspecified()
                || (v6.segments()[0] & 0xffc0 == 0xfe80) // fe80::/10 link-local
        }
    }
}

impl PolicyEngine {
    /// Construct with the fail-closed default (`default_allow = false`): an
    /// empty allow-list denies all non-blocked traffic. This is the secure
    /// default used by tests; production wiring goes through
    /// [`PolicyEngine::with_default_allow`] to honor the operator's config.
    pub fn new(scope: &ScopeConfig) -> Self {
        Self::with_default_allow(scope, false)
    }

    /// Construct honoring the operator's `policy.default_allow`. `false` =
    /// deny-all when the allow-list is empty (fail-closed); `true` = observe-
    /// only (allow everything not explicitly blocked).
    pub fn with_default_allow(scope: &ScopeConfig, default_allow: bool) -> Self {
        let allow_rules: Vec<PolicyRule> = scope
            .allow
            .iter()
            .enumerate()
            .map(|(i, net)| PolicyRule {
                id: format!("allow-{i}"),
                action: RuleAction::Allow,
                network: *net,
                description: format!("Initial allow rule: {net}"),
                expires_at: None,
                source: RuleSource::Config,
            })
            .collect();

        let block_rules: Vec<PolicyRule> = scope
            .block
            .iter()
            .enumerate()
            .map(|(i, net)| PolicyRule {
                id: format!("block-{i}"),
                action: RuleAction::Block,
                network: *net,
                description: format!("Initial block rule: {net}"),
                expires_at: None,
                source: RuleSource::Config,
            })
            .collect();

        Self {
            state: Arc::new(RwLock::new(PolicyState {
                allow_rules,
                block_rules,
                // Fail-closed default — no external egress until an operator
                // explicitly switches the mode. Resets to Block on restart.
                egress_mode: EgressMode::Block,
                default_allow,
            })),
            hostname: Arc::new(RwLock::new(HostnamePolicy::new(
                &scope.hostname_allow,
                &scope.hostname_block,
            ))),
        }
    }

    /// Name-level verdict for a hostname (the IPC `check_hostname` entry point).
    /// Block wins; a non-empty allow-list is a whitelist; otherwise Neutral.
    /// IP literals are never name-policed (→ Neutral). See `hostname` module.
    pub fn check_hostname(&self, host: &str) -> HostnameVerdict {
        self.hostname.read().unwrap().check(host)
    }

    /// The current hostname allow/block patterns (config + runtime), for
    /// operator visibility.
    pub fn hostname_patterns(&self) -> (Vec<String>, Vec<String>) {
        let h = self.hostname.read().unwrap();
        (h.allow_patterns(), h.block_patterns())
    }

    /// Add a hostname pattern to the allow or block list at runtime. Returns
    /// false on a malformed pattern. In-memory only (resets on restart).
    pub fn add_hostname_pattern(&self, list: HostnameList, pattern: &str) -> bool {
        self.hostname.write().unwrap().add(list, pattern)
    }

    /// Remove a hostname pattern from the allow or block list. Returns true if
    /// one was removed.
    pub fn remove_hostname_pattern(&self, list: HostnameList, pattern: &str) -> bool {
        self.hostname.write().unwrap().remove(list, pattern)
    }

    /// Current operator-set egress mode. Takes (and releases) a read-lock.
    pub fn egress_mode(&self) -> EgressMode {
        self.state.read().unwrap().egress_mode
    }

    /// Set the operator-controlled egress mode. Takes a write-lock.
    pub fn set_egress_mode(&self, mode: EgressMode) {
        let mut state = self.state.write().unwrap();
        state.egress_mode = mode;
        tracing::info!(mode = %mode, "egress mode set");
    }

    /// Egress-mode-aware destination check — the entry point the IPC
    /// `CheckScope` handler uses. The always-blocked floor is checked first in
    /// **every** mode (Allow does not override it). Then:
    /// - `Block` → `Block` (no external egress)
    /// - `Allow` → `Allow` (blanket egress)
    /// - `Ask`   → run the existing allow/block-list `evaluate()`; an allow
    ///   (static allow rule or already-opened temporary rule) → `Allow`,
    ///   otherwise → `NeedsApproval`.
    ///
    /// `evaluate()` already performs its own `is_always_blocked` floor, but we
    /// keep the explicit check here so Allow mode short-circuits the floor too.
    pub fn check_egress(&self, dst: IpAddr) -> EgressVerdict {
        if is_always_blocked(dst) {
            return EgressVerdict::Block;
        }
        // Read the mode once and release its lock before calling evaluate()
        // (which takes its own read-lock) — avoids re-entrant double-locking.
        let mode = self.egress_mode();
        match mode {
            EgressMode::Block => EgressVerdict::Block,
            EgressMode::Allow => EgressVerdict::Allow,
            EgressMode::Ask => match self.evaluate(dst).0 {
                Decision::Allow => EgressVerdict::Allow,
                Decision::Block => EgressVerdict::NeedsApproval,
            },
        }
    }

    /// Egress verdict for a target the gateway cannot parse as an IP (a bare
    /// hostname like "google.com"). The gateway has no resolver, so the
    /// per-IP allow/block list and the always-blocked floor cannot be applied
    /// here — the answer is purely the operator-set mode: Allow → Allow (the
    /// FORWARD chain blanket-accepts sandbox egress, so the hostname is
    /// reachable and the sandbox does its own DNS), Block → Block, Ask →
    /// NeedsApproval (the orchestrator resolves the hostname to IP(s) at
    /// approval time and opens those). This is what lets `ping google.com`
    /// work in Allow mode instead of failing with "invalid IP address".
    pub fn check_egress_unresolved(&self) -> EgressVerdict {
        match self.egress_mode() {
            EgressMode::Block => EgressVerdict::Block,
            EgressMode::Allow => EgressVerdict::Allow,
            EgressMode::Ask => EgressVerdict::NeedsApproval,
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
        if is_always_blocked(dst) {
            return (Decision::Block, "always_blocked_address_class");
        }

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

        // Empty allow-list: decided by the operator's `policy.default_allow`.
        // Default `false` = deny-all / fail-closed (a target is reachable only
        // once explicitly allowed — in Ask mode this surfaces as a per-
        // destination approval). `true` = observe-only, allow everything not
        // blocked. Previously this was hardcoded to Allow, silently ignoring
        // the config field.
        if state.default_allow {
            (Decision::Allow, "default_allow")
        } else {
            (Decision::Block, "default_deny")
        }
    }

    /// Check if a specific IP is in scope (for IPC queries).
    pub fn is_in_scope(&self, ip: IpAddr) -> bool {
        matches!(self.evaluate(ip).0, Decision::Allow)
    }

    /// Add a temporary allow rule with TTL (from IPC request).
    /// Returns the rule ID and expiration timestamp.
    pub fn add_temporary_allow(&self, net: IpNet, ttl_secs: u64, description: String) -> (String, i64) {
        self.add_allow_rule("temp", RuleSource::Agent, net, ttl_secs, description)
    }

    /// Add a manually-issued temporary allow rule (operator UI path).
    /// Always tagged `source: Manual` — the caller cannot override this.
    pub fn add_manual_allow(&self, net: IpNet, ttl_secs: u64, reason: String) -> (String, i64) {
        self.add_allow_rule("manual", RuleSource::Manual, net, ttl_secs, reason)
    }

    /// Shared implementation behind add_temporary_allow/add_manual_allow —
    /// the two differ only in rule-ID prefix and RuleSource tag.
    fn add_allow_rule(
        &self,
        id_prefix: &str,
        source: RuleSource,
        net: IpNet,
        ttl_secs: u64,
        description: String,
    ) -> (String, i64) {
        let now_unix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;

        let expires_at = now_unix + ttl_secs as i64;
        let rule_id = format!("{}-{}-{}", id_prefix, now_unix, uuid::Uuid::new_v4());

        let rule = PolicyRule {
            id: rule_id.clone(),
            action: RuleAction::Allow,
            network: net,
            description,
            expires_at: Some(expires_at),
            source,
        };

        let mut state = self.state.write().unwrap();
        state.allow_rules.push(rule);
        tracing::info!(%net, rule_id, expires_at, source = id_prefix, "allow rule added");

        (rule_id, expires_at)
    }

    /// Snapshot of all active (non-expired) allow and block rules, for the
    /// management UI / ListRules IPC request.
    pub fn list_rules(&self) -> Vec<PolicyRule> {
        let now_unix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;
        let state = self.state.read().unwrap();
        state
            .allow_rules
            .iter()
            .chain(state.block_rules.iter())
            .filter(|r| !r.is_expired(now_unix))
            .cloned()
            .collect()
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
            ..Default::default()
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
        assert_eq!(reason, "always_blocked_address_class"); // was "blocked_range"
    }

    #[test]
    fn test_always_blocks_loopback_even_with_no_block_rule_configured() {
        let scope = ScopeConfig { allow: vec![], block: vec![], ..Default::default() }; // no block rules at all
        let engine = PolicyEngine::new(&scope);
        let (decision, reason) = engine.evaluate("127.0.0.1".parse().unwrap());
        assert_eq!(decision, Decision::Block);
        assert_eq!(reason, "always_blocked_address_class");
    }

    #[test]
    fn test_always_blocks_link_local_even_when_in_allow_list() {
        // Configured allow list deliberately includes the link-local range —
        // the floor must still win.
        let scope = ScopeConfig {
            allow: vec!["169.254.0.0/16".parse().unwrap()],
            block: vec![],
            ..Default::default()
        };
        let engine = PolicyEngine::new(&scope);
        let (decision, _) = engine.evaluate("169.254.1.1".parse().unwrap());
        assert_eq!(decision, Decision::Block);
    }

    #[test]
    fn test_always_blocks_multicast_and_unspecified() {
        let scope = ScopeConfig { allow: vec![], block: vec![], ..Default::default() };
        let engine = PolicyEngine::new(&scope);
        assert_eq!(engine.evaluate("224.0.0.1".parse().unwrap()).0, Decision::Block);
        assert_eq!(engine.evaluate("0.0.0.0".parse().unwrap()).0, Decision::Block);
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

    // With an empty allow-list and default_allow=true, evaluate() is observe-
    // only: everything not explicitly blocked is allowed. (Named for the
    // behavior, not the now-removed PolicyConfig.observe_only field, which
    // was a dead/unread config knob distinct from this.)
    #[test]
    fn test_default_allow_true_is_observe_only() {
        let scope = ScopeConfig {
            allow: vec![],
            block: vec!["127.0.0.0/8".parse().unwrap()],
            ..Default::default()
        };
        let engine = PolicyEngine::with_default_allow(&scope, true);

        let (decision, reason) = engine.evaluate("8.8.8.8".parse().unwrap());
        assert_eq!(decision, Decision::Allow);
        assert_eq!(reason, "default_allow");

        let (decision, _) = engine.evaluate("127.0.0.1".parse().unwrap());
        assert_eq!(decision, Decision::Block);
    }

    // Regression: an empty allow-list with the secure default (default_allow=
    // false, as in config/default.yaml) must DENY non-blocked traffic, not
    // allow it. Previously evaluate() hardcoded Allow here, silently ignoring
    // the config and making the gateway fail-OPEN when no allow rules existed.
    #[test]
    fn test_empty_allowlist_denies_by_default() {
        let scope = ScopeConfig {
            allow: vec![],
            block: vec!["127.0.0.0/8".parse().unwrap()],
            ..Default::default()
        };
        let engine = PolicyEngine::new(&scope); // default_allow = false

        let (decision, reason) = engine.evaluate("8.8.8.8".parse().unwrap());
        assert_eq!(decision, Decision::Block);
        assert_eq!(reason, "default_deny");
    }

    // In Ask mode, deny-by-default means a fresh out-of-scope target needs
    // operator approval rather than silently going out — the behavior the
    // egress round-trip relies on.
    #[test]
    fn test_ask_mode_needs_approval_with_empty_allowlist() {
        let scope = ScopeConfig { allow: vec![], block: vec![], ..Default::default() };
        let engine = PolicyEngine::new(&scope); // default_allow = false
        engine.set_egress_mode(EgressMode::Ask);
        assert_eq!(
            engine.check_egress("8.8.8.8".parse().unwrap()),
            EgressVerdict::NeedsApproval
        );
    }

    #[test]
    fn test_temporary_allow() {
        let scope = ScopeConfig {
            allow: vec!["10.0.0.0/8".parse().unwrap()],
            block: vec![],
            ..Default::default()
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
        let scope = ScopeConfig { allow: vec![], block: vec![], ..Default::default() };
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
            ..Default::default()
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

    #[test]
    fn test_add_manual_allow_tags_source_manual() {
        let engine = PolicyEngine::new(&ScopeConfig { allow: vec![], block: vec![], ..Default::default() });
        let (rule_id, _expires) = engine.add_manual_allow(
            "10.0.0.5/32".parse().unwrap(),
            600,
            "operator-supplied reason".to_string(),
        );
        let rules = engine.list_rules();
        let rule = rules.iter().find(|r| r.id == rule_id).expect("rule present");
        assert_eq!(rule.source, RuleSource::Manual);
        assert_eq!(rule.description, "operator-supplied reason");
    }

    #[test]
    fn test_list_rules_excludes_expired() {
        let engine = PolicyEngine::new(&ScopeConfig { allow: vec![], block: vec![], ..Default::default() });
        engine.add_manual_allow("10.0.0.1/32".parse().unwrap(), 0, "expires immediately".to_string());
        std::thread::sleep(std::time::Duration::from_secs(1));
        let rules = engine.list_rules();
        assert!(rules.is_empty(), "expired rule should not appear in list_rules");
    }

    #[test]
    fn test_egress_mode_defaults_to_block() {
        let engine = PolicyEngine::new(&test_scope());
        assert_eq!(engine.egress_mode(), EgressMode::Block);
    }

    #[test]
    fn test_check_egress_block_mode_blocks_external() {
        let engine = PolicyEngine::new(&ScopeConfig { allow: vec![], block: vec![], ..Default::default() });
        // default mode is Block
        assert_eq!(engine.check_egress("8.8.8.8".parse().unwrap()), EgressVerdict::Block);
    }

    #[test]
    fn test_check_egress_allow_mode_allows_external() {
        let engine = PolicyEngine::new(&ScopeConfig { allow: vec![], block: vec![], ..Default::default() });
        engine.set_egress_mode(EgressMode::Allow);
        assert_eq!(engine.check_egress("8.8.8.8".parse().unwrap()), EgressVerdict::Allow);
    }

    #[test]
    fn test_check_egress_unresolved_follows_mode() {
        // A bare hostname (non-IP) is decided purely by the egress mode, never
        // an "invalid IP" error — this is what makes `ping google.com` work in
        // Allow mode and queue for approval in Ask mode.
        let engine = PolicyEngine::new(&ScopeConfig { allow: vec![], block: vec![], ..Default::default() });
        assert_eq!(engine.check_egress_unresolved(), EgressVerdict::Block); // default
        engine.set_egress_mode(EgressMode::Allow);
        assert_eq!(engine.check_egress_unresolved(), EgressVerdict::Allow);
        engine.set_egress_mode(EgressMode::Ask);
        assert_eq!(engine.check_egress_unresolved(), EgressVerdict::NeedsApproval);
    }

    #[test]
    fn test_check_egress_ask_mode_unlisted_needs_approval() {
        // allow list configured but does NOT include 8.8.8.8
        let scope = ScopeConfig {
            allow: vec!["10.0.0.0/8".parse().unwrap()],
            block: vec![],
            ..Default::default()
        };
        let engine = PolicyEngine::new(&scope);
        engine.set_egress_mode(EgressMode::Ask);
        assert_eq!(
            engine.check_egress("8.8.8.8".parse().unwrap()),
            EgressVerdict::NeedsApproval
        );
    }

    #[test]
    fn test_check_egress_ask_mode_listed_allows() {
        let scope = ScopeConfig {
            allow: vec!["8.8.8.0/24".parse().unwrap()],
            block: vec![],
            ..Default::default()
        };
        let engine = PolicyEngine::new(&scope);
        engine.set_egress_mode(EgressMode::Ask);
        assert_eq!(engine.check_egress("8.8.8.8".parse().unwrap()), EgressVerdict::Allow);
    }

    #[test]
    fn test_check_egress_always_blocked_floor_wins_in_all_modes() {
        let engine = PolicyEngine::new(&ScopeConfig { allow: vec![], block: vec![], ..Default::default() });
        for mode in [EgressMode::Block, EgressMode::Allow, EgressMode::Ask] {
            engine.set_egress_mode(mode);
            assert_eq!(
                engine.check_egress("127.0.0.1".parse().unwrap()),
                EgressVerdict::Block,
                "loopback must be blocked in {mode} mode"
            );
        }
    }

    #[test]
    fn test_list_rules_includes_both_allow_and_block() {
        // block-1 comes from ScopeConfig (loaded at construction, source: Config).
        let engine = PolicyEngine::new(&ScopeConfig {
            allow: vec![],
            block: vec!["127.0.0.0/8".parse().unwrap()],
            ..Default::default()
        });
        engine.add_manual_allow("10.0.0.1/32".parse().unwrap(), 600, "test".to_string());
        let rules = engine.list_rules();
        assert_eq!(rules.len(), 2);
        assert!(rules.iter().any(|r| r.source == RuleSource::Config && r.action == RuleAction::Block));
        assert!(rules.iter().any(|r| r.source == RuleSource::Manual));
    }
}
