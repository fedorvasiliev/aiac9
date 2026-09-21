package interactive

import (
	"fmt"
	"io"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// cmdProfile is CLAUDE.md's "### Команды в консоле": "/profile - выводит
// текущий активный профайл".
const cmdProfile = "/profile"

// consoleCommand recognizes and runs a console command typed as line
// (case-insensitively, surrounding whitespace ignored), reporting whether
// line was one — so a caller can decide what unrecognized input means in
// its own context (Step T: send it as prompt text; the in-flight wait
// window in runWithConsole: report it as not a command).
func consoleCommand(store *dialogstore.Store, stdout io.Writer, masker *secretmask.Masker, line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case cmdProfile:
		printActiveProfile(store, stdout, masker)
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
