package interactive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
	"github.com/fedorvasiliev/aiac9/internal/term"
)

// Phase names for CLAUDE.md's "### State Machine": "planning → execution →
// validation → done. Каждый шаг должен явно отображаться в консоле."
const (
	phasePlanning   = "planning"
	phaseExecution  = "execution"
	phaseValidation = "validation"
	phaseDone       = "done"
)

// extraJumps holds CLAUDE.md's state machine additions: "Кроме
// последовательного перехода (planning → execution → validation → done)
// разрешены так же переходы состояний: execution -> planning, validation
// -> planning, done -> validation, done -> planning." The normal forward
// sequence is handled by runTurn's own phase loop, not this table — this
// is only consulted for a backward jump requested via "/planning" or
// "/validation" (CLAUDE.md's "### Команды в консоле").
var extraJumps = map[string][]string{
	phaseExecution:  {phasePlanning},
	phaseValidation: {phasePlanning},
	phaseDone:       {phaseValidation, phasePlanning},
}

// jumpAllowed reports whether extraJumps permits from -> to.
func jumpAllowed(from, to string) bool {
	for _, t := range extraJumps[from] {
		if t == to {
			return true
		}
	}
	return false
}

// phaseWaitTimeout bounds each runPhase poll as a safety net only — normal
// wake-ups are event-driven (stdin becoming readable, or fn's completion
// pipe), not timer-driven, so this being generous adds no latency.
const phaseWaitTimeout = 2 * time.Second

// runPhase prints name as the current state-machine step (CLAUDE.md:
// "Каждый шаг должен явно отображаться в консоле"), then runs fn in the
// background while letting the operator type console commands — CLAUDE.md:
// "Пока модель обрабатывает результат управление должно вернуться
// пользователю - что бы он мог вбивать команды в консоле. Запрос в llm
// должен быть отправлен и обрабатываться в отдельной goroutine в фоновом
// режиме". In particular "/pause" (CLAUDE.md: "Пользователь должен иметь
// возможность поставить паузу на любом из этапов") cancels fn's context
// and makes runPhase report paused=true immediately, without waiting any
// further for fn's own result. "/planning" and "/validation" (CLAUDE.md's
// extra state machine transitions) likewise cancel fn's context and make
// runPhase return the requested phase as jumpTo, if extraJumps allows a
// jump from name to it — runTurn's phase loop then re-enters at jumpTo
// instead of advancing to name's normal successor.
//
// On a real terminal, stdin can't be given a read deadline (TTYs here
// reject *os.File.SetReadDeadline: "file type does not support deadline"),
// so this polls readability with term.WaitAny instead (select(2) on the
// raw fds, consuming nothing) — watching stdin and a private wake-up pipe
// that fn's goroutine closes when it returns, so runPhase reacts to fn
// finishing immediately rather than on a timer tick. That keeps exactly
// one goroutine ever reading from reader (concurrent reads from a
// *bufio.Reader are not safe) and guarantees no blocking Read is left in
// flight once runPhase returns, so the next phase's own runPhase call
// can't race with it. Otherwise (piped input/output, tests) there is no
// sensible "type while waiting" interaction, so it just runs fn
// synchronously and is never paused.
func runPhase(ctx context.Context, stdin *os.File, reader *bufio.Reader, stdout io.Writer, store *dialogstore.Store, masker *secretmask.Masker, name string, fn func(context.Context)) (paused bool, jumpTo string) {
	fmt.Fprintln(stdout, colorize(stdout, ansiYellow, "Этап:"), name)

	f, ok := stdout.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) || !term.IsTerminal(stdin.Fd()) {
		fn(ctx)
		return false, ""
	}

	phaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wakeR, wakeW, err := os.Pipe()
	if err != nil {
		fn(phaseCtx)
		return false, ""
	}
	defer wakeR.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(phaseCtx)
		wakeW.Write([]byte{0})
		wakeW.Close()
	}()

loop:
	for {
		// A previous ReadString call may already have buffered more than
		// one line (e.g. pasted input) — that data is ours to consume
		// without waiting on the fd to become readable again.
		if reader.Buffered() == 0 {
			ready, err := term.WaitAny([]uintptr{stdin.Fd(), wakeR.Fd()}, phaseWaitTimeout)
			if err != nil {
				// Polling isn't available here: stop trying and just wait
				// for fn to finish.
				break loop
			}
			if ready[1] {
				break loop
			}
			if !ready[0] {
				continue
			}
		}

		line, rerr := reader.ReadString('\n')
		if rerr != nil && line == "" {
			// A real read error (e.g. Ctrl+D): stop polling and just wait
			// for fn — the wizard's normal input steps handle EOF once fn
			// returns and control reaches them again.
			break loop
		}

		trimmed := strings.TrimSpace(line)
		switch {
		case strings.EqualFold(trimmed, cmdPause):
			fmt.Fprintln(stdout, "принято: приостанавливаю этап", name)
			cancel()
			paused = true
			break loop
		case strings.EqualFold(trimmed, cmdResume):
			fmt.Fprintln(stdout, "задача уже выполняется — возобновлять нечего")
		case strings.EqualFold(trimmed, cmdPlanning):
			if !jumpAllowed(name, phasePlanning) {
				fmt.Fprintln(stdout, "переход недопустим:", name, "->", phasePlanning)
				continue
			}
			fmt.Fprintln(stdout, "принято: перехожу на этап", phasePlanning)
			cancel()
			jumpTo = phasePlanning
			break loop
		case strings.EqualFold(trimmed, cmdValidation):
			if !jumpAllowed(name, phaseValidation) {
				fmt.Fprintln(stdout, "переход недопустим:", name, "->", phaseValidation)
				continue
			}
			fmt.Fprintln(stdout, "принято: перехожу на этап", phaseValidation)
			cancel()
			jumpTo = phaseValidation
			break loop
		case consoleCommand(store, stdout, masker, trimmed):
			// handled
		case trimmed != "":
			fmt.Fprintln(stdout, "во время выполнения доступны только команды, например /profile или /pause")
		}
	}

	<-done
	return paused, jumpTo
}
