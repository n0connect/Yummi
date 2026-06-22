//! IPC server — Unix domain socket for orchestrator communication.
//!
//! Security model:
//! - Read-only ops (Ping, CheckScope, GetStatus) are open to any local process.
//! - Write ops (AddTemporaryRule, RemoveRule) require the connecting process to
//!   match an allowlisted UID (Linux: SO_PEERCRED; macOS/other: bypassed).
//! - A shared Semaphore caps concurrent connections at MAX_CONNECTIONS.

pub mod protocol;

use std::sync::Arc;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::UnixListener;
use tokio::sync::Semaphore;
use tracing::{error, info, warn};

use self::protocol::{AuditRowView, IpcRequest, IpcResponse, RuleView};
use crate::audit::AuditLog;
use crate::network::NftablesController;
use crate::policy::rules::{RuleAction, RuleSource};
use crate::policy::PolicyEngine;

/// Maximum concurrent IPC connections. Prevents file-descriptor exhaustion.
const MAX_CONNECTIONS: usize = 32;

/// IPC server exposing policy queries to orchestrator.
pub struct IpcServer {
    socket_path: String,
    policy: PolicyEngine,
    audit: AuditLog,
    /// UIDs allowed to perform write operations. Empty = allow any local UID.
    allowed_uids: Vec<u32>,
    /// Caps concurrent connections to prevent DoS.
    semaphore: Arc<Semaphore>,
    /// Live nftables controller — when present, AddTemporaryRule also updates kernel rules.
    ctrl: Option<Arc<NftablesController>>,
}

impl IpcServer {
    pub fn new(cfg: &crate::config::IpcConfig, policy: PolicyEngine, audit: AuditLog) -> Self {
        // Default: allow root (0) and current process UID.
        let current_uid = {
            #[cfg(target_os = "linux")]
            { unsafe { libc_uid() } }
            #[cfg(not(target_os = "linux"))]
            { 0u32 }
        };
        let allowed_uids = if current_uid == 0 {
            vec![0u32]
        } else {
            vec![0u32, current_uid]
        };

        Self {
            socket_path: cfg.socket_path.clone(),
            policy,
            audit,
            allowed_uids,
            semaphore: Arc::new(Semaphore::new(MAX_CONNECTIONS)),
            ctrl: None,
        }
    }

    /// Attach the live nftables controller so write ops also update kernel rules.
    pub fn with_controller(mut self, ctrl: Arc<NftablesController>) -> Self {
        self.ctrl = Some(ctrl);
        self
    }

    /// Run the IPC server loop.
    pub async fn run(self) {
        let _ = std::fs::remove_file(&self.socket_path);

        if let Some(parent) = std::path::Path::new(&self.socket_path).parent() {
            let _ = std::fs::create_dir_all(parent);
        }

        let listener = match UnixListener::bind(&self.socket_path) {
            Ok(l) => l,
            Err(e) => {
                error!(path = %self.socket_path, error = %e, "failed to bind IPC socket");
                return;
            }
        };

        info!(path = %self.socket_path, max_connections = MAX_CONNECTIONS, "IPC server listening");

        let sem = self.semaphore.clone();
        loop {
            // Acquire a connection slot before accepting — this blocks new
            // connections when MAX_CONNECTIONS are active (back-pressure).
            let permit = match sem.clone().acquire_owned().await {
                Ok(p) => p,
                Err(_) => break, // semaphore closed
            };

            match listener.accept().await {
                Ok((stream, _addr)) => {
                    let policy = self.policy.clone();
                    let audit = self.audit.clone();
                    let allowed_uids = self.allowed_uids.clone();
                    let ctrl = self.ctrl.clone();
                    tokio::spawn(async move {
                        let _permit = permit; // released when handler finishes
                        if let Err(e) = handle_connection(stream, policy, audit, allowed_uids, ctrl).await {
                            warn!(error = %e, "IPC connection error");
                        }
                    });
                }
                Err(e) => {
                    drop(permit);
                    error!(error = %e, "IPC accept error");
                }
            }
        }
    }
}

