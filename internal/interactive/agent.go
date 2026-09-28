package interactive

import (
	"fmt"
	"io"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

// clearFieldSentinel is CLAUDE.md's promptfile "Profile"/"Invariants"
// headings: "Если передано no - стираем текущий Profile/Invariants".
const clearFieldSentinel = "no"

// saveAgentFields persists tmpl's Profile/Invariants headings (if present)
// to the agent table — CLAUDE.md's promptfile "Profile"/"Invariants"
// headings: "сохраняются в таблицу Agent ... и применяются ко всем
// задачам которые решает агент". A value of "no" (case-insensitively)
// clears the field instead of literally saving the text "no". A nil store
// or nil tmpl is a no-op.
func saveAgentFields(store *dialogstore.Store, tmpl *promptfile.Prompt, stdout io.Writer) {
	if store == nil || tmpl == nil {
		return
	}
	if v, ok := tmpl.Value(promptfile.HeaderProfile); ok {
		if strings.EqualFold(strings.TrimSpace(v), clearFieldSentinel) {
			v = ""
			fmt.Fprintln(stdout, "профиль агента стёрт")
		}
		if err := store.SetAgentProfile(v); err != nil {
			fmt.Fprintln(stdout, "не удалось сохранить профиль агента:", err)
		}
	}
	if v, ok := tmpl.Value(promptfile.HeaderInvariants); ok {
		if strings.EqualFold(strings.TrimSpace(v), clearFieldSentinel) {
			v = ""
			fmt.Fprintln(stdout, "инварианты агента стёрты")
		}
		if err := store.SetAgentInvariants(v); err != nil {
			fmt.Fprintln(stdout, "не удалось сохранить инварианты агента:", err)
		}
	}
}

// agentContext builds the system messages that enrich every request with
// the agent's saved Profile/Invariants, regardless of which dialog or
// CONTEXT_STRATEGY is active — CLAUDE.md: "применяются ко всем задачам
// которые решает агент". A nil store, or an agent with neither field set,
// yields nil.
func agentContext(store *dialogstore.Store, stdout io.Writer) []string {
	if store == nil {
		return nil
	}
	profile, invariants, err := store.Agent()
	if err != nil {
		fmt.Fprintln(stdout, "не удалось загрузить профиль агента:", err)
		return nil
	}

	var msgs []string
	if profile != "" {
		msgs = append(msgs, "Профиль агента:\n"+profile)
	}
	if invariants != "" {
		msgs = append(msgs, "Инварианты агента:\n"+invariants)
	}
	return msgs
}
