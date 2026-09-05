package interactive

import (
	"io"
	"os"

	"github.com/fedorvasiliev/aiac9/internal/term"
)

const (
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiReset  = "\x1b[0m"
)

// colorize wraps s in the given ANSI color code, but only when out is a
// real terminal — piped output (scripts, tests, redirected logs) stays
// plain so it isn't cluttered with escape codes.
func colorize(out io.Writer, code, s string) string {
	if f, ok := out.(*os.File); ok && term.IsTerminal(f.Fd()) {
		return code + s + ansiReset
	}
	return s
}
