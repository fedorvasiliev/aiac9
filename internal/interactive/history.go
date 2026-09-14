package interactive

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/llm"
)

// turn groups one Step A/Step T exchange's persisted rows: its request-side
// messages (system/user, in original order) plus the assistant reply that
// followed them.
type turn struct {
	request []dialogstore.Message
	reply   dialogstore.Message
}

// groupIntoTurns splits ordered dialog history (as from Store.History)
// into turns: every "system"/"user" message up to the next "assistant"
// reply belongs to that reply's turn. A dangling request with no reply yet
// is dropped — a turn is only ever persisted once its reply has arrived.
func groupIntoTurns(history []dialogstore.Message) []turn {
	var turns []turn
	var pending []dialogstore.Message
	for _, m := range history {
		if m.Role == "assistant" {
			turns = append(turns, turn{request: pending, reply: m})
			pending = nil
			continue
		}
		pending = append(pending, m)
	}
	return turns
}

// messageIDs returns every dialog-table row ID belonging to t, for
// Store.DeleteMessages.
func (t turn) messageIDs() []int64 {
	ids := make([]int64, 0, len(t.request)+1)
	for _, m := range t.request {
		ids = append(ids, m.ID)
	}
	return append(ids, t.reply.ID)
}

// userText concatenates t's user-role request messages — the "user
// message" slot of CLAUDE.md's summarization prompt.
func (t turn) userText() string {
	var parts []string
	for _, m := range t.request {
		if m.Role == "user" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// summarizeTurn folds one turn into currentSummary via an LLM call to
// model, using CLAUDE.md's summarization prompt verbatim (just with its
// placeholders filled in).
func summarizeTurn(ctx context.Context, client *llm.Client, model, currentSummary string, t turn) (string, error) {
	prompt := fmt.Sprintf(
		"Обогати текущее summary диалога: %s\n\nСледующими сообщениями:\nuser message - %s\nassistant message - %s\n\nРезультирующее summary - должно быть коротким и лаконичным",
		currentSummary, t.userText(), t.reply.Content,
	)
	content, _, err := client.Complete(ctx, model, []llm.Message{{Role: "user", Content: prompt}}, llm.Options{}, nil)
	if err != nil {
		return "", err
	}
	return content, nil
}

// lastUsedModel returns the model of history's most recent turn (the last
// message that recorded one) — CLAUDE.md's summarization call uses
// "последней использованной модели" of the dialog, not a fixed default.
// Falls back to defaultModel for an empty history, or one predating the
// model column (see dialogstore's schema upgrade).
func lastUsedModel(history []dialogstore.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Model != "" {
			return history[i].Model
		}
	}
	return defaultModel
}

// strategyHardDeletesOverflow reports whether cfg's ContextStrategy trims
// a dialog by outright deleting turns past HistoryMsgCount rather than
// (optionally) summarizing them first — CLAUDE.md's "### Context
// Strategy": true for both "Sliding_Window" and "STICKY_FACTS".
func strategyHardDeletesOverflow(strategy string) bool {
	return strings.EqualFold(strategy, config.ContextStrategySlidingWindow) ||
		strings.EqualFold(strategy, config.ContextStrategyStickyFacts)
}

// enforceHistoryLimit keeps at most cfg.HistoryMsgCount of dialogID's most
// recent turns "active" — CLAUDE.md's "Работа с историей диалогов": "Для
// каждого диалога храним последние <HISTORY_MSG_COUNT> запросов и столько
// же соответствующих ответов". summary is the dialog's saved summary
// (loaded from dialog_summary; "" if it has none). When
// cfg.HistorySummarization is on (and ContextStrategy is neither
// Sliding_Window nor STICKY_FACTS — see strategyHardDeletesOverflow), turns
// older than the limit are folded into summary one at a time via the LLM
// and deleted from ./aiac9.db, so dialog_summary and the returned summary
// stay in sync with each other; when it's off, older turns are left in the
// database untouched but simply excluded from kept. kept is always capped
// to the newest cfg.HistoryMsgCount turns.
func enforceHistoryLimit(ctx context.Context, cfg *config.Config, store *dialogstore.Store, dialogID string, history []dialogstore.Message, stdout io.Writer) (summary string, kept []turn) {
	turns := groupIntoTurns(history)

	if store != nil {
		var err error
		summary, err = store.Summary(dialogID)
		if err != nil {
			fmt.Fprintln(stdout, "не удалось загрузить summary диалога:", err)
		}
	}

	// A non-positive HistoryMsgCount (in particular the zero value of an
	// unconfigured Config) means "no cap", not "keep nothing".
	if cfg.HistoryMsgCount <= 0 || len(turns) <= cfg.HistoryMsgCount {
		return summary, turns
	}

	overflow := turns[:len(turns)-cfg.HistoryMsgCount]
	kept = turns[len(turns)-cfg.HistoryMsgCount:]

	// CLAUDE.md's "### Context Strategy": both Sliding_Window and
	// STICKY_FACTS hard-delete everything past the cap, no summarization
	// involved — takes priority over HistorySummarization when either is
	// explicitly selected. STICKY_FACTS additionally keeps its own
	// per-dialog facts (see facts.go), independent of this trimming.
	if strategyHardDeletesOverflow(cfg.ContextStrategy) {
		if store != nil {
			for _, t := range overflow {
				if err := store.DeleteMessages(t.messageIDs()); err != nil {
					fmt.Fprintln(stdout, "не удалось удалить старые сообщения диалога:", err)
				}
			}
		}
		return summary, kept
	}

	if !cfg.HistorySummarization || store == nil {
		return summary, kept
	}

	model := lastUsedModel(history)
	client, err := resolveClient(model, cfg)
	if err != nil {
		fmt.Fprintln(stdout, "не удалось суммаризировать историю диалога:", err)
		return summary, kept
	}

	for _, t := range overflow {
		newSummary, err := summarizeTurn(ctx, client, model, summary, t)
		if err != nil {
			fmt.Fprintln(stdout, "не удалось суммаризировать сообщение диалога:", err)
			break // stop folding further turns; whatever succeeded so far is kept
		}
		summary = newSummary
		if err := store.SetSummary(dialogID, summary); err != nil {
			fmt.Fprintln(stdout, "не удалось сохранить summary диалога:", err)
		}
		if err := store.DeleteMessages(t.messageIDs()); err != nil {
			fmt.Fprintln(stdout, "не удалось удалить сжатые сообщения диалога:", err)
		}
	}

	return summary, kept
}
