// Package interactive implements the step-based console wizard that starts
// when aiac9 is launched without arguments: pick or resume a dialog (Step
// D), pick an optional pre-filled prompt template (Step A), type a prompt
// (Step T), send it to an LLM provider (Moonshot AI/Kimi or DeepSeek,
// chosen by model name — see internal/llm, internal/kimi,
// internal/deepseek), print the reply, and repeat. Repeated turns
// accumulate into one dialog (CLAUDE.md's "## Диалоги"), persisted to
// SQLite, until the operator resets it with "/clear", "/new", or by
// re-selecting at Step D. See CLAUDE.md's "Логика интерактивного режима"
// for the step-by-step spec.
package interactive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/exchangelog"
	"github.com/fedorvasiliev/aiac9/internal/llm"
	"github.com/fedorvasiliev/aiac9/internal/promptfile"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// defaultModel is used when no prompt template is selected at Step A, or
// the selected one doesn't specify a Model heading.
const defaultModel = "kimi-k2.6"

// userPromptOption is Step A's default first choice — "no template, just
// whatever gets typed at Step T" — named after CLAUDE.md's
// "Пользовательский промпт".
const userPromptOption = "Пользовательский промпт"

// newDialogOption is Step D's default first choice, standing in for
// "start a fresh dialog" per CLAUDE.md.
const newDialogOption = "/new"

// newBranchOption and continueOption are the CONTEXT_STRATEGY=Branching
// sub-step's two built-in choices (CLAUDE.md's "### Context Strategy"),
// offered after Step D resolves an existing dialog. newBranchOption is the
// default.
const (
	newBranchOption = "/new-branch"
	continueOption  = "/continue"
)

// Options are CLI-flag-driven overrides for Run (see internal/cli's
// "-f"/"-timeout" flags).
type Options struct {
	// PromptFilter, if non-empty, restricts Step A's list to ./prompts
	// file names containing this substring (CLI flag -f).
	PromptFilter string

	// ResponseTimeout, if non-nil, overrides internal/llm's default
	// response timeout for every request this run makes (CLI flag
	// -timeout, given in seconds).
	ResponseTimeout *time.Duration
}

// Run drives the interactive wizard. Each dialog — from its Step D
// selection to the operator resetting it (typing "/clear"/"/new" at Step
// T, or re-picking at the next Step D) — runs in its own goroutine
// (CLAUDE.md: "каждый диалог должен обрабатываться в отдельной
// горутине"), so that a future mode juggling many concurrent dialogs needs
// no rework here; today, with a single stdin to read from, only one such
// goroutine is ever running at a time. Run returns when the operator exits
// (typing "exit"/"quit", or Ctrl+D) or the context is canceled.
func Run(ctx context.Context, cfg *config.Config, stdin *os.File, stdout io.Writer, opts Options) error {
	fmt.Fprintln(stdout, "aiac9 — интерактивный режим (LLM). \"exit\"/\"quit\" или Ctrl+D — выход, \"/clear\"/\"/new\" — новый диалог.")

	masker := secretmask.New(cfg.MoonshotAPIKey, cfg.DeepSeekAPIKey)
	reader := bufio.NewReader(stdin)

	store, err := dialogstore.Open(dialogstore.DefaultPath)
	if err != nil {
		fmt.Fprintln(stdout, "не удалось открыть базу диалогов:", err)
		store = nil // degrade gracefully: the wizard still works, just without history persistence
	} else {
		defer store.Close()
	}

	for {
		if ctx.Err() != nil {
			return nil
		}

		resultCh := make(chan dialogResult, 1)
		go runDialog(ctx, cfg, store, masker, stdin, reader, stdout, opts, resultCh)

		result := <-resultCh
		if result.err != nil {
			return result.err
		}
		if !result.restart {
			return nil
		}
	}
}

// dialogResult is how a runDialog goroutine reports back to Run.
type dialogResult struct {
	restart bool // true: the operator typed "/clear"/"/new" — return to Step D
	err     error
}

func newDialogID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// isNewDialogCommand reports whether extra (Step T's answer) is a command
// to reset the dialog rather than prompt text.
func isNewDialogCommand(extra string) bool {
	extra = strings.TrimSpace(extra)
	return strings.EqualFold(extra, "/clear") || strings.EqualFold(extra, "/new")
}

