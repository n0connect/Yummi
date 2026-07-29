//! IPC server — Unix domain socket for orchestrator communication.
//!
//! Security model:
//! - Read-only ops (Ping, CheckScope, GetStatus) are open to any local process.
//! - Write ops (AddTemporaryRule, RemoveRule) require the connecting process to
//!   match an allowlisted UID (Linux: SO_PEERCRED; macOS/other: bypassed).
//! - A shared Semaphore caps concurrent connections at MAX_CONNECTIONS.

pub mod protocol;

use std::os::unix::fs::PermissionsExt;
use std::sync::Arc;
use std::time::Instant;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::UnixListener;
use tokio::sync::Semaphore;
use tracing::{error, info, warn};

use self::protocol::{AuditRowView, IpcRequest, IpcResponse, RuleView};
use crate::audit::AuditLog;
use crate::network::NftablesController;
use crate::policy::rules::{EgressMode, RuleAction, RuleSource};
use crate::policy::{EgressVerdict, PolicyEngine};

/// Maximum concurrent IPC connections. Prevents file-descriptor exhaustion.
const MAX_CONNECTIONS: usize = 32;

/// IPC server exposing policy queries to orchestrator.
pub struct IpcServer {
    socket_path: String,
    /// Unix file mode applied to the bound socket after creation (config
    /// `ipc.socket_mode`, default 0o600). Defense-in-depth for deployments
    /// where the socket path is reachable by more than gateway/orchestrator's
    /// own containers — SO_PEERCRED UID gating gates write ops regardless,
    /// but a wide-open socket file is still a wider surface than necessary.
    socket_mode: u32,
    policy: PolicyEngine,
    audit: AuditLog,
    /// UIDs allowed to perform write operations. Empty = allow any local UID.
    allowed_uids: Vec<u32>,
    /// Caps concurrent connections to prevent DoS.
    semaphore: Arc<Semaphore>,
    /// Live nftables controller — when present, AddTemporaryRule also updates kernel rules.
    ctrl: Option<Arc<NftablesController>>,
    /// Process start time, for GetStatus's uptime_secs.
    started_at: Instant,
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
            socket_mode: cfg.socket_mode,
            policy,
            audit,
            allowed_uids,
            semaphore: Arc::new(Semaphore::new(MAX_CONNECTIONS)),
            ctrl: None,
            started_at: Instant::now(),
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

