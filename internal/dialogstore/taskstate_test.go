package dialogstore

import (
	"path/filepath"
	"testing"
)

func TestTaskState_NoneSavedReportsNotOK(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	_, _, _, ok, err := s.TaskState("d1")
	if err != nil {
		t.Fatalf("TaskState: %v", err)
	}
	if ok {
		t.Fatal("TaskState ok = true, want false for a dialog with nothing saved")
	}
}

func TestSaveTaskState_RoundTripsAndUpserts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.SaveTaskState("d1", "execution", "p.prompt.md", "доп. текст"); err != nil {
		t.Fatalf("SaveTaskState: %v", err)
	}

	phase, promptFile, extra, ok, err := s.TaskState("d1")
	if err != nil {
		t.Fatalf("TaskState: %v", err)
	}
	if !ok {
		t.Fatal("TaskState ok = false, want true")
	}
	if phase != "execution" || promptFile != "p.prompt.md" || extra != "доп. текст" {
		t.Fatalf("TaskState = (%q, %q, %q), want (execution, p.prompt.md, доп. текст)", phase, promptFile, extra)
	}

	// Saving again for the same dialog overwrites, not duplicates.
	if err := s.SaveTaskState("d1", "validation", "", ""); err != nil {
		t.Fatalf("second SaveTaskState: %v", err)
	}
	phase, promptFile, extra, ok, err = s.TaskState("d1")
	if err != nil {
		t.Fatalf("TaskState after overwrite: %v", err)
	}
	if !ok || phase != "validation" || promptFile != "" || extra != "" {
		t.Fatalf("TaskState after overwrite = (%v, %q, %q, %q), want (true, validation, \"\", \"\")", ok, phase, promptFile, extra)
	}
}

func TestClearTaskState_RemovesSavedState(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.SaveTaskState("d1", "planning", "p.md", "x"); err != nil {
		t.Fatalf("SaveTaskState: %v", err)
	}
	if err := s.ClearTaskState("d1"); err != nil {
		t.Fatalf("ClearTaskState: %v", err)
	}

	_, _, _, ok, err := s.TaskState("d1")
	if err != nil {
		t.Fatalf("TaskState: %v", err)
	}
	if ok {
		t.Fatal("TaskState ok = true after ClearTaskState, want false")
	}
}

func TestClearTaskState_NoneSavedIsANoOp(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.ClearTaskState("no-such-dialog"); err != nil {
		t.Fatalf("ClearTaskState on nothing saved: %v", err)
	}
}
