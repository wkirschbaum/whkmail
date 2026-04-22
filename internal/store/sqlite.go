package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/wkirschbaum/whkmail/internal/types"
	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed message and folder cache.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Serialize all access through one connection to prevent SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	s := &Store{db: db}
	return s, s.migrate()
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS messages (
			uid       INTEGER NOT NULL,
			folder    TEXT    NOT NULL,
			subject   TEXT    NOT NULL DEFAULT '',
			from_addr TEXT    NOT NULL DEFAULT '',
			to_addr   TEXT    NOT NULL DEFAULT '',
			date      INTEGER NOT NULL DEFAULT 0,
			unread    INTEGER NOT NULL DEFAULT 1,
			flagged   INTEGER NOT NULL DEFAULT 0,
			body_text TEXT    NOT NULL DEFAULT '',
			PRIMARY KEY (folder, uid)
		);
		CREATE INDEX IF NOT EXISTS idx_messages_folder_date ON messages (folder, date DESC);
		CREATE TABLE IF NOT EXISTS folders (
			name          TEXT    PRIMARY KEY,
			delimiter     TEXT    NOT NULL DEFAULT '',
			message_count INTEGER NOT NULL DEFAULT 0,
			unread        INTEGER NOT NULL DEFAULT 0
		);
	`)
	return err
}

// UpsertMessage inserts or updates a message in the cache.
func (s *Store) UpsertMessage(ctx context.Context, m types.Message) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (uid, folder, subject, from_addr, to_addr, date, unread, flagged, body_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder, uid) DO UPDATE SET
			subject=excluded.subject, from_addr=excluded.from_addr,
			to_addr=excluded.to_addr, date=excluded.date,
			unread=excluded.unread, flagged=excluded.flagged,
			body_text=excluded.body_text
	`, m.UID, m.Folder, m.Subject, m.From, m.To, m.Date.Unix(), boolInt(m.Unread), boolInt(m.Flagged), m.BodyText)
	return err
}

// ListMessages returns the most recent limit messages from folder, newest first.
func (s *Store) ListMessages(ctx context.Context, folder string, limit int) ([]types.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT uid, folder, subject, from_addr, to_addr, date, unread, flagged
		FROM messages WHERE folder = ? ORDER BY date DESC LIMIT ?
	`, folder, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []types.Message
	for rows.Next() {
		var m types.Message
		var ts int64
		var unread, flagged int
		if err := rows.Scan(&m.UID, &m.Folder, &m.Subject, &m.From, &m.To, &ts, &unread, &flagged); err != nil {
			return nil, err
		}
		m.Date = time.Unix(ts, 0)
		m.Unread = unread == 1
		m.Flagged = flagged == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMessage returns a single message including its body, or nil if not found.
func (s *Store) GetMessage(ctx context.Context, folder string, uid uint32) (*types.Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT uid, folder, subject, from_addr, to_addr, date, unread, flagged, body_text
		FROM messages WHERE folder = ? AND uid = ?
	`, folder, uid)
	var m types.Message
	var ts int64
	var unread, flagged int
	err := row.Scan(&m.UID, &m.Folder, &m.Subject, &m.From, &m.To, &ts, &unread, &flagged, &m.BodyText)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Date = time.Unix(ts, 0)
	m.Unread = unread == 1
	m.Flagged = flagged == 1
	return &m, nil
}

// UpsertFolder inserts or updates a folder record.
func (s *Store) UpsertFolder(ctx context.Context, f types.Folder) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folders (name, delimiter, message_count, unread)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			delimiter=excluded.delimiter,
			message_count=excluded.message_count,
			unread=excluded.unread
	`, f.Name, f.Delimiter, f.MessageCount, f.Unread)
	return err
}

// ListFolders returns all known folders ordered by name.
func (s *Store) ListFolders(ctx context.Context) ([]types.Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, delimiter, message_count, unread FROM folders ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []types.Folder
	for rows.Next() {
		var f types.Folder
		if err := rows.Scan(&f.Name, &f.Delimiter, &f.MessageCount, &f.Unread); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
