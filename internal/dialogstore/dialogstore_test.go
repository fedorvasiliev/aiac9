package dialogstore

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpen_CreatesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// Opening an existing database again must not fail (CREATE TABLE IF
	// NOT EXISTS).
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
}

func TestOpen_UpgradesPreExistingDatabaseMissingTokenColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	// Simulate a database created by a version of this package before the
	// prompt_tokens/completion_tokens columns existed.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE dialog (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		dialog_id  TEXT NOT NULL,
		role       TEXT NOT NULL,
		content    TEXT NOT NULL,
		created_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a legacy database: %v", err)
	}
	defer s.Close()

	promptTokens := 3
	if err := s.AppendMessage("dlg-1", "user", "hello", &promptTokens, nil); err != nil {
		t.Fatalf("AppendMessage after upgrade: %v", err)
	}
	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History after upgrade: %v", err)
	}
	if len(got) != 1 || got[0].PromptTokens == nil || *got[0].PromptTokens != 3 {
		t.Fatalf("History after upgrade = %+v, want one row with PromptTokens=3", got)
	}
}

func TestAppendMessage_StoresRowsUnderDialogID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.AppendMessage("dlg-1", "user", "hello", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "assistant", "hi there", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-2", "user", "unrelated dialog", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	rows, err := s.db.Query(`SELECT role, content FROM dialog WHERE dialog_id = ? ORDER BY id`, "dlg-1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var got []struct{ role, content string }
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, struct{ role, content string }{role, content})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d rows for dlg-1, want 2: %+v", len(got), got)
	}
	if got[0].role != "user" || got[0].content != "hello" {
		t.Fatalf("row 0 = %+v, want {user hello}", got[0])
	}
	if got[1].role != "assistant" || got[1].content != "hi there" {
		t.Fatalf("row 1 = %+v, want {assistant hi there}", got[1])
	}
}

func TestHistory_ReturnsOrderedMessagesForOneDialog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.AppendMessage("dlg-1", "user", "hello", nil, nil)
	s.AppendMessage("dlg-1", "assistant", "hi there", nil, nil)
	s.AppendMessage("dlg-2", "user", "unrelated", nil, nil)

	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := []Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi there"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("History(dlg-1) = %+v, want %+v", got, want)
	}
}

func TestAppendMessage_PersistsPromptAndCompletionTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	promptTokens := 5
	completionTokens := 7
	// CLAUDE.md: prompt_tokens on the request row, completion_tokens on
	// the response row — the other left nil on each.
	if err := s.AppendMessage("dlg-1", "user", "hello", &promptTokens, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "assistant", "hi there", nil, &completionTokens); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("History(dlg-1) = %+v, want 2 rows", got)
	}
	if got[0].PromptTokens == nil || *got[0].PromptTokens != 5 {
		t.Fatalf("row 0 PromptTokens = %v, want 5", got[0].PromptTokens)
	}
	if got[0].CompletionTokens != nil {
		t.Fatalf("row 0 CompletionTokens = %v, want nil", *got[0].CompletionTokens)
	}
	if got[1].CompletionTokens == nil || *got[1].CompletionTokens != 7 {
		t.Fatalf("row 1 CompletionTokens = %v, want 7", got[1].CompletionTokens)
	}
	if got[1].PromptTokens != nil {
		t.Fatalf("row 1 PromptTokens = %v, want nil", *got[1].PromptTokens)
	}
}

func TestHistory_EmptyForUnknownDialog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.History("does-not-exist")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("History(unknown) = %+v, want empty", got)
	}
}

func TestListDialogs_OrdersByMostRecentAndIncludesExcerpt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.AppendMessage("dlg-old", "user", "old question", nil, nil)
	s.AppendMessage("dlg-old", "assistant", "old answer", nil, nil)
	time.Sleep(10 * time.Millisecond) // ensure a distinct created_at ordering
	s.AppendMessage("dlg-new", "user", "new question", nil, nil)
	s.AppendMessage("dlg-new", "assistant", "new answer", nil, nil)

	got, err := s.ListDialogs()
	if err != nil {
		t.Fatalf("ListDialogs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListDialogs() = %+v, want 2 summaries", got)
	}
	if got[0].ID != "dlg-new" {
		t.Fatalf("ListDialogs()[0].ID = %q, want the most recently active dialog %q", got[0].ID, "dlg-new")
	}
	if !strings.Contains(got[0].Label, "new question") {
		t.Fatalf("ListDialogs()[0].Label = %q, want it to contain the opening question", got[0].Label)
	}
	if got[1].ID != "dlg-old" {
		t.Fatalf("ListDialogs()[1].ID = %q, want %q", got[1].ID, "dlg-old")
	}
}

func TestListDialogs_EmptyWhenNoMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.ListDialogs()
	if err != nil {
		t.Fatalf("ListDialogs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListDialogs() = %+v, want empty", got)
	}
}

func TestTruncateExcerpt_CollapsesAndCaps(t *testing.T) {
	got := truncateExcerpt("line one\nline two   with   spaces", 12)
	want := "line one lin…"
	if got != want {
		t.Fatalf("truncateExcerpt() = %q, want %q", got, want)
	}
}

func TestClose_ClosesUnderlyingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "user", "hello", nil, nil); err == nil {
		t.Fatal("expected AppendMessage to fail on a closed store")
	}
}