/// Handle a single IPC connection.
/// Protocol: 4-byte big-endian length prefix + JSON payload.
async fn handle_connection(
    mut stream: tokio::net::UnixStream,
    policy: PolicyEngine,
    audit: AuditLog,
    allowed_uids: Vec<u32>,
    ctrl: Option<Arc<NftablesController>>,
) -> Result<(), Box<dyn std::error::Error>> {
    // Resolve peer UID once per connection (Linux only via SO_PEERCRED).
    let peer_uid: Option<u32> = peer_uid_from_stream(&stream);

    loop {
        let mut len_buf = [0u8; 4];
        if stream.read_exact(&mut len_buf).await.is_err() {
            return Ok(()); // client disconnected
        }
        let len = u32::from_be_bytes(len_buf) as usize;

        if len > 1_048_576 {
            warn!(len, "IPC message too large, dropping connection");
            return Ok(());
        }

        let mut buf = vec![0u8; len];
        stream.read_exact(&mut buf).await?;

        let request: IpcRequest = serde_json::from_slice(&buf)?;
        let response = process_request(request, &policy, peer_uid, &allowed_uids, ctrl.as_deref(), &audit);

        let resp_bytes = serde_json::to_vec(&response)?;
        let resp_len = (resp_bytes.len() as u32).to_be_bytes();
        stream.write_all(&resp_len).await?;
        stream.write_all(&resp_bytes).await?;
    }
}

/// Returns true if the peer UID is allowed to execute write operations.
fn is_write_authorized(peer_uid: Option<u32>, allowed_uids: &[u32]) -> bool {
    if allowed_uids.is_empty() {
        return true; // no restriction configured
    }
    match peer_uid {
        Some(uid) => allowed_uids.contains(&uid),
        // On non-Linux we skip auth (dev-only path).
        None => cfg!(not(target_os = "linux")),
    }
}

