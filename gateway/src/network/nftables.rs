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

use std::collections::HashMap;
use std::io::Write as IoWrite;
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Mutex, RwLock};
use std::time::{SystemTime, UNIX_EPOCH};

use crate::config::GatewayConfig;
use crate::network::NetworkError;
use crate::policy::rules::EgressMode;

const NFT_BIN: &str = "/usr/sbin/nft";

#[derive(Debug, Clone)]
pub struct EnforcementStatus {
    pub rules_active: bool,
    pub policy_kernel_consistent: bool,
    pub generation: u64,
    pub last_error: Option<String>,
}

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
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .subsec_nanos();
    (nanos ^ nanos.rotate_left(13)) as u8
}

fn format_mac(mac: [u8; 6]) -> String {
    mac.iter()
        .map(|b| format!("{b:02x}"))
        .collect::<Vec<_>>()
        .join(":")
}

/// Linux nftables-based network controller.
///
/// Installs and manages an `inet` table with two chains:
/// - `yummi_forward`: FORWARD hook — filters sandbox's outbound traffic.
/// - `yummi_nat`:     POSTROUTING hook — masquerades sandbox's packets so they
///   appear from the gateway container when forwarded.
pub struct NftablesController {
    table_name: String,
    forward_chain: String,
    nat_chain: String,
    sandbox_ip: String,
    allow_cidrs: Vec<String>,
    block_cidrs: Vec<String>,
    ttl_normalize: Option<u8>,
    mac_randomization: bool,
    interface: String,
    active: AtomicBool,
    generation: AtomicU64,
    last_error: RwLock<Option<String>>,
    /// Kernel set elements mirrored in userspace so a full atomic mode swap
    /// can rebuild them instead of silently discarding active approvals.
    dynamic_targets: RwLock<HashMap<ipnet::IpNet, i64>>,
    mutation: Mutex<()>,
    /// The controller's OWN copy of the operator egress mode, used purely for
    /// ruleset generation. The IPC handler keeps it in sync with PolicyEngine
    /// by calling both `PolicyEngine::set_egress_mode` and
    /// `NftablesController::set_egress_mode` together. Defaults to `Block`
    /// (fail-closed) and does not persist across restarts.
    egress_mode: RwLock<EgressMode>,
}

impl NftablesController {
    pub fn new(cfg: &GatewayConfig) -> Self {
        Self {
            table_name: cfg.network.table_name.clone(),
            forward_chain: cfg.network.forward_chain.clone(),
            nat_chain: cfg.network.nat_chain.clone(),
            sandbox_ip: cfg.network.sandbox_ip.clone(),
            allow_cidrs: cfg.scope.allow.iter().map(|n| n.to_string()).collect(),
            block_cidrs: cfg.scope.block.iter().map(|n| n.to_string()).collect(),
            ttl_normalize: cfg.network.ttl_normalize,
            mac_randomization: cfg.network.mac_randomization,
            interface: cfg.network.interface.clone(),
            active: AtomicBool::new(false),
            generation: AtomicU64::new(0),
            last_error: RwLock::new(None),
            dynamic_targets: RwLock::new(HashMap::new()),
            mutation: Mutex::new(()),
            egress_mode: RwLock::new(EgressMode::Block),
        }
    }

    /// Apply an egress-mode candidate to the kernel, then publish it locally.
    /// A failed check/apply leaves both the previous kernel table and this
    /// controller's committed mode untouched.
    pub fn set_egress_mode(&self, m: EgressMode) -> Result<(), NetworkError> {
        self.apply_candidate(m)
    }

    /// Install fail-closed FORWARD-chain rules.
    /// Idempotent: flushes and recreates the table each time.
    pub fn install_rules(&self) -> Result<(), NetworkError> {
        if self.mac_randomization {
            self.randomize_egress_mac(&self.interface);
        }

        // The delete and recreate commands are submitted as one nft batch.
        // nf_tables commits a successful batch atomically, so there is no
        // unfiltered window and a rejected candidate leaves the old table live.
        let mode = *self.egress_mode.read().unwrap();
        self.apply_candidate(mode)?;

        tracing::info!(
            table = %self.table_name,
            sandbox_ip = %self.sandbox_ip,
            allow = self.allow_cidrs.len(),
            block  = self.block_cidrs.len(),
            "nftables FORWARD rules installed (fail-closed)"
        );
        Ok(())
    }