// runDialog handles one dialog's whole lifetime: Step D picks it, then
// repeated Step A/Step T cycles accumulate its history as a system-prompt
// digest so each new request carries the prior turns as context
// (CLAUDE.md's "## Диалоги" and "Шаг D"). It sends exactly one
// dialogResult to resultCh before returning.
func runDialog(ctx context.Context, cfg *config.Config, store *dialogstore.Store, masker *secretmask.Masker, stdin *os.File, reader *bufio.Reader, stdout io.Writer, opts Options, resultCh chan<- dialogResult) {
	sel := selectDialog(ctx, cfg, store, stdin, reader, stdout)
	if !sel.ok {
		resultCh <- dialogResult{}
		return
	}
	dialogID, parentID, dialogContext := sel.id, sel.parentID, sel.context
	dialogPromptTotal, dialogCompletionTotal := sel.promptTotal, sel.completionTotal

	for {
		if ctx.Err() != nil {
			resultCh <- dialogResult{}
			return
		}

		names, err := promptfile.List(promptfile.Dir)
		if err != nil {
			fmt.Fprintf(stdout, "не удалось прочитать ./%s: %v\n", promptfile.Dir, err)
		}
		if opts.PromptFilter != "" {
			names = filterNames(names, opts.PromptFilter)
		}
		promptOption, ok := selectStep(stdin, reader, stdout, "Use prompt", append([]string{userPromptOption}, names...), 0)
		if !ok {
			resultCh <- dialogResult{}
			return
		}

		var tmpl *promptfile.Prompt
		if promptOption != userPromptOption {
			tmpl, err = promptfile.Parse(filepath.Join(promptfile.Dir, promptOption))
			if err != nil {
				fmt.Fprintf(stdout, "не удалось разобрать %s: %v\n", promptOption, err)
				continue
			}
			printPromptTable(stdout, tmpl)
		}

		extra, ok := inputStep(reader, stdout, "Дополнить user prompt и отправить")
		if !ok {
			resultCh <- dialogResult{}
			return
		}
		if isNewDialogCommand(extra) {
			fmt.Fprintln(stdout, "начат новый диалог")
			resultCh <- dialogResult{restart: true}
			return
		}

		turnMessages, model, llmOpts := assembleRequest(tmpl, defaultModel, extra)
		if len(turnMessages) == 0 {
			fmt.Fprintln(stdout, "нечего отправлять: выберите промпт-шаблон или введите текст")
			continue
		}

		client, err := resolveClient(model, cfg)
		if err != nil {
			fmt.Fprintln(stdout, masker.Mask(err.Error()))
			continue
		}
		if opts.ResponseTimeout != nil {
			client.HTTPClient.Timeout = *opts.ResponseTimeout
		}

		stickyFacts := strings.EqualFold(cfg.ContextStrategy, config.ContextStrategyStickyFacts)

		// The dialog's history so far is folded into one system message
		// (CLAUDE.md's Step D: "обогатить ей system prompt"), ahead of
		// this turn's own messages. CONTEXT_STRATEGY=STICKY_FACTS adds a
		// system message of previously extracted facts too, plus an
		// instruction (after the turn's own messages) telling the model to
		// extract new facts into a dedicated section of its reply.
		fullMessages := make([]llm.Message, 0, len(turnMessages)+3)
		if stickyFacts {
			if fc := factsContext(store, dialogID, stdout); fc != "" {
				fullMessages = append(fullMessages, llm.Message{Role: "system", Content: fc})
			}
		}
		if dialogContext != "" {
			fullMessages = append(fullMessages, llm.Message{Role: "system", Content: dialogContext})
		}
		fullMessages = append(fullMessages, turnMessages...)
		if stickyFacts {
			fullMessages = append(fullMessages, llm.Message{Role: "system", Content: factsInstruction})
		}

		// The request must be logged right before it is sent (CLAUDE.md),
		// not only once the response comes back — so the log file is
		// started from inside Complete, right before the HTTP call.
		var logPath string
		onRequest := func(reqBody []byte) {
			path, logErr := exchangelog.Start(exchangelog.Dir, model, reqBody, masker)
			if logErr != nil {
				fmt.Fprintln(stdout, "не удалось записать лог:", masker.Mask(logErr.Error()))
				return
			}
			logPath = path
		}

		var content string
		var ex *llm.Exchange
		runWithSpinner(stdout, func() {
			content, ex, err = client.Complete(ctx, model, fullMessages, llmOpts, onRequest)
		})
		if err != nil {
			fmt.Fprintln(stdout, masker.Mask(err.Error()))
			if ctx.Err() != nil {
				resultCh <- dialogResult{}
				return
			}
			continue
		}

		if stickyFacts {
			var facts []dialogstore.Fact
			content, facts = extractFacts(content)
			if store != nil {
				saveFacts(store, dialogID, facts, stdout)
			}
		}

		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, colorize(stdout, ansiGreen, masker.Mask(content)))

		dialogPromptTotal += ex.PromptTokens
		dialogCompletionTotal += ex.CompletionTokens
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "prompt_tokens: %d, completion_tokens: %d\n", ex.PromptTokens, ex.CompletionTokens)
		fmt.Fprintf(stdout, "dialog prompt_tokens: %d, dialog completion_tokens: %d\n", dialogPromptTotal, dialogCompletionTotal)
		fmt.Fprintf(stdout, "время выполнения: %.2fs\n", ex.Duration.Seconds())

		if logPath != "" {
			if err := exchangelog.Finish(logPath, ex.StatusCode, ex.ResponseBody, ex.Duration, masker); err != nil {
				fmt.Fprintln(stdout, "не удалось дописать лог:", masker.Mask(err.Error()))
			} else {
				fmt.Fprintln(stdout, "лог:", logPath)
			}
		}

		// This turn's messages plus the reply enrich the dialog's context
		// for its next turn, and are persisted per CLAUDE.md — prompt_tokens
		// on the request-side rows, completion_tokens on the reply's row.
		// parentID is only non-empty for a freshly forked branch (see
		// selectBranch), and is then stamped on every row of that branch,
		// same as model.
		if store != nil {
			promptTokens := ex.PromptTokens
			for _, m := range turnMessages {
				if err := store.AppendMessage(dialogID, m.Role, m.Content, model, parentID, &promptTokens, nil); err != nil {
					fmt.Fprintln(stdout, "не удалось сохранить сообщение диалога:", err)
				}
			}
			completionTokens := ex.CompletionTokens
			if err := store.AppendMessage(dialogID, "assistant", content, model, parentID, nil, &completionTokens); err != nil {
				fmt.Fprintln(stdout, "не удалось сохранить сообщение диалога:", err)
			}
		}
		for _, m := range turnMessages {
			appendDialogContext(&dialogContext, m.Role, m.Content)
		}
		appendDialogContext(&dialogContext, "assistant", content)
	}
}

