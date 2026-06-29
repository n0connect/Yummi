//! Traffic observation — Level 0 (passive).
//!
//! Reads /proc/net/tcp and /proc/net/udp to observe active connections
//! without modifying traffic. Logs out-of-scope connections to the audit trail.

pub mod capture;

use std::collections::HashSet;
use std::net::IpAddr;
use std::str::FromStr;
use std::time::Duration;

use crate::audit::AuditLog;
use crate::policy::PolicyEngine;

/// Observer receives packet metadata and records policy decisions.
#[derive(Clone)]
pub struct Observer {
    #[allow(dead_code)]
    policy: PolicyEngine,
    #[allow(dead_code)]
    audit: AuditLog,
}

impl Observer {
    pub fn new(policy: PolicyEngine, audit: AuditLog) -> Self {
        Self { policy, audit }
    }

    /// Main observation loop.
    /// Reads /proc/net/tcp+udp every 5 seconds.
    pub async fn run(self) {
        tracing::info!("observer started (Level 0 — passive /proc/net monitor)");
        self.run_loop().await;
    }

    async fn run_loop(self) {
        let interval = Duration::from_secs(5);
        let mut seen: HashSet<String> = HashSet::new();

        loop {
            tokio::time::sleep(interval).await;

            let conns = read_proc_net_connections();
            for (src, dst, proto) in &conns {
                let key = format!("{src}-{dst}-{proto}");
                if seen.contains(&key) {
                    continue;
                }
                seen.insert(key);

                // Check destination against gateway policy scope.
                let Some(dst_ip_str) = extract_ip_str(dst) else {
                    continue;
                };
                if let Ok(dst_ip) = IpAddr::from_str(dst_ip_str) {
                    let (decision, reason) = self.policy.evaluate(dst_ip);
                    if decision == crate::policy::Decision::Block {
                        tracing::warn!(
                            src = %src, dst = %dst, proto = %proto, reason = %reason,
                            "observer: blocked-range connection detected"
                        );
                        let now_ns = std::time::SystemTime::now()
                            .duration_since(std::time::UNIX_EPOCH)
                            .map(|d| d.as_nanos() as i64)
                            .unwrap_or(0);
                        let _ = self.audit.record_decision(
                            now_ns, None, dst_ip_str, dst_ip_str, None, None, proto, "block",
                            reason,
                        );
                    }
                }
            }

            // Evict old entries every 1000 to bound memory.
            if seen.len() > 1000 {
                seen.clear();
            }
        }
    }
}

/// Extracts the bare IP-address portion from a "host:port" or "[ipv6]:port"
/// string produced by parse_proc_addr. Returns None for anything that
/// doesn't match one of those two well-defined shapes — an allowlist on
/// structure rather than a best-effort strip, since silently misparsing the
/// destination would mean evaluating policy against the wrong address.
fn extract_ip_str(addr: &str) -> Option<&str> {
    if let Some(rest) = addr.strip_prefix('[') {
        // "[ipv6]:port" — the IP is everything up to the matching ']'.
        let end = rest.find(']')?;
        Some(&rest[..end])
    } else {
        // "ipv4:port" — the IP is everything before the last ':'.
        let p = addr.rfind(':')?;
        Some(&addr[..p])
    }
}

/// Parse /proc/net/tcp and /proc/net/udp, return (src, dst, proto) tuples.
/// Only returns entries in ESTABLISHED state (01) for TCP; all for UDP.
fn read_proc_net_connections() -> Vec<(String, String, String)> {
    let mut out = Vec::new();
    for (path, proto) in &[
        ("/proc/net/tcp", "tcp"),
        ("/proc/net/udp", "udp"),
        ("/proc/net/tcp6", "tcp6"),
    ] {
        if let Ok(content) = std::fs::read_to_string(path) {
            for line in content.lines().skip(1) {
                let fields: Vec<&str> = line.split_whitespace().collect();
                if fields.len() < 4 {
                    continue;
                }
                // For TCP only include ESTABLISHED (state=01).
                if *proto != "udp" && fields[3] != "01" {
                    continue;
                }
                let src = parse_proc_addr(fields[1]);
                let dst = parse_proc_addr(fields[2]);
                if src.is_empty() || dst.is_empty() {
                    continue;
                }
                out.push((src, dst, proto.to_string()));
            }
        }
    }
    out
}

