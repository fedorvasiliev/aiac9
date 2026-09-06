package interactive

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/llm"
	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

// assembleRequest builds the messages, effective model and per-request
// options for one Step T submission, per CLAUDE.md:
//   - tmpl (nil if "no prompt" was chosen at Step A) supplies any System/User
//     prompt messages, and may override fallbackModel.
//   - extra is Step T's typed text. A non-empty extra is appended to every
//     user-prompt message; if there were none, it becomes the sole user
//     message (the plain, template-less case).
//   - a template's "Words limit" is turned into a sentence appended to every
//     user-prompt message, same as extra.
func assembleRequest(tmpl *promptfile.Prompt, fallbackModel, extra string) (messages []llm.Message, model string, opts llm.Options) {
	model = fallbackModel

	var systemPrompts, userPrompts []string
	if tmpl != nil {
		if v, ok := tmpl.Value(promptfile.HeaderModel); ok {
			model = v
		}
		systemPrompts = tmpl.Values(promptfile.HeaderSystemPrompt)
		userPrompts = tmpl.Values(promptfile.HeaderUserPrompt)
		opts.ResponseFormat, _ = tmpl.Value(promptfile.HeaderResponseFormat)
		opts.Stop, _ = tmpl.Value(promptfile.HeaderStop)
		if v, ok := tmpl.Value(promptfile.HeaderTemperature); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				opts.Temperature = &f
			}
		}
	}

	extra = strings.TrimSpace(extra)
	if extra != "" {
		if len(userPrompts) == 0 {
			userPrompts = []string{extra}
		} else {
			for i := range userPrompts {
				userPrompts[i] = userPrompts[i] + "\n" + extra
			}
		}
	}

	if tmpl != nil {
		if limit, ok := tmpl.Value(promptfile.HeaderWordsLimit); ok {
			phrase := fmt.Sprintf("Максимальное количество слов в ответе должно быть %s.", limit)
			for i := range userPrompts {
				userPrompts[i] = userPrompts[i] + "\n" + phrase
			}
		}
	}

	for _, sp := range systemPrompts {
		messages = append(messages, llm.Message{Role: "system", Content: sp})
	}
	for _, up := range userPrompts {
		messages = append(messages, llm.Message{Role: "user", Content: up})
	}
	return messages, model, opts
}
