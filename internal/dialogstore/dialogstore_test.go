package dialogstore

import (
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

func TestAppendMessage_StoresRowsUnderDialogID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.AppendMessage("dlg-1", "user", "hello"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-1", "assistant", "hi there"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage("dlg-2", "user", "unrelated dialog"); err != nil {
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

	s.AppendMessage("dlg-1", "user", "hello")
	s.AppendMessage("dlg-1", "assistant", "hi there")
	s.AppendMessage("dlg-2", "user", "unrelated")

	got, err := s.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := []Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi there"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("History(dlg-1) = %+v, want %+v", got, want)
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

	s.AppendMessage("dlg-old", "user", "old question")
	s.AppendMessage("dlg-old", "assistant", "old answer")
	time.Sleep(10 * time.Millisecond) // ensure a distinct created_at ordering
	s.AppendMessage("dlg-new", "user", "new question")
	s.AppendMessage("dlg-new", "assistant", "new answer")

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
	if err := s.AppendMessage("dlg-1", "user", "hello"); err == nil {
		t.Fatal("expected AppendMessage to fail on a closed store")
	}
}