// dialogSelection is selectDialog's result.
type dialogSelection struct {
	id              string // the dialog_id future turns are written under
	parentID        string // non-empty only for a freshly forked branch — CLAUDE.md's Branching
	context         string
	promptTotal     int
	completionTotal int
	ok              bool // false when the operator exited (Ctrl+D, "exit"/"quit", or "q")
}

// selectDialog runs Step D: pick "/new" to start a fresh dialog, or an
// existing one to resume it (possibly redirected by the
// CONTEXT_STRATEGY=Branching sub-step — see selectBranch). Resuming prints
// its last two Q&A pairs (per CLAUDE.md) and returns a system-prompt
// context string built from its saved summary (if any — CLAUDE.md: "если
// для диалога существует summary ... его также необходимо добавить к
// system prompt") plus its most recent turns (see enforceHistoryLimit and
// appendDialogContext), along with the full history's summed
// prompt_tokens/completion_tokens to seed the running "dialog
// prompt_tokens"/"dialog completion_tokens" totals.
func selectDialog(ctx context.Context, cfg *config.Config, store *dialogstore.Store, stdin *os.File, reader *bufio.Reader, stdout io.Writer) dialogSelection {
	var summaries []dialogstore.DialogSummary
	if store != nil {
		var err error
		summaries, err = store.ListDialogs()
		if err != nil {
			fmt.Fprintln(stdout, "не удалось прочитать список диалогов:", err)
		}
	}

	options := make([]string, 0, len(summaries)+1)
	options = append(options, newDialogOption)
	labelToID := make(map[string]string, len(summaries))
	for _, s := range summaries {
		options = append(options, s.Label)
		labelToID[s.Label] = s.ID
	}

	choice, selected := selectStep(stdin, reader, stdout, "Dialog", options, 0)
	if !selected {
		return dialogSelection{}
	}
	if choice == newDialogOption {
		return dialogSelection{id: newDialogID(), ok: true}
	}

	id := labelToID[choice]
	historyID := id // whose history/summary/facts seed the context — differs from id only for a fresh branch fork
	var parentID string

	if strings.EqualFold(cfg.ContextStrategy, config.ContextStrategyBranching) {
		writeID, hID, pID, ok := selectBranch(store, id, stdin, reader, stdout)
		if !ok {
			return dialogSelection{}
		}
		id, historyID, parentID = writeID, hID, pID
	}

	var history []dialogstore.Message
	if store != nil {
		var err error
		history, err = store.History(historyID)
		if err != nil {
			fmt.Fprintln(stdout, "не удалось загрузить историю диалога:", err)
		}
	}

	printRecentHistory(stdout, history, 2)

	// The dialog's lifetime token totals cover its whole persisted
	// history, independent of how much of that history stays "active"
	// context below.
	var promptTotal, completionTotal int
	for _, m := range history {
		if m.PromptTokens != nil {
			promptTotal += *m.PromptTokens
		}
		if m.CompletionTokens != nil {
			completionTotal += *m.CompletionTokens
		}
	}

	summary, kept := enforceHistoryLimit(ctx, cfg, store, historyID, history, stdout)
	var turnsContext string
	for _, t := range kept {
		for _, m := range t.request {
			appendDialogContext(&turnsContext, m.Role, m.Content)
		}
		appendDialogContext(&turnsContext, "assistant", t.reply.Content)
	}

	var dialogContext string
	switch {
	case summary != "" && turnsContext != "":
		dialogContext = "Summary предыдущего диалога:\n" + summary + "\n\n" + turnsContext
	case summary != "":
		dialogContext = "Summary предыдущего диалога:\n" + summary
	default:
		dialogContext = turnsContext
	}

	return dialogSelection{id: id, parentID: parentID, context: dialogContext, promptTotal: promptTotal, completionTotal: completionTotal, ok: true}
}

