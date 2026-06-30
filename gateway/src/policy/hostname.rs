//! Hostname-level scope policy — name allow/block patterns the orchestrator
//! enforces *before* it resolves a name to IPs.
//!
//! The gateway only ever sees IPs in packets, so it cannot match a hostname at
//! the packet layer. Instead it stores the operator's patterns and answers a
//! `check_hostname` verdict over IPC; the orchestrator enforces that verdict by
//! refusing to run the command (the same "gateway decides, orchestrator
//! enforces" split as the IP `check_scope` path). This keeps the gateway the
//! single scope authority for both IPs and names.

/// Verdict for a hostname against the configured patterns.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HostnameVerdict {
    /// Matched a block pattern, or an allow-list is configured and the name
    /// matched none of it — a hard, name-level deny.
    Block,
    /// Matched an allow pattern (an explicit whitelist hit).
    Allow,
    /// No name-level restriction applies (nothing blocked it and no allow-list
    /// is configured) — defer to the IP-egress flow.
    Neutral,
}

impl HostnameVerdict {
    pub fn as_str(self) -> &'static str {
        match self {
            HostnameVerdict::Block => "block",
            HostnameVerdict::Allow => "allow",
            HostnameVerdict::Neutral => "neutral",
        }
    }
}

/// Compiled hostname allow/block patterns. Immutable after construction
/// (config-time), so it lives outside the policy RwLock.
#[derive(Debug, Clone, Default)]
pub struct HostnamePolicy {
    allow: Vec<HostPattern>,
    block: Vec<HostPattern>,
}

impl HostnamePolicy {
    pub fn new(allow: &[String], block: &[String]) -> Self {
        Self {
            allow: allow.iter().filter_map(|p| HostPattern::parse(p)).collect(),
            block: block.iter().filter_map(|p| HostPattern::parse(p)).collect(),
        }
    }

    /// Verdict precedence: a block match always wins; then a non-empty allow-
    /// list acts as a whitelist (no match → block); an empty allow-list yields
    /// Neutral. An IP literal (or empty input) is never name-policed → Neutral.
    pub fn check(&self, host: &str) -> HostnameVerdict {
        let h = normalize(host);
        if h.is_empty() {
            return HostnameVerdict::Neutral;
        }
        if self.block.iter().any(|p| p.matches(&h)) {
            return HostnameVerdict::Block;
        }
        if self.allow.is_empty() {
            return HostnameVerdict::Neutral;
        }
        if self.allow.iter().any(|p| p.matches(&h)) {
            HostnameVerdict::Allow
        } else {
            HostnameVerdict::Block
        }
    }

    pub fn allow_patterns(&self) -> Vec<String> {
        self.allow.iter().map(HostPattern::original).collect()
    }

    pub fn block_patterns(&self) -> Vec<String> {
        self.block.iter().map(HostPattern::original).collect()
    }

    /// Add a pattern to the allow or block list at runtime. Idempotent (a
    /// pattern already present is a no-op). Returns false if the pattern is
    /// malformed (empty / bare "*."). Runtime additions are in-memory only —
    /// they reset on restart when the config patterns reload (the same
    /// ephemeral model as temporary IP rules and the egress mode).
    pub fn add(&mut self, list: HostnameList, raw: &str) -> bool {
        let Some(p) = HostPattern::parse(raw) else {
            return false;
        };
        let canon = p.original();
        let target = match list {
            HostnameList::Allow => &mut self.allow,
            HostnameList::Block => &mut self.block,
        };
        if target.iter().any(|e| e.original() == canon) {
            return true; // already present — idempotent
        }
        target.push(p);
        true
    }

    /// Remove a pattern from the allow or block list. Returns true if one was
    /// removed. In-memory; a config-seeded pattern removed at runtime reappears
    /// on restart.
    pub fn remove(&mut self, list: HostnameList, raw: &str) -> bool {
        let Some(p) = HostPattern::parse(raw) else {
            return false;
        };
        let canon = p.original();
        let target = match list {
            HostnameList::Allow => &mut self.allow,
            HostnameList::Block => &mut self.block,
        };
        let before = target.len();
        target.retain(|e| e.original() != canon);
        target.len() != before
    }
}

/// Which list a runtime add/remove targets.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HostnameList {
    Allow,
    Block,
}

