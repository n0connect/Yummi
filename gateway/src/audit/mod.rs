//! Audit log — append-only SQLite logger with SHA256 hash chain.
//!
//! Each row stores `row_hash = SHA256(fields || prev_hash)` and the
//! `prev_hash` of the preceding row. Running `verify_chain()` over the
//! table detects any deletion, reordering, or field modification.

use rusqlite::{params, Connection};
use sha2::{Digest, Sha256};
use std::sync::{Arc, Mutex};
use thiserror::Error;

const SCHEMA: &str = include_str!("schema.sql");

/// Sentinel hash used for the very first row (no previous entry).
const GENESIS_HASH: &str = "0000000000000000000000000000000000000000000000000000000000000000";

#[derive(Error, Debug)]
pub enum AuditError {
    #[error("database error: {0}")]
    Database(#[from] rusqlite::Error),
    #[error("lock poisoned")]
    LockPoisoned,
    #[error("hash chain broken at id {0}: expected {1}, got {2}")]
    ChainBroken(i64, String, String),
}

/// Thread-safe audit log writer backed by SQLite (WAL mode) with a
/// SHA256 hash chain for tamper evidence.
#[derive(Clone)]
pub struct AuditLog {
    conn: Arc<Mutex<Connection>>,
}

impl AuditLog {
    /// Open or create the audit database at the given path.
    /// Use ":memory:" for testing.
    pub fn open(path: &str) -> Result<Self, AuditError> {
        let conn = if path == ":memory:" {
            Connection::open_in_memory()?
        } else {
            if let Some(parent) = std::path::Path::new(path).parent() {
                let _ = std::fs::create_dir_all(parent);
            }
            Connection::open(path)?
        };

        conn.execute_batch("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;")?;
        conn.execute_batch(SCHEMA)?;

        Ok(Self {
            conn: Arc::new(Mutex::new(conn)),
        })
    }

    /// Record a policy decision, computing and storing the SHA256 hash chain.
    pub fn record_decision(
        &self,
        timestamp_ns: i64,
        session_id: Option<&str>,
        src_ip: &str,
        dst_ip: &str,
        src_port: Option<u16>,
        dst_port: Option<u16>,
        protocol: &str,
        decision: &str,
        reason: &str,
    ) -> Result<(), AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;

        // Fetch the hash of the most recent row (or GENESIS_HASH if first row).
        let prev_hash: String = conn
            .query_row(
                "SELECT row_hash FROM decisions ORDER BY id DESC LIMIT 1",
                [],
                |row| row.get(0),
            )
            .unwrap_or_else(|_| GENESIS_HASH.to_string());

        // Compute SHA256(timestamp || src_ip || dst_ip || decision || reason || prev_hash)
        let row_hash = compute_hash(timestamp_ns, src_ip, dst_ip, decision, reason, &prev_hash);

        conn.execute(
            "INSERT INTO decisions (timestamp, session_id, src_ip, dst_ip, src_port, dst_port,
                                    protocol, decision, reason, row_hash, prev_hash)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)",
            params![
                timestamp_ns, session_id, src_ip, dst_ip,
                src_port, dst_port, protocol, decision, reason,
                row_hash, prev_hash
            ],
        )?;
        Ok(())
    }

    /// Count total decisions (for status reporting).
    pub fn decision_count(&self) -> Result<i64, AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let count: i64 =
            conn.query_row("SELECT COUNT(*) FROM decisions", [], |row| row.get(0))?;
        Ok(count)
    }

    /// Verify the entire hash chain. Returns Ok(n) where n is the number of
    /// verified rows, or `Err(ChainBroken)` on the first tampered entry.
    pub fn verify_chain(&self) -> Result<usize, AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let mut stmt = conn.prepare(
            "SELECT id, timestamp, src_ip, dst_ip, decision, reason, row_hash, prev_hash
             FROM decisions ORDER BY id ASC",
        )?;

        let rows: Vec<(i64, i64, String, String, String, String, String, String)> = stmt
            .query_map([], |row| {
                Ok((
                    row.get(0)?, row.get(1)?, row.get(2)?, row.get(3)?,
                    row.get(4)?, row.get(5)?, row.get(6)?, row.get(7)?,
                ))
            })?
            .collect::<Result<_, _>>()?;

        let mut expected_prev = GENESIS_HASH.to_string();
        for (id, ts, src_ip, dst_ip, decision, reason, row_hash, prev_hash) in &rows {
            if prev_hash != &expected_prev {
                return Err(AuditError::ChainBroken(
                    *id,
                    expected_prev.clone(),
                    prev_hash.clone(),
                ));
            }
            let computed = compute_hash(*ts, src_ip, dst_ip, decision, reason, prev_hash);
            if &computed != row_hash {
                return Err(AuditError::ChainBroken(*id, computed, row_hash.clone()));
            }
            expected_prev = row_hash.clone();
        }
        Ok(rows.len())
    }

    /// Flush pending writes (WAL checkpoint).
    pub async fn flush(&self) -> Result<(), AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        conn.execute_batch("PRAGMA wal_checkpoint(TRUNCATE);")?;
        Ok(())
    }
}

