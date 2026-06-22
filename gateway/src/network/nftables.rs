//! Controls sandbox's outbound traffic by inserting rules into the FORWARD hook of
//! the Docker bridge network. Rules are keyed by sandbox's source IP so other
//! containers are unaffected.
//!
//! Fail-closed guarantee:
//! - At startup, a DROP rule is installed for sandbox's IP in the FORWARD chain.
//! - Explicit allow rules open individual CIDRs.
//! - On gateway restart, rules survive (NOT removed) — sandbox stays blocked until
//!   gateway restarts and re-evaluates policy.
//! - `--cleanup` removes the nftables table; only use for admin maintenance.
//!
//! Requires: CAP_NET_ADMIN (set in docker-compose.yml for the gateway service).

use std::io::Write as IoWrite;
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};

use crate::config::GatewayConfig;
use crate::network::NetworkError;

const NFT_BIN: &str = "/usr/sbin/nft";

/// Generate a random locally-administered, unicast MAC address suitable for
/// masking a real vendor OUI (e.g. avoiding a recognizable Docker/cloud
/// pattern on a shared L2 segment). Setting bit 1 of the first octet marks
/// it locally-administered; clearing bit 0 marks it unicast (not multicast).
fn random_locally_administered_mac() -> [u8; 6] {
    let mut mac = [0u8; 6];
    for b in mac.iter_mut() {
        *b = rand_byte();
    }
    mac[0] = (mac[0] | 0b0000_0010) & 0b1111_1110;
    mac
}

fn rand_byte() -> u8 {
    use std::time::{SystemTime, UNIX_EPOCH};
    // Crate already depends on no extra RNG crate for this one-shot, best-effort
    // value — nanosecond-jitter mixing is sufficient since this is a fingerprint
    // mask, not a cryptographic secret.
    let nanos = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().subsec_nanos();
    (nanos ^ nanos.rotate_left(13)) as u8
}

fn format_mac(mac: [u8; 6]) -> String {
    mac.iter().map(|b| format!("{:02x}", b)).collect::<Vec<_>>().join(":")
}

/// Linux nftables-based network controller.
///
/// Installs and manages an `inet` table with two chains:
/// - `yummi_forward`: FORWARD hook — filters sandbox's outbound traffic.
/// - `yummi_nat`:     POSTROUTING hook — masquerades sandbox's packets so they
///                    appear from the gateway container when forwarded.
pub struct NftablesController {
    table_name:    String,
    forward_chain: String,
    nat_chain:     String,
    sandbox_ip:    String,
    allow_cidrs:   Vec<String>,
    block_cidrs:   Vec<String>,
    ttl_normalize: Option<u8>,
    mac_randomization: bool,
    interface:     String,
    active:        AtomicBool,
}

impl NftablesController {
    pub fn new(cfg: &GatewayConfig) -> Self {
        Self {
            table_name:    cfg.network.table_name.clone(),
            forward_chain: cfg.network.forward_chain.clone(),
            nat_chain:     cfg.network.nat_chain.clone(),
            sandbox_ip:    cfg.network.sandbox_ip.clone(),
            allow_cidrs:   cfg.scope.allow.iter().map(|n| n.to_string()).collect(),
            block_cidrs:   cfg.scope.block.iter().map(|n| n.to_string()).collect(),
            ttl_normalize: cfg.network.ttl_normalize,
            mac_randomization: cfg.network.mac_randomization,
            interface:     cfg.network.interface.clone(),
            active:        AtomicBool::new(false),
        }
    }

    /// Install fail-closed FORWARD-chain rules.
    /// Idempotent: flushes and recreates the table each time.
    pub fn install_rules(&self) -> Result<(), NetworkError> {
        if self.mac_randomization {
            self.randomize_egress_mac(&self.interface);
        }

        // Atomically replace — delete first (ignore failure if not present).
        let _ = Command::new(NFT_BIN)
            .args(["delete", "table", "inet", &self.table_name])
            .output();

        let ruleset = self.build_ruleset();
        self.nft_apply(&ruleset)?;
        self.active.store(true, Ordering::SeqCst);

        tracing::info!(
            table = %self.table_name,
            sandbox_ip = %self.sandbox_ip,
            allow = self.allow_cidrs.len(),
            block  = self.block_cidrs.len(),
            "nftables FORWARD rules installed (fail-closed)"
        );
        Ok(())
    }

    /// Remove the gateway nftables table.
    /// WARN: sandbox traffic becomes unfiltered. Use only for admin maintenance.
    pub fn cleanup(&self) -> Result<(), NetworkError> {
        let out = Command::new(NFT_BIN)
            .args(["delete", "table", "inet", &self.table_name])
            .output()
            .map_err(|e| NetworkError::CleanupFailed(e.to_string()))?;

        if !out.status.success() {
            let msg = String::from_utf8_lossy(&out.stderr);
            return Err(NetworkError::CleanupFailed(msg.into_owned()));
        }

        self.active.store(false, Ordering::SeqCst);
        tracing::warn!(table = %self.table_name, "nftables rules removed — sandbox traffic UNCONTROLLED");
        Ok(())
    }

