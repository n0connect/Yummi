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
                let dst_raw = dst.trim_matches(|c| c == '[' || c == ']');
                let dst_ip_str = if let Some(p) = dst_raw.rfind(':') {
                    &dst_raw[..p]
                } else {
                    dst_raw
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
        return format!("{}:{}", ip, port);
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
        return format!("[{:x?}]:{}", bytes, port);
    }
    String::new()
}