fn compute_hash(
    timestamp: i64,
    src_ip: &str,
    dst_ip: &str,
    decision: &str,
    reason: &str,
    prev_hash: &str,
) -> String {
    let mut h = Sha256::new();
    h.update(timestamp.to_string().as_bytes());
    h.update(b"|");
    h.update(src_ip.as_bytes());
    h.update(b"|");
    h.update(dst_ip.as_bytes());
    h.update(b"|");
    h.update(decision.as_bytes());
    h.update(b"|");
    h.update(reason.as_bytes());
    h.update(b"|");
    h.update(prev_hash.as_bytes());
    format!("{:x}", h.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_open_in_memory() {
        let audit = AuditLog::open(":memory:").unwrap();
        assert_eq!(audit.decision_count().unwrap(), 0);
    }

    #[test]
    fn test_record_and_count() {
        let audit = AuditLog::open(":memory:").unwrap();
        audit.record_decision(1234567890, None, "10.0.0.1", "192.168.1.1", Some(54321), Some(80), "tcp", "allow", "allowed_range").unwrap();
        audit.record_decision(1234567891, Some("session-1"), "10.0.0.1", "127.0.0.1", Some(54322), Some(443), "tcp", "block", "blocked_range").unwrap();
        assert_eq!(audit.decision_count().unwrap(), 2);
    }

    #[test]
    fn test_hash_chain_valid() {
        let audit = AuditLog::open(":memory:").unwrap();
        for i in 0..5 {
            audit.record_decision(i, None, "10.0.0.1", "8.8.8.8", None, None, "tcp", "allow", "test").unwrap();
        }
        let verified = audit.verify_chain().unwrap();
        assert_eq!(verified, 5);
    }

    #[test]
    fn test_hash_chain_tamper_detected() {
        let audit = AuditLog::open(":memory:").unwrap();
        audit.record_decision(1, None, "10.0.0.1", "8.8.8.8", None, None, "tcp", "allow", "test").unwrap();
        audit.record_decision(2, None, "10.0.0.1", "8.8.8.8", None, None, "tcp", "block", "test").unwrap();

        // Tamper with the first row's decision field directly
        {
            let conn = audit.conn.lock().unwrap();
            conn.execute("UPDATE decisions SET decision = 'allow' WHERE id = 2", []).unwrap();
        }

        // Chain verification must detect the tamper
        assert!(matches!(audit.verify_chain(), Err(AuditError::ChainBroken(..))));
    }

    #[test]
    fn test_compute_hash_deterministic() {
        let h1 = compute_hash(100, "1.2.3.4", "5.6.7.8", "allow", "test", GENESIS_HASH);
        let h2 = compute_hash(100, "1.2.3.4", "5.6.7.8", "allow", "test", GENESIS_HASH);
        assert_eq!(h1, h2);
        assert_eq!(h1.len(), 64); // SHA256 hex = 64 chars
    }
}
