package config

import (
	"os"
	"testing"
)

func TestLoad_DefaultsWhenNothingIsSet(t *testing.T) {
	t.Chdir(t.TempDir()) // no ./aiac9.config here

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" || cfg.Env != "development" {
		t.Fatalf("Addr/Env = %q/%q, want defaults", cfg.Addr, cfg.Env)
	}
	if cfg.HistoryMsgCount != defaultHistoryMsgCount {
		t.Fatalf("HistoryMsgCount = %d, want default %d", cfg.HistoryMsgCount, defaultHistoryMsgCount)
	}
	if cfg.HistorySummarization {
		t.Fatal("HistorySummarization = true, want false by default")
	}
}

func TestLoad_ReadsConfigFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfigFile(t, "ADDR=:9090\nHISTORY_MSG_COUNT=5\nHISTORY_SUMMARIZATION=on\nCONTEXT_STRATEGY=Sliding_Window\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, ":9090")
	}
	if cfg.HistoryMsgCount != 5 {
		t.Fatalf("HistoryMsgCount = %d, want 5", cfg.HistoryMsgCount)
	}
	if !cfg.HistorySummarization {
		t.Fatal("HistorySummarization = false, want true")
	}
	if cfg.ContextStrategy != "Sliding_Window" {
		t.Fatalf("ContextStrategy = %q, want %q (as written, case preserved)", cfg.ContextStrategy, "Sliding_Window")
	}
}

func TestLoad_ContextStrategyDefaultsToEmpty(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ContextStrategy != "" {
		t.Fatalf("ContextStrategy = %q, want empty by default", cfg.ContextStrategy)
	}
}

func TestLoad_EnvVarOverridesConfigFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfigFile(t, "ADDR=:9090\n")
	t.Setenv("ADDR", ":7070")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":7070" {
		t.Fatalf("Addr = %q, want the env var value %q (env must win over file)", cfg.Addr, ":7070")
	}
}

func TestLoad_MissingConfigFileIsNotAnError(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v, want no error for a missing config file", err)
	}
}

func TestLoad_IgnoresBlankAndCommentLines(t *testing.T) {
	t.Chdir(t.TempDir())
	writeConfigFile(t, "# a comment\n\nADDR=:6060\n   \n# another\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":6060" {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, ":6060")
	}
}

func TestGetBoolValue_RecognizedForms(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"on", true}, {"1", true}, {"yes", true}, {"ON", true},
		{"off", false}, {"0", false}, {"no", false}, {"OFF", false},
	}
	for _, c := range cases {
		got := getBoolValue(map[string]string{"K": c.value}, "K", !c.want)
		if got != c.want {
			t.Errorf("getBoolValue(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestGetBoolValue_UnrecognizedFallsBack(t *testing.T) {
	if got := getBoolValue(map[string]string{"K": "maybe"}, "K", true); got != true {
		t.Fatalf("getBoolValue(unrecognized) = %v, want the fallback true", got)
	}
}

func TestGetIntValue_UnparsableFallsBack(t *testing.T) {
	if got := getIntValue(map[string]string{"K": "not-a-number"}, "K", 42); got != 42 {
		t.Fatalf("getIntValue(unparsable) = %d, want the fallback 42", got)
	}
}

func writeConfigFile(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(FilePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", FilePath, err)
	}
}
