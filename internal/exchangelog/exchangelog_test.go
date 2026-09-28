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

func TestStart_SameSecondCallsGetDistinctFilesNotOverwritten(t *testing.T) {
	// A tool-call round trip (CLAUDE.md's "## Работа с MCP") can easily
	// make two requests to the same model within the same second — the
	// file name pattern only has second granularity, so without special
	// handling the second Start would silently clobber the first's file.
	dir := t.TempDir()
	masker := secretmask.New()

	path1, err := Start(dir, "kimi-k2.6", []byte(`{"n":1}`), masker)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := Finish(path1, 200, []byte(`{"r":1}`), time.Second, masker); err != nil {
		t.Fatalf("first Finish: %v", err)
	}

	path2, err := Start(dir, "kimi-k2.6", []byte(`{"n":2}`), masker)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := Finish(path2, 200, []byte(`{"r":2}`), time.Second, masker); err != nil {
		t.Fatalf("second Finish: %v", err)
	}

	if path1 == path2 {
		t.Fatalf("both calls got the same path %q, want distinct files", path1)
	}

	data1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("read first log file: %v", err)
	}
	if !strings.Contains(string(data1), `"n":1`) || !strings.Contains(string(data1), `"r":1`) {
		t.Fatalf("first log file was overwritten, got:\n%s", data1)
	}

	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("read second log file: %v", err)
	}
	if !strings.Contains(string(data2), `"n":2`) || !strings.Contains(string(data2), `"r":2`) {
		t.Fatalf("second log file content unexpected, got:\n%s", data2)
	}
}
