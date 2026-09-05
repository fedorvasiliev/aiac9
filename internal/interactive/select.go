package interactive

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"github.com/fedorvasiliev/aiac9/internal/term"
)

// selectStep asks the operator to choose one of options via label, and
// reports the chosen value and whether a choice was made at all (false
// means the input stream ended, e.g. Ctrl+D, and the wizard should stop).
//
// On a real terminal the operator cycles options with the up/down arrow
// keys or space (wrapping around) and confirms with Enter, per CLAUDE.md.
// Otherwise (piped input — scripts, tests) it falls back to a plain
// numbered prompt read from reader.
func selectStep(stdin *os.File, reader *bufio.Reader, stdout io.Writer, label string, options []string, defaultIndex int) (string, bool) {
	fmt.Fprintf(stdout, "\n%s:\n", colorize(stdout, ansiYellow, label))

	if !term.IsTerminal(stdin.Fd()) {
		return selectFallback(reader, stdout, options, defaultIndex)
	}
	return selectInteractive(stdin, stdout, options, defaultIndex)
}

func optionLine(marker, opt string, isDefault bool) string {
	suffix := ""
	if isDefault {
		suffix = " (default)"
	}
	return marker + opt + suffix
}

// selectFallback is the non-tty select prompt: an operator types the option
// number, its exact text, or an empty line to accept the default.
func selectFallback(reader *bufio.Reader, stdout io.Writer, options []string, defaultIndex int) (string, bool) {
	for i, opt := range options {
		fmt.Fprintln(stdout, "  "+optionLine(fmt.Sprintf("%d) ", i+1), opt, i == defaultIndex))
	}

	for {
		fmt.Fprint(stdout, "> ")
		line, eof := readLine(reader)

		switch {
		case line == "" && eof:
			return "", false
		case line == "":
			return options[defaultIndex], true
		case strings.EqualFold(line, "exit"), strings.EqualFold(line, "quit"):
			return "", false
		}

		if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(options) {
			return options[n-1], true
		}
		for _, opt := range options {
			if strings.EqualFold(opt, line) {
				return opt, true
			}
		}

		if eof {
			return "", false
		}
		fmt.Fprintf(stdout, "не понял выбор %q, попробуйте ещё раз\n", line)
	}
}

// selectInteractive is the raw-terminal, arrow-key select prompt.
func selectInteractive(stdin *os.File, stdout io.Writer, options []string, defaultIndex int) (string, bool) {
	oldState, err := term.MakeRaw(stdin.Fd())
	if err != nil {
		// Raw mode unavailable for some reason — degrade gracefully rather
		// than leaving the operator stuck with no way to answer.
		return selectFallback(bufio.NewReader(stdin), stdout, options, defaultIndex)
	}

	var restoreOnce sync.Once
	restore := func() { restoreOnce.Do(func() { term.Restore(stdin.Fd(), oldState) }) }
	defer restore()

	// A Ctrl+C during raw mode must still restore the terminal before the
	// process exits, or the shell is left unusable afterwards.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		if _, ok := <-sigCh; ok {
			restore()
			os.Exit(130)
		}
	}()

	cur := defaultIndex
	redraw(stdout, options, cur, defaultIndex, true)

	buf := make([]byte, 3)
	for {
		n, err := stdin.Read(buf[:1])
		if err != nil || n == 0 {
			return "", false
		}

		switch buf[0] {
		case '\r', '\n':
			fmt.Fprintln(stdout)
			return options[cur], true
		case ' ':
			cur = (cur + 1) % len(options)
			redraw(stdout, options, cur, defaultIndex, false)
		case 0x03: // Ctrl+C, in case the terminal delivers it as a byte
			restore()
			os.Exit(130)
		case 'q', 'Q':
			fmt.Fprintln(stdout)
			return "", false
		case 0x1b: // ESC — possibly an arrow-key escape sequence "\x1b[A"/"\x1b[B"
			n2, _ := stdin.Read(buf[1:3])
			if n2 == 2 && buf[1] == '[' {
				switch buf[2] {
				case 'A': // up
					cur = (cur - 1 + len(options)) % len(options)
					redraw(stdout, options, cur, defaultIndex, false)
				case 'B': // down
					cur = (cur + 1) % len(options)
					redraw(stdout, options, cur, defaultIndex, false)
				}
			}
		}
	}
}

// redraw (re)prints the option list with the cursor on cur. On repeat calls
// (first == false) it first moves the cursor up and erases the previously
// printed lines, so the list updates in place instead of scrolling.
func redraw(stdout io.Writer, options []string, cur, defaultIndex int, first bool) {
	if !first {
		fmt.Fprintf(stdout, "\x1b[%dA", len(options))
	}
	for i, opt := range options {
		marker := "  "
		if i == cur {
			marker = "> "
		}
		fmt.Fprintf(stdout, "\x1b[2K\r%s\n", optionLine(marker, opt, i == defaultIndex))
	}
}

// readLine reads one line from reader, trimmed of surrounding whitespace.
// eof reports that the underlying stream is exhausted; when it is true and
// line is empty there is nothing further to read at all. A non-empty line
// alongside eof == true still carries the stream's final, unterminated
// line and should be used normally.
func readLine(reader *bufio.Reader) (line string, eof bool) {
	raw, err := reader.ReadString('\n')
	return strings.TrimSpace(raw), err != nil
}
