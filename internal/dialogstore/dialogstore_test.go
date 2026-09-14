package dialogstore

import (
	"database/sql"
	"path/filepath"
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
	// prompt_tokens/completion_tokens/model columns existed.
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
	if err := s.AppendMessage("dlg-1", "user", "hello", "test-model", &promptTokens, nil); err != nil {
		t.Fatalf("AppendMessage after upgrade: %v", err)
	}
	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History after upgrade: %v", err)
	}
	if len(got) != 1 || got[0].PromptTokens == nil || *got[0].PromptTokens != 3 || got[0].Model != "test-model" {
		t.Fatalf("History after upgrade = %+v, want one row with PromptTokens=3 and Model=test-model", got)
	}
}

func TestAppendMessage_StoresRowsUnderDialogID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.AppendMessage("dlg-1", "user", "hello", "test-model", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "assistant", "hi there", "test-model", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-2", "user", "unrelated dialog", "test-model", nil, nil); err != nil {
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

	s.AppendMessage("dlg-1", "user", "hello", "test-model", nil, nil)
	s.AppendMessage("dlg-1", "assistant", "hi there", "test-model", nil, nil)
	s.AppendMessage("dlg-2", "user", "unrelated", "test-model", nil, nil)

	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("History(dlg-1) = %+v, want 2 rows", got)
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Fatalf("row 0 = %+v, want {user hello}", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "hi there" {
		t.Fatalf("row 1 = %+v, want {assistant \"hi there\"}", got[1])
	}
	if got[0].ID == 0 || got[1].ID == 0 || got[0].ID == got[1].ID {
		t.Fatalf("expected distinct non-zero row IDs, got %d and %d", got[0].ID, got[1].ID)
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
	if err := s.AppendMessage("dlg-1", "user", "hello", "test-model", &promptTokens, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "assistant", "hi there", "test-model", nil, &completionTokens); err != nil {
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

func TestAppendMessage_PersistsModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.AppendMessage("dlg-1", "user", "hello", "kimi-k2.6", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "assistant", "hi there", "kimi-k2.6", nil, nil); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 || got[0].Model != "kimi-k2.6" || got[1].Model != "kimi-k2.6" {
		t.Fatalf("History(dlg-1) = %+v, want both rows to carry Model=kimi-k2.6", got)
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

	s.AppendMessage("dlg-old", "user", "old question", "test-model", nil, nil)
	s.AppendMessage("dlg-old", "assistant", "old answer", "test-model", nil, nil)
	time.Sleep(10 * time.Millisecond) // ensure a distinct created_at ordering
	s.AppendMessage("dlg-new", "user", "new question", "test-model", nil, nil)
	s.AppendMessage("dlg-new", "assistant", "new answer", "test-model", nil, nil)

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

func TestDeleteMessages_RemovesOnlyGivenRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.AppendMessage("dlg-1", "user", "keep me", "test-model", nil, nil)
	s.AppendMessage("dlg-1", "user", "delete me", "test-model", nil, nil)

	before, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("before = %+v, want 2 rows", before)
	}

	if err := s.DeleteMessages([]int64{before[1].ID}); err != nil {
		t.Fatalf("DeleteMessages: %v", err)
	}

	after, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(after) != 1 || after[0].Content != "keep me" {
		t.Fatalf("after = %+v, want only the row that was not deleted", after)
	}
}

func TestDeleteMessages_EmptyIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.DeleteMessages(nil); err != nil {
		t.Fatalf("DeleteMessages(nil): %v", err)
	}
}

func TestSummary_RoundTripsAndUpdatesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.Summary("dlg-1")
	if err != nil {
		t.Fatalf("Summary (before any set): %v", err)
	}
	if got != "" {
		t.Fatalf("Summary (before any set) = %q, want empty", got)
	}

	if err := s.SetSummary("dlg-1", "first summary"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	got, err = s.Summary("dlg-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != "first summary" {
		t.Fatalf("Summary = %q, want %q", got, "first summary")
	}

	// A second SetSummary for the same dialog must overwrite, not add a
	// second row.
	if err := s.SetSummary("dlg-1", "updated summary"); err != nil {
		t.Fatalf("SetSummary (update): %v", err)
	}
	got, err = s.Summary("dlg-1")
	if err != nil {
		t.Fatalf("Summary (after update): %v", err)
	}
	if got != "updated summary" {
		t.Fatalf("Summary (after update) = %q, want %q", got, "updated summary")
	}
}

func TestFacts_EmptyWhenNoneSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.Facts("dlg-1")
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Facts (none saved) = %+v, want empty", got)
	}
}

func TestSetFact_RoundTripsAndUpdatesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.SetFact("dlg-1", "имя", "Федя"); err != nil {
		t.Fatalf("SetFact: %v", err)
	}
	if err := s.SetFact("dlg-1", "возраст", "41"); err != nil {
		t.Fatalf("SetFact: %v", err)
	}
	if err := s.SetFact("dlg-2", "имя", "другой диалог"); err != nil {
		t.Fatalf("SetFact (other dialog): %v", err)
	}

	got, err := s.Facts("dlg-1")
	if err != nil {
		t.Fatalf("Facts: %v", err)
	}
	want := []Fact{{Key: "возраст", Value: "41"}, {Key: "имя", Value: "Федя"}} // ordered by key
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Facts(dlg-1) = %+v, want %+v", got, want)
	}

	// Updating an existing key overwrites in place rather than adding a
	// second row.
	if err := s.SetFact("dlg-1", "имя", "Фёдор"); err != nil {
		t.Fatalf("SetFact (update): %v", err)
	}
	got, err = s.Facts("dlg-1")
	if err != nil {
		t.Fatalf("Facts (after update): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Facts(dlg-1) after update = %+v, want still 2 rows", got)
	}
	for _, f := range got {
		if f.Key == "имя" && f.Value != "Фёдор" {
			t.Fatalf("fact \"имя\" = %q, want it updated to %q", f.Value, "Фёдор")
		}
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
	if err := s.AppendMessage("dlg-1", "user", "hello", "test-model", nil, nil); err == nil {
		t.Fatal("expected AppendMessage to fail on a closed store")
	}
}
