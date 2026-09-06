// Package exchangelog writes each LLM request/response pair to its own file
// under ./logs, as required by CLAUDE.md.
package exchangelog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// Dir is the default log directory, relative to the working directory.
const Dir = "logs"

// Start creates a new log file for one exchange, containing only the
// (masked) request — per CLAUDE.md, "запрос логируется прямо перед
// отправкой": the request must be on disk before the call goes out, not
// only after the response comes back. The file name follows the pattern
// from CLAUDE.md: "<day of month>-<month name>-<24h hour>-<minutes>-
// <seconds>-<model>.log". It returns the path, to be passed to Finish once
// the response arrives.
func Start(dir, model string, reqBody []byte, masker *secretmask.Masker) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}

	now := time.Now()
	name := fmt.Sprintf("%d-%s-%02d-%02d-%02d-%s.log",
		now.Day(), now.Month().String(), now.Hour(), now.Minute(), now.Second(), model)
	path := filepath.Join(dir, name)

	content := fmt.Sprintf("=== REQUEST ===\n%s\n", masker.Mask(string(reqBody)))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write log file: %w", err)
	}
	return path, nil
}

// Finish appends the (masked) response to the log file path, as created by
// Start.
func Finish(path string, statusCode int, respBody []byte, masker *secretmask.Masker) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	content := fmt.Sprintf("\n=== RESPONSE (HTTP %d) ===\n%s\n", statusCode, masker.Mask(string(respBody)))
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append response to log file: %w", err)
	}
	return nil
}