fn process_request(
    req: IpcRequest,
    policy: &PolicyEngine,
    peer_uid: Option<u32>,
    allowed_uids: &[u32],
    ctrl: Option<&NftablesController>,
    audit: &AuditLog,
) -> IpcResponse {
    match req {
        // ── Read-only ops — no UID check ──────────────────────────────────
        IpcRequest::CheckScope { ip } => match ip.parse::<std::net::IpAddr>() {
            Ok(addr) => {
                let in_scope = policy.is_in_scope(addr);
                IpcResponse::ScopeResult { ip, in_scope }
            }
            Err(_) => IpcResponse::Error {
                message: format!("invalid IP address: {ip}"),
            },
        },
        IpcRequest::GetStatus => IpcResponse::Status {
            level: 0,
            rules_active: true,
            uptime_secs: 0,
        },
        IpcRequest::Ping => IpcResponse::Pong,

        IpcRequest::ListRules => {
            let rules: Vec<RuleView> = policy
                .list_rules()
                .into_iter()
                .map(|r| RuleView {
                    id: r.id,
                    action: match r.action {
                        RuleAction::Allow => "allow".to_string(),
                        RuleAction::Block => "block".to_string(),
                    },
                    network: r.network.to_string(),
                    description: r.description,
                    expires_at: r.expires_at,
                    source: match r.source {
                        RuleSource::Config => "config".to_string(),
                        RuleSource::Agent => "agent".to_string(),
                        RuleSource::Manual => "manual".to_string(),
                    },
                })
                .collect();
            IpcResponse::RuleList { rules }
        }
        IpcRequest::GetAuditLog { limit } => match audit.recent_rows(limit) {
            Ok((rows, chain_verified)) => IpcResponse::AuditLog {
                rows: rows
                    .into_iter()
                    .map(|r| AuditRowView {
                        id: r.id,
                        timestamp: r.timestamp,
                        src_ip: r.src_ip,
                        dst_ip: r.dst_ip,
                        decision: r.decision,
                        reason: r.reason,
                    })
                    .collect(),
                chain_verified,
            },
            Err(e) => IpcResponse::Error {
                message: format!("audit log read failed: {e}"),
            },
        },

        // ── Write ops — require allowlisted UID ───────────────────────────
        IpcRequest::AddTemporaryRule { network, ttl_secs, description } => {
            if !is_write_authorized(peer_uid, allowed_uids) {
                warn!(peer_uid, "AddTemporaryRule denied: UID not allowlisted");
                return IpcResponse::Error {
                    message: "permission denied: UID not allowlisted for write operations".to_string(),
                };
            }
            match network.parse::<ipnet::IpNet>() {
                Ok(net) => {
                    let desc = description.unwrap_or_else(|| format!("Temporary rule: {}", network));
                    let (rule_id, expires_at) = policy.add_temporary_allow(net.clone(), ttl_secs, desc.clone());

                    // Also unlock at the nftables kernel level so sandbox's packets actually reach the target.
                    if let Some(c) = ctrl {
                        if let Err(e) = c.add_allowed_target(&net, ttl_secs) {
                            warn!(network = %network, error = %e, "nftables target unlock failed (policy added, kernel rule skipped)");
                        }
                    }

                    let now_ns = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap()
                        .as_nanos() as i64;
                    if let Err(e) = audit.record_rule_event(now_ns, &network, "rule_added", &format!("[agent] {desc}")) {
                        warn!(error = %e, "failed to audit agent rule add (rule itself succeeded)");
                    }

                    IpcResponse::RuleAdded { rule_id, expires_at }
                }
                Err(_) => IpcResponse::Error {
                    message: format!("invalid network CIDR: {network}"),
                },
            }
        }
        IpcRequest::RemoveRule { rule_id } => {
            if !is_write_authorized(peer_uid, allowed_uids) {
                warn!(peer_uid, "RemoveRule denied: UID not allowlisted");
                return IpcResponse::Error {
                    message: "permission denied: UID not allowlisted for write operations".to_string(),
                };
            }
            if policy.remove_rule(&rule_id) {
                let now_ns = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_nanos() as i64;
                if let Err(e) = audit.record_rule_event(now_ns, &rule_id, "rule_removed", "[removed]") {
                    warn!(error = %e, "failed to audit rule removal (removal itself succeeded)");
                }
                IpcResponse::RuleRemoved { rule_id }
            } else {
                IpcResponse::Error {
                    message: format!("rule not found: {rule_id}"),
                }
            }
        }

        // ── Write op — requires allowlisted UID ───────────────────────────
        IpcRequest::AddManualRule { network, ttl_secs, reason } => {
            if !is_write_authorized(peer_uid, allowed_uids) {
                warn!(peer_uid, "AddManualRule denied: UID not allowlisted");
                return IpcResponse::Error {
                    message: "permission denied: UID not allowlisted for write operations".to_string(),
                };
            }
            if reason.trim().is_empty() {
                return IpcResponse::Error {
                    message: "reason is required for manual rules".to_string(),
                };
            }
            match network.parse::<ipnet::IpNet>() {
                Ok(net) => {
                    let (rule_id, expires_at) = policy.add_manual_allow(net.clone(), ttl_secs, reason.clone());
                    if let Some(c) = ctrl {
                        if let Err(e) = c.add_allowed_target(&net, ttl_secs) {
                            warn!(network = %network, error = %e, "nftables target unlock failed (policy added, kernel rule skipped)");
                        }
                    }
                    let now_ns = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap()
                        .as_nanos() as i64;
                    if let Err(e) = audit.record_rule_event(now_ns, &network, "rule_added", &format!("[manual] {reason}")) {
                        warn!(error = %e, "failed to audit manual rule add (rule itself succeeded)");
                    }
                    IpcResponse::RuleAdded { rule_id, expires_at }
                }
                Err(_) => IpcResponse::Error {
                    message: format!("invalid network CIDR: {network}"),
                },
            }
        }
    }
}

/// Extract the peer UID from a Unix stream via SO_PEERCRED (Linux only).
fn peer_uid_from_stream(stream: &tokio::net::UnixStream) -> Option<u32> {
    #[cfg(target_os = "linux")]
    {
        match stream.peer_cred() {
            Ok(cred) => Some(cred.uid()),
            Err(e) => {
                warn!(error = %e, "failed to read peer credentials");
                None
            }
        }
    }
    #[cfg(not(target_os = "linux"))]
    {
        let _ = stream;
        None // dev mode — skip auth
    }
}

