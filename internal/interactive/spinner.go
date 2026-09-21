package interactive

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
	"github.com/fedorvasiliev/aiac9/internal/term"
)

var spinnerFrames = []rune{'|', '/', '-', '\\'}

// consolePollInterval is both the spinner's animation tick and how often
// runWithConsole checks whether fn has finished while it is polling stdin
// for a command line.
const consolePollInterval = 100 * time.Millisecond

// runWithConsole runs fn (a blocking LLM request) in the background while
// animating a spinner and — CLAUDE.md's Step T: "Пока модель обрабатывает
// результат в консоле должен выводиться анимированный Loader/Spinner. При
// этом управление должно вернуться пользователю - что бы он мог вбивать
// команды в консоле" — letting the operator type a console command (see
// commands.go, e.g. "/profile") that runs immediately, without waiting for
// fn to return.
//
// On a real terminal, stdin can't be given a read deadline — TTYs here
// reject *os.File.SetReadDeadline ("file type does not support deadline"),
// even though a plain pipe accepts it — so this polls readability with
// term.WaitReadable instead (select(2) on the raw fd, consuming nothing),
// alternating between redrawing the spinner frame (nothing to read yet)
// and handling a typed line (something is). That keeps exactly one
// goroutine ever reading from reader — concurrent reads from a
// *bufio.Reader are not safe — and guarantees no blocking Read is left in
// flight once this stops polling, so the next step's read can't race with
// it. Otherwise (piped input/output, tests) there is no sensible "type
// while waiting" interaction, so it just runs fn synchronously, as before.
func runWithConsole(stdin *os.File, reader *bufio.Reader, stdout io.Writer, store *dialogstore.Store, masker *secretmask.Masker, fn func()) {
	f, ok := stdout.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) || !term.IsTerminal(stdin.Fd()) {
		fn()
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()

	frame := 0
loop:
	for {
		select {
		case <-done:
			break loop
		default:
		}

		// A previous ReadString call may already have buffered more than
		// one line (e.g. pasted input) — that data is ours to consume
		// without waiting on the fd to become readable again.
		if reader.Buffered() == 0 {
			ready, err := term.WaitReadable(stdin.Fd(), consolePollInterval)
			if err != nil {
				// Polling isn't available here: stop trying and just wait
				// for fn to finish.
				break loop
			}
			if !ready {
				fmt.Fprintf(stdout, "\r%c", spinnerFrames[frame%len(spinnerFrames)])
				frame++
				continue
			}
		}

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			// A real read error (e.g. Ctrl+D): stop polling and just wait
			// for fn — the wizard's normal input steps handle EOF once fn
			// returns and control reaches them again.
			break loop
		}

		fmt.Fprint(stdout, "\r \r")
		if !consoleCommand(store, stdout, masker, line) && strings.TrimSpace(line) != "" {
			fmt.Fprintln(stdout, "во время ожидания ответа доступны только команды, например /profile")
		}
	}

	<-done
	fmt.Fprint(stdout, "\r \r")
}
