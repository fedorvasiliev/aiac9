package interactive

import (
	"context"
	"fmt"
	"io"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/exchangelog"
	"github.com/fedorvasiliev/aiac9/internal/llm"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// validationSystemPrompt frames the extra request CLAUDE.md's validation
// phase makes: "необходимо отправить еще один запрос (валидационный) в
// llm чтобы убедиться что текущие invariants были учтены в ответе".
const validationSystemPrompt = "Ты — валидатор ответов LLM-агента. Проверь, соблюдены ли в ответе ниже все перечисленные инварианты агента. Ответь коротко: \"OK\", если все инварианты соблюдены; иначе перечисли, что именно нарушено."

// validateInvariants runs the validation phase's LLM check, reusing the
// same client/model the turn's own request used. A nil store, or an agent
// with no saved invariants (commands.go's /invariants), has nothing to
// check against, so this is then a no-op — there being no invariants is
// not itself a violation. Errors (including the request itself failing)
// are reported but never fail the turn: the primary answer was already
// obtained successfully in the execution phase, and a validation hiccup
// shouldn't discard it.
func validateInvariants(ctx context.Context, store *dialogstore.Store, client *llm.Client, model, content string, masker *secretmask.Masker, stdout io.Writer) {
	if store == nil {
		return
	}
	_, invariants, err := store.Agent()
	if err != nil {
		fmt.Fprintln(stdout, "не удалось загрузить инварианты агента:", masker.Mask(err.Error()))
		return
	}
	if invariants == "" {
		fmt.Fprintln(stdout, "инварианты не заданы — проверка соответствия пропущена")
		return
	}

	messages := []llm.Message{
		{Role: "system", Content: validationSystemPrompt + "\n\nИнварианты:\n" + invariants},
		{Role: "user", Content: "Ответ для проверки:\n" + content},
	}

	var logPath string
	onRequest := func(reqBody []byte) {
		path, logErr := exchangelog.Start(exchangelog.Dir, model, reqBody, masker)
		if logErr != nil {
			fmt.Fprintln(stdout, "не удалось записать лог валидации:", masker.Mask(logErr.Error()))
			return
		}
		logPath = path
	}

	verdict, ex, err := client.Complete(ctx, model, messages, llm.Options{}, onRequest)
	if err != nil {
		fmt.Fprintln(stdout, "не удалось выполнить валидацию инвариантов:", masker.Mask(err.Error()))
		return
	}

	fmt.Fprintln(stdout, colorize(stdout, ansiYellow, "Валидация инвариантов:"))
	fmt.Fprintln(stdout, masker.Mask(verdict))

	if logPath != "" {
		if err := exchangelog.Finish(logPath, ex.StatusCode, ex.ResponseBody, ex.Duration, masker); err != nil {
			fmt.Fprintln(stdout, "не удалось дописать лог валидации:", masker.Mask(err.Error()))
		}
	}
}