    /// Check whether gateway rules are currently installed.
    pub fn rules_active(&self) -> Result<bool, NetworkError> {
        let out = Command::new(NFT_BIN)
            .args(["list", "table", "inet", &self.table_name])
            .output()
            .map_err(|e| NetworkError::InstallFailed(e.to_string()))?;
        let active = out.status.success();
        self.active.store(active, Ordering::SeqCst);
        Ok(active)
    }

    /// Best-effort: assign a random locally-administered MAC to the
    /// configured egress interface. Never fails startup — a missing
    /// interface name, insufficient capability, or busy device degrades to
    /// "less stealthy", not "less safe". Fail-closed (DROP-all) is about
    /// network reachability, not fingerprint masking, and this runs before
    /// `install_rules()` swaps the FORWARD chain in, consistent with that
    /// ordering invariant.
    fn randomize_egress_mac(&self, interface: &str) {
        if interface.is_empty() {
            tracing::warn!("mac_randomization enabled but network.interface is empty — skipped");
            return;
        }
        let mac = format_mac(random_locally_administered_mac());
        let out = Command::new("ip")
            .args(["link", "set", interface, "address", &mac])
            .output();
        match out {
            Ok(o) if o.status.success() => {
                tracing::info!(interface, mac, "egress MAC randomized");
            }
            Ok(o) => {
                let stderr = String::from_utf8_lossy(&o.stderr);
                tracing::warn!(interface, mac, error = %stderr, "MAC randomization failed — continuing with existing MAC");
            }
            Err(e) => {
                tracing::warn!(interface, error = %e, "MAC randomization failed to execute — continuing with existing MAC");
            }
        }
    }

    // ── Internal ──────────────────────────────────────────────────────────────

