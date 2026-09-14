package interactive

import (
	"fmt"
	"io"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
)

// factsInstruction is appended to a turn's outgoing request when
// CONTEXT_STRATEGY=STICKY_FACTS is active — CLAUDE.md: "к каждому запросу
// в LLM необходимо добавить промпт: вычлени из user prompt факты
// (ключ-значение) и добавь их к твоему ответу в отдельной секции фактов."
const factsInstruction = "Вычлени из user prompt факты (ключ-значение) и добавь их к своему ответу в отдельной секции, начинающейся строкой \"Факты:\" — каждый факт на отдельной строке в формате \"ключ: значение\"."

// factsSectionHeader marks the start of the facts section in a model
// reply, per factsInstruction above (matched case-insensitively).
const factsSectionHeader = "факты:"

// extractFacts splits a model's reply into the part before its facts
// section (if any) and the key/value facts found in that section. A reply
// with no recognizable facts section returns it unchanged and no facts.
func extractFacts(content string) (body string, facts []dialogstore.Fact) {
	lines := strings.Split(content, "\n")
	headerIdx := -1
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), factsSectionHeader) {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		return content, nil
	}

	body = strings.TrimRight(strings.Join(lines[:headerIdx], "\n"), "\n ")
	for _, line := range lines[headerIdx+1:] {
		key, value, ok := strings.Cut(line, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			continue
		}
		facts = append(facts, dialogstore.Fact{Key: key, Value: value})
	}
	return body, facts
}

// saveFacts persists every extracted fact for dialogID.
func saveFacts(store *dialogstore.Store, dialogID string, facts []dialogstore.Fact, stdout io.Writer) {
	for _, f := range facts {
		if err := store.SetFact(dialogID, f.Key, f.Value); err != nil {
			fmt.Fprintln(stdout, "не удалось сохранить факт диалога:", err)
		}
	}
}

// factsContext builds a system-prompt blurb listing dialogID's saved
// facts, or "" if it has none — the STICKY_FACTS counterpart to
// appendDialogContext's history digest.
func factsContext(store *dialogstore.Store, dialogID string, stdout io.Writer) string {
	if store == nil {
		return ""
	}
	facts, err := store.Facts(dialogID)
	if err != nil {
		fmt.Fprintln(stdout, "не удалось загрузить факты диалога:", err)
		return ""
	}
	if len(facts) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Известные факты о диалоге:\n")
	for _, f := range facts {
		fmt.Fprintf(&b, "%s: %s\n", f.Key, f.Value)
	}
	return strings.TrimRight(b.String(), "\n")
}
