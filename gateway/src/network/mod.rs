//! Network controller — Linux/nftables only.
//!
//! gateway is designed exclusively for Linux (Docker). It manages nftables FORWARD
//! chain rules on the Docker bridge so that sandbox's outbound traffic is filtered
//! at the network level before reaching external targets.
//!
//! There is no cross-platform abstraction: this module compiles and runs only
//! on Linux. The trait layer has been removed — `NftablesController` is the
//! single concrete implementation.

pub mod nftables;

use thiserror::Error;

#[derive(Error, Debug)]
pub enum NetworkError {
    #[error("nftables install failed: {0}")]
    InstallFailed(String),
    #[error("nftables cleanup failed: {0}")]
    CleanupFailed(String),
}

pub use nftables::NftablesController;

// ── Traffic metadata ─────────────────────────────────────────────────────────
// Used by the observer to record connection decisions in the audit log.

/// Traffic metadata captured from /proc/net entries.
#[derive(Debug, Clone)]
pub struct PacketMeta {
    pub timestamp_ns: i64,
    pub src_ip:       std::net::IpAddr,
    pub dst_ip:       std::net::IpAddr,
    pub src_port:     Option<u16>,
    pub dst_port:     Option<u16>,
    pub protocol:     Protocol,
    pub byte_count:   u64,
    pub tcp_flags:    Option<u8>,
}

/// Network protocol identifier.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Protocol {
    Tcp,
    Udp,
    Icmp,
    Other(u8),
}

impl std::fmt::Display for Protocol {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Protocol::Tcp      => write!(f, "tcp"),
            Protocol::Udp      => write!(f, "udp"),
            Protocol::Icmp     => write!(f, "icmp"),
            Protocol::Other(n) => write!(f, "proto:{n}"),
        }
    }
}
