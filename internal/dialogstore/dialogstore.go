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
	"errors"
	"fmt"
	"strings"
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
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	dialog_id         TEXT NOT NULL,
	parent_id         TEXT,
	role              TEXT NOT NULL,
	content           TEXT NOT NULL,
	model             TEXT,
	prompt_tokens     INTEGER,
	completion_tokens INTEGER,
	created_at        DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS dialog_summary (
	dialog_id  TEXT PRIMARY KEY,
	summary    TEXT NOT NULL,
	updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS facts (
	dialog_id  TEXT NOT NULL,
	key        TEXT NOT NULL,
	value      TEXT NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (dialog_id, key)
);
CREATE TABLE IF NOT EXISTS agent (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	profile    TEXT NOT NULL DEFAULT '',
	invariants TEXT NOT NULL DEFAULT '',
	updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS task_state (
	dialog_id   TEXT PRIMARY KEY,
	phase       TEXT NOT NULL,
	prompt_file TEXT NOT NULL DEFAULT '',
	extra       TEXT NOT NULL DEFAULT '',
	updated_at  DATETIME NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create dialog tables: %w", err)
	}

	// Best-effort upgrade for a database file created before these columns
	// existed: SQLite has no "ADD COLUMN IF NOT EXISTS", so a "duplicate
	// column" failure (the common case: the table was already current) is
	// expected and ignored.
	for _, stmt := range []string{
		`ALTER TABLE dialog ADD COLUMN prompt_tokens INTEGER`,
		`ALTER TABLE dialog ADD COLUMN completion_tokens INTEGER`,
		`ALTER TABLE dialog ADD COLUMN model TEXT`,
		`ALTER TABLE dialog ADD COLUMN parent_id TEXT`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("upgrade dialog table: %w", err)
		}
	}

	return &Store{db: db}, nil
}

// AppendMessage records one dialog message: dialogID groups every message
// of the same conversation together, role/content mirror the LLM chat
// message they came from ("system"/"user"/"assistant" and message.content).
// model is the model this turn was sent to — CLAUDE.md's "Работа с
// историей диалогов" reuses "последней использованной модели" of a
// dialog when summarizing it, so every row records which model its turn
// used. parentID is CLAUDE.md's Context Strategy "Branching": non-empty
// only for a dialog forked off another one ("в значение поля parent_id
// пишется id того диалога чьей веткой является создаваемый диалог"), and
// then the same on every row of the branch, mirroring how model is
// stamped on every row. promptTokens/completionTokens are nil unless known
// — CLAUDE.md: "prompt_tokens" в строчку с запросом и "completion_tokens"
// в строчку с ответом", so callers pass promptTokens for a turn's
// request-side messages (system/user) and completionTokens for its
// assistant reply, leaving the other nil.
func (s *Store) AppendMessage(dialogID, role, content, model, parentID string, promptTokens, completionTokens *int) error {
	var parent any
	if parentID != "" {
		parent = parentID
	}
	_, err := s.db.Exec(
		`INSERT INTO dialog (dialog_id, parent_id, role, content, model, prompt_tokens, completion_tokens, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		// Stored as text (not a driver-specific time.Time encoding) so it
		// round-trips predictably regardless of SQL driver quirks.
		dialogID, parent, role, content, model, promptTokens, completionTokens, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert dialog message: %w", err)
	}
	return nil
}

// Message is one persisted dialog message, as returned by History.
type Message struct {
	ID               int64 // the dialog table's row id, for DeleteMessages
	Role             string
	Content          string
	Model            string // the model this row's turn was sent to
	PromptTokens     *int   // nil unless this row recorded a request's prompt_tokens
	CompletionTokens *int   // nil unless this row recorded a response's completion_tokens
}

// History returns every message of dialogID, oldest first.
func (s *Store) History(dialogID string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, model, prompt_tokens, completion_tokens FROM dialog WHERE dialog_id = ? ORDER BY id`, dialogID,
	)
	if err != nil {
		return nil, fmt.Errorf("query dialog history: %w", err)
	}
	defer rows.Close()

	var history []Message
	for rows.Next() {
		var m Message
		var model sql.NullString
		var promptTokens, completionTokens sql.NullInt64
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &model, &promptTokens, &completionTokens); err != nil {
			return nil, fmt.Errorf("scan dialog message: %w", err)
		}
		m.Model = model.String
		if promptTokens.Valid {
			v := int(promptTokens.Int64)
			m.PromptTokens = &v
		}
		if completionTokens.Valid {
			v := int(completionTokens.Int64)
			m.CompletionTokens = &v
		}
		history = append(history, m)
	}
	return history, rows.Err()
}

