package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_MissingFileYieldsEmptyConfig(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "no-such-file.json"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("Servers = %+v, want empty", cfg.Servers)
	}
}

func TestLoadConfig_ParsesServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeFile(t, path, `{
		"mcpServers": {
			"calc": {"command": "/abs/path/calc-mcp-server"},
			"notes": {"command": "/abs/path/notes-mcp-server", "args": ["--verbose"], "env": {"FOO": "bar"}}
		}
	}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("Servers = %+v, want 2 entries", cfg.Servers)
	}
	calc := cfg.Servers["calc"]
	if calc.Command != "/abs/path/calc-mcp-server" {
		t.Fatalf("calc.Command = %q", calc.Command)
	}
	notes := cfg.Servers["notes"]
	if notes.Command != "/abs/path/notes-mcp-server" || len(notes.Args) != 1 || notes.Args[0] != "--verbose" || notes.Env["FOO"] != "bar" {
		t.Fatalf("notes = %+v, unexpected", notes)
	}
}

func TestLoadConfig_ParsesHTTPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeFile(t, path, `{
		"mcpServers": {
			"calc": {"type": "http", "url": "http://127.0.0.1:8081/mcp"}
		}
	}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	calc := cfg.Servers["calc"]
	if calc.Type != "http" || calc.URL != "http://127.0.0.1:8081/mcp" {
		t.Fatalf("calc = %+v, unexpected", calc)
	}
}

func TestLoadConfig_InvalidJSONIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	writeFile(t, path, `{not valid json`)

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig returned nil error for invalid JSON")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
