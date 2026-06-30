//! Configuration file parsing for gateway.

use ipnet::IpNet;
use serde::Deserialize;
use std::path::Path;
use thiserror::Error;

#[derive(Error, Debug)]
pub enum ConfigError {
    #[error("failed to read config file: {0}")]
    Read(#[from] std::io::Error),
    #[error("failed to parse config: {0}")]
    Parse(#[from] serde_yaml::Error),
}

/// Top-level gateway configuration.
#[derive(Debug, Clone, Deserialize)]
pub struct GatewayConfig {
    pub scope: ScopeConfig,
    #[serde(default)]
    pub policy: PolicyConfig,
    pub logging: LoggingConfig,
    pub ipc: IpcConfig,
    #[serde(default)]
    pub watchdog: WatchdogConfig,
    #[serde(default)]
    pub network: NetworkConfig,
}

/// Policy engine settings.
#[derive(Debug, Clone, Deserialize)]
#[derive(Default)]
pub struct PolicyConfig {
    #[serde(default)]
    pub default_allow: bool,
    #[serde(default)]
    pub observe_only: bool,
}


/// Network controller settings.
///
/// gateway is Linux-only. It manages nftables FORWARD-chain rules on the Docker
/// bridge so sandbox's outbound traffic is gated at the network level.
#[derive(Debug, Clone, Deserialize)]
pub struct NetworkConfig {
    #[serde(default = "default_table_name")]
    pub table_name: String,
    /// Chain name for the FORWARD hook (filters sandbox's outbound traffic).
    #[serde(default = "default_forward_chain")]
    pub forward_chain: String,
    /// Chain name for the POSTROUTING/NAT hook (masquerades sandbox's packets).
    #[serde(default = "default_nat_chain")]
    pub nat_chain: String,
    /// Egress interface (optional; empty = any).
    #[serde(default)]
    pub interface: String,
    /// sandbox container IP address (e.g. "172.20.0.10").
    /// When set, only traffic sourced from this IP is filtered; other containers
    /// are unaffected. Leave empty to filter ALL forwarded traffic.
    #[serde(default)]
    pub sandbox_ip: String,
    /// Normalize the IP TTL of all sandbox-originated forwarded packets to
    /// this value, defeating simple OS-fingerprinting (p0f, `nmap -O`) that
    /// relies on the real Linux default TTL. None = no normalization.
    #[serde(default)]
    pub ttl_normalize: Option<u8>,
    /// Assign a random locally-administered MAC to `interface` at startup.
    /// Only meaningful when sandbox traffic shares an L2 segment with the
    /// target (internal/on-prem engagements) — has no effect on traffic
    /// already NATed onto a routed network.
    #[serde(default)]
    pub mac_randomization: bool,
}

impl Default for NetworkConfig {
    fn default() -> Self {
        Self {
            table_name:    default_table_name(),
            forward_chain: default_forward_chain(),
            nat_chain:     default_nat_chain(),
            interface:     String::new(),
            sandbox_ip:    String::new(),
            ttl_normalize: None,
            mac_randomization: false,
        }
    }
}

/// Scope rules: which networks are allowed/blocked, plus name-level
/// allow/block patterns the orchestrator enforces (the gateway sees only IPs in
/// packets, so it stores and rules on hostname patterns but cannot act on them
/// at the packet layer — the orchestrator consults `check_hostname` before it
/// resolves a name).
#[derive(Debug, Clone, Deserialize, Default)]
pub struct ScopeConfig {
    #[serde(default)]
    pub allow: Vec<IpNet>,
    #[serde(default)]
    pub block: Vec<IpNet>,
    /// Hostname glob patterns that are in scope. Empty = no name whitelist (any
    /// name may proceed to the IP-egress flow). Non-empty = whitelist mode: a
    /// name must match one of these or it is blocked. `*.example.com` matches
    /// `example.com` and any subdomain at any depth.
    #[serde(default)]
    pub hostname_allow: Vec<String>,
    /// Hostname glob patterns that are always out of scope — a hard, name-level
    /// deny that wins over everything (even Allow egress mode). Same wildcard
    /// semantics as `hostname_allow`.
    #[serde(default)]
    pub hostname_block: Vec<String>,
}

/// Logging and audit configuration.
#[derive(Debug, Clone, Deserialize)]
pub struct LoggingConfig {
    #[serde(default = "default_log_level")]
    pub level: String,
    #[serde(default = "default_audit_db")]
    pub audit_db: String,
}

/// IPC socket configuration.
#[derive(Debug, Clone, Deserialize)]
pub struct IpcConfig {
    #[serde(default = "default_socket_path")]
    pub socket_path: String,
    #[serde(default = "default_socket_mode")]
    pub socket_mode: u32,
}

/// Watchdog configuration.
#[derive(Debug, Clone, Deserialize)]
pub struct WatchdogConfig {
    #[serde(default = "default_heartbeat_interval")]
    pub heartbeat_interval_secs: u64,
    #[serde(default = "default_max_memory_mb")]
    pub max_memory_mb: u64,
    #[serde(default = "default_heartbeat_path")]
    pub heartbeat_path: String,
}

impl Default for WatchdogConfig {
    fn default() -> Self {
        Self {
            heartbeat_interval_secs: default_heartbeat_interval(),
            max_memory_mb: default_max_memory_mb(),
            heartbeat_path: default_heartbeat_path(),
        }
    }
}

fn default_log_level() -> String {
    "info".to_string()
}
fn default_audit_db() -> String {
    "/var/lib/yummi/gateway/audit.db".to_string()
}
fn default_socket_path() -> String {
    "/run/yummi/gateway.sock".to_string()
}
fn default_socket_mode() -> u32 {
    0o600
}
fn default_heartbeat_interval() -> u64 {
    5
}
fn default_max_memory_mb() -> u64 {
    256
}
fn default_heartbeat_path() -> String {
    "/var/run/yummi/gateway.heartbeat".to_string()
}
fn default_table_name() -> String {
    "yummi_gateway".to_string()
}
fn default_forward_chain() -> String {
    "yummi_forward".to_string()
}
fn default_nat_chain() -> String {
    "yummi_nat".to_string()
}

impl GatewayConfig {
    /// Load configuration from a YAML file.
    /// Returns defaults for missing fields.
    pub fn load(path: &str) -> Result<Self, ConfigError> {
        let contents = std::fs::read_to_string(Path::new(path))?;
        let cfg: Self = serde_yaml::from_str(&contents)?;
        Ok(cfg)
    }

    /// Create a config suitable for testing.
    pub fn test_default() -> Self {
        Self {
            scope: ScopeConfig {
                allow: vec![],
                block: vec![
                    "127.0.0.0/8".parse().expect("valid CIDR"),
                    "169.254.0.0/16".parse().expect("valid CIDR"),
                    "224.0.0.0/4".parse().expect("valid CIDR"),
                ],
                ..Default::default()
            },
            policy: PolicyConfig::default(),
            logging: LoggingConfig {
                level: "debug".to_string(),
                audit_db: ":memory:".to_string(),
            },
            ipc: IpcConfig {
                socket_path: std::env::temp_dir().join("yummi-gateway-test.sock").to_string_lossy().to_string(),
                socket_mode: 0o600,
            },
            watchdog: WatchdogConfig::default(),
            network: NetworkConfig {
                sandbox_ip: "172.20.0.10".to_string(),
                ..NetworkConfig::default()
            },
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_default_config() {
        let cfg = GatewayConfig::test_default();
        assert_eq!(cfg.logging.level, "debug");
        assert_eq!(cfg.scope.block.len(), 3);
    }

    #[test]
    fn test_parse_yaml() {
        let tmp = std::env::temp_dir();
        let yaml = format!(
            r#"
scope:
  allow:
    - 10.0.0.0/8
    - 192.168.1.0/24
  block:
    - 127.0.0.0/8
logging:
  level: info
  audit_db: {audit_db}
ipc:
  socket_path: {socket_path}
  socket_mode: 384
network:
  sandbox_ip: "172.20.0.10"
"#,
            audit_db = tmp.join("test.db").to_string_lossy(),
            socket_path = tmp.join("test.sock").to_string_lossy(),
        );
        let cfg: GatewayConfig = serde_yaml::from_str(&yaml).unwrap();
        assert_eq!(cfg.scope.allow.len(), 2);
        assert_eq!(cfg.scope.block.len(), 1);
        assert_eq!(cfg.ipc.socket_mode, 384);
        assert_eq!(cfg.network.sandbox_ip, "172.20.0.10");
    }

    #[test]
    fn test_network_config_fingerprint_fields_default_off() {
        let cfg = NetworkConfig::default();
        assert_eq!(cfg.ttl_normalize, None);
        assert!(!cfg.mac_randomization);
    }

    #[test]
    fn test_network_config_parses_fingerprint_fields_from_yaml() {
        let yaml = r#"
table_name: yummi_gw
forward_chain: yummi_forward
nat_chain: yummi_nat
interface: eth0
sandbox_ip: "172.20.0.10"
ttl_normalize: 64
mac_randomization: true
"#;
        let cfg: NetworkConfig = serde_yaml::from_str(yaml).unwrap();
        assert_eq!(cfg.ttl_normalize, Some(64));
        assert!(cfg.mac_randomization);
    }
}
