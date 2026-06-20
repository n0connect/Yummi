//! Packet metadata extraction.

use serde::Serialize;

/// Captured packet metadata (Level 0 — no payload inspection).
#[derive(Debug, Clone, Serialize)]
pub struct PacketCapture {
    pub timestamp_ns: i64,
    pub src_ip: String,
    pub dst_ip: String,
    pub src_port: Option<u16>,
    pub dst_port: Option<u16>,
    pub protocol: String,
    pub byte_count: u64,
    pub tcp_flags: Option<u8>,
}

impl PacketCapture {
    /// Create from network-layer packet metadata.
    pub fn from_meta(meta: &crate::network::PacketMeta) -> Self {
        Self {
            timestamp_ns: meta.timestamp_ns,
            src_ip: meta.src_ip.to_string(),
            dst_ip: meta.dst_ip.to_string(),
            src_port: meta.src_port,
            dst_port: meta.dst_port,
            protocol: meta.protocol.to_string(),
            byte_count: meta.byte_count,
            tcp_flags: meta.tcp_flags,
        }
    }
}

/// TCP flag constants.
pub mod tcp_flags {
    pub const FIN: u8 = 0x01;
    pub const SYN: u8 = 0x02;
    pub const RST: u8 = 0x04;
    pub const PSH: u8 = 0x08;
    pub const ACK: u8 = 0x10;

    /// Format TCP flags as a human-readable string.
    pub fn to_string(flags: u8) -> String {
        let mut parts = Vec::new();
        if flags & SYN != 0 {
            parts.push("SYN");
        }
        if flags & ACK != 0 {
            parts.push("ACK");
        }
        if flags & FIN != 0 {
            parts.push("FIN");
        }
        if flags & RST != 0 {
            parts.push("RST");
        }
        if flags & PSH != 0 {
            parts.push("PSH");
        }
        parts.join("|")
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::network::Protocol;
    use std::net::IpAddr;

    #[test]
    fn test_packet_capture_from_meta() {
        let meta = crate::network::PacketMeta {
            timestamp_ns: 1234567890,
            src_ip: "10.0.0.1".parse::<IpAddr>().unwrap(),
            dst_ip: "192.168.1.1".parse::<IpAddr>().unwrap(),
            src_port: Some(54321),
            dst_port: Some(80),
            protocol: Protocol::Tcp,
            byte_count: 1500,
            tcp_flags: Some(tcp_flags::SYN),
        };
        let cap = PacketCapture::from_meta(&meta);
        assert_eq!(cap.src_ip, "10.0.0.1");
        assert_eq!(cap.dst_ip, "192.168.1.1");
        assert_eq!(cap.protocol, "tcp");
    }

    #[test]
    fn test_tcp_flags_display() {
        assert_eq!(
            tcp_flags::to_string(tcp_flags::SYN | tcp_flags::ACK),
            "SYN|ACK"
        );
        assert_eq!(tcp_flags::to_string(tcp_flags::FIN), "FIN");
        assert_eq!(tcp_flags::to_string(0), "");
    }
}
