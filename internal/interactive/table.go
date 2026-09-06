package interactive

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

// longValueThreshold: past this many characters a table cell stops being
// readable, so CLAUDE.md has the whole printout switch to one
// heading-and-value block per section instead.
const longValueThreshold = 80

// printPromptTable prints, per CLAUDE.md, "всё что получилось" after
// parsing a chosen prompt template: one row per non-empty section, first
// column/heading in yellow, empty sections skipped. If any value is longer
// than longValueThreshold, the compact table would make it unreadable, so
// every section is printed instead as its own "Заголовок:\n<значение>"
// block, preserving the value's original line breaks.
func printPromptTable(stdout io.Writer, p *promptfile.Prompt) {
	rows := p.Rows()
	if len(rows) == 0 {
		return
	}

	long := false
	for _, r := range rows {
		if utf8.RuneCountInString(r[1]) > longValueThreshold {
			long = true
			break
		}
	}

	if long {
		printPromptBlocks(stdout, rows)
		return
	}
	printPromptTableCompact(stdout, rows)
}

func printPromptBlocks(stdout io.Writer, rows [][2]string) {
	fmt.Fprintln(stdout)
	for i, r := range rows {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%s:\n%s\n", colorize(stdout, ansiYellow, r[0]), r[1])
	}
	fmt.Fprintln(stdout)
}

func printPromptTableCompact(stdout io.Writer, rows [][2]string) {
	const paramHeader, valueHeader = "Параметр", "Значение"

	type row struct{ param, value string }
	flat := make([]row, 0, len(rows))
	for _, r := range rows {
		flat = append(flat, row{r[0], strings.ReplaceAll(r[1], "\n", " ")})
	}

	paramWidth := utf8.RuneCountInString(paramHeader)
	valueWidth := utf8.RuneCountInString(valueHeader)
	for _, r := range flat {
		paramWidth = max(paramWidth, utf8.RuneCountInString(r.param))
		valueWidth = max(valueWidth, utf8.RuneCountInString(r.value))
	}

	printRow := func(param, value string) {
		fmt.Fprintf(stdout, "| %s | %s |\n",
			colorize(stdout, ansiYellow, padRight(param, paramWidth)),
			padRight(value, valueWidth))
	}

	fmt.Fprintln(stdout)
	printRow(paramHeader, valueHeader)
	fmt.Fprintf(stdout, "|%s|%s|\n", strings.Repeat("-", paramWidth+2), strings.Repeat("-", valueWidth+2))
	for _, r := range flat {
		printRow(r.param, r.value)
	}
	fmt.Fprintln(stdout)
}

func padRight(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