impl HostnameList {
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "allow" => Some(HostnameList::Allow),
            "block" => Some(HostnameList::Block),
            _ => None,
        }
    }
}

/// A single hostname pattern: an exact name or a `*.suffix` wildcard.
#[derive(Debug, Clone)]
enum HostPattern {
    Exact(String),
    /// `*.example.com` → suffix "example.com": matches the apex `example.com`
    /// AND any subdomain at any depth (`a.example.com`, `a.b.example.com`).
    Suffix(String),
}

impl HostPattern {
    fn parse(raw: &str) -> Option<Self> {
        let p = raw.trim().trim_end_matches('.').to_ascii_lowercase();
        if p.is_empty() {
            return None;
        }
        if let Some(rest) = p.strip_prefix("*.") {
            if rest.is_empty() {
                return None;
            }
            Some(HostPattern::Suffix(rest.to_string()))
        } else {
            Some(HostPattern::Exact(p))
        }
    }

    fn matches(&self, host: &str) -> bool {
        match self {
            HostPattern::Exact(name) => host == name,
            HostPattern::Suffix(suffix) => {
                host == suffix || host.ends_with(&format!(".{suffix}"))
            }
        }
    }

    fn original(&self) -> String {
        match self {
            HostPattern::Exact(n) => n.clone(),
            HostPattern::Suffix(s) => format!("*.{s}"),
        }
    }
}

/// Lowercase + strip a single trailing dot + trim. Returns "" for an
/// IP-literal input so hostname policy is a no-op on IP targets.
fn normalize(host: &str) -> String {
    let h = host.trim().trim_end_matches('.').to_ascii_lowercase();
    if h.parse::<std::net::IpAddr>().is_ok() {
        return String::new();
    }
    h
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_wildcard_covers_apex_and_subdomains() {
        let p = HostnamePolicy::new(&["*.google.com".to_string()], &[]);
        // Allow-list is a whitelist: google names match, others are blocked.
        assert_eq!(p.check("google.com"), HostnameVerdict::Allow); // apex
        assert_eq!(p.check("mail.google.com"), HostnameVerdict::Allow); // one label
        assert_eq!(p.check("a.b.google.com"), HostnameVerdict::Allow); // deep
        assert_eq!(p.check("GOOGLE.COM"), HostnameVerdict::Allow); // case-insensitive
        assert_eq!(p.check("notgoogle.com"), HostnameVerdict::Block); // not in list
        assert_eq!(p.check("google.com.evil.com"), HostnameVerdict::Block); // suffix trick
    }

    #[test]
    fn test_block_wins_over_allow() {
        let p = HostnamePolicy::new(
            &["*.google.com".to_string()],
            &["ads.google.com".to_string()],
        );
        assert_eq!(p.check("mail.google.com"), HostnameVerdict::Allow);
        assert_eq!(p.check("ads.google.com"), HostnameVerdict::Block); // block beats allow
    }

    #[test]
    fn test_blocklist_only_denies_listed_others_neutral() {
        let p = HostnamePolicy::new(&[], &["*.facebook.com".to_string()]);
        assert_eq!(p.check("www.facebook.com"), HostnameVerdict::Block);
        assert_eq!(p.check("facebook.com"), HostnameVerdict::Block); // apex covered
        // No allow-list → anything not blocked is Neutral (defer to IP egress).
        assert_eq!(p.check("example.com"), HostnameVerdict::Neutral);
    }

    #[test]
    fn test_empty_policy_is_neutral() {
        let p = HostnamePolicy::default();
        assert_eq!(p.check("anything.com"), HostnameVerdict::Neutral);
    }

    #[test]
    fn test_exact_pattern_no_subdomains() {
        let p = HostnamePolicy::new(&["example.com".to_string()], &[]);
        assert_eq!(p.check("example.com"), HostnameVerdict::Allow);
        assert_eq!(p.check("sub.example.com"), HostnameVerdict::Block); // exact = no subdomains
    }

    #[test]
    fn test_ip_literal_is_neutral() {
        let p = HostnamePolicy::new(&["*.google.com".to_string()], &[]);
        // An IP target carries no name to police — even with a whitelist set.
        assert_eq!(p.check("8.8.8.8"), HostnameVerdict::Neutral);
    }
}
