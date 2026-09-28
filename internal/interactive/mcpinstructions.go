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

// mcpInstructionRe matches CLAUDE.md's "## Работа с MCP" inline syntax:
// "MCP:calc->tools" or "MCP:calc->add(4, 9)".
var mcpInstructionRe = regexp.MustCompile(`MCP:([A-Za-z0-9_-]+)->(tools|[A-Za-z0-9_]+\([^)]*\))`)

var toolCallRe = regexp.MustCompile(`^([A-Za-z0-9_]+)\(([^)]*)\)$`)

// expandMCPInstructions replaces every "MCP:<server>-><command>"
// instruction embedded in content with the result of actually running it
// locally against a connected MCP server — CLAUDE.md: "Если в User prompt
// есть инструкции для MCP - необходимо выполнить их локально и заменить в
// User prompt эти инструкции результатом обращения к mcp". Content with
// no such instruction is returned unchanged, untouched by the regexp at
// all.
func expandMCPInstructions(ctx context.Context, content string, registry mcpInline, masker *secretmask.Masker, stdout io.Writer) string {
	if !strings.Contains(content, "MCP:") {
		return content
	}
	return mcpInstructionRe.ReplaceAllStringFunc(content, func(match string) string {
		m := mcpInstructionRe.FindStringSubmatch(match)
		server, command := m[1], m[2]
		if command == "tools" {
			return mcpExpandToolsList(registry, server, stdout)
		}
		return mcpExpandToolCall(ctx, registry, server, command, masker, stdout)
	})
}

// mcpLabel is the yellow "MCP:" prefix used to echo each inline expansion
// to the console, so the operator sees what was substituted and why.
func mcpLabel(stdout io.Writer) string {
	return colorize(stdout, ansiYellow, "MCP:")
}

func mcpExpandToolsList(registry mcpInline, server string, stdout io.Writer) string {
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

func mcpExpandToolCall(ctx context.Context, registry mcpInline, server, callText string, masker *secretmask.Masker, stdout io.Writer) string {
	label := mcpLabel(stdout)
	echo := server + "->" + callText

	m := toolCallRe.FindStringSubmatch(callText)
	if m == nil {
		msg := fmt.Sprintf("[MCP ошибка: не удалось разобрать %q]", callText)
		fmt.Fprintln(stdout, label, echo, "-> ошибка:", msg)
		return msg
	}
	toolName, argsText := m[1], m[2]

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
		raws = strings.Split(argsText, ",")
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
