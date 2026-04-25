package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wkirschbaum/whkmail/internal/types"
	_ "modernc.org/sqlite"
)

// SQLite is a SQLite-backed Store. Use OpenSQLite to construct one.
type SQLite struct {
	db *sql.DB
}

// Compile-time check that *SQLite satisfies the Store contract.
var _ Store = (*SQLite)(nil)

// OpenSQLite opens (or creates) the SQLite database at path and runs migrations.
func OpenSQLite(path string) (*SQLite, error) {
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
	s := &SQLite{db: db}
	return s, s.migrate()
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) migrate() error {
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
			draft     INTEGER NOT NULL DEFAULT 0,
			body_text TEXT    NOT NULL DEFAULT '',
			PRIMARY KEY (folder, uid)
		);
		CREATE INDEX IF NOT EXISTS idx_messages_folder_date ON messages (folder, date DESC);
		CREATE TABLE IF NOT EXISTS folders (
			name         TEXT    PRIMARY KEY,
			delimiter    TEXT    NOT NULL DEFAULT '',
			uid_validity INTEGER NOT NULL DEFAULT 0,
			uid_next     INTEGER NOT NULL DEFAULT 1
		);
	`)
	if err != nil {
		return err
	}
	// Idempotent column additions for databases created before these columns
	// existed. SQLite returns "duplicate column name" when a column is already
	// present — that error is expected and safe to ignore; any other error
	// (disk full, corrupt DB, etc.) is surfaced.
	for _, stmt := range []string{
		`ALTER TABLE folders ADD COLUMN uid_validity INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE folders ADD COLUMN uid_next INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE messages ADD COLUMN draft INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN body_fetched INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN message_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN in_reply_to TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN answered INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.ExecContext(context.Background(), stmt); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("migrate: %w", err)
			}
		}
	}
	return nil
}

func (s *SQLite) UpsertMessage(ctx context.Context, m types.Message) (bool, error) {
	out, err := s.UpsertMessages(ctx, []types.Message{m})
	if err != nil {
		return false, err
	}
	return out[0], nil
}