#[cfg(target_os = "linux")]
unsafe fn libc_uid() -> u32 {
    libc::getuid()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_policy() -> PolicyEngine {
        let cfg = crate::config::ScopeConfig {
            allow: vec!["10.0.0.0/8".parse().unwrap()],
            block: vec![],
        };
        PolicyEngine::new(&cfg)
    }

    #[test]
    fn test_process_scope_check() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::CheckScope { ip: "10.0.0.1".to_string() },
            &policy,
            None,
            &[],
            None,
            &audit,
        );
        match resp {
            IpcResponse::ScopeResult { in_scope, .. } => assert!(in_scope),
            _ => panic!("unexpected response"),
        }
    }

    #[test]
    fn test_process_ping() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(IpcRequest::Ping, &policy, None, &[], None, &audit);
        assert!(matches!(resp, IpcResponse::Pong));
    }

    #[test]
    fn test_write_denied_unknown_uid() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        // UID 9999 is not in the allowlist [0]
        let resp = process_request(
            IpcRequest::AddTemporaryRule {
                network: "1.2.3.0/24".to_string(),
                ttl_secs: 60,
                description: None,
            },
            &policy,
            Some(9999),
            &[0u32],
            None,
            &audit,
        );
        assert!(matches!(resp, IpcResponse::Error { .. }));
    }

    #[test]
    fn test_write_allowed_root() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::AddTemporaryRule {
                network: "1.2.3.0/24".to_string(),
                ttl_secs: 60,
                description: None,
            },
            &policy,
            Some(0),
            &[0u32],
            None, // no live controller in unit tests
            &audit,
        );
        assert!(matches!(resp, IpcResponse::RuleAdded { .. }));
    }

    #[test]
    fn test_is_write_authorized() {
        assert!(is_write_authorized(Some(0), &[0]));
        assert!(is_write_authorized(Some(1000), &[0, 1000]));
        assert!(!is_write_authorized(Some(9999), &[0]));
        assert!(is_write_authorized(None, &[])); // no restriction
    }

    #[test]
    fn test_list_rules_no_auth_required() {
        let policy = make_policy();
        policy.add_manual_allow("10.0.0.1/32".parse().unwrap(), 600, "test".to_string());
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(IpcRequest::ListRules, &policy, None, &[0], None, &audit);
        match resp {
            IpcResponse::RuleList { rules } => assert_eq!(rules.len(), 1),
            other => panic!("unexpected response: {other:?}"),
        }
    }

    #[test]
    fn test_add_manual_rule_denied_without_allowlisted_uid() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::AddManualRule {
                network: "10.0.0.1/32".to_string(),
                ttl_secs: 600,
                reason: "test".to_string(),
            },
            &policy,
            Some(9999), // not in allowed_uids
            &[0],
            None,
            &audit,
        );
        match resp {
            IpcResponse::Error { message } => assert!(message.contains("permission denied")),
            other => panic!("expected permission denied, got {other:?}"),
        }
    }

    #[test]
    fn test_add_manual_rule_rejects_empty_reason() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::AddManualRule {
                network: "10.0.0.1/32".to_string(),
                ttl_secs: 600,
                reason: "  ".to_string(),
            },
            &policy,
            Some(0),
            &[0],
            None,
            &audit,
        );
        match resp {
            IpcResponse::Error { message } => assert!(message.contains("reason is required")),
            other => panic!("expected reason-required error, got {other:?}"),
        }
    }

    #[test]
    fn test_add_manual_rule_writes_audit_row() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let _ = process_request(
            IpcRequest::AddManualRule {
                network: "10.0.0.1/32".to_string(),
                ttl_secs: 600,
                reason: "emergency".to_string(),
            },
            &policy,
            Some(0),
            &[0],
            None,
            &audit,
        );
        let (rows, _) = audit.recent_rows(10).expect("recent rows");
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].decision, "rule_added");
        assert!(rows[0].reason.starts_with("[manual]"));
    }
}