// DeleteMessages permanently removes the given rows from the dialog table
// — CLAUDE.md's "Работа с историей диалогов": once turns older than
// HISTORY_MSG_COUNT are folded into dialog_summary, they're deleted here.
// A nil/empty ids is a no-op.
func (s *Store) DeleteMessages(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := s.db.Exec(`DELETE FROM dialog WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("delete dialog messages: %w", err)
	}
	return nil
}

// Summary returns dialogID's saved summary, or "" if it has none.
func (s *Store) Summary(dialogID string) (string, error) {
	var summary string
	err := s.db.QueryRow(`SELECT summary FROM dialog_summary WHERE dialog_id = ?`, dialogID).Scan(&summary)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load dialog summary: %w", err)
	}
	return summary, nil
}

// SetSummary creates or overwrites dialogID's saved summary — CLAUDE.md:
// "Получившееся summary необходимо сохранить в dialog_summary обновив
// соответствующую диалогу строчку."
func (s *Store) SetSummary(dialogID, summary string) error {
	_, err := s.db.Exec(
		`INSERT INTO dialog_summary (dialog_id, summary, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(dialog_id) DO UPDATE SET summary = excluded.summary, updated_at = excluded.updated_at`,
		dialogID, summary, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save dialog summary: %w", err)
	}
	return nil
}

// Fact is one persisted key/value fact for a dialog — CLAUDE.md's Context
// Strategy "STICKY_FACTS".
type Fact struct {
	Key   string
	Value string
}

// SetFact creates or overwrites one (dialogID, key) fact — CLAUDE.md:
// "Секцию фактов из ответа необходимо сохранить в таблицу facts."
func (s *Store) SetFact(dialogID, key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO facts (dialog_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(dialog_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		dialogID, key, value, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save fact: %w", err)
	}
	return nil
}

// Facts returns every fact saved for dialogID, ordered by key.
func (s *Store) Facts(dialogID string) ([]Fact, error) {
	rows, err := s.db.Query(`SELECT key, value FROM facts WHERE dialog_id = ? ORDER BY key`, dialogID)
	if err != nil {
		return nil, fmt.Errorf("query facts: %w", err)
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.Key, &f.Value); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

// SetAgentProfile creates or overwrites the agent's saved profile —
// CLAUDE.md's promptfile "Profile" heading: "текущий профиль сохраняется в
// таблицу Agent в поле Profile и применяется ко всем задачам которые
// решает агент". The single row (id=1) is shared by every dialog; its
// invariants column is left untouched.
func (s *Store) SetAgentProfile(profile string) error {
	_, err := s.db.Exec(
		`INSERT INTO agent (id, profile, invariants, updated_at) VALUES (1, ?, '', ?)
		 ON CONFLICT(id) DO UPDATE SET profile = excluded.profile, updated_at = excluded.updated_at`,
		profile, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save agent profile: %w", err)
	}
	return nil
}

// SetAgentInvariants creates or overwrites the agent's saved invariants —
// CLAUDE.md's promptfile "Invariants" heading, applied the same way as
// SetAgentProfile but to the invariants column.
func (s *Store) SetAgentInvariants(invariants string) error {
	_, err := s.db.Exec(
		`INSERT INTO agent (id, profile, invariants, updated_at) VALUES (1, '', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET invariants = excluded.invariants, updated_at = excluded.updated_at`,
		invariants, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save agent invariants: %w", err)
	}
	return nil
}

// Agent returns the agent's saved profile and invariants, or "" for either
// that has never been set.
func (s *Store) Agent() (profile, invariants string, err error) {
	err = s.db.QueryRow(`SELECT profile, invariants FROM agent WHERE id = 1`).Scan(&profile, &invariants)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("load agent: %w", err)
	}
	return profile, invariants, nil
}

// SaveTaskState records dialogID's "### State Machine" progress — CLAUDE.md:
// "Пользователь должен иметь возможность поставить паузу на любом из
// этапов - команда /pause. Это приводит к сохранению текущего состояния в
// базу данных." promptFile and extra are the paused turn's Step A/Step T
// inputs, so /resume can rebuild the same request; phase is which state
// machine step ("planning"/"execution"/"validation"/"done") it was
// paused at, kept for display only.
func (s *Store) SaveTaskState(dialogID, phase, promptFile, extra string) error {
	_, err := s.db.Exec(
		`INSERT INTO task_state (dialog_id, phase, prompt_file, extra, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(dialog_id) DO UPDATE SET phase = excluded.phase, prompt_file = excluded.prompt_file, extra = excluded.extra, updated_at = excluded.updated_at`,
		dialogID, phase, promptFile, extra, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save task state: %w", err)
	}
	return nil
}

// TaskState returns dialogID's saved pause state, if any — CLAUDE.md's
// "/resume - приводит к возобновлению процесса с того шага на котором мы
// закончили". ok is false when nothing is paused for this dialog.
func (s *Store) TaskState(dialogID string) (phase, promptFile, extra string, ok bool, err error) {
	err = s.db.QueryRow(
		`SELECT phase, prompt_file, extra FROM task_state WHERE dialog_id = ?`, dialogID,
	).Scan(&phase, &promptFile, &extra)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, fmt.Errorf("load task state: %w", err)
	}
	return phase, promptFile, extra, true, nil
}

// ClearTaskState removes dialogID's saved pause state, if any — called
// once a turn actually completes (successfully or not) so a stale pause
// can't be resumed into a dialog that has since moved on.
func (s *Store) ClearTaskState(dialogID string) error {
	if _, err := s.db.Exec(`DELETE FROM task_state WHERE dialog_id = ?`, dialogID); err != nil {
		return fmt.Errorf("clear task state: %w", err)
	}
	return nil
}

// DialogSummary identifies one existing dialog for Step D's selection list
// (CLAUDE.md): ID is the value to pass to History, Label is what to show
// the operator.
type DialogSummary struct {
	ID    string
	Label string
}

// ListDialogs returns a summary of every dialog with at least one message,
// most recently active first.
func (s *Store) ListDialogs() ([]DialogSummary, error) {
	return s.summarizeDialogs(`
		SELECT dialog_id, MAX(created_at) AS last_at
		FROM dialog
		GROUP BY dialog_id
		ORDER BY last_at DESC`)
}

// Branches returns a summary of every dialog forked off parentID (its
// parent_id — CLAUDE.md's Context Strategy "Branching"), most recently
// active first — "если у диалога есть ветки, их необходимо давать выбирать
// из консоли".
func (s *Store) Branches(parentID string) ([]DialogSummary, error) {
	return s.summarizeDialogs(`
		SELECT dialog_id, MAX(created_at) AS last_at
		FROM dialog
		WHERE parent_id = ?
		GROUP BY dialog_id
		ORDER BY last_at DESC`, parentID)
}

func (s *Store) summarizeDialogs(query string, args ...any) ([]DialogSummary, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list dialogs: %w", err)
	}
	defer rows.Close()

	var ids []string
	var lastAts []string
	for rows.Next() {
		var id, lastAt string
		if err := rows.Scan(&id, &lastAt); err != nil {
			return nil, fmt.Errorf("scan dialog summary: %w", err)
		}
		ids = append(ids, id)
		lastAts = append(lastAts, lastAt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	summaries := make([]DialogSummary, 0, len(ids))
	for i, id := range ids {
		firstQuestion, err := s.firstUserMessage(id)
		if err != nil {
			return nil, err
		}
		at, _ := time.Parse(time.RFC3339Nano, lastAts[i]) // zero time on parse failure: label degrades, doesn't fail
		summaries = append(summaries, DialogSummary{ID: id, Label: formatDialogLabel(at, firstQuestion)})
	}
	return summaries, nil
}

func (s *Store) firstUserMessage(dialogID string) (string, error) {
	var content string
	err := s.db.QueryRow(
		`SELECT content FROM dialog WHERE dialog_id = ? AND role = 'user' ORDER BY id LIMIT 1`, dialogID,
	).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load first question for dialog %s: %w", dialogID, err)
	}
	return content, nil
}

// formatDialogLabel builds a Step D select-list entry: a timestamp plus a
// short excerpt of the dialog's opening question, e.g.
// "14 Sep 15:04 — Сколько будет 6*7?".
func formatDialogLabel(at time.Time, firstQuestion string) string {
	stamp := at.Format("02 Jan 15:04")
	if excerpt := truncateExcerpt(firstQuestion, 50); excerpt != "" {
		return stamp + " — " + excerpt
	}
	return stamp
}

// truncateExcerpt collapses s to a single line and caps it at maxRunes.
func truncateExcerpt(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
