package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/fedorvasiliev/aiac9/internal/mcp"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// mcpInline is the subset of *mcp.Registry the inline "MCP:..." expansion
// below needs — narrowed to an interface so tests can supply a fake.
type mcpInline interface {
	ToolsFor(server string) []mcp.RegisteredTool
	CallOn(ctx context.Context, server, tool string, arguments json.RawMessage) (text string, isError bool, err error)
}

// mcpInstrPrefix marks the start of an inline instruction — CLAUDE.md's
// "## Работа с MCP" inline syntax: "MCP:calc->tools" or
// "MCP:calc->add(4, 9)".
const mcpInstrPrefix = "MCP:"

// serverArrowRe matches a server name followed by "->" in one pattern
// (rather than two separate steps) so the regex engine's own backtracking
// resolves the ambiguity between a server name allowed to contain "-" and
// the "->" delimiter that follows it: greedily matching the name alone
// would swallow the "-" belonging to "->", leaving only ">" to match
// against the literal "->" and failing; requiring "->" as part of the
// same match forces the engine to give that character back.
var serverArrowRe = regexp.MustCompile(`^([A-Za-z0-9_-]+)->`)
var toolNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+`)

// expandMCPInstructions replaces every "MCP:<server>-><command>"
// instruction embedded in content with the result of actually running it
// locally against a connected MCP server — CLAUDE.md: "Если в User prompt
// есть инструкции для MCP - необходимо выполнить их локально и заменить в
// User prompt эти инструкции результатом обращения к mcp". Instructions
// may nest — "Инструкции вызовов MCP могут быть вложенными" — e.g.
// "MCP:calc->add(MCP:calc->multiply(2, 3), 9)": a call's own argument
// text is expanded (innermost first, via recursion) before the call
// itself runs, so the outer call sees already-resolved arguments. Content
// with no "MCP:" at all is returned unchanged, without even attempting to
// parse anything.
//
// This can't be done with a single regexp: matching a call's argument
// list requires counting balanced parentheses, which regular expressions
// (Go's RE2 included) cannot express for arbitrary nesting depth — so
// this is a small hand-written scanner instead.
//
// mcpOnly reports whether content, once every instruction is stripped
// out, had no other non-blank text at all — CLAUDE.md: "Если в User
// prompt нет никаких инструкций (непустого текста) кроме MCP - выполняем
// их в полном объеме, но запрос в llm не отправляем". It is false for
// content with no recognized instruction in it at all (nothing was
// "выполнено локально"), even if content itself is blank.
func expandMCPInstructions(ctx context.Context, content string, registry mcpInline, masker *secretmask.Masker, stdout io.Writer) (result string, mcpOnly bool) {
	if !strings.Contains(content, mcpInstrPrefix) {
		return content, false
	}

	var b strings.Builder
	i := 0
	foundAny := false
	onlyMCP := true
	for {
		idx := strings.Index(content[i:], mcpInstrPrefix)
		if idx < 0 {
			tail := content[i:]
			b.WriteString(tail)
			if strings.TrimSpace(tail) != "" {
				onlyMCP = false
			}
			break
		}
		start := i + idx
		gap := content[i:start]
		b.WriteString(gap)
		if strings.TrimSpace(gap) != "" {
			onlyMCP = false
		}

		end, res, ok := parseAndRunMCPInstruction(ctx, content, start, registry, masker, stdout)
		if !ok {
			// Not a well-formed instruction (e.g. a bare "MCP:" with no
			// valid server->command shape) — emit the prefix literally and
			// resume searching right after it, so this can't loop forever
			// on the same spot. Leftover literal "MCP:" text counts as
			// "other text", not something handled locally.
			b.WriteString(mcpInstrPrefix)
			i = start + len(mcpInstrPrefix)
			onlyMCP = false
			continue
		}
		b.WriteString(res)
		foundAny = true
		i = end
	}
	return b.String(), foundAny && onlyMCP
}

// parseAndRunMCPInstruction parses the single instruction starting at
// content[start:] (which begins with mcpInstrPrefix) and, if well-formed,
// runs it. end is the index right after the instruction's last consumed
// character; ok is false if content[start:] doesn't actually form a valid
// instruction, in which case end/result are meaningless.
func parseAndRunMCPInstruction(ctx context.Context, content string, start int, registry mcpInline, masker *secretmask.Masker, stdout io.Writer) (end int, result string, ok bool) {
	rest := content[start+len(mcpInstrPrefix):]

	m := serverArrowRe.FindStringSubmatch(rest)
	if m == nil {
		return 0, "", false
	}
	server := m[1]
	pos := len(m[0])

	name := toolNameRe.FindString(rest[pos:])
	if name == "" {
		return 0, "", false
	}
	afterName := pos + len(name)

	if afterName < len(rest) && rest[afterName] == '(' {
		closeIdx, balanced := scanBalancedParens(rest, afterName)
		if !balanced {
			return 0, "", false
		}
		rawArgs := rest[afterName+1 : closeIdx]
		// Nested instructions resolve first — e.g. in
		// "add(MCP:calc->multiply(2, 3), 9)" the multiply call must run
		// (and its result splice in) before add's own arguments are split
		// and coerced. Whether the argument text itself was "MCP only"
		// doesn't matter here — only the outermost call's content decides
		// CLAUDE.md's "запрос в llm не отправляем".
		resolvedArgs, _ := expandMCPInstructions(ctx, rawArgs, registry, masker, stdout)
		out := mcpRunToolCall(ctx, registry, server, name, resolvedArgs, masker, stdout)
		return start + len(mcpInstrPrefix) + closeIdx + 1, out, true
	}

	if name == "tools" {
		out := mcpListTools(registry, server, stdout)
		return start + len(mcpInstrPrefix) + afterName, out, true
	}

	return 0, "", false
}

// scanBalancedParens finds the index (into s) of the ')' that closes the
// '(' at s[openIdx], counting nested parentheses so it doesn't stop at
// the first ')' belonging to a nested call's own argument list.
func scanBalancedParens(s string, openIdx int) (closeIdx int, ok bool) {
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

// mcpLabel is the yellow "MCP:" prefix used to echo each inline expansion
// to the console, so the operator sees what was substituted and why.
func mcpLabel(stdout io.Writer) string {
	return colorize(stdout, ansiYellow, "MCP:")
}

func mcpListTools(registry mcpInline, server string, stdout io.Writer) string {
	tools := registry.ToolsFor(server)
	if len(tools) == 0 {
		msg := fmt.Sprintf("[MCP ошибка: сервер %q не подключён или не имеет инструментов]", server)
		fmt.Fprintln(stdout, mcpLabel(stdout), server+"->tools", "-> ошибка:", msg)
		return msg
	}

	prefix := server + "__"
	parts := make([]string, len(tools))
	for i, t := range tools {
		name := strings.TrimPrefix(t.Name, prefix)
		if t.Description != "" {
			parts[i] = name + ": " + t.Description
		} else {
			parts[i] = name
		}
	}
	result := strings.Join(parts, "; ")
	fmt.Fprintln(stdout, mcpLabel(stdout), server+"->tools", "->", result)
	return result
}

// mcpRunToolCall runs one already-parsed call: server/toolName plus its
// argument text, with any nested instructions inside argsText already
// resolved by the caller.
func mcpRunToolCall(ctx context.Context, registry mcpInline, server, toolName, argsText string, masker *secretmask.Masker, stdout io.Writer) string {
	label := mcpLabel(stdout)
	echo := fmt.Sprintf("%s->%s(%s)", server, toolName, argsText)

	prefix := server + "__"
	var schema json.RawMessage
	found := false
	for _, t := range registry.ToolsFor(server) {
		if strings.TrimPrefix(t.Name, prefix) == toolName {
			schema, found = t.InputSchema, true
			break
		}
	}
	if !found {
		msg := fmt.Sprintf("[MCP ошибка: инструмент %q не найден на сервере %q]", toolName, server)
		fmt.Fprintln(stdout, label, echo, "-> ошибка:", msg)
		return msg
	}

	argsJSON, err := buildToolArguments(schema, argsText)
	if err != nil {
		msg := fmt.Sprintf("[MCP ошибка: %s]", err)
		fmt.Fprintln(stdout, label, echo, "-> ошибка:", msg)
		return msg
	}

	text, isError, callErr := registry.CallOn(ctx, server, toolName, argsJSON)
	switch {
	case callErr != nil:
		msg := fmt.Sprintf("[MCP ошибка: %s]", masker.Mask(callErr.Error()))
		fmt.Fprintln(stdout, label, echo, "-> ошибка:", msg)
		return msg
	case isError:
		msg := fmt.Sprintf("[MCP ошибка инструмента: %s]", masker.Mask(text))
		fmt.Fprintln(stdout, label, echo, "->", msg)
		return msg
	default:
		fmt.Fprintln(stdout, label, echo, "->", masker.Mask(text))
		return text
	}
}

// buildToolArguments turns "4, 9" into {"a":4,"b":9} using schema's
// "properties" — in the order they appear in the raw JSON Schema, since a
// decoded map loses that order and positional arguments (CLAUDE.md's
// "MCP:calc->add(4, 9)") need it to know which number is "a" and which is
// "b". Each argument is coerced to its property's declared JSON Schema
// "type" (number/integer/boolean/string) rather than always sent as a
// string, since these tools expect real JSON numbers.
func buildToolArguments(schema json.RawMessage, argsText string) (json.RawMessage, error) {
	names, types := schemaProperties(schema)
	argsText = strings.TrimSpace(argsText)

	var raws []string
	if argsText != "" {
		raws = splitTopLevelArgs(argsText)
	}
	if len(raws) > len(names) {
		return nil, fmt.Errorf("передано %d аргументов, инструмент принимает %d", len(raws), len(names))
	}

	obj := make(map[string]any, len(raws))
	for i, raw := range raws {
		name := names[i]
		val, err := coerceArgument(strings.TrimSpace(raw), types[name])
		if err != nil {
			return nil, fmt.Errorf("аргумент %q: %w", name, err)
		}
		obj[name] = val
	}
	return json.Marshal(obj)
}

// splitTopLevelArgs splits argsText on commas, except commas inside a
// matching pair of single or double quotes — needed because a quoted
// string argument (CLAUDE.md's "MCP:notes->add_note('{result}')", where
// {result} is a previous LLM answer) can easily contain a literal comma
// of its own, which a naive strings.Split(argsText, ",") would wrongly
// treat as another argument.
func splitTopLevelArgs(argsText string) []string {
	var parts []string
	var cur strings.Builder
	var inQuote byte
	for i := 0; i < len(argsText); i++ {
		c := argsText[i]
		switch {
		case inQuote != 0:
			cur.WriteByte(c)
			if c == inQuote {
				inQuote = 0
			}
		case c == '\'' || c == '"':
			inQuote = c
			cur.WriteByte(c)
		case c == ',':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

// coerceArgument converts one positional argument's raw text into the Go
// value that will marshal as the JSON type propType calls for. Unknown or
// empty types, and "string", are kept as text (with a matching pair of
// surrounding quotes stripped, if present).
func coerceArgument(raw, propType string) (any, error) {
	switch propType {
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("ожидалось число, получено %q", raw)
		}
		return f, nil
	case "integer":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ожидалось целое число, получено %q", raw)
		}
		return n, nil
	case "boolean":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("ожидалось true/false, получено %q", raw)
		}
		return b, nil
	default:
		if len(raw) >= 2 {
			if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
				return raw[1 : len(raw)-1], nil
			}
		}
		return raw, nil
	}
}

// schemaProperties reads a JSON Schema object's "properties" member,
// returning its property names in the order they're declared in the raw
// JSON (needed for positional arguments — a decoded map loses that order)
// alongside each one's declared "type".
func schemaProperties(schema json.RawMessage) (order []string, types map[string]string) {
	types = map[string]string{}
	var wrapper struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &wrapper); err != nil || len(wrapper.Properties) == 0 {
		return nil, types
	}

	var typed map[string]struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(wrapper.Properties, &typed); err == nil {
		for name, p := range typed {
			types[name] = p.Type
		}
	}

	order = orderedObjectKeys(wrapper.Properties)
	return order, types
}

// orderedObjectKeys walks a JSON object's raw bytes and returns its
// immediate keys in the order they're written — encoding/json has no way
// to preserve this via a normal Unmarshal into a map.
func orderedObjectKeys(obj json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(obj))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil
	}

	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, ok := keyTok.(string)
		if !ok {
			return keys
		}
		keys = append(keys, key)

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}