        // UnixListener::bind creates the socket file with a mode governed by
        // the process umask, NOT `ipc.socket_mode` — the config field was
        // parsed and tested but never actually applied to the file, so an
        // operator setting socket_mode: 0o600 got no enforcement at all. Set
        // it explicitly now that the file exists.
        if let Err(e) = std::fs::set_permissions(
            &self.socket_path,
            std::fs::Permissions::from_mode(self.socket_mode),
        ) {
            error!(path = %self.socket_path, mode = format!("{:o}", self.socket_mode), error = %e,
                "failed to set IPC socket file permissions — socket may be more widely reachable than configured");
        }

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
                    let started_at = self.started_at;
                    tokio::spawn(async move {
                        let _permit = permit; // released when handler finishes
                        if let Err(e) = handle_connection(stream, policy, audit, allowed_uids, ctrl, started_at).await {
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
    started_at: Instant,
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
        let response = process_request(request, &policy, peer_uid, &allowed_uids, ctrl.as_deref(), &audit, started_at);

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
    started_at: Instant,
) -> IpcResponse {
    match req {
        // ── Read-only ops — no UID check ──────────────────────────────────
        IpcRequest::CheckScope { ip } => {
            // A non-IP target (bare hostname) is not an error: the gateway has
            // no resolver, so the verdict for it is decided purely by the
            // egress mode (check_egress_unresolved). This lets Allow mode pass
            // hostnames like "google.com" through instead of rejecting them.
            let verdict = match ip.parse::<std::net::IpAddr>() {
                Ok(addr) => policy.check_egress(addr),
                Err(_) => policy.check_egress_unresolved(),
            };
            let verdict_str = match verdict {
                EgressVerdict::Allow => "allow",
                EgressVerdict::Block => "block",
                EgressVerdict::NeedsApproval => "needs_approval",
            };
            // Forensic trail: a scope check that did NOT allow the target is a
            // "proposed but blocked/queued" event. Record it in the tamper-
            // evident chain so a denied target leaves an auditable trace even
            // though no connection was ever attempted (the observer only logs
            // actual connection attempts). Allows are not chained here —
            // they're already represented by the rule_added event that opens
            // them, and an allowed scope check makes no security decision worth
            // a row.
            if verdict != EgressVerdict::Allow {
                let now_ns = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .map(|d| d.as_nanos() as i64)
                    .unwrap_or(0);
                if let Err(e) = audit.record_decision(
                    now_ns, None, "orchestrator", &ip, None, None, "scope_check",
                    verdict_str, "scope_check_rejected",
                ) {
                    warn!(error = %e, "failed to record scope-check rejection in audit");
                }
            }
            IpcResponse::ScopeResult {
                ip,
                in_scope: verdict == EgressVerdict::Allow,
                verdict: verdict_str.to_string(),
            }
        }
        // `level` has no implemented multi-tier semantics elsewhere in the
        // gateway (the observer's "Level 0" label refers to itself, not a
        // general enforcement level) — left at 0 rather than inventing a
        // value. `rules_active` is genuinely always true here: main.rs aborts
        // startup if `install_rules()` fails, so by the time the IPC socket
        // is even listening, the fail-closed FORWARD rules are installed.
        // `uptime_secs` is real elapsed process time, not a stub.
        IpcRequest::GetStatus => IpcResponse::Status {
            level: 0,
            rules_active: true,
            uptime_secs: started_at.elapsed().as_secs(),
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
        IpcRequest::GetFingerprintConfig => IpcResponse::FingerprintConfig {
            ttl_normalize: ctrl.and_then(|c| c.ttl_normalize()),
            mac_randomization: ctrl.map(|c| c.mac_randomization()).unwrap_or(false),
        },
        IpcRequest::GetEgressMode => IpcResponse::EgressMode {
            mode: policy.egress_mode().as_str().to_string(),
        },
        IpcRequest::CheckHostname { host } => {
            let verdict = policy.check_hostname(&host);
            // Forensic trail: a name-level block is a "proposed but blocked by
            // hostname policy" event — record it in the tamper-evident chain
            // (the orchestrator enforces it by not resolving/running the name).
            if verdict == crate::policy::hostname::HostnameVerdict::Block {
                let now_ns = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .map(|d| d.as_nanos() as i64)
                    .unwrap_or(0);
                if let Err(e) = audit.record_decision(
                    now_ns, None, "orchestrator", &host, None, None, "hostname_check",
                    "block", "hostname_blocked",
                ) {
                    warn!(error = %e, "failed to record hostname block in audit");
                }
            }
            IpcResponse::HostnameResult {
                host,
                verdict: verdict.as_str().to_string(),
            }
        }
        IpcRequest::GetHostnamePolicy => {
            let (allow, block) = policy.hostname_patterns();
            IpcResponse::HostnamePolicy { allow, block }
        }

        // ── Write ops — require allowlisted UID ───────────────────────────
        IpcRequest::AddTemporaryRule { network, ttl_secs, description } => {
            if !is_write_authorized(peer_uid, allowed_uids) {
                warn!(peer_uid, "AddTemporaryRule denied: UID not allowlisted");
                return IpcResponse::Error {
                    message: "permission denied: UID not allowlisted for write operations".to_string(),
                };
            }
            if network.len() > 45 {
                return IpcResponse::Error {
                    message: "network CIDR string too long".to_string(),
                };
            }
            if ttl_secs > 86400 {
                return IpcResponse::Error {
                    message: "ttl_secs exceeds maximum limit (86400)".to_string(),
                };
            }
            if let Some(ref d) = description {
                if d.len() > 500 {
                    return IpcResponse::Error {
                        message: "description exceeds maximum limit (500)".to_string(),
                    };
                }
            }
            match network.parse::<ipnet::IpNet>() {
                Ok(net) => {
                    let desc = description.unwrap_or_else(|| format!("Temporary rule: {network}"));
                    let (rule_id, expires_at) = policy.add_temporary_allow(net, ttl_secs, desc.clone());

                    // Also unlock at the nftables kernel level so sandbox's packets actually reach the target.
                    let mut kernel_warning = None;
                    if let Some(c) = ctrl {
                        if let Err(e) = c.add_allowed_target(&net, ttl_secs) {
                            warn!(network = %network, error = %e, "nftables target unlock failed (policy added, kernel rule skipped)");
                            kernel_warning = Some(format!(
                                "policy rule added but the nftables kernel unlock for {network} failed ({e}) — sandbox traffic to this target is still dropped"
                            ));
                        }
                    }

                    let now_ns = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap()
                        .as_nanos() as i64;
                    if let Err(e) = audit.record_rule_event(now_ns, &network, "rule_added", &format!("[agent] {desc}")) {
                        warn!(error = %e, "failed to audit agent rule add (rule itself succeeded)");
                    }

                    IpcResponse::RuleAdded { rule_id, expires_at, kernel_warning }
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
            if rule_id.len() > 64 {
                return IpcResponse::Error {
                    message: "rule_id exceeds maximum limit (64)".to_string(),
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
            if network.len() > 45 {
                return IpcResponse::Error {
                    message: "network CIDR string too long".to_string(),
                };
            }
            if ttl_secs > 86400 {
                return IpcResponse::Error {
                    message: "ttl_secs exceeds maximum limit (86400)".to_string(),
                };
            }
            if reason.len() > 500 {
                return IpcResponse::Error {
                    message: "reason exceeds maximum limit (500)".to_string(),
                };
            }
            if reason.trim().is_empty() {
                return IpcResponse::Error {
                    message: "reason is required for manual rules".to_string(),
                };
            }
            match network.parse::<ipnet::IpNet>() {
                Ok(net) => {
                    let (rule_id, expires_at) = policy.add_manual_allow(net, ttl_secs, reason.clone());
                    let mut kernel_warning = None;
                    if let Some(c) = ctrl {
                        if let Err(e) = c.add_allowed_target(&net, ttl_secs) {
                            warn!(network = %network, error = %e, "nftables target unlock failed (policy added, kernel rule skipped)");
                            kernel_warning = Some(format!(
                                "policy rule added but the nftables kernel unlock for {network} failed ({e}) — sandbox traffic to this target is still dropped"
                            ));
                        }
                    }
                    let now_ns = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap()
                        .as_nanos() as i64;
                    if let Err(e) = audit.record_rule_event(now_ns, &network, "rule_added", &format!("[manual] {reason}")) {
                        warn!(error = %e, "failed to audit manual rule add (rule itself succeeded)");
                    }
                    IpcResponse::RuleAdded { rule_id, expires_at, kernel_warning }
                }
                Err(_) => IpcResponse::Error {
                    message: format!("invalid network CIDR: {network}"),
                },
            }
        }

        // ── Write op — requires allowlisted UID ───────────────────────────
        IpcRequest::SetEgressMode { mode } => {
            if !is_write_authorized(peer_uid, allowed_uids) {
                warn!(peer_uid, "SetEgressMode denied: UID not allowlisted");
                return IpcResponse::Error {
                    message: "permission denied: UID not allowlisted for write operations".to_string(),
                };
            }
            if mode.len() > 10 {
                return IpcResponse::Error {
                    message: "egress mode too long".to_string(),
                };
            }
            let m = match EgressMode::parse(&mode) {
                Some(m) => m,
                None => {
                    return IpcResponse::Error {
                        message: format!("invalid egress mode: {mode} (expected block|allow|ask)"),
                    };
                }
            };
            // Update the policy engine first, then re-apply the kernel ruleset so
            // the controller's own copy stays in sync with PolicyEngine. The
            // controller keeps its OWN egress-mode copy purely for ruleset
            // generation — keeping both setters in lock-step here is what keeps
            // them consistent.
            policy.set_egress_mode(m);
            if let Some(c) = ctrl {
                if let Err(e) = c.set_egress_mode(m) {
                    warn!(mode = %mode, error = %e, "nftables egress-mode re-apply failed (policy updated, kernel ruleset NOT re-applied)");
                }
            }
            let now_ns = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos() as i64;
            if let Err(e) = audit.record_rule_event(now_ns, "-", "egress_mode_set", &format!("[operator] mode={mode}")) {
                warn!(error = %e, "failed to audit egress mode change (mode change itself succeeded)");
            }
            IpcResponse::EgressMode { mode: m.as_str().to_string() }
        }

        IpcRequest::AddHostnameRule { list, pattern } => {
            handle_hostname_edit(policy, audit, peer_uid, allowed_uids, &list, &pattern, true)
        }
        IpcRequest::RemoveHostnameRule { list, pattern } => {
            handle_hostname_edit(policy, audit, peer_uid, allowed_uids, &list, &pattern, false)
        }
    }
}

/// Shared add/remove handler for runtime hostname patterns. Write op — UID-
/// gated, audited. Returns the updated HostnamePolicy view on success.
#[allow(clippy::too_many_arguments)]
fn handle_hostname_edit(
    policy: &PolicyEngine,
    audit: &AuditLog,
    peer_uid: Option<u32>,
    allowed_uids: &[u32],
    list: &str,
    pattern: &str,
    add: bool,
) -> IpcResponse {
    if !is_write_authorized(peer_uid, allowed_uids) {
        warn!(peer_uid, "hostname rule edit denied: UID not allowlisted");
        return IpcResponse::Error {
            message: "permission denied: UID not allowlisted for write operations".to_string(),
        };
    }
    if pattern.is_empty() || pattern.len() > 253 {
        return IpcResponse::Error {
            message: "hostname pattern must be 1-253 characters".to_string(),
        };
    }
    let hl = match crate::policy::hostname::HostnameList::parse(list) {
        Some(hl) => hl,
        None => {
            return IpcResponse::Error {
                message: format!("invalid list: {list} (expected allow|block)"),
            };
        }
    };

    let changed = if add {
        policy.add_hostname_pattern(hl, pattern)
    } else {
        policy.remove_hostname_pattern(hl, pattern)
    };
    if add && !changed {
        return IpcResponse::Error {
            message: format!("malformed hostname pattern: {pattern}"),
        };
    }

    let now_ns = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos() as i64)
        .unwrap_or(0);
    let event = if add { "hostname_rule_added" } else { "hostname_rule_removed" };
    if let Err(e) = audit.record_rule_event(now_ns, pattern, event, &format!("[operator] list={list}")) {
        warn!(error = %e, "failed to audit hostname rule edit (edit itself succeeded)");
    }

    let (allow, block) = policy.hostname_patterns();
    IpcResponse::HostnamePolicy { allow, block }
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
            ..Default::default()
        };
        PolicyEngine::new(&cfg)
    }

    // TestSetTimeout-style regression: `ipc.socket_mode` was parsed from YAML
    // and unit-tested at the config layer, but IpcServer::run() never actually
    // applied it to the bound socket file — UnixListener::bind leaves the file
    // at whatever the process umask produces, not the configured mode. An
    // operator setting socket_mode: 0o600 (the documented default) got no
    // enforcement at all. This spawns a real IpcServer against a temp path and
    // asserts the file's actual mode matches what was configured.
    #[tokio::test(flavor = "multi_thread")]
    async fn test_run_applies_configured_socket_mode() {
        let _ = tracing_subscriber::fmt().with_test_writer().try_init();
        // Deliberately NOT std::env::temp_dir(): on macOS that resolves to a
        // long /var/folders/.../T path which, combined with a UUID filename,
        // exceeds a Unix domain socket's SUN_LEN limit (~104 bytes) — a
        // test-environment issue unrelated to the gateway code under test.
        // /tmp itself is short enough on every platform this test runs on.
        let socket_path = format!("/tmp/yummi-test-{}.sock", std::process::id());
        let _ = std::fs::remove_file(&socket_path);

        let ipc_cfg = crate::config::IpcConfig {
            socket_path: socket_path.clone(),
            socket_mode: 0o600,
        };
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let server = IpcServer::new(&ipc_cfg, policy, audit);

        let handle = tokio::spawn(server.run());
        // Poll for the socket file to appear rather than a fixed sleep — bind
        // happens near-instantly, but this avoids a flaky race on a slow CI box.
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(2);
        while !std::path::Path::new(&socket_path).exists() {
            if std::time::Instant::now() > deadline {
                panic!("socket file never appeared at {socket_path}");
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }

        let mode = std::fs::metadata(&socket_path)
            .expect("stat socket file")
            .permissions()
            .mode()
            & 0o777;
        assert_eq!(mode, 0o600, "socket file mode must match configured socket_mode, got {mode:o}");

        handle.abort();
        let _ = std::fs::remove_file(&socket_path);
    }

    #[test]
    fn test_process_scope_check() {
        let policy = make_policy();
        // The default mode is Block (fail-closed), under which nothing is in
        // scope. In Ask mode the per-IP allow list decides: an allow-listed IP
        // (10.0.0.1 ∈ 10.0.0.0/8) resolves to Allow → in_scope, while an
        // unlisted one would be NeedsApproval. This is the meaningful check of
        // "an allow-listed target is in scope" under the egress-mode model.
        policy.set_egress_mode(EgressMode::Ask);
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::CheckScope { ip: "10.0.0.1".to_string() },
            &policy,
            None,
            &[],
            None,
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::ScopeResult { in_scope, verdict, .. } => {
                assert!(in_scope, "allow-listed IP should be in scope in Ask mode");
                assert_eq!(verdict, "allow");
            }
            _ => panic!("unexpected response"),
        }
    }

    fn make_hostname_policy() -> PolicyEngine {
        let cfg = crate::config::ScopeConfig {
            allow: vec![],
            block: vec![],
            hostname_allow: vec!["*.acme.com".to_string()],
            hostname_block: vec!["*.evil.com".to_string()],
        };
        PolicyEngine::new(&cfg)
    }

    #[test]
    fn test_check_hostname_verdicts_and_audit() {
        let policy = make_hostname_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");

        let verdict = |host: &str, audit: &AuditLog| -> String {
            match process_request(
                IpcRequest::CheckHostname { host: host.to_string() },
                &policy, None, &[], None, audit, std::time::Instant::now(),
            ) {
                IpcResponse::HostnameResult { verdict, .. } => verdict,
                other => panic!("unexpected response: {other:?}"),
            }
        };

        assert_eq!(verdict("api.acme.com", &audit), "allow"); // whitelisted
        assert_eq!(verdict("acme.com", &audit), "allow"); // apex covered
        assert_eq!(verdict("c2.evil.com", &audit), "block"); // blocklisted
        assert_eq!(verdict("random.com", &audit), "block"); // whitelist set, no match

        // Only the two blocks were audited (allows are not chained).
        let (rows, verified) = audit.recent_rows(10).expect("rows");
        assert!(verified, "audit chain must verify");
        assert_eq!(rows.len(), 2, "expected 2 hostname-block audit rows");
        assert!(rows.iter().all(|r| r.reason == "hostname_blocked"));
    }

    #[test]
    fn test_runtime_hostname_add_remove() {
        let policy = make_hostname_policy(); // allow=[*.acme.com], block=[*.evil.com]
        let audit = AuditLog::open(":memory:").expect("audit log");

        // Add to the block list at runtime → takes effect immediately.
        let resp = process_request(
            IpcRequest::AddHostnameRule { list: "block".into(), pattern: "*.tracker.com".into() },
            &policy, Some(0), &[0], None, &audit, std::time::Instant::now(),
        );
        match resp {
            IpcResponse::HostnamePolicy { block, .. } => {
                assert!(block.contains(&"*.tracker.com".to_string()), "added pattern missing: {block:?}");
            }
            other => panic!("unexpected: {other:?}"),
        }
        assert_eq!(policy.check_hostname("a.tracker.com"), crate::policy::hostname::HostnameVerdict::Block);

        // Remove it → no longer blocked (and since a whitelist is set, it
        // becomes a whitelist-miss → block, but not via the explicit blocklist).
        let resp = process_request(
            IpcRequest::RemoveHostnameRule { list: "block".into(), pattern: "*.tracker.com".into() },
            &policy, Some(0), &[0], None, &audit, std::time::Instant::now(),
        );
        match resp {
            IpcResponse::HostnamePolicy { block, .. } => {
                assert!(!block.contains(&"*.tracker.com".to_string()), "removed pattern still present");
            }
            other => panic!("unexpected: {other:?}"),
        }

        // Both edits were audited.
        let (rows, verified) = audit.recent_rows(10).expect("rows");
        assert!(verified);
        assert!(rows.iter().any(|r| r.reason.contains("hostname_rule_added") || r.decision == "hostname_rule_added"));
    }

    #[test]
    fn test_hostname_edit_requires_authorized_uid() {
        let policy = make_hostname_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        // peer_uid 1000 not in allowlist [0] → denied.
        let resp = process_request(
            IpcRequest::AddHostnameRule { list: "block".into(), pattern: "*.x.com".into() },
            &policy, Some(1000), &[0], None, &audit, std::time::Instant::now(),
        );
        assert!(matches!(resp, IpcResponse::Error { .. }), "unauthorized edit must be denied");
    }

    #[test]
    fn test_get_hostname_policy_returns_patterns() {
        let policy = make_hostname_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        match process_request(
            IpcRequest::GetHostnamePolicy,
            &policy, None, &[], None, &audit, std::time::Instant::now(),
        ) {
            IpcResponse::HostnamePolicy { allow, block } => {
                assert_eq!(allow, vec!["*.acme.com".to_string()]);
                assert_eq!(block, vec!["*.evil.com".to_string()]);
            }
            other => panic!("unexpected response: {other:?}"),
        }
    }

    #[test]
    fn test_process_ping() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(IpcRequest::Ping, &policy, None, &[], None, &audit, std::time::Instant::now());
        assert!(matches!(resp, IpcResponse::Pong));
    }

    // A scope check that is not allowed must leave a hash-chained audit row —
    // the "proposed but blocked/queued" forensic trail. An allowed check must
    // not (it makes no security decision; the rule_added event covers it).
    #[test]
    fn test_scope_check_rejection_is_audited() {
        let policy = make_policy(); // allow-list = 10.0.0.0/8
        policy.set_egress_mode(EgressMode::Ask);
        let audit = AuditLog::open(":memory:").expect("audit log");

        // Out-of-scope target → needs_approval → audited.
        let _ = process_request(
            IpcRequest::CheckScope { ip: "8.8.8.8".to_string() },
            &policy, None, &[], None, &audit, std::time::Instant::now(),
        );
        let (rows, verified) = audit.recent_rows(10).expect("rows");
        assert!(verified, "audit chain must verify");
        assert_eq!(rows.len(), 1, "rejection should produce exactly one audit row");
        assert_eq!(rows[0].decision, "needs_approval");
        assert_eq!(rows[0].dst_ip, "8.8.8.8");

        // In-scope target → allow → NOT audited (still just the one row).
        let _ = process_request(
            IpcRequest::CheckScope { ip: "10.0.0.1".to_string() },
            &policy, None, &[], None, &audit, std::time::Instant::now(),
        );
        let (rows, _) = audit.recent_rows(10).expect("rows");
        assert_eq!(rows.len(), 1, "an allowed scope check must not add an audit row");
    }

    // Regression: GetStatus used to return a hardcoded uptime_secs: 0
    // regardless of how long the process had actually been running.
    #[test]
    fn test_get_status_reports_real_uptime() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let started_at = Instant::now() - std::time::Duration::from_secs(42);
        let resp = process_request(IpcRequest::GetStatus, &policy, None, &[], None, &audit, started_at);
        match resp {
            IpcResponse::Status { uptime_secs, rules_active, .. } => {
                assert!(uptime_secs >= 42, "expected uptime_secs >= 42, got {uptime_secs}");
                assert!(rules_active);
            }
            other => panic!("unexpected response: {other:?}"),
        }
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
            std::time::Instant::now(),
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
            std::time::Instant::now(),
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
        // make_policy() seeds one Config-source allow rule (10.0.0.0/8); adding
        // a manual rule brings the total to two. The point of this test is that
        // listing requires NO auth (peer_uid = None still returns a RuleList,
        // not an Error) and surfaces the manually-added rule.
        let policy = make_policy();
        policy.add_manual_allow("10.0.0.1/32".parse().unwrap(), 600, "test".to_string());
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(IpcRequest::ListRules, &policy, None, &[0], None, &audit, std::time::Instant::now());
        match resp {
            IpcResponse::RuleList { rules } => {
                assert_eq!(rules.len(), 2, "expected 1 config rule + 1 manual rule");
                assert!(
                    rules.iter().any(|r| r.source == "manual" && r.network == "10.0.0.1/32"),
                    "manual rule missing from list: {rules:?}"
                );
            }
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
            std::time::Instant::now(),
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
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::Error { message } => assert!(message.contains("reason is required")),
            other => panic!("expected reason-required error, got {other:?}"),
        }
    }

    #[test]
    fn test_get_fingerprint_config_reflects_controller_state() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let mut cfg = crate::config::GatewayConfig::test_default();
        cfg.network.ttl_normalize = Some(64);
        cfg.network.mac_randomization = true;
        let ctrl = NftablesController::new(&cfg);
        let resp = process_request(
            IpcRequest::GetFingerprintConfig,
            &policy,
            None,
            &[0],
            Some(&ctrl),
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::FingerprintConfig { ttl_normalize, mac_randomization } => {
                assert_eq!(ttl_normalize, Some(64));
                assert!(mac_randomization);
            }
            other => panic!("unexpected response: {other:?}"),
        }
    }

    #[test]
    fn test_get_fingerprint_config_defaults_off_without_controller() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::GetFingerprintConfig,
            &policy,
            None,
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::FingerprintConfig { ttl_normalize, mac_randomization } => {
                assert_eq!(ttl_normalize, None);
                assert!(!mac_randomization);
            }
            other => panic!("unexpected response: {other:?}"),
        }
    }

