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
// <seconds>-<model>.log", which only has second granularity — a tool-call
// round trip (CLAUDE.md's "## Работа с MCP") can easily make two of these
// within the same second for the same model, so a second Start call that
// collides with an existing, still-fresh file gets "-2", "-3", ... spliced
// in before ".log" rather than silently overwriting CLAUDE.md's "каждая
// пара запроса и ответа" of the first one. It returns the path, to be
// passed to Finish once the response arrives.
func Start(dir, model string, reqBody []byte, masker *secretmask.Masker) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}

	now := time.Now()
	base := fmt.Sprintf("%d-%s-%02d-%02d-%02d-%s",
		now.Day(), now.Month().String(), now.Hour(), now.Minute(), now.Second(), model)

	content := []byte(fmt.Sprintf("=== REQUEST ===\n%s\n", masker.Mask(string(reqBody))))

	const maxAttempts = 1000
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		name := base + ".log"
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d.log", base, attempt)
		}
		path := filepath.Join(dir, name)

		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue // this second (or "-N" variant) is already taken; try the next one
			}
			return "", fmt.Errorf("create log file: %w", err)
		}
		_, writeErr := f.Write(content)
		closeErr := f.Close()
		if writeErr != nil {
			return "", fmt.Errorf("write log file: %w", writeErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("write log file: %w", closeErr)
		}
		return path, nil
	}
	return "", fmt.Errorf("create log file: %d consecutive names already taken for %q", maxAttempts, base)
}

// Finish appends the (masked) response, plus the request's total duration
// (CLAUDE.md: "в лог также пишем общее время исполнения запроса"), to the
// log file path, as created by Start.
func Finish(path string, statusCode int, respBody []byte, duration time.Duration, masker *secretmask.Masker) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	content := fmt.Sprintf(
		"\n=== RESPONSE (HTTP %d) ===\n%s\n\nВремя выполнения: %s\n",
		statusCode, masker.Mask(string(respBody)), duration,
	)
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("append response to log file: %w", err)
	}
	return nil
}
