//! Yummi gateway trust boundary — Linux-only entry point.
//!
//! gateway controls sandbox's outbound network traffic via nftables FORWARD-chain rules
//! on the Docker bridge. It must run inside Docker with CAP_NET_ADMIN.
//!
//! Architecture:
//!   sandbox (Kali) → gateway (gateway) → external network
//!
//! gateway installs fail-closed rules at startup: sandbox traffic is dropped by default.
//! The IPC server accepts scope-unlock requests from orchestrator that
//! dynamically open temporary allow rules per-target.

use anyhow::Result;
use clap::Parser;

#[derive(Parser)]
#[command(name = "yummi-gateway", about = "Yummi gateway trust boundary (Linux/nftables)")]
struct Cli {
    /// Path to config file
    #[arg(short, long, default_value = "/etc/yummi/gateway.yaml")]
    config: String,

    /// Remove nftables rules and exit (admin maintenance only)
    #[arg(long)]
    cleanup: bool,
}

#[tokio::main]
async fn main() {
    if let Err(e) = run().await {
        eprintln!("gateway fatal: {e:#}");
        std::process::exit(1);
    }
}

async fn run() -> Result<()> {
    #[cfg(not(target_os = "linux"))]
    return Err(anyhow::anyhow!("gateway only runs on Linux (nftables + /proc required)"));

    #[cfg(target_os = "linux")]
    run_linux().await
}

#[cfg(target_os = "linux")]
async fn run_linux() -> Result<()> {
    use tracing::info;
    use yummi_gateway::audit::AuditLog;
    use yummi_gateway::config::GatewayConfig;
    use yummi_gateway::ipc::IpcServer;
    use yummi_gateway::network::NftablesController;
    use yummi_gateway::observer::Observer;
    use yummi_gateway::policy::PolicyEngine;
    use yummi_gateway::watchdog::Watchdog;

    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .json()
        .init();

    let cli = Cli::parse();
    let cfg = GatewayConfig::load(&cli.config)?;

    info!(
        version = env!("CARGO_PKG_VERSION"),
        sandbox_ip = %cfg.network.sandbox_ip,
        "gateway starting"
    );

    let controller = NftablesController::new(&cfg);

    if cli.cleanup {
        info!("cleanup mode: removing nftables rules");
        controller.cleanup()?;
        info!("cleanup complete — sandbox traffic is NOW UNCONTROLLED");
        return Ok(());
    }

    // Install fail-closed FORWARD rules. On error, abort — never start without rules.
    controller.install_rules()?;
    let controller = std::sync::Arc::new(controller);
    info!("nftables FORWARD rules active (fail-closed)");

    let audit    = AuditLog::open(&cfg.logging.audit_db)?;
    let policy   = PolicyEngine::with_default_allow(&cfg.scope, cfg.policy.default_allow);
    let observer = Observer::new(policy.clone(), audit.clone());
    let ipc      = IpcServer::new(&cfg.ipc, policy.clone(), audit.clone())
        .with_controller(controller.clone());
    let watchdog = Watchdog::new(&cfg, audit.clone());

    // Background: evict TTL-expired temporary allow rules every 60 seconds.
    let policy_gc = policy.clone();
    let gc_handle = tokio::spawn(async move {
        let mut interval = tokio::time::interval(std::time::Duration::from_secs(60));
        loop {
            interval.tick().await;
            policy_gc.cleanup_expired();
        }
    });

    let observer_handle = tokio::spawn(observer.run());
    let ipc_handle      = tokio::spawn(ipc.run());
    let watchdog_handle = tokio::spawn(watchdog.run());

    info!("gateway ready — sandbox traffic gated (Level 0 passive observation)");

    tokio::signal::ctrl_c().await?;
    info!("shutdown signal received");

    // Intentionally NOT removing nftables rules on shutdown (fail-closed).
    // Use `--cleanup` to explicitly remove rules for maintenance.
    observer_handle.abort();
    ipc_handle.abort();
    watchdog_handle.abort();
    gc_handle.abort();

    audit.flush().await?;
    info!("gateway stopped cleanly (nftables rules still active)");

    Ok(())
}
