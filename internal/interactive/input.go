package interactive

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// inputStep prompts label and reads one line of free text from reader. An
// empty line is a valid answer (CLAUDE.md: "допускается введение пустого
// значения"). ok is false when the operator typed "exit"/"quit" or the
// input stream ended with nothing typed (Ctrl+D) — the wizard should stop
// in either case.
func inputStep(reader *bufio.Reader, stdout io.Writer, label string) (text string, ok bool) {
	fmt.Fprintf(stdout, "\n%s: ", colorize(stdout, ansiYellow, label))
	line, eof := readLine(reader)

	switch {
	case line == "" && eof:
		return "", false
	case strings.EqualFold(line, "exit"), strings.EqualFold(line, "quit"):
		return "", false
	default:
		return line, true
	}
}