    #[test]
    fn test_set_egress_mode_denied_without_allowlisted_uid() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::SetEgressMode { mode: "allow".to_string() },
            &policy,
            Some(9999), // not in allowed_uids
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::Error { message } => assert!(message.contains("permission denied")),
            other => panic!("expected permission denied, got {other:?}"),
        }
        // mode must be unchanged (still default Block)
        assert_eq!(policy.egress_mode(), EgressMode::Block);
    }

    #[test]
    fn test_set_egress_mode_accepted_with_allowlisted_uid() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::SetEgressMode { mode: "allow".to_string() },
            &policy,
            Some(0),
            &[0],
            None, // no live controller in unit tests
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::EgressMode { mode } => assert_eq!(mode, "allow"),
            other => panic!("expected EgressMode, got {other:?}"),
        }
        assert_eq!(policy.egress_mode(), EgressMode::Allow);
    }

    #[test]
    fn test_set_egress_mode_rejects_invalid_mode() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(
            IpcRequest::SetEgressMode { mode: "wide-open".to_string() },
            &policy,
            Some(0),
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::Error { message } => assert!(message.contains("invalid egress mode")),
            other => panic!("expected error, got {other:?}"),
        }
        assert_eq!(policy.egress_mode(), EgressMode::Block);
    }

    #[test]
    fn test_get_egress_mode_returns_current() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let resp = process_request(IpcRequest::GetEgressMode, &policy, None, &[0], None, &audit, std::time::Instant::now());
        match resp {
            IpcResponse::EgressMode { mode } => assert_eq!(mode, "block"),
            other => panic!("expected EgressMode, got {other:?}"),
        }
        policy.set_egress_mode(EgressMode::Ask);
        let resp = process_request(IpcRequest::GetEgressMode, &policy, None, &[0], None, &audit, std::time::Instant::now());
        match resp {
            IpcResponse::EgressMode { mode } => assert_eq!(mode, "ask"),
            other => panic!("expected EgressMode, got {other:?}"),
        }
    }

    #[test]
    fn test_check_scope_verdict_allow_after_set_allow_mode() {
        // make_policy() allows 10.0.0.0/8 only; 8.8.8.8 is out of scope, so in
        // Block (default) it blocks. After switching to Allow, the verdict for
        // 8.8.8.8 becomes "allow".
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");

        let resp = process_request(
            IpcRequest::CheckScope { ip: "8.8.8.8".to_string() },
            &policy,
            None,
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::ScopeResult { verdict, in_scope, .. } => {
                assert_eq!(verdict, "block");
                assert!(!in_scope);
            }
            other => panic!("unexpected response: {other:?}"),
        }

        let _ = process_request(
            IpcRequest::SetEgressMode { mode: "allow".to_string() },
            &policy,
            Some(0),
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );

        let resp = process_request(
            IpcRequest::CheckScope { ip: "8.8.8.8".to_string() },
            &policy,
            None,
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );
        match resp {
            IpcResponse::ScopeResult { verdict, in_scope, .. } => {
                assert_eq!(verdict, "allow");
                assert!(in_scope);
            }
            other => panic!("unexpected response: {other:?}"),
        }
    }

    #[test]
    fn test_set_egress_mode_writes_audit_row() {
        let policy = make_policy();
        let audit = AuditLog::open(":memory:").expect("audit log");
        let _ = process_request(
            IpcRequest::SetEgressMode { mode: "ask".to_string() },
            &policy,
            Some(0),
            &[0],
            None,
            &audit,
            std::time::Instant::now(),
        );
        let (rows, _) = audit.recent_rows(10).expect("recent rows");
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].decision, "egress_mode_set");
        assert!(rows[0].reason.contains("mode=ask"));
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
            std::time::Instant::now(),
        );
        let (rows, _) = audit.recent_rows(10).expect("recent rows");
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].decision, "rule_added");
        assert!(rows[0].reason.starts_with("[manual]"));
    }
}
