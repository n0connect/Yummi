// Package session manages pentest session lifecycle and persistence.
package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    gateway_level INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    timestamp INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    actor TEXT NOT NULL,
    payload TEXT
);

CREATE TABLE IF NOT EXISTS executions (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    proposal_id TEXT,
    command TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'manual_terminal',
    status TEXT NOT NULL,
    output TEXT,
    error TEXT,
    pinned_ips TEXT,
    mcp_tool TEXT,
    mcp_args TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    payload TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_log(session_id);
CREATE INDEX IF NOT EXISTS idx_executions_session ON executions(session_id);
CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id, id);
`

// Session represents a pentest engagement.
type Session struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	Status       string `json:"status"`
	GatewayLevel int    `json:"gateway_level"`
}

// AuditEvent represents a normalized audit log entry.
type AuditEvent struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Timestamp int64  `json:"timestamp"`
	EventType string `json:"event_type"`
	Actor     string `json:"actor"`
	Payload   string `json:"payload"`
}

// ChatMessage is one persisted turn of a session's conversation. Content is the
// human-readable text; Payload is optional opaque JSON the UI uses to rebuild a
// richer message (intent / proposals / task id) on reload. Role is "user",
// "assistant", or "system".
type ChatMessage struct {
	ID        int64  `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Payload   string `json:"payload,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// OpenDB opens (or creates) the SQLite database and applies schema.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	// Migrate databases whose executions table predates the pinned_ips column.
	_, _ = db.Exec(`ALTER TABLE executions ADD COLUMN pinned_ips TEXT`)
	// Migrate databases whose executions table predates the MCP-tool-call columns.
	_, _ = db.Exec(`ALTER TABLE executions ADD COLUMN mcp_tool TEXT`)
	_, _ = db.Exec(`ALTER TABLE executions ADD COLUMN mcp_args TEXT`)

	// Limit connections for SQLite
	db.SetMaxOpenConns(1)

	return db, nil
}

// Manager handles session CRUD operations.
type Manager struct {
	db *sql.DB
}

// NewManager creates a session manager.
func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db}
}

// Create inserts a new session.
func (m *Manager) Create(s *Session) error {
	_, err := m.db.Exec(
		"INSERT INTO sessions (id, name, created_at, updated_at, status, gateway_level) VALUES (?, ?, ?, ?, ?, ?)",
		s.ID, s.Name, s.CreatedAt, s.UpdatedAt, s.Status, s.GatewayLevel,
	)
	return err
}

// Get retrieves a session by ID.
func (m *Manager) Get(id string) (*Session, error) {
	s := &Session{}
	err := m.db.QueryRow(
		"SELECT id, name, created_at, updated_at, status, gateway_level FROM sessions WHERE id = ?", id,
	).Scan(&s.ID, &s.Name, &s.CreatedAt, &s.UpdatedAt, &s.Status, &s.GatewayLevel)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// List returns all sessions.
func (m *Manager) List() ([]Session, error) {
	rows, err := m.db.Query("SELECT id, name, created_at, updated_at, status, gateway_level FROM sessions ORDER BY updated_at DESC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Name, &s.CreatedAt, &s.UpdatedAt, &s.Status, &s.GatewayLevel); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// Update modifies a session.
func (m *Manager) Update(s *Session) error {
	_, err := m.db.Exec(
		"UPDATE sessions SET name = ?, updated_at = ?, status = ?, gateway_level = ? WHERE id = ?",
		s.Name, s.UpdatedAt, s.Status, s.GatewayLevel, s.ID,
	)
	return err
}

// Delete soft-deletes a session (sets status to 'closed').
func (m *Manager) Delete(id string) error {
	_, err := m.db.Exec("UPDATE sessions SET status = 'closed' WHERE id = ?", id)
	return err
}

// AppendAudit writes a single audit event.
func (m *Manager) AppendAudit(sessionID, eventType, actor string, payload any) error {
	serialized := ""
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal audit payload: %w", err)
		}
		serialized = string(buf)
	}

	_, err := m.db.Exec(
		"INSERT INTO audit_log (session_id, timestamp, event_type, actor, payload) VALUES (?, ?, ?, ?, ?)",
		sessionID, time.Now().Unix(), eventType, actor, serialized,
	)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

// maxChatContentBytes bounds a single persisted message. Execution output and
// tool results fed into the thread originate on a (hostile) target; an
// unbounded field could bloat the DB and the reload payload with one oversized
// record. Mirrors the UI's own MAX_MESSAGE_CONTENT_CHARS cap.
const maxChatContentBytes = 64 * 1024

// AppendMessage persists one conversation turn. content is capped; payload may
// be empty. Best-effort: callers treat a persistence failure as non-fatal
// (the live UI already has the message in memory).
func (m *Manager) AppendMessage(sessionID, role, content, payload string) error {
	if sessionID == "" || role == "" {
		return fmt.Errorf("session id and role are required")
	}
	if len(content) > maxChatContentBytes {
		content = content[:maxChatContentBytes]
	}
	var payloadArg any
	if payload != "" {
		payloadArg = payload
	}
	_, err := m.db.Exec(
		"INSERT INTO chat_messages (session_id, role, content, payload, created_at) VALUES (?, ?, ?, ?, ?)",
		sessionID, role, content, payloadArg, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert chat message: %w", err)
	}
	return nil
}

// ListMessages returns a session's conversation in chronological order (oldest
// first), capped at the most recent limit turns.
func (m *Manager) ListMessages(sessionID string, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	// Take the newest `limit` rows (id DESC), then flip to chronological order
	// so the caller gets the tail of a long conversation, oldest-first.
	rows, err := m.db.Query(
		"SELECT id, session_id, role, content, COALESCE(payload,''), created_at FROM chat_messages WHERE session_id = ? ORDER BY id DESC LIMIT ?",
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	msgs := make([]ChatMessage, 0, limit)
	for rows.Next() {
		var c ChatMessage
		if err := rows.Scan(&c.ID, &c.SessionID, &c.Role, &c.Content, &c.Payload, &c.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse in place → chronological.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// ListAudit returns audit events for a session ordered by latest first.
func (m *Manager) ListAudit(sessionID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	rows, err := m.db.Query(
		"SELECT id, COALESCE(session_id,''), timestamp, event_type, actor, COALESCE(payload,'') FROM audit_log WHERE session_id = ? ORDER BY id DESC LIMIT ?",
		sessionID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]AuditEvent, 0, limit)
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Timestamp, &e.EventType, &e.Actor, &e.Payload); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
