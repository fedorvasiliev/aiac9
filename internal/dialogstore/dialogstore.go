// Package dialogstore persists LLM dialog messages to a local SQLite
// database — CLAUDE.md's "## Диалоги": "Сохраняем сообщения диалога в
// таблицу dialog в SQLite."
//
// The standard library has no SQL driver of its own, so this is the one
// place the project's "только стандартная библиотека" convention is
// deliberately relaxed — CLAUDE.md's own conventions now explicitly call
// for "локальный SQLite последней версии". modernc.org/sqlite is a pure-Go
// driver (no cgo, no C toolchain needed), so `go build`/`go run` keep
// working exactly as before.
package dialogstore

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultPath is where the dialog history database lives by default.
const DefaultPath = "aiac9.db"

// Store persists dialog messages, one row per message.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and
// ensures its "dialog" table exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	const schema = `
CREATE TABLE IF NOT EXISTS dialog (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	dialog_id  TEXT NOT NULL,
	role       TEXT NOT NULL,
	content    TEXT NOT NULL,
	created_at DATETIME NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create dialog table: %w", err)
	}

	return &Store{db: db}, nil
}

// AppendMessage records one dialog message: dialogID groups every message
// of the same conversation together, role/content mirror the LLM chat
// message they came from ("system"/"user"/"assistant" and message.content).
func (s *Store) AppendMessage(dialogID, role, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO dialog (dialog_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		dialogID, role, content, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert dialog message: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
