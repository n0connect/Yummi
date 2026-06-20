//! Watchdog — self-monitoring and emergency lockdown.
//!
//! On memory limit breach: flushes the audit log, removes the heartbeat
//! file, then exits. This ensures audit integrity is preserved even on
//! emergency shutdown (no partial WAL left unflushed).

use std::time::Duration;
use tracing::{info, warn};

use crate::audit::AuditLog;
use crate::config::GatewayConfig;

/// Watchdog monitors gateway health and writes heartbeat.
///
/// Memory limit is checked via /proc/self/status (Linux-only).
/// On breach: audit log is flushed, heartbeat is removed, process exits.
pub struct Watchdog {
    heartbeat_path: String,
    interval:       Duration,
    max_memory_mb:  u64,
    audit:          AuditLog,
}

impl Watchdog {
    pub fn new(cfg: &GatewayConfig, audit: AuditLog) -> Self {
        Self {
            heartbeat_path: cfg.watchdog.heartbeat_path.clone(),
            interval: Duration::from_secs(cfg.watchdog.heartbeat_interval_secs),
            max_memory_mb: cfg.watchdog.max_memory_mb,
            audit,
        }
    }

    /// Run the watchdog loop — heartbeat + health checks.
    pub async fn run(self) {
        info!(interval_secs = self.interval.as_secs(), "watchdog started");
        loop {
            tokio::time::sleep(self.interval).await;
            self.write_heartbeat();
            self.check_memory().await;
        }
    }

    fn write_heartbeat(&self) {
        let timestamp = chrono::Utc::now().timestamp();
        if let Some(parent) = std::path::Path::new(&self.heartbeat_path).parent() {
            let _ = std::fs::create_dir_all(parent);
        }
        if let Err(e) = std::fs::write(&self.heartbeat_path, timestamp.to_string()) {
            warn!(error = %e, "failed to write heartbeat");
        }
    }

    /// Check process RSS; on limit breach, flush audit then exit.
    async fn check_memory(&self) {
        if let Ok(status) = std::fs::read_to_string("/proc/self/status") {
            for line in status.lines() {
                if line.starts_with("VmRSS:") {
                    if let Some(kb_str) = line.split_whitespace().nth(1) {
                        if let Ok(kb) = kb_str.parse::<u64>() {
                            let mb = kb / 1024;
                            if mb > self.max_memory_mb {
                                warn!(
                                    rss_mb = mb,
                                    limit_mb = self.max_memory_mb,
                                    "memory limit exceeded — flushing audit and halting"
                                );
                                // Flush audit WAL before exit to preserve integrity.
                                if let Err(e) = self.audit.flush().await {
                                    warn!(error = %e, "audit flush failed during emergency shutdown");
                                }
                                // Remove heartbeat so external monitors notice the absence.
                                let _ = std::fs::remove_file(&self.heartbeat_path);
                                std::process::exit(1);
                            }
                        }
                    }
                }
            }
        }
    }
}
