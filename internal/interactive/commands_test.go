package interactive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

func openTestStore(t *testing.T) *dialogstore.Store {
	t.Helper()
	store, err := dialogstore.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestConsoleCommand_ProfileUnsetReportsSo(t *testing.T) {
	store := openTestStore(t)
	var out bytes.Buffer

	if !consoleCommand(store, &out, secretmask.New(), "/profile") {
		t.Fatal("consoleCommand(\"/profile\") = false, want true")
	}
	if !strings.Contains(out.String(), "не задан") {
		t.Fatalf("output = %q, want a note that no profile is set", out.String())
	}
}

func TestConsoleCommand_ProfilePrintsSavedValue(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetAgentProfile("Ты полезный ассистент."); err != nil {
		t.Fatalf("SetAgentProfile: %v", err)
	}

	var out bytes.Buffer
	if !consoleCommand(store, &out, secretmask.New(), "/PROFILE") {
		t.Fatal("consoleCommand(\"/PROFILE\") = false, want true (case-insensitive)")
	}
	if !strings.Contains(out.String(), "Ты полезный ассистент.") {
		t.Fatalf("output = %q, want it to contain the saved profile", out.String())
	}
}

func TestConsoleCommand_UnknownLineIsNotACommand(t *testing.T) {
	store := openTestStore(t)
	var out bytes.Buffer

	if consoleCommand(store, &out, secretmask.New(), "просто текст запроса") {
		t.Fatal("consoleCommand(plain text) = true, want false")
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want nothing written for non-command input", out.String())
	}
}

func TestConsoleCommand_ProfileMasksSecrets(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetAgentProfile("token=SECRETVALUE"); err != nil {
		t.Fatalf("SetAgentProfile: %v", err)
	}

	var out bytes.Buffer
	consoleCommand(store, &out, secretmask.New("SECRETVALUE"), "/profile")
	if strings.Contains(out.String(), "SECRETVALUE") {
		t.Fatalf("output = %q, want the secret masked", out.String())
	}
	if !strings.Contains(out.String(), "***") {
		t.Fatalf("output = %q, want a masked placeholder", out.String())
	}
}
