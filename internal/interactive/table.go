package interactive

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

// printPromptTable prints, per CLAUDE.md, "всё что получилось" after
// parsing a chosen prompt template: one row per non-empty section, first
// column ("Параметр") in yellow. Multi-line values (System/User prompt) are
// flattened to a single line for the table; the full text is still what
// gets sent to the model.
func printPromptTable(stdout io.Writer, p *promptfile.Prompt) {
	const paramHeader, valueHeader = "Параметр", "Значение"

	type row struct{ param, value string }
	var rows []row
	for _, r := range p.Rows() {
		rows = append(rows, row{r[0], strings.ReplaceAll(r[1], "\n", " ")})
	}
	if len(rows) == 0 {
		return
	}

	paramWidth := utf8.RuneCountInString(paramHeader)
	valueWidth := utf8.RuneCountInString(valueHeader)
	for _, r := range rows {
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
	for _, r := range rows {
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
