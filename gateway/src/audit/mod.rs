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

type AuditChainRow = (i64, i64, String, String, String, String, String, String);

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
    // The arguments intentionally mirror the append-only database columns at
    // this boundary; packaging them would obscure the audited field order.
    #[allow(clippy::too_many_arguments)]
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
                timestamp_ns,
                session_id,
                src_ip,
                dst_ip,
                src_port,
                dst_port,
                protocol,
                decision,
                reason,
                row_hash,
                prev_hash
            ],
        )?;
        Ok(())
    }

    /// Count total decisions (for status reporting).
    pub fn decision_count(&self) -> Result<i64, AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let count: i64 = conn.query_row("SELECT COUNT(*) FROM decisions", [], |row| row.get(0))?;
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

        let rows: Vec<AuditChainRow> = stmt
            .query_map([], |row| {
                Ok((
                    row.get(0)?,
                    row.get(1)?,
                    row.get(2)?,
                    row.get(3)?,
                    row.get(4)?,
                    row.get(5)?,
                    row.get(6)?,
                    row.get(7)?,
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

    /// Record a rule lifecycle event (add/remove) into the same tamper-evident
    /// chain used for network decisions. `event` is "rule_added" or
    /// "rule_removed"; `reason` is prefixed with the rule's source so the audit
    /// trail distinguishes operator-issued from agent-issued rules without a
    /// schema change.
    pub fn record_rule_event(
        &self,
        timestamp_ns: i64,
        network: &str,
        event: &str,
        source_prefixed_reason: &str,
    ) -> Result<(), AuditError> {
        self.record_decision(
            timestamp_ns,
            None,
            "gateway",
            network,
            None,
            None,
            "rule",
            event,
            source_prefixed_reason,
        )
    }

    /// Fetch the most recent `limit` audit rows (oldest first, for chronological
    /// display) plus whether the full chain currently verifies.
    pub fn recent_rows(&self, limit: usize) -> Result<(Vec<AuditRow>, bool), AuditError> {
        let conn = self.conn.lock().map_err(|_| AuditError::LockPoisoned)?;
        let mut stmt = conn.prepare(
            "SELECT id, timestamp, src_ip, dst_ip, decision, reason
             FROM decisions ORDER BY id DESC LIMIT ?1",
        )?;
        let mut rows: Vec<AuditRow> = stmt
            .query_map([limit as i64], |row| {
                Ok(AuditRow {
                    id: row.get(0)?,
                    timestamp: row.get(1)?,
                    src_ip: row.get(2)?,
                    dst_ip: row.get(3)?,
                    decision: row.get(4)?,
                    reason: row.get(5)?,
                })
            })?
            .collect::<Result<_, _>>()?;
        rows.reverse(); // oldest first
        drop(stmt);
        drop(conn);
        let verified = self.verify_chain().is_ok();
        Ok((rows, verified))
    }
}

/// A single audit row read back for display (e.g. in the gateway management
/// UI's recent-activity tail). Distinct from `ipc::protocol::AuditRowView`,
/// which is the IPC wire type — a later task converts between them.
#[derive(Debug, Clone)]
pub struct AuditRow {
    pub id: i64,
    pub timestamp: i64,
    pub src_ip: String,
    pub dst_ip: String,
    pub decision: String,
    pub reason: String,
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
        audit
            .record_decision(
                1234567890,
                None,
                "10.0.0.1",
                "192.168.1.1",
                Some(54321),
                Some(80),
                "tcp",
                "allow",
                "allowed_range",
            )
            .unwrap();
        audit
            .record_decision(
                1234567891,
                Some("session-1"),
                "10.0.0.1",
                "127.0.0.1",
                Some(54322),
                Some(443),
                "tcp",
                "block",
                "blocked_range",
            )
            .unwrap();
        assert_eq!(audit.decision_count().unwrap(), 2);
    }

    #[test]
    fn test_hash_chain_valid() {
        let audit = AuditLog::open(":memory:").unwrap();
        for i in 0..5 {
            audit
                .record_decision(
                    i, None, "10.0.0.1", "8.8.8.8", None, None, "tcp", "allow", "test",
                )
                .unwrap();
        }
        let verified = audit.verify_chain().unwrap();
        assert_eq!(verified, 5);
    }

    #[test]
    fn test_hash_chain_tamper_detected() {
        let audit = AuditLog::open(":memory:").unwrap();
        audit
            .record_decision(
                1, None, "10.0.0.1", "8.8.8.8", None, None, "tcp", "allow", "test",
            )
            .unwrap();
        audit
            .record_decision(
                2, None, "10.0.0.1", "8.8.8.8", None, None, "tcp", "block", "test",
            )
            .unwrap();

        // Tamper with the first row's decision field directly
        {
            let conn = audit.conn.lock().unwrap();
            conn.execute("UPDATE decisions SET decision = 'allow' WHERE id = 2", [])
                .unwrap();
        }

        // Chain verification must detect the tamper
        assert!(matches!(
            audit.verify_chain(),
            Err(AuditError::ChainBroken(..))
        ));
    }

    #[test]
    fn test_compute_hash_deterministic() {
        let h1 = compute_hash(100, "1.2.3.4", "5.6.7.8", "allow", "test", GENESIS_HASH);
        let h2 = compute_hash(100, "1.2.3.4", "5.6.7.8", "allow", "test", GENESIS_HASH);
        assert_eq!(h1, h2);
        assert_eq!(h1.len(), 64); // SHA256 hex = 64 chars
    }

    #[test]
    fn test_record_rule_event_then_recent_rows() {
        let log = AuditLog::open(":memory:").expect("open audit log");
        log.record_rule_event(1000, "10.0.0.5/32", "rule_added", "[manual] test reason")
            .expect("record rule event");
        let (rows, verified) = log.recent_rows(10).expect("recent rows");
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].decision, "rule_added");
        assert_eq!(rows[0].reason, "[manual] test reason");
        assert!(verified);
    }

    #[test]
    fn test_recent_rows_respects_limit() {
        let log = AuditLog::open(":memory:").expect("open audit log");
        for i in 0..5 {
            log.record_rule_event(1000 + i, "10.0.0.1/32", "rule_added", "[agent] test")
                .expect("record");
        }
        let (rows, _verified) = log.recent_rows(2).expect("recent rows");
        assert_eq!(rows.len(), 2);
        // Must be the 2 MOST RECENT rows (timestamps 1003, 1004), not the oldest 2 —
        // and returned oldest-first (chronological order).
        assert_eq!(
            rows[0].timestamp, 1003,
            "expected the second-most-recent row first (oldest-first ordering)"
        );
        assert_eq!(rows[1].timestamp, 1004, "expected the most recent row last");
    }
}