// selectBranch runs CONTEXT_STRATEGY=Branching's sub-step, shown after
// Step D resolves an existing dialog (CLAUDE.md's "### Context Strategy"):
// choose "/new-branch" (default) to fork it, "/continue" to keep working
// in it unchanged, or jump directly into one of its existing branches if
// it has any ("если у диалога есть ветки, их необходимо давать выбирать").
//
// writeID is the dialog_id future turns should be written under; historyID
// is whose history/summary/facts should seed the context (equal to
// dialogID unless writeID ended up being a different existing branch);
// parentID is non-empty only when writeID is a brand-new forked branch
// (dialogID itself), for AppendMessage to stamp on its rows.
func selectBranch(store *dialogstore.Store, dialogID string, stdin *os.File, reader *bufio.Reader, stdout io.Writer) (writeID, historyID, parentID string, ok bool) {
	var branches []dialogstore.DialogSummary
	if store != nil {
		var err error
		branches, err = store.Branches(dialogID)
		if err != nil {
			fmt.Fprintln(stdout, "не удалось прочитать ветки диалога:", err)
		}
	}

	options := make([]string, 0, len(branches)+2)
	options = append(options, newBranchOption, continueOption)
	labelToID := make(map[string]string, len(branches))
	for _, b := range branches {
		options = append(options, b.Label)
		labelToID[b.Label] = b.ID
	}

	choice, selected := selectStep(stdin, reader, stdout, "Branch", options, 0)
	if !selected {
		return "", "", "", false
	}
	switch choice {
	case newBranchOption:
		return newDialogID(), dialogID, dialogID, true
	case continueOption:
		return dialogID, dialogID, "", true
	default:
		branchID := labelToID[choice]
		return branchID, branchID, "", true
	}
}

// appendDialogContext folds one more message into *ctx, in the same
// "Role: content" line format used both to seed a resumed dialog's context
// (from its stored history) and to grow it turn by turn.
func appendDialogContext(ctx *string, role, content string) {
	line := fmt.Sprintf("%s: %s", dialogRoleLabel(role), content)
	if *ctx == "" {
		*ctx = "Контекст предыдущего диалога:\n" + line
		return
	}
	*ctx = *ctx + "\n" + line
}

func dialogRoleLabel(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "system":
		return "System"
	default:
		return role
	}
}

// qaPair is one question/answer turn, for printRecentHistory.
type qaPair struct{ question, answer string }

// lastQAPairs pairs each "user" message with the "assistant" message that
// follows it, and returns at most the last n such pairs, oldest first.
func lastQAPairs(history []dialogstore.Message, n int) []qaPair {
	var pairs []qaPair
	var pendingQuestion string
	haveQuestion := false
	for _, m := range history {
		switch m.Role {
		case "user":
			pendingQuestion = m.Content
			haveQuestion = true
		case "assistant":
			if haveQuestion {
				pairs = append(pairs, qaPair{question: pendingQuestion, answer: m.Content})
				haveQuestion = false
			}
		}
	}
	if len(pairs) > n {
		pairs = pairs[len(pairs)-n:]
	}
	return pairs
}

// printRecentHistory prints the last n Q&A pairs of history, per CLAUDE.md
// Step D: "в консоли необходимо вывести последние 2 вопроса и ответа из
// истории диалога (выводим в стандартном виде)" — the same yellow-label,
// green-answer style used for a live turn's own output.
func printRecentHistory(stdout io.Writer, history []dialogstore.Message, n int) {
	pairs := lastQAPairs(history, n)
	if len(pairs) == 0 {
		return
	}

	fmt.Fprintln(stdout)
	for _, p := range pairs {
		fmt.Fprintln(stdout, colorize(stdout, ansiYellow, "Вопрос:"), p.question)
		fmt.Fprintln(stdout, colorize(stdout, ansiYellow, "Ответ:"))
		fmt.Fprintln(stdout, colorize(stdout, ansiGreen, p.answer))
		fmt.Fprintln(stdout)
	}
}

// filterNames keeps only the names containing substr.
func filterNames(names []string, substr string) []string {
	var out []string
	for _, n := range names {
		if strings.Contains(n, substr) {
			out = append(out, n)
		}
	}
	return out
}
