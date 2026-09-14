// Package config loads application configuration from a config file and
// environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
)

// FilePath is the config file Load reads, relative to the working
// directory — CLAUDE.md: "конфигурационный файл ./aiac9.config".
const FilePath = "aiac9.config"

// defaultHistoryMsgCount is used when HISTORY_MSG_COUNT is unset in both
// the environment and the config file. CLAUDE.md doesn't specify a
// default, so a modest value is picked to keep a live dialog's context
// bounded without needing summarization to kick in on ordinary use.
const defaultHistoryMsgCount = 20

// Config holds runtime configuration for the application.
type Config struct {
	Addr string // HTTP listen address, e.g. ":8080"
	Env  string // deployment environment, e.g. "development", "production"

	// MoonshotAPIKey authenticates requests to the Moonshot AI (Kimi) chat
	// completions API — see internal/kimi and internal/llm. It is a
	// secret: never log or print it verbatim (internal/secretmask masks it
	// wherever it might otherwise leak into logs or terminal output).
	MoonshotAPIKey string

	// MoonshotBaseURL overrides internal/kimi's default API endpoint, e.g.
	// to point at a proxy or a local server in tests. Empty means "use the
	// client's built-in default".
	MoonshotBaseURL string

	// DeepSeekAPIKey authenticates requests to the DeepSeek chat
	// completions API — see internal/deepseek and internal/llm. Same
	// secrecy rules as MoonshotAPIKey.
	DeepSeekAPIKey string

	// DeepSeekBaseURL overrides internal/deepseek's default API endpoint,
	// same purpose as MoonshotBaseURL.
	DeepSeekBaseURL string

	// HistoryMsgCount is how many of a dialog's most recent request/
	// response turns stay "active" context on Step D — CLAUDE.md's
	// "Работа с историей диалогов". Older turns are either summarized
	// (see HistorySummarization) or simply left out of the context.
	HistoryMsgCount int

	// HistorySummarization enables folding turns older than
	// HistoryMsgCount into a per-dialog summary (table dialog_summary)
	// via an LLM call, then deleting them from the dialog table. Off by
	// default — CLAUDE.md phrases it as an opt-in feature ("если ...
	// включена").
	HistorySummarization bool

	// ContextStrategy selects how a dialog's context is managed beyond
	// the plain HistoryMsgCount/HistorySummarization behavior above —
	// CLAUDE.md's "### Context Strategy". Empty means that plain
	// behavior; see the ContextStrategy* constants for the recognized
	// values ("Sliding_Window", "STICKY_FACTS"), matched
	// case-insensitively.
	ContextStrategy string
}

// Recognized ContextStrategy values (CLAUDE.md's own casing; compare
// case-insensitively via strings.EqualFold, never by exact match).
const (
	ContextStrategySlidingWindow = "SLIDING_WINDOW"
	ContextStrategyStickyFacts   = "STICKY_FACTS"
)

// Load reads configuration from ./aiac9.config (if present) and
// environment variables, falling back to sensible defaults for local
// development. Environment variables always win over the config file —
// CLAUDE.md: "переменные окружения — перезаписывают значения из файла".
func Load() (*Config, error) {
	fileValues, err := loadFile(FilePath)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Addr:                 getValue(fileValues, "ADDR", ":8080"),
		Env:                  getValue(fileValues, "APP_ENV", "development"),
		MoonshotAPIKey:       getValue(fileValues, "MOONSHOT_API_KEY", ""),
		MoonshotBaseURL:      getValue(fileValues, "MOONSHOT_BASE_URL", ""),
		DeepSeekAPIKey:       getValue(fileValues, "DEEPSEEK_API_KEY", ""),
		DeepSeekBaseURL:      getValue(fileValues, "DEEPSEEK_BASE_URL", ""),
		HistoryMsgCount:      getIntValue(fileValues, "HISTORY_MSG_COUNT", defaultHistoryMsgCount),
		HistorySummarization: getBoolValue(fileValues, "HISTORY_SUMMARIZATION", false),
		ContextStrategy:      getValue(fileValues, "CONTEXT_STRATEGY", ""),
	}
	return cfg, nil
}

// loadFile parses simple "KEY=VALUE" lines from path (blank lines and
// lines starting with "#" are skipped). A missing file is not an error —
// it yields an empty map, since the config file itself is optional.
func loadFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}

	values := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values, nil
}

// getValue resolves key from the environment first, then fileValues, then
// fallback.
func getValue(fileValues map[string]string, key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v, ok := fileValues[key]; ok && v != "" {
		return v
	}
	return fallback
}

// getIntValue is getValue, parsed as an integer; an unparsable value falls
// back the same as an absent one.
func getIntValue(fileValues map[string]string, key string, fallback int) int {
	v := getValue(fileValues, key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// getBoolValue is getValue, parsed per CLAUDE.md's convention: "on"/"1"/
// "yes" is true, "off"/"0"/"no" is false (case-insensitive); anything else
// (including absent) falls back to fallback.
func getBoolValue(fileValues map[string]string, key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(getValue(fileValues, key, ""))) {
	case "on", "1", "yes":
		return true
	case "off", "0", "no":
		return false
	default:
		return fallback
	}
}
