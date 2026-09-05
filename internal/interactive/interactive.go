// Package interactive implements the step-based console wizard that starts
// when aiac9 is launched without arguments: pick a model, type a prompt,
// send it to the Moonshot AI (Kimi) chat completions API, print the reply,
// and repeat. See CLAUDE.md's "Логика интерактивного режима" for the spec.
package interactive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/exchangelog"
	"github.com/fedorvasiliev/aiac9/internal/kimi"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// models are Step 1's choices, in CLAUDE.md's order.
var models = []string{
	"kimi-k3",
	"kimi-k2.7-code",
	"kimi-k2.7-code-highspeed",
	"kimi-k2.6",
}

// defaultModelIndex is "kimi-k2.6 (default)" from CLAUDE.md.
const defaultModelIndex = 3

// Run drives the interactive wizard: Step 1 selects a model, Step T (the
// terminal step) reads a user prompt and sends the request, its reply is
// printed and the exchange logged, then the wizard repeats from Step 1. It
// returns when the operator exits (typing "exit"/"quit", or Ctrl+D) or the
// context is canceled.
func Run(ctx context.Context, cfg *config.Config, stdin *os.File, stdout io.Writer) error {
	fmt.Fprintln(stdout, "aiac9 — интерактивный режим (LLM). \"exit\"/\"quit\" или Ctrl+D — выход.")

	client := kimi.NewClient(cfg.MoonshotAPIKey)
	if cfg.MoonshotBaseURL != "" {
		client.BaseURL = cfg.MoonshotBaseURL
	}
	masker := secretmask.New(cfg.MoonshotAPIKey)
	reader := bufio.NewReader(stdin)

	for {
		if ctx.Err() != nil {
			return nil
		}

		model, ok := selectStep(stdin, reader, stdout, "Model", models, defaultModelIndex)
		if !ok {
			return nil
		}

		prompt, ok := inputStep(reader, stdout, "Дополнить user prompt и отправить")
		if !ok {
			return nil
		}

		var content string
		var ex *kimi.Exchange
		var err error
		runWithSpinner(stdout, func() {
			content, ex, err = client.Complete(ctx, model, []kimi.Message{{Role: "user", Content: prompt}})
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

		path, err := exchangelog.Write(exchangelog.Dir, ex, masker)
		if err != nil {
			fmt.Fprintln(stdout, "не удалось записать лог:", masker.Mask(err.Error()))
			continue
		}
		fmt.Fprintln(stdout, "лог:", path)
	}
}
