package interactive

import (
	"fmt"
	"io"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
)

// saveTaskState persists CLAUDE.md's "### State Machine" pause point for
// dialogID: "Это приводит к сохранению текущего состояния в базу данных."
// A nil store is a no-op (the wizard already degrades gracefully without
// history persistence elsewhere).
func saveTaskState(store *dialogstore.Store, dialogID, phase, promptFile, extra string, stdout io.Writer) {
	if store == nil {
		fmt.Fprintln(stdout, "состояние не сохранено: база диалогов не открыта")
		return
	}
	if err := store.SaveTaskState(dialogID, phase, promptFile, extra); err != nil {
		fmt.Fprintln(stdout, "не удалось сохранить состояние:", err)
		return
	}
	fmt.Fprintln(stdout, "состояние сохранено, этап:", phase)
}

// loadTaskState looks up dialogID's saved pause point for /resume
// (CLAUDE.md: "возобновление процесса с того шага на котором мы
// закончили"). ok is false when there is nothing to resume, whether
// because none was ever saved or because of a lookup error (already
// reported to stdout).
func loadTaskState(store *dialogstore.Store, dialogID string, stdout io.Writer) (phase, promptFile, extra string, ok bool) {
	if store == nil {
		return "", "", "", false
	}
	phase, promptFile, extra, ok, err := store.TaskState(dialogID)
	if err != nil {
		fmt.Fprintln(stdout, "не удалось загрузить сохранённое состояние:", err)
		return "", "", "", false
	}
	return phase, promptFile, extra, ok
}

// clearTaskState drops dialogID's saved pause point, if any — called once
// a turn actually finishes (successfully or not), so a stale pause can't
// be resumed into a dialog that has since moved on.
func clearTaskState(store *dialogstore.Store, dialogID string, stdout io.Writer) {
	if store == nil {
		return
	}
	if err := store.ClearTaskState(dialogID); err != nil {
		fmt.Fprintln(stdout, "не удалось очистить сохранённое состояние:", err)
	}
}
