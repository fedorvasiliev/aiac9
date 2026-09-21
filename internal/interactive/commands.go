package interactive

import (
	"fmt"
	"io"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// CLAUDE.md's "### Команды в консоле": "/profile - выводит текущий
// активный профайл", "/pause - пауза в работе агента", "/resume -
// возобновление работы агента с сохраненного шага", "/invariants -
// выводит текущие инварианты". /pause and /resume have control-flow
// effects (aborting/replaying a turn) beyond printing something, so
// they're recognized separately — isPauseCommand/isResumeCommand below,
// checked by phase.go's runPhase and by runDialog's Step T handling —
// rather than through consoleCommand, which only ever prints and returns.
const (
	cmdProfile    = "/profile"
	cmdPause      = "/pause"
	cmdResume     = "/resume"
	cmdInvariants = "/invariants"
)

// isPauseCommand and isResumeCommand report whether s (case-insensitively,
// surrounding whitespace ignored) is that command.
func isPauseCommand(s string) bool  { return strings.EqualFold(strings.TrimSpace(s), cmdPause) }
func isResumeCommand(s string) bool { return strings.EqualFold(strings.TrimSpace(s), cmdResume) }

// consoleCommand recognizes and runs a print-only console command typed as
// line (case-insensitively, surrounding whitespace ignored), reporting
// whether line was one — so a caller can decide what unrecognized input
// means in its own context (Step T: send it as prompt text; a runPhase
// wait window: report it as not a command).
func consoleCommand(store *dialogstore.Store, stdout io.Writer, masker *secretmask.Masker, line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case cmdProfile:
		printActiveProfile(store, stdout, masker)
		return true
	case cmdInvariants:
		printActiveInvariants(store, stdout, masker)
		return true
	default:
		return false
	}
}

// printActiveProfile handles /profile. saveAgentFields (agent.go) is what
// populates the agent table's Profile column, from a prompt template's
// "Profile" heading.
func printActiveProfile(store *dialogstore.Store, stdout io.Writer, masker *secretmask.Masker) {
	if store == nil {
		fmt.Fprintln(stdout, "профиль агента недоступен: база диалогов не открыта")
		return
	}
	profile, _, err := store.Agent()
	if err != nil {
		fmt.Fprintln(stdout, "не удалось загрузить профиль агента:", masker.Mask(err.Error()))
		return
	}
	if profile == "" {
		fmt.Fprintln(stdout, "текущий активный профиль не задан")
		return
	}
	fmt.Fprintln(stdout, colorize(stdout, ansiYellow, "Профиль агента:"))
	fmt.Fprintln(stdout, masker.Mask(profile))
}

// printActiveInvariants handles /invariants. saveAgentFields (agent.go) is
// what populates the agent table's Invariants column, from a prompt
// template's "Invariants" heading.
func printActiveInvariants(store *dialogstore.Store, stdout io.Writer, masker *secretmask.Masker) {
	if store == nil {
		fmt.Fprintln(stdout, "инварианты агента недоступны: база диалогов не открыта")
		return
	}
	_, invariants, err := store.Agent()
	if err != nil {
		fmt.Fprintln(stdout, "не удалось загрузить инварианты агента:", masker.Mask(err.Error()))
		return
	}
	if invariants == "" {
		fmt.Fprintln(stdout, "текущие инварианты не заданы")
		return
	}
	fmt.Fprintln(stdout, colorize(stdout, ansiYellow, "Инварианты агента:"))
	fmt.Fprintln(stdout, masker.Mask(invariants))
}