    /// Dynamically add a target CIDR to the allowed_targets set with TTL.
    ///
    /// Uses `nft add element` which is atomic and does not flush existing rules.
    /// The set has `flags interval, timeout` so both host IPs and subnets work.
    pub fn add_allowed_target(&self, net: &ipnet::IpNet, ttl_secs: u64) -> Result<(), NetworkError> {
        // Strip /32 (or /128) for individual hosts — nft syntax is cleaner.
        let element = match net {
            ipnet::IpNet::V4(n) if n.prefix_len() == 32 => n.addr().to_string(),
            ipnet::IpNet::V6(n) if n.prefix_len() == 128 => n.addr().to_string(),
            other => other.to_string(),
        };

        let cmd = format!(
            "add element inet {} allowed_targets {{ {} timeout {}s }}",
            self.table_name, element, ttl_secs,
        );

        let out = Command::new(NFT_BIN)
            .args(["-e", &cmd])
            .output()
            .map_err(|e| NetworkError::InstallFailed(format!("nft add element: {e}")))?;

        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr);
            return Err(NetworkError::InstallFailed(format!(
                "nft add element failed: {stderr}"
            )));
        }

        tracing::info!(target = %element, ttl_secs, "nftables: target added to allowed_targets");
        Ok(())
    }

    /// Build the nftables ruleset as a single atomic script.
    ///
    /// FORWARD chain design:
    ///   1. Pass non-sandbox traffic immediately (other containers untouched).
    ///   2. Allow established/related return traffic.
    ///   3. Block list (highest priority explicit denies).
    ///   4. Static allow list (from config) + dynamic `allowed_targets` set.
    ///   5. Allow DNS (UDP 53) for resolution.
    ///   6. Rate-limited log + DROP for everything else.
    pub(crate) fn build_ruleset(&self) -> String {
        let mut s = String::new();

        s.push_str(&format!("table inet {} {{\n", self.table_name));

        // ── Dynamic target set — populated via IPC AddTemporaryRule ──────────
        // flags interval: supports CIDR notation (e.g. 10.0.0.0/24)
        // flags timeout:  TTL expiry managed by kernel automatically
        s.push_str("  set allowed_targets {\n");
        s.push_str("    type ipv4_addr\n");
        s.push_str("    flags interval, timeout\n");
        s.push_str("  }\n\n");

        // ── FORWARD chain ──────────────────────────────────────────────────
        s.push_str(&format!("  chain {} {{\n", self.forward_chain));
        // policy accept: we only drop sandbox's unauthorized traffic, not everything.
        s.push_str("    type filter hook forward priority 0; policy accept;\n");

        // Fingerprint minimization: normalize TTL on all sandbox-originated
        // packets before any verdict is reached. This is a bare statement
        // (no accept/drop) so it falls through to the rest of the chain
        // unchanged — every later rule still applies, just with a rewritten
        // TTL on matching packets.
        if let Some(ttl) = self.ttl_normalize {
            if !self.sandbox_ip.is_empty() {
                if let Ok(ip) = self.sandbox_ip.parse::<std::net::IpAddr>() {
                    s.push_str(&format!("    ip saddr {} ip ttl set {}\n", ip, ttl));
                }
            }
        }

        // Pass through traffic NOT originating from sandbox (other containers, host).
        if !self.sandbox_ip.is_empty() {
            if let Ok(ip) = self.sandbox_ip.parse::<std::net::IpAddr>() {
                s.push_str(&format!("    ip saddr != {} accept\n", ip));
            } else {
                tracing::warn!(sandbox_ip = %self.sandbox_ip, "invalid sandbox_ip — applying rules to ALL forwarded traffic");
            }
        }

        // Allow return traffic for sandbox-initiated connections.
        s.push_str("    ct state established,related accept\n");

        // Block rules — re-validate each CIDR to prevent injection.
        for cidr in &self.block_cidrs {
            if let Ok(net) = cidr.parse::<ipnet::IpNet>() {
                let safe = net.to_string();
                if safe.contains(':') {
                    s.push_str(&format!("    ip6 daddr {} drop\n", safe));
                } else {
                    s.push_str(&format!("    ip daddr {} drop\n", safe));
                }
            } else {
                tracing::error!(cidr, "invalid CIDR in block list — skipped");
            }
        }

        // Static allow rules from config.
        for cidr in &self.allow_cidrs {
            if let Ok(net) = cidr.parse::<ipnet::IpNet>() {
                let safe = net.to_string();
                if safe.contains(':') {
                    s.push_str(&format!("    ip6 daddr {} accept\n", safe));
                } else {
                    s.push_str(&format!("    ip daddr {} accept\n", safe));
                }
            } else {
                tracing::error!(cidr, "invalid CIDR in allow list — skipped");
            }
        }

        // Dynamic targets unlocked via IPC AddTemporaryRule.
        s.push_str("    ip daddr @allowed_targets accept\n");

        // DNS is always permitted so sandbox can resolve target hostnames.
        s.push_str("    udp dport 53 accept\n");

        // Rate-limited log + drop for everything else.
        s.push_str("    limit rate 5/minute log prefix \"yummi_gateway_drop: \" drop\n");

        s.push_str("  }\n\n");

        // ── NAT / MASQUERADE chain ─────────────────────────────────────────
        // Rewrites sandbox's source IP to gateway's so external hosts see gateway as
        // the originator. Required when gateway is sandbox's default gateway.
        s.push_str(&format!("  chain {} {{\n", self.nat_chain));
        s.push_str("    type nat hook postrouting priority 100;\n");
        if !self.sandbox_ip.is_empty() {
            if let Ok(ip) = self.sandbox_ip.parse::<std::net::IpAddr>() {
                s.push_str(&format!("    ip saddr {} masquerade\n", ip));
            }
        }
        s.push_str("  }\n");

        s.push_str("}\n");
        s
    }

    /// Feed a ruleset script to `nft -f -` via stdin.
    fn nft_apply(&self, ruleset: &str) -> Result<(), NetworkError> {
        let mut child = Command::new(NFT_BIN)
            .args(["-f", "-"])
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|e| NetworkError::InstallFailed(format!("spawn nft: {e}")))?;

        if let Some(mut stdin) = child.stdin.take() {
            stdin
                .write_all(ruleset.as_bytes())
                .map_err(|e| NetworkError::InstallFailed(format!("write ruleset: {e}")))?;
        }

        let result = child
            .wait_with_output()
            .map_err(|e| NetworkError::InstallFailed(format!("nft wait: {e}")))?;

        if !result.status.success() {
            let stderr = String::from_utf8_lossy(&result.stderr);
            return Err(NetworkError::InstallFailed(format!("nft: {stderr}")));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{IpcConfig, LoggingConfig, NetworkConfig, GatewayConfig, PolicyConfig, ScopeConfig, WatchdogConfig};

    fn test_config() -> GatewayConfig {
        GatewayConfig {
            scope: ScopeConfig {
                allow: vec![
                    "10.0.0.0/24".parse().unwrap(),
                    "192.168.1.0/24".parse().unwrap(),
                ],
                block: vec![
                    "127.0.0.0/8".parse().unwrap(),
                    "169.254.0.0/16".parse().unwrap(),
                ],
            },
            policy: PolicyConfig::default(),
            logging: LoggingConfig {
                level: "info".to_string(),
                audit_db: ":memory:".to_string(),
            },
            ipc: IpcConfig {
                socket_path: std::env::temp_dir().join("yummi-test.sock").to_string_lossy().to_string(),
                socket_mode: 0o600,
            },
            watchdog: WatchdogConfig::default(),
            network: NetworkConfig {
                table_name:    "yummi_gateway".to_string(),
                forward_chain: "yummi_forward".to_string(),
                nat_chain:     "yummi_nat".to_string(),
                interface:     String::new(),
                sandbox_ip:    "172.20.0.10".to_string(),
                ttl_normalize: None,
                mac_randomization: false,
            },
        }
    }

    #[test]
    fn test_ruleset_forward_chain() {
        let ctrl = NftablesController::new(&test_config());
        let ruleset = ctrl.build_ruleset();

        assert!(ruleset.contains("table inet yummi_gateway"));
        assert!(ruleset.contains("chain yummi_forward"));
        assert!(ruleset.contains("chain yummi_nat"));
        assert!(ruleset.contains("hook forward"));
        assert!(ruleset.contains("hook postrouting"));
        // sandbox exclusion rule
        assert!(ruleset.contains("ip saddr != 172.20.0.10 accept"));
        // established/related for return traffic
        assert!(ruleset.contains("ct state established,related accept"));
        // dynamic targets set
        assert!(ruleset.contains("set allowed_targets"));
        assert!(ruleset.contains("flags interval, timeout"));
        assert!(ruleset.contains("ip daddr @allowed_targets accept"));
    }

    #[test]
    fn test_ruleset_block_before_allow() {
        let ctrl = NftablesController::new(&test_config());
        let ruleset = ctrl.build_ruleset();

        let block_pos = ruleset.find("127.0.0.0/8").unwrap();
        let allow_pos = ruleset.find("10.0.0.0/24").unwrap();
        assert!(block_pos < allow_pos, "block rules must precede allow rules");
    }

    #[test]
    fn test_ruleset_cidr_family_prefix() {
        let ctrl = NftablesController::new(&test_config());
        let ruleset = ctrl.build_ruleset();

        // IPv4 uses `ip daddr`, not `ip6 daddr`
        assert!(ruleset.contains("ip daddr 127.0.0.0/8 drop"));
        assert!(ruleset.contains("ip daddr 10.0.0.0/24 accept"));
        assert!(!ruleset.contains("ip6 daddr 127.0.0.0/8"));
    }

    #[test]
    fn test_ruleset_dns_always_allowed() {
        let ctrl = NftablesController::new(&test_config());
        assert!(ctrl.build_ruleset().contains("udp dport 53 accept"));
    }

    #[test]
    fn test_ruleset_nat_masquerade() {
        let ctrl = NftablesController::new(&test_config());
        let ruleset = ctrl.build_ruleset();
        assert!(ruleset.contains("masquerade"));
        assert!(ruleset.contains("ip saddr 172.20.0.10 masquerade"));
    }

    #[test]
    fn test_random_mac_is_locally_administered_and_unicast() {
        for _ in 0..20 {
            let mac = random_locally_administered_mac();
            assert_eq!(mac[0] & 0b0000_0010, 0b0000_0010, "locally-administered bit must be set");
            assert_eq!(mac[0] & 0b0000_0001, 0, "unicast bit must be clear");
        }
    }

    #[test]
    fn test_format_mac_produces_colon_separated_hex() {
        let formatted = format_mac([0x02, 0xAB, 0x00, 0x11, 0xFF, 0x9c]);
        assert_eq!(formatted, "02:ab:00:11:ff:9c");
    }

    #[test]
    fn test_build_ruleset_includes_ttl_normalize_when_configured() {
        let mut cfg = test_config();
        cfg.network.sandbox_ip = "172.20.0.10".to_string();
        cfg.network.ttl_normalize = Some(64);
        let ctrl = NftablesController::new(&cfg);
        let ruleset = ctrl.build_ruleset();
        assert!(ruleset.contains("ip saddr 172.20.0.10 ip ttl set 64"));
    }

    #[test]
    fn test_build_ruleset_omits_ttl_normalize_when_unset() {
        let mut cfg = test_config();
        cfg.network.sandbox_ip = "172.20.0.10".to_string();
        cfg.network.ttl_normalize = None;
        let ctrl = NftablesController::new(&cfg);
        let ruleset = ctrl.build_ruleset();
        assert!(!ruleset.contains("ip ttl set"));
    }

    #[test]
    fn test_ruleset_no_sandbox_ip_no_saddr_filter() {
        let mut cfg = test_config();
        cfg.network.sandbox_ip = String::new();
        let ctrl = NftablesController::new(&cfg);
        let ruleset = ctrl.build_ruleset();
        // Without sandbox_ip, no source-IP pass-through rule for other containers
        assert!(!ruleset.contains("ip saddr !="));
    }
}
