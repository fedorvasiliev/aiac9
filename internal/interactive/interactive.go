// Package interactive implements the step-based console wizard that starts
// when aiac9 is launched without arguments: pick an optional pre-filled
// prompt template (Step A), type a prompt (Step T), send it to an LLM
// provider (Moonshot AI/Kimi or DeepSeek, chosen by model name — see
// internal/llm, internal/kimi, internal/deepseek), print the reply, and
// repeat. See CLAUDE.md's "Логика интерактивного режима" for the spec.
package interactive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fedorvasiliev/aiac9/internal/config"
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

// Run drives the interactive wizard: Step A picks an optional prompt
// template from ./prompts, Step T (the terminal step) reads additional
// free text and sends the request; the reply is printed and the exchange
// logged, then the wizard repeats from Step A. It returns when the
// operator exits (typing "exit"/"quit", or Ctrl+D) or the context is
// canceled.
func Run(ctx context.Context, cfg *config.Config, stdin *os.File, stdout io.Writer) error {
	fmt.Fprintln(stdout, "aiac9 — интерактивный режим (LLM). \"exit\"/\"quit\" или Ctrl+D — выход.")

	masker := secretmask.New(cfg.MoonshotAPIKey, cfg.DeepSeekAPIKey)
	reader := bufio.NewReader(stdin)

	for {
		if ctx.Err() != nil {
			return nil
		}

		names, err := promptfile.List(promptfile.Dir)
		if err != nil {
			fmt.Fprintf(stdout, "не удалось прочитать ./%s: %v\n", promptfile.Dir, err)
		}
		promptOption, ok := selectStep(stdin, reader, stdout, "Use prompt", append([]string{noPromptOption}, names...), 0)
		if !ok {
			return nil
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
			return nil
		}

		messages, model, opts := assembleRequest(tmpl, defaultModel, extra)
		if len(messages) == 0 {
			fmt.Fprintln(stdout, "нечего отправлять: выберите промпт-шаблон или введите текст")
			continue
		}

		client, err := resolveClient(model, cfg)
		if err != nil {
			fmt.Fprintln(stdout, masker.Mask(err.Error()))
			continue
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
			content, ex, err = client.Complete(ctx, model, messages, opts, onRequest)
		})
		if err != nil {
			fmt.Fprintln(stdout, masker.Mask(err.Error()))
			if ctx.Err() != nil {
				return nil // Ctrl+C/SIGTERM while waiting on the API call
			}
			continue
		}

		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, colorize(stdout, ansiGreen, masker.Mask(content)))

		if logPath != "" {
			if err := exchangelog.Finish(logPath, ex.StatusCode, ex.ResponseBody, masker); err != nil {
				fmt.Fprintln(stdout, "не удалось дописать лог:", masker.Mask(err.Error()))
				continue
			}
			fmt.Fprintln(stdout, "лог:", logPath)
		}
	}
}