    /// Periodic desired-state repair. Rebuilds the full table (including
    /// unexpired dynamic elements) from userspace policy in one transaction.
    pub fn reconcile(&self, policy_mode: EgressMode) -> Result<(), NetworkError> {
        self.apply_candidate(policy_mode)
    }

    pub fn enforcement_status(&self, policy_mode: EgressMode) -> EnforcementStatus {
        let rules_active = self.rules_active().unwrap_or(false);
        let controller_mode = *self.egress_mode.read().unwrap();
        let last_error = self.last_error.read().unwrap().clone();
        EnforcementStatus {
            rules_active,
            policy_kernel_consistent: rules_active
                && controller_mode == policy_mode
                && last_error.is_none(),
            generation: self.generation.load(Ordering::SeqCst),
            last_error,
        }
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

    fn table_exists(&self) -> Result<bool, NetworkError> {
        let out = Command::new(NFT_BIN)
            .args(["list", "table", "inet", &self.table_name])
            .output()
            .map_err(|e| NetworkError::InstallFailed(format!("nft list table: {e}")))?;
        Ok(out.status.success())
    }

    fn apply_candidate(&self, mode: EgressMode) -> Result<(), NetworkError> {
        let _guard = self.mutation.lock().unwrap();
        let result: Result<(), NetworkError> = (|| {
            let script = self.build_replacement_ruleset(self.table_exists()?, mode);
            self.nft_check(&script)?;
            self.nft_apply(&script)?;
            Ok(())
        })();
        match result {
            Ok(()) => {
                *self.egress_mode.write().unwrap() = mode;
                self.active.store(true, Ordering::SeqCst);
                self.generation.fetch_add(1, Ordering::SeqCst);
                *self.last_error.write().unwrap() = None;
                Ok(())
            }
            Err(err) => {
                *self.last_error.write().unwrap() = Some(err.to_string());
                Err(err)
            }
        }
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

    /// Read the configured TTL-normalization value (None = disabled).
    pub fn ttl_normalize(&self) -> Option<u8> {
        self.ttl_normalize
    }

    /// Whether egress MAC randomization is enabled.
    pub fn mac_randomization(&self) -> bool {
        self.mac_randomization
    }

    // ── Internal ──────────────────────────────────────────────────────────────

    /// Renders the nft set element string and the target set's name for a
    /// CIDR/host — shared by add_allowed_target and remove_allowed_target so
    /// the two can never derive different element text for the same input.
    fn target_element(net: &ipnet::IpNet) -> (String, &'static str) {
        match net {
            ipnet::IpNet::V4(n) => {
                let el = if n.prefix_len() == 32 {
                    n.addr().to_string()
                } else {
                    n.to_string()
                };
                (el, "allowed_targets")
            }
            ipnet::IpNet::V6(n) => {
                let el = if n.prefix_len() == 128 {
                    n.addr().to_string()
                } else {
                    n.to_string()
                };
                (el, "allowed_targets_v6")
            }
        }
    }

    /// Dynamically add a target CIDR to the allowed_targets set with TTL.
    ///
    /// Uses `nft add element` which is atomic and does not flush existing rules.
    /// The set has `flags interval, timeout` so both host IPs and subnets work.
    pub fn add_allowed_target(
        &self,
        net: &ipnet::IpNet,
        ttl_secs: u64,
    ) -> Result<(), NetworkError> {
        let _guard = self.mutation.lock().unwrap();
        let (element, set_name) = Self::target_element(net);

        let cmd = format!(
            "add element inet {} {} {{ {} timeout {}s }}",
            self.table_name, set_name, element, ttl_secs,
        );

        let out = Command::new(NFT_BIN)
            .args(["-e", &cmd])
            .output()
            .map_err(|e| NetworkError::InstallFailed(format!("nft add element: {e}")))?;

        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr);
            // "interval overlaps with an existing one" is nft's rejection
            // when the new element's address range is already fully covered
            // by a broader interval already in the set — e.g. an operator
            // scoped a /24 via the static config or a manual GATEWAY-tab
            // rule, and a later per-command auto-unlock then tries to add a
            // narrower /32 for one IP inside it. Found live: every command
            // against a target already covered this way surfaced a raw
            // kernel error as a noisy execution "warning", even though
            // nothing was actually blocked — the broader interval already
            // permits exactly that traffic, so the narrower add was always
            // redundant. Not a real failure: the end state (target
            // reachable) is exactly what was requested, so this falls
            // through to the same bookkeeping update as a real success,
            // the same way remove_allowed_target already treats "already
            // absent" as a benign no-op rather than an error.
            if !stderr.contains("interval overlaps with an existing one") {
                return Err(NetworkError::InstallFailed(format!(
                    "nft add element failed: {stderr}"
                )));
            }
            tracing::debug!(target = %element, "nftables: target already covered by a broader allowed interval");
        } else {
            tracing::info!(target = %element, ttl_secs, "nftables: target added to allowed_targets");
        }

        let expires_at = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_secs() as i64)
            .unwrap_or(0)
            .saturating_add(ttl_secs as i64);
        self.dynamic_targets
            .write()
            .unwrap()
            .entry(*net)
            .and_modify(|existing| *existing = (*existing).max(expires_at))
            .or_insert(expires_at);
        Ok(())
    }

    /// Revoke a previously-added target immediately, rather than waiting for
    /// its `timeout` to elapse on its own. Without this, an operator's
    /// explicit "remove rule" action only updated the policy engine's own
    /// bookkeeping — the corresponding kernel-level `allowed_targets`/
    /// `allowed_targets_v6` set element (installed by add_allowed_target)
    /// stayed live until its ORIGINAL ttl_secs naturally expired (up to 24h,
    /// the IPC layer's max), so the target remained reachable from sandbox
    /// well after the operator believed it was revoked.
    ///
    /// `nft delete element` targets a set element by its key alone — the
    /// `timeout` clause used at insertion time is not part of the key and
    /// must not be repeated here.
    pub fn remove_allowed_target(&self, net: &ipnet::IpNet) -> Result<(), NetworkError> {
        let _guard = self.mutation.lock().unwrap();
        let (element, set_name) = Self::target_element(net);

        let cmd = format!(
            "delete element inet {} {} {{ {} }}",
            self.table_name, set_name, element,
        );

        let out = Command::new(NFT_BIN)
            .args(["-e", &cmd])
            .output()
            .map_err(|e| NetworkError::InstallFailed(format!("nft delete element: {e}")))?;

        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr);
            // "no such file or directory" is nft's error for a missing element —
            // e.g. the element already expired naturally between the operator's
            // click and this call. Not a real failure: the end state (target not
            // reachable) is exactly what was requested.
            if stderr.contains("No such file or directory") {
                tracing::debug!(target = %element, "nftables: target already absent from allowed_targets");
                self.dynamic_targets.write().unwrap().remove(net);
                return Ok(());
            }
            return Err(NetworkError::InstallFailed(format!(
                "nft delete element failed: {stderr}"
            )));
        }

        tracing::info!(target = %element, "nftables: target removed from allowed_targets");
        self.dynamic_targets.write().unwrap().remove(net);
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
    #[cfg(test)]
    pub(crate) fn build_ruleset(&self) -> String {
        self.build_ruleset_for(*self.egress_mode.read().unwrap())
    }

    pub(crate) fn build_replacement_ruleset(&self, table_exists: bool, mode: EgressMode) -> String {
        let mut script = String::new();
        if table_exists {
            script.push_str(&format!("delete table inet {}\n", self.table_name));
        }
        script.push_str(&self.build_ruleset_for(mode));
        script
    }

    pub(crate) fn build_ruleset_for(&self, mode: EgressMode) -> String {
        let mut s = String::new();

        s.push_str(&format!("table inet {} {{\n", self.table_name));

        // ── Dynamic target set — populated via IPC AddTemporaryRule ──────────
        // flags interval: supports CIDR notation (e.g. 10.0.0.0/24)
        // flags timeout:  TTL expiry managed by kernel automatically
        s.push_str("  set allowed_targets {\n");
        s.push_str("    type ipv4_addr\n");
        s.push_str("    flags interval, timeout\n");
        self.render_dynamic_elements(&mut s, false);
        s.push_str("  }\n\n");

        s.push_str("  set allowed_targets_v6 {\n");
        s.push_str("    type ipv6_addr\n");
        s.push_str("    flags interval, timeout\n");
        self.render_dynamic_elements(&mut s, true);
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
                    s.push_str(&format!("    ip saddr {ip} ip ttl set {ttl}\n"));
                }
            }
        }

        // Pass through traffic NOT originating from sandbox (other containers, host).
        if !self.sandbox_ip.is_empty() {
            if let Ok(ip) = self.sandbox_ip.parse::<std::net::IpAddr>() {
                s.push_str(&format!("    ip saddr != {ip} accept\n"));
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
                    s.push_str(&format!("    ip6 daddr {safe} drop\n"));
                } else {
                    s.push_str(&format!("    ip daddr {safe} drop\n"));
                }
            } else {
                tracing::error!(cidr, "invalid CIDR in block list — skipped");
            }
        }

        // Block mode is the default and means "deny by default" — but, like
        // Ask mode, it still honors an EXPLICIT allow: the static config
        // allow list and the dynamic @allowed_targets set (populated by
        // AddTemporaryRule/AddManualRule) are emitted in every mode except
        // Allow (which is already a kernel-level blanket accept, so emitting
        // these too would be redundant). This mirrors policy::PolicyEngine's
        // check_egress, whose own doc comment explains why: an operator who
        // explicitly scopes a target via the GATEWAY tab must be able to
        // reach it without ALSO flipping the separate egress-mode switch —
        // confirmed live, a temporary rule the gateway had genuinely
        // accepted and installed was still unreachable because this
        // function alone skipped emitting its accept line while in Block.
        //
        // This used to be stricter — Block omitted both lists entirely, so
        // switching INTO Block mid-session was a hard instant kill-switch
        // that revoked every previously-opened target, static or dynamic, no
        // exceptions. That is still true for anything NOT covered by an
        // explicit rule (there is no blanket accept in Block mode — compare
        // the Allow-only `ip saddr {ip} accept` below), and for the
        // always-blocked floor and the config block list (checked earlier,
        // above, and unaffected by this). What changed is narrower: a
        // specific target an operator has explicitly allowed now stays
        // explicitly allowed in Block mode too, exactly as it already does
        // in Ask mode. An operator who wants a true no-exceptions lockdown
        // removes or lets expire the specific rules first (GATEWAY tab), or
        // the config's static allow list.
        if mode != EgressMode::Allow {
            for cidr in &self.allow_cidrs {
                if let Ok(net) = cidr.parse::<ipnet::IpNet>() {
                    let safe = net.to_string();
                    if safe.contains(':') {
                        s.push_str(&format!("    ip6 daddr {safe} accept\n"));
                    } else {
                        s.push_str(&format!("    ip daddr {safe} accept\n"));
                    }
                } else {
                    tracing::error!(cidr, "invalid CIDR in allow list — skipped");
                }
            }

            // Dynamic targets unlocked via IPC AddTemporaryRule.
            s.push_str("    ip daddr @allowed_targets accept\n");
            s.push_str("    ip6 daddr @allowed_targets_v6 accept\n");
        }

        // DNS is always permitted so sandbox can resolve target hostnames.
        s.push_str("    udp dport 53 accept\n");

        // Egress mode = Allow: blanket-accept all remaining sandbox-originated
        // egress. Sits AFTER the block rules (so explicit denies + the
        // always-blocked floor still win) and BEFORE the final fail-closed drop,
        // which is preserved. Block/Ask modes add no blanket accept — Ask relies
        // on per-target @allowed_targets and Block drops everything.
        if mode == EgressMode::Allow && !self.sandbox_ip.is_empty() {
            if let Ok(ip) = self.sandbox_ip.parse::<std::net::IpAddr>() {
                s.push_str(&format!("    ip saddr {ip} accept\n"));
            }
        }

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
                s.push_str(&format!("    ip saddr {ip} masquerade\n"));
            }
        }
        s.push_str("  }\n");

        s.push_str("}\n");
        s
    }

    fn render_dynamic_elements(&self, out: &mut String, ipv6: bool) {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_secs() as i64)
            .unwrap_or(0);
        let mut elements = self
            .dynamic_targets
            .read()
            .unwrap()
            .iter()
            .filter_map(|(net, expires_at)| {
                let is_v6 = matches!(net, ipnet::IpNet::V6(_));
                if is_v6 != ipv6 || *expires_at <= now {
                    return None;
                }
                let (element, _) = Self::target_element(net);
                Some(format!("{element} timeout {}s", expires_at - now))
            })
            .collect::<Vec<_>>();
        elements.sort();
        if !elements.is_empty() {
            out.push_str(&format!("    elements = {{ {} }}\n", elements.join(", ")));
        }
    }

    /// Feed a ruleset script to `nft -f -` via stdin.
    fn nft_check(&self, ruleset: &str) -> Result<(), NetworkError> {
        self.nft_run(&["-c", "-f", "-"], ruleset, "check")
    }

    fn nft_apply(&self, ruleset: &str) -> Result<(), NetworkError> {
        self.nft_run(&["-f", "-"], ruleset, "apply")
    }

    fn nft_run(&self, args: &[&str], ruleset: &str, operation: &str) -> Result<(), NetworkError> {
        let mut child = Command::new(NFT_BIN)
            .args(args)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|e| NetworkError::InstallFailed(format!("spawn nft {operation}: {e}")))?;

        if let Some(mut stdin) = child.stdin.take() {
            stdin.write_all(ruleset.as_bytes()).map_err(|e| {
                NetworkError::InstallFailed(format!("write {operation} ruleset: {e}"))
            })?;
        }

        let result = child
            .wait_with_output()
            .map_err(|e| NetworkError::InstallFailed(format!("nft {operation} wait: {e}")))?;

        if !result.status.success() {
            let stderr = String::from_utf8_lossy(&result.stderr);
            return Err(NetworkError::InstallFailed(format!(
                "nft {operation}: {stderr}"
            )));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{
        GatewayConfig, IpcConfig, LoggingConfig, NetworkConfig, PolicyConfig, ScopeConfig,
        WatchdogConfig,
    };

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
                ..Default::default()
            },
            policy: PolicyConfig::default(),
            logging: LoggingConfig {
                level: "info".to_string(),
                audit_db: ":memory:".to_string(),
            },
            ipc: IpcConfig {
                socket_path: std::env::temp_dir()
                    .join("yummi-test.sock")
                    .to_string_lossy()
                    .to_string(),
                socket_mode: 0o600,
            },
            watchdog: WatchdogConfig::default(),
            network: NetworkConfig {
                table_name: "yummi_gateway".to_string(),
                forward_chain: "yummi_forward".to_string(),
                nat_chain: "yummi_nat".to_string(),
                interface: String::new(),
                sandbox_ip: "172.20.0.10".to_string(),
                ttl_normalize: None,
                mac_randomization: false,
            },
        }
    }

    #[test]
    fn test_replacement_ruleset_deletes_and_recreates_in_one_transaction() {
        let ctrl = NftablesController::new(&test_config());
        let script = ctrl.build_replacement_ruleset(true, EgressMode::Block);
        assert!(script.starts_with("delete table inet yummi_gateway\n"));
        assert!(script.contains("\ntable inet yummi_gateway {"));
    }

    #[test]
    fn test_candidate_mode_render_does_not_publish_unapplied_policy() {
        let ctrl = NftablesController::new(&test_config());
        let candidate = ctrl.build_ruleset_for(EgressMode::Allow);
        assert!(candidate.contains("ip saddr 172.20.0.10 accept"));
        assert_eq!(*ctrl.egress_mode.read().unwrap(), EgressMode::Block);
        assert!(!ctrl.build_ruleset().contains("ip saddr 172.20.0.10 accept"));
    }

    #[test]
    fn test_mode_rebuild_preserves_unexpired_dynamic_targets() {
        let ctrl = NftablesController::new(&test_config());
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;
        ctrl.dynamic_targets
            .write()
            .unwrap()
            .insert("203.0.113.7/32".parse().unwrap(), now + 120);

        let candidate = ctrl.build_ruleset_for(EgressMode::Ask);
        assert!(candidate.contains("elements = { 203.0.113.7 timeout"));
        assert!(candidate.contains("ip daddr @allowed_targets accept"));
    }

    #[test]
    fn test_ruleset_forward_chain() {
        let ctrl = NftablesController::new(&test_config());
        // Ask mode: allow-list mechanics (static + dynamic) are rendered.
        // Block mode deliberately omits them — see test_block_mode_excludes_all_allow_rules.
        *ctrl.egress_mode.write().unwrap() = EgressMode::Ask;
        let ruleset = ctrl.build_ruleset();

        assert!(ruleset.contains("table inet yummi_gateway"));
        assert!(ruleset.contains("chain yummi_forward"));
        assert!(ruleset.contains("chain yummi_nat"));
        assert!(ruleset.contains("hook forward"));
        assert!(ruleset.contains("hook postrouting"));
        // dynamic targets set
        assert!(ruleset.contains("set allowed_targets"));
        assert!(ruleset.contains("set allowed_targets_v6"));
        assert!(ruleset.contains("flags interval, timeout"));
        assert!(ruleset.contains("ip daddr @allowed_targets accept"));
        assert!(ruleset.contains("ip6 daddr @allowed_targets_v6 accept"));
    }

    #[test]
    fn test_ruleset_block_before_allow() {
        let ctrl = NftablesController::new(&test_config());
        *ctrl.egress_mode.write().unwrap() = EgressMode::Ask;
        let ruleset = ctrl.build_ruleset();

        let block_pos = ruleset.find("127.0.0.0/8").unwrap();
        let allow_pos = ruleset.find("10.0.0.0/24").unwrap();
        assert!(
            block_pos < allow_pos,
            "block rules must precede allow rules"
        );
    }

    #[test]
    fn test_ruleset_cidr_family_prefix() {
        let ctrl = NftablesController::new(&test_config());
        *ctrl.egress_mode.write().unwrap() = EgressMode::Ask;
        let ruleset = ctrl.build_ruleset();

        // IPv4 uses `ip daddr`, not `ip6 daddr`
        assert!(ruleset.contains("ip daddr 127.0.0.0/8 drop"));
        assert!(ruleset.contains("ip daddr 10.0.0.0/24 accept"));
        assert!(!ruleset.contains("ip6 daddr 127.0.0.0/8"));
    }

    // Config-level parity check: an IPv6 unique-local block entry (the
    // RFC4193 analogue of RFC1918, e.g. the fc00::/7 entry shipped in
    // gateway/config/default.yaml) must render as an `ip6 daddr ... drop`
    // rule, the same as an IPv4 private range does — proving the block list
    // isn't silently IPv4-only.
    #[test]
    fn test_ruleset_ipv6_block_cidr_renders_as_ip6_drop() {
        let mut cfg = test_config();
        cfg.scope.block.push("fc00::/7".parse().unwrap());
        let ctrl = NftablesController::new(&cfg);
        let ruleset = ctrl.build_ruleset();

        assert!(ruleset.contains("ip6 daddr fc00::/7 drop"));
        assert!(!ruleset.contains("ip daddr fc00::/7"));
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
            assert_eq!(
                mac[0] & 0b0000_0010,
                0b0000_0010,
                "locally-administered bit must be set"
            );
            assert_eq!(mac[0] & 0b0000_0001, 0, "unicast bit must be clear");
        }
    }

    #[test]
    fn test_format_mac_produces_colon_separated_hex() {
        let formatted = format_mac([0x02, 0xAB, 0x00, 0x11, 0xFF, 0x9c]);
        assert_eq!(formatted, "02:ab:00:11:ff:9c");
    }

    // target_element is the shared basis for both add_allowed_target and
    // remove_allowed_target's `nft` element text — a mismatch here would mean
    // remove_allowed_target could silently target the WRONG set element and
    // never actually revoke what add_allowed_target installed. The real `nft`
    // invocation itself isn't unit-tested (needs root + CAP_NET_ADMIN + a real
    // nftables-capable kernel — see docs/installation.md's Docker Desktop VM
    // networking callout), only verified live via `make start`.
    #[test]
    fn test_target_element_v4_host_vs_subnet() {
        let (el, set) = NftablesController::target_element(&"1.2.3.4/32".parse().unwrap());
        assert_eq!((el.as_str(), set), ("1.2.3.4", "allowed_targets"));

        let (el, set) = NftablesController::target_element(&"10.0.0.0/24".parse().unwrap());
        assert_eq!((el.as_str(), set), ("10.0.0.0/24", "allowed_targets"));
    }

    #[test]
    fn test_target_element_v6_host_vs_subnet() {
        let (el, set) = NftablesController::target_element(&"2001:db8::1/128".parse().unwrap());
        assert_eq!((el.as_str(), set), ("2001:db8::1", "allowed_targets_v6"));

        let (el, set) = NftablesController::target_element(&"2001:db8::/32".parse().unwrap());
        assert_eq!((el.as_str(), set), ("2001:db8::/32", "allowed_targets_v6"));
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
    fn test_blanket_accept_only_in_allow_mode() {
        // Block (default): no blanket sandbox-saddr accept.
        let ctrl = NftablesController::new(&test_config());
        let blanket = "ip saddr 172.20.0.10 accept";
        assert!(
            !ctrl.build_ruleset().contains(blanket),
            "Block mode must NOT emit a blanket sandbox accept"
        );

        // Ask: still no blanket accept.
        *ctrl.egress_mode.write().unwrap() = EgressMode::Ask;
        assert!(
            !ctrl.build_ruleset().contains(blanket),
            "Ask mode must NOT emit a blanket sandbox accept"
        );

        // Allow: blanket accept present, and it precedes the final drop.
        *ctrl.egress_mode.write().unwrap() = EgressMode::Allow;
        let ruleset = ctrl.build_ruleset();
        assert!(
            ruleset.contains(blanket),
            "Allow mode must emit a blanket sandbox accept"
        );
        let accept_pos = ruleset.find(blanket).unwrap();
        let drop_pos = ruleset.find("yummi_gateway_drop").unwrap();
        assert!(
            accept_pos < drop_pos,
            "blanket accept must precede the fail-closed final drop"
        );
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

    // Block mode is the default and means "deny unless explicitly allowed" —
    // it still honors the static config allow list and the dynamic
    // @allowed_targets set (an operator-opened GATEWAY-tab rule), same as
    // Ask mode, so a scoped target is reachable without the operator also
    // having to flip the separate egress-mode switch (see build_ruleset_for's
    // own doc comment for the live bug this fixes and what stayed the same:
    // there is still no blanket accept in Block mode, so anything NOT
    // explicitly listed is still dropped).
    #[test]
    fn test_block_mode_honors_explicit_allow_rules() {
        let ctrl = NftablesController::new(&test_config()); // default mode: Block
        let ruleset = ctrl.build_ruleset();

        // Static config allow list (10.0.0.0/24, 192.168.1.0/24 in test_config) IS honored.
        assert!(
            ruleset.contains("10.0.0.0/24 accept") && ruleset.contains("192.168.1.0/24 accept"),
            "Block mode must still honor the static config allow list:\n{ruleset}"
        );
        // Dynamic set (IPC-opened temporary/manual rules) is referenced too.
        assert!(
            ruleset.contains("@allowed_targets accept")
                && ruleset.contains("@allowed_targets_v6 accept"),
            "Block mode must still honor the dynamic allowed_targets set:\n{ruleset}"
        );
        // No blanket accept-everything line — only the explicit lists above.
        assert!(
            !ruleset.contains(&format!("ip saddr {} accept", ctrl.sandbox_ip)),
            "Block mode must not emit a blanket accept-all line:\n{ruleset}"
        );
        // The block (drop) list and the always-on DNS allowance are unaffected.
        assert!(ruleset.contains("ip daddr 127.0.0.0/8 drop"));
        assert!(ruleset.contains("udp dport 53 accept"));
    }

    // Ask mode is documented as relying on the static allow list and
    // per-target @allowed_targets set (only new/unresolved destinations need
    // approval) — confirm the allow rules are still emitted there.
    #[test]
    fn test_ask_mode_still_emits_allow_rules() {
        let ctrl = NftablesController::new(&test_config());
        *ctrl.egress_mode.write().unwrap() = EgressMode::Ask;
        let ruleset = ctrl.build_ruleset();

        assert!(ruleset.contains("10.0.0.0/24 accept"));
        assert!(ruleset.contains("@allowed_targets accept"));
        assert!(ruleset.contains("@allowed_targets_v6 accept"));
    }
}