// SQL fragments shared by UpsertMessages — kept at file scope so each call
// isn't re-allocating strings.
const (
	sqlInsertIgnoreMessage = `
		INSERT OR IGNORE INTO messages
		  (uid, folder, subject, from_addr, to_addr, date, unread, flagged, answered, draft, body_text, message_id, in_reply_to)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	sqlUpdateMessageHeaders = `
		UPDATE messages SET
		  subject=?, from_addr=?, to_addr=?, date=?,
		  unread=?, flagged=?, answered=?, draft=?
		WHERE folder=? AND uid=?`
)

// UpsertMessages writes the entire slice inside one transaction using two
// prepared statements — an INSERT OR IGNORE that detects first-time
// arrivals via rowsAffected, followed by an UPDATE of header + flag fields
// when the row already existed. The body_text column is *not* touched here;
// body caching lives on its own path (SetBodyText) so a re-sync never clobbers
// a cached body.
func (s *SQLite) UpsertMessages(ctx context.Context, msgs []types.Message) ([]bool, error) {
	out := make([]bool, len(msgs))
	if len(msgs) == 0 {
		return out, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	insertStmt, err := tx.PrepareContext(ctx, sqlInsertIgnoreMessage)
	if err != nil {
		return nil, err
	}
	defer func() { _ = insertStmt.Close() }()

	updateStmt, err := tx.PrepareContext(ctx, sqlUpdateMessageHeaders)
	if err != nil {
		return nil, err
	}
	defer func() { _ = updateStmt.Close() }()

	for i, m := range msgs {
		res, err := insertStmt.ExecContext(ctx,
			m.UID, m.Folder, m.Subject, m.From, m.To, m.Date.Unix(),
			boolInt(m.Unread), boolInt(m.Flagged), boolInt(m.Answered), boolInt(m.Draft), m.BodyText,
			m.MessageID, m.InReplyTo,
		)
		if err != nil {
			return nil, fmt.Errorf("insert uid %d: %w", m.UID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 1 {
			out[i] = true
			continue
		}
		// Row existed — refresh the headers + flags (but not body_text).
		if _, err := updateStmt.ExecContext(ctx,
			m.Subject, m.From, m.To, m.Date.Unix(),
			boolInt(m.Unread), boolInt(m.Flagged), boolInt(m.Answered), boolInt(m.Draft),
			m.Folder, m.UID,
		); err != nil {
			return nil, fmt.Errorf("update uid %d: %w", m.UID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLite) ListMessages(ctx context.Context, folder string, limit int) ([]types.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT uid, folder, subject, from_addr, to_addr, date, unread, flagged, answered, draft, message_id, in_reply_to
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
		var unread, flagged, answered, draft int
		if err := rows.Scan(&m.UID, &m.Folder, &m.Subject, &m.From, &m.To, &ts, &unread, &flagged, &answered, &draft, &m.MessageID, &m.InReplyTo); err != nil {
			return nil, err
		}
		m.Date = time.Unix(ts, 0)
		m.Unread = unread == 1
		m.Flagged = flagged == 1
		m.Answered = answered == 1
		m.Draft = draft == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *SQLite) GetMessage(ctx context.Context, folder string, uid uint32) (*types.Message, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT uid, folder, subject, from_addr, to_addr, date, unread, flagged, answered, draft, body_text, body_fetched, message_id, in_reply_to
		FROM messages WHERE folder = ? AND uid = ?
	`, folder, uid)
	var m types.Message
	var ts int64
	var unread, flagged, answered, draft, bodyFetched int
	err := row.Scan(&m.UID, &m.Folder, &m.Subject, &m.From, &m.To, &ts, &unread, &flagged, &answered, &draft, &m.BodyText, &bodyFetched, &m.MessageID, &m.InReplyTo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Date = time.Unix(ts, 0)
	m.Unread = unread == 1
	m.Flagged = flagged == 1
	m.Answered = answered == 1
	m.Draft = draft == 1
	m.BodyFetched = bodyFetched == 1
	return &m, nil
}

func (s *SQLite) UpsertFolder(ctx context.Context, f types.Folder) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO folders (name, delimiter)
		VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET delimiter=excluded.delimiter
	`, f.Name, f.Delimiter)
	return err
}

func (s *SQLite) ListFolders(ctx context.Context) ([]types.Folder, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.name, f.delimiter,
		       COUNT(m.uid)              AS message_count,
		       COALESCE(SUM(m.unread), 0) AS unread
		FROM folders f
		LEFT JOIN messages m ON m.folder = f.name
		GROUP BY f.name, f.delimiter
		ORDER BY f.name
	`)
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

func (s *SQLite) SetBodyText(ctx context.Context, folder string, uid uint32, body string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET body_text = ?, body_fetched = 1 WHERE folder = ? AND uid = ?`,
		body, folder, uid)
	return err
}

func (s *SQLite) MarkSeen(ctx context.Context, folder string, uid uint32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET unread = 0 WHERE folder = ? AND uid = ?`,
		folder, uid)
	return err
}

func (s *SQLite) MarkUnseen(ctx context.Context, folder string, uid uint32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET unread = 1 WHERE folder = ? AND uid = ?`,
		folder, uid)
	return err
}

func (s *SQLite) DeleteMessage(ctx context.Context, folder string, uid uint32) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM messages WHERE folder = ? AND uid = ?`,
		folder, uid)
	return err
}

func (s *SQLite) GetFolderSync(ctx context.Context, folder string) (uidValidity, uidNext uint32, err error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT uid_validity, uid_next FROM folders WHERE name = ?`, folder)
	err = row.Scan(&uidValidity, &uidNext)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 1, nil
	}
	return
}

func (s *SQLite) UpdateFolderSync(ctx context.Context, folder string, uidValidity, uidNext uint32) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE folders SET uid_validity = ?, uid_next = ? WHERE name = ?`,
		uidValidity, uidNext, folder)
	return err
}

func (s *SQLite) DeleteFolderMessages(ctx context.Context, folder string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM messages WHERE folder = ?`, folder)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
