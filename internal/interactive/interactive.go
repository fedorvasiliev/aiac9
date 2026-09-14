// Package interactive implements the step-based console wizard that starts
// when aiac9 is launched without arguments: pick an optional pre-filled
// prompt template (Step A), type a prompt (Step T), send it to an LLM
// provider (Moonshot AI/Kimi or DeepSeek, chosen by model name — see
// internal/llm, internal/kimi, internal/deepseek), print the reply, and
// repeat. Repeated turns accumulate into one dialog (CLAUDE.md's
// "## Диалоги"), persisted to SQLite, until the operator resets it with
// "/clear" or "/new". See CLAUDE.md's "Логика интерактивного режима" for
// the step-by-step spec.
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

// noPromptOption is Step A's synthetic first choice, standing in for "don't
// use a template" — CLAUDE.md describes Step A's list as coming from
// ./prompts but never states it's mandatory, and Step T's own free-text
// answer would otherwise be unreachable.
const noPromptOption = "(без промпта)"

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

// Run drives the interactive wizard. Each dialog — the sequence of turns
// between an operator "/clear"/"/new" reset and the next one — runs in its
// own goroutine (CLAUDE.md: "каждый диалог должен обрабатываться в
// отдельной горутине"), so that a future mode juggling many concurrent
// dialogs needs no rework here; today, with a single stdin to read from,
// only one such goroutine is ever running at a time. Run returns when the
// operator exits (typing "exit"/"quit", or Ctrl+D) or the context is
// canceled.
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
		go runDialog(ctx, cfg, newDialogID(), store, masker, stdin, reader, stdout, opts, resultCh)

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
	restart bool // true: the operator typed "/clear"/"/new" — start a fresh dialog
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

// runDialog handles one dialog's whole lifetime: repeated Step A/Step T
// cycles, accumulating message history so each new request carries the
// prior turns as context (CLAUDE.md's "## Диалоги"). It sends exactly one
// dialogResult to resultCh before returning.
func runDialog(ctx context.Context, cfg *config.Config, dialogID string, store *dialogstore.Store, masker *secretmask.Masker, stdin *os.File, reader *bufio.Reader, stdout io.Writer, opts Options, resultCh chan<- dialogResult) {
	var history []llm.Message

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
		promptOption, ok := selectStep(stdin, reader, stdout, "Use prompt", append([]string{noPromptOption}, names...), 0)
		if !ok {
			resultCh <- dialogResult{}
			return
		}

		var tmpl *promptfile.Prompt
		if promptOption != noPromptOption {
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

		// The dialog's prior turns are the context; this turn's own
		// messages are appended on top of it for the actual API call.
		fullMessages := make([]llm.Message, 0, len(history)+len(turnMessages))
		fullMessages = append(fullMessages, history...)
		fullMessages = append(fullMessages, turnMessages...)

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

		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, colorize(stdout, ansiGreen, masker.Mask(content)))
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "total_tokens: %d, время выполнения: %.2fs\n", ex.TotalTokens, ex.Duration.Seconds())

		if logPath != "" {
			if err := exchangelog.Finish(logPath, ex.StatusCode, ex.ResponseBody, ex.Duration, masker); err != nil {
				fmt.Fprintln(stdout, "не удалось дописать лог:", masker.Mask(err.Error()))
			} else {
				fmt.Fprintln(stdout, "лог:", logPath)
			}
		}

		// This turn's messages plus the reply become part of the dialog's
		// context for its next turn, and are persisted per CLAUDE.md.
		if store != nil {
			for _, m := range turnMessages {
				if err := store.AppendMessage(dialogID, m.Role, m.Content); err != nil {
					fmt.Fprintln(stdout, "не удалось сохранить сообщение диалога:", err)
				}
			}
			if err := store.AppendMessage(dialogID, "assistant", content); err != nil {
				fmt.Fprintln(stdout, "не удалось сохранить сообщение диалога:", err)
			}
		}
		history = append(history, turnMessages...)
		history = append(history, llm.Message{Role: "assistant", Content: content})
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
