package exchangelog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/kimi"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

func TestWrite_MasksSecretsAndNamesFilePerPattern(t *testing.T) {
	dir := t.TempDir()
	masker := secretmask.New("supersecret")
	ex := &kimi.Exchange{
		Model:        "kimi-k2.6",
		RequestBody:  []byte(`{"authorization":"supersecret"}`),
		ResponseBody: []byte(`response containing supersecret too`),
		StatusCode:   200,
	}

	path, err := Write(dir, ex, masker)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("path = %q, want it inside %q", path, dir)
	}

	pattern := regexp.MustCompile(`^\d{1,2}-[A-Za-z]+-\d{2}-\d{2}-\d{2}-kimi-k2\.6\.log$`)
	if !pattern.MatchString(filepath.Base(path)) {
		t.Fatalf("file name %q does not match the CLAUDE.md pattern", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "supersecret") {
		t.Fatalf("log content still contains the secret verbatim:\n%s", content)
	}
	if !strings.Contains(content, "***") {
		t.Fatalf("expected masked content to contain \"***\":\n%s", content)
	}
}
