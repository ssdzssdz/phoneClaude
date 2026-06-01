package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Store is the SQLite-backed persistence layer for the bridge daemon.
type Store struct {
	db *sql.DB
}

// MessageRow represents a single stored message within a session.
type MessageRow struct {
	ID        int64
	SessionID string
	Role      string
	Type      string
	Content   string
	CreatedAt time.Time
}

// SessionRow represents a terminal session.
type SessionRow struct {
	ID         string
	Name       string
	WorkDir    string
	Status     string
	CreatedAt  time.Time
	LastActive time.Time
}

// DeviceRow represents a registered push device (FCM token).
type DeviceRow struct {
	ID        int64
	Token     string
	Name      string
	CreatedAt time.Time
}

// ---------------------------------------------------------------------------
// Store lifecycle
// ---------------------------------------------------------------------------

// Open opens (or creates) the SQLite database at path, enables WAL journal mode
// and foreign keys, and runs the migration.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// WAL journal mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=wal"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	// Enforce foreign-key constraints.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close shuts down the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate creates the schema tables and indexes if they do not exist.
func (s *Store) migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL DEFAULT '',
			work_dir   TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL,
			last_active TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role       TEXT NOT NULL,
			type       TEXT NOT NULL DEFAULT 'text',
			content    TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS devices (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			token      TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_created
		 ON messages(session_id, created_at)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate query: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// SaveMessage inserts a new message row.
func (s *Store) SaveMessage(msg MessageRow) error {
	const q = `INSERT INTO messages (session_id, role, type, content, created_at)
	            VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(q, msg.SessionID, msg.Role, msg.Type, msg.Content, msg.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}
	return nil
}

// GetMessages retrieves the most recent `limit` messages for a session,
// returned in chronological (oldest-first) order.
func (s *Store) GetMessages(sessionID string, limit int) ([]MessageRow, error) {
	const q = `SELECT id, session_id, role, type, content, created_at
	            FROM messages
	            WHERE session_id = ?
	            ORDER BY created_at DESC
	            LIMIT ?`
	rows, err := s.db.Query(q, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var msgs []MessageRow
	for rows.Next() {
		var m MessageRow
		var createdStr string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Type, &m.Content, &createdStr); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	// Reverse to chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// CreateSession inserts a new session row.
func (s *Store) CreateSession(sess SessionRow) error {
	const q = `INSERT INTO sessions (id, name, work_dir, status, created_at, last_active)
	            VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(q,
		sess.ID,
		sess.Name,
		sess.WorkDir,
		sess.Status,
		sess.CreatedAt.UTC().Format(time.RFC3339),
		sess.LastActive.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// UpdateSessionStatus sets the session status and bumps last_active.
func (s *Store) UpdateSessionStatus(id, status string) error {
	const q = `UPDATE sessions SET status = ?, last_active = ? WHERE id = ?`
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(q, status, now, id)
	if err != nil {
		return fmt.Errorf("update session status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session %q not found", id)
	}
	return nil
}

// TouchSession updates only the last_active timestamp.
func (s *Store) TouchSession(id string) error {
	const q = `UPDATE sessions SET last_active = ? WHERE id = ?`
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(q, now, id)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session %q not found", id)
	}
	return nil
}

// DeleteSession removes a session and its messages (via CASCADE).
func (s *Store) DeleteSession(id string) error {
	const q = `DELETE FROM sessions WHERE id = ?`
	res, err := s.db.Exec(q, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session %q not found", id)
	}
	return nil
}

// ListSessions returns all sessions ordered by last_active descending.
func (s *Store) ListSessions() ([]SessionRow, error) {
	const q = `SELECT id, name, work_dir, status, created_at, last_active
	            FROM sessions
	            ORDER BY last_active DESC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRow
	for rows.Next() {
		var sess SessionRow
		var createdStr, lastStr string
		if err := rows.Scan(&sess.ID, &sess.Name, &sess.WorkDir, &sess.Status, &createdStr, &lastStr); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sess.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		sess.LastActive, _ = time.Parse(time.RFC3339, lastStr)
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return sessions, nil
}

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

// SaveDevice inserts or replaces a device row (keyed on token).
func (s *Store) SaveDevice(device DeviceRow) error {
	const q = `INSERT OR REPLACE INTO devices (token, name, created_at)
	            VALUES (?, ?, ?)`
	_, err := s.db.Exec(q, device.Token, device.Name, device.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save device: %w", err)
	}
	return nil
}

// ListDevices returns all registered devices.
func (s *Store) ListDevices() ([]DeviceRow, error) {
	const q = `SELECT id, token, name, created_at FROM devices ORDER BY id`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var devices []DeviceRow
	for rows.Next() {
		var d DeviceRow
		var createdStr string
		if err := rows.Scan(&d.ID, &d.Token, &d.Name, &createdStr); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return devices, nil
}
