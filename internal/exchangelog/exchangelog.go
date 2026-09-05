// Package exchangelog writes each LLM request/response pair to its own file
// under ./logs, as required by CLAUDE.md.
package exchangelog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/kimi"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// Dir is the default log directory, relative to the working directory.
const Dir = "logs"

// Write logs one request/response exchange to a new file under dir, masking
// any secret registered in masker (e.g. the Moonshot API key) before it
// touches disk. The file name follows the pattern from CLAUDE.md:
// "<day of month>-<month name>-<24h hour>-<minutes>-<seconds>-<model>.log".
// It returns the path written.
func Write(dir string, ex *kimi.Exchange, masker *secretmask.Masker) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}

	now := time.Now()
	name := fmt.Sprintf("%d-%s-%02d-%02d-%02d-%s.log",
		now.Day(), now.Month().String(), now.Hour(), now.Minute(), now.Second(), ex.Model)
	path := filepath.Join(dir, name)

	content := fmt.Sprintf(
		"=== REQUEST ===\n%s\n\n=== RESPONSE (HTTP %d) ===\n%s\n",
		masker.Mask(string(ex.RequestBody)),
		ex.StatusCode,
		masker.Mask(string(ex.ResponseBody)),
	)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write log file: %w", err)
	}
	return path, nil
}
