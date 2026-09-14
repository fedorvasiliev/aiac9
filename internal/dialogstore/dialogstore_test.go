package dialogstore

import (
	"path/filepath"
	"testing"
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