/// Convert /proc/net hex address (e.g. "0100007F:0050") to "127.0.0.1:80".
fn parse_proc_addr(hex: &str) -> String {
    let parts: Vec<&str> = hex.split(':').collect();
    if parts.len() != 2 {
        return String::new();
    }
    let port = u16::from_str_radix(parts[1], 16).unwrap_or(0);

    // IPv4: 8 hex chars
    if parts[0].len() == 8 {
        let n = u32::from_str_radix(parts[0], 16).unwrap_or(0);
        // /proc/net stores little-endian on little-endian arch
        let bytes = n.to_le_bytes();
        let ip = IpAddr::from(bytes);
        return format!("{ip}:{port}");
    }
    // IPv6: 32 hex chars
    if parts[0].len() == 32 {
        let mut bytes = [0u8; 16];
        for i in 0..4 {
            let chunk = &parts[0][i * 8..(i + 1) * 8];
            if let Ok(w) = u32::from_str_radix(chunk, 16) {
                let wb = w.to_le_bytes();
                bytes[i * 4..(i + 1) * 4].copy_from_slice(&wb);
            }
        }
        let ip = IpAddr::from(bytes);
        return format!("[{ip}]:{port}");
    }
    String::new()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_proc_addr_ipv4() {
        // 0100007F = 127.0.0.1 little-endian; 0050 = port 80.
        assert_eq!(parse_proc_addr("0100007F:0050"), "127.0.0.1:80");
    }

    #[test]
    fn test_parse_proc_addr_ipv6_produces_parseable_literal() {
        // ::1 (loopback) encoded the way /proc/net/tcp6 stores it.
        let out = parse_proc_addr("00000000000000000000000001000000:0050");
        assert_eq!(out, "[::1]:80");
        // Regression for the audit finding: the previous `{:x?}` Debug-format
        // produced "[[0, 0, ..., 1]]:80", which extract_ip_str/IpAddr::from_str
        // could never parse back — every IPv6 entry was silently dropped from
        // the audit log. The fixed output must round-trip through the same
        // extraction the run loop uses.
        let ip_str = extract_ip_str(&out).expect("extract_ip_str should find the IPv6 literal");
        assert_eq!(ip_str, "::1");
        assert!(
            IpAddr::from_str(ip_str).is_ok(),
            "produced literal {out:?} did not round-trip through IpAddr::from_str"
        );
    }

    #[test]
    fn test_extract_ip_str_ipv4() {
        assert_eq!(extract_ip_str("127.0.0.1:80"), Some("127.0.0.1"));
    }

    #[test]
    fn test_extract_ip_str_ipv6() {
        assert_eq!(extract_ip_str("[::1]:80"), Some("::1"));
        assert_eq!(extract_ip_str("[2001:db8::1]:443"), Some("2001:db8::1"));
    }

    #[test]
    fn test_extract_ip_str_malformed_returns_none() {
        assert_eq!(extract_ip_str("[::1"), None); // unterminated bracket
        assert_eq!(extract_ip_str("no-colon-here"), None);
        assert_eq!(extract_ip_str(""), None);
    }

    #[test]
    fn test_parse_proc_addr_malformed_input_returns_empty() {
        assert_eq!(parse_proc_addr("not-a-valid-entry"), "");
        assert_eq!(parse_proc_addr(""), "");
        assert_eq!(parse_proc_addr("ZZZZZZZZ:0050"), "0.0.0.0:80"); // bad hex falls back to 0, never panics
    }
}
