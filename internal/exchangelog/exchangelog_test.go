package exchangelog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

func TestStart_WritesRequestAndNamesFilePerPattern(t *testing.T) {
	dir := t.TempDir()
	masker := secretmask.New("supersecret")

	path, err := Start(dir, "kimi-k2.6", []byte(`{"authorization":"supersecret"}`), masker)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
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
	if !strings.Contains(content, "=== REQUEST ===") {
		t.Fatalf("expected the request section right away, got:\n%s", content)
	}
	if strings.Contains(content, "RESPONSE") {
		t.Fatalf("Start must not write a response section yet, got:\n%s", content)
	}
}

func TestFinish_AppendsResponseToStartsFile(t *testing.T) {
	dir := t.TempDir()
	masker := secretmask.New("supersecret")

	path, err := Start(dir, "kimi-k2.6", []byte(`{"model":"kimi-k2.6"}`), masker)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if err := Finish(path, 200, []byte(`response containing supersecret too`), 1234*time.Millisecond, masker); err != nil {
		t.Fatalf("Finish returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "=== REQUEST ===") || !strings.Contains(content, "=== RESPONSE (HTTP 200) ===") {
		t.Fatalf("expected both request and response sections, got:\n%s", content)
	}
	if !strings.Contains(content, "1.234s") {
		t.Fatalf("expected the request duration to be logged, got:\n%s", content)
	}
	if strings.Contains(content, "supersecret") {
		t.Fatalf("log content still contains the secret verbatim:\n%s", content)
	}
	if !strings.Contains(content, "***") {
		t.Fatalf("expected masked content to contain \"***\":\n%s", content)
	}
}
