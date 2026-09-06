package interactive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

func parsePrompt(t *testing.T, text string) *promptfile.Prompt {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	p, err := promptfile.Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestPrintPromptTable_CompactForShortValues(t *testing.T) {
	p := parsePrompt(t, "### Model\nkimi-k2.6\n\n### User Prompt\nhi\n")

	var out bytes.Buffer
	printPromptTable(&out, p)

	got := out.String()
	if !strings.Contains(got, "| Параметр") {
		t.Fatalf("expected the compact table form, got:\n%s", got)
	}
	if strings.Contains(got, "Model:\n") {
		t.Fatalf("did not expect the long block form, got:\n%s", got)
	}
}

func TestPrintPromptTable_BlocksWhenAValueIsLong(t *testing.T) {
	longValue := strings.Repeat("a", 81)
	p := parsePrompt(t, "### Model\nkimi-k2.6\n\n### User Prompt\n"+longValue+"\n")

	var out bytes.Buffer
	printPromptTable(&out, p)

	got := out.String()
	if strings.Contains(got, "| Параметр") {
		t.Fatalf("expected the long block form (a value exceeds 80 chars), got:\n%s", got)
	}
	if !strings.Contains(got, "User Prompt:\n"+longValue) {
		t.Fatalf("expected a 'Заголовок:\\nзначение' block preserving the value, got:\n%s", got)
	}
	// Model is short but the whole printout still switches format.
	if !strings.Contains(got, "Model:\nkimi-k2.6") {
		t.Fatalf("expected every section in block form, not just the long one, got:\n%s", got)
	}
}
