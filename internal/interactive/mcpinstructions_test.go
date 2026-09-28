package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/mcp"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// fakeInline is a mcpInline test double for the inline "MCP:..." expansion
// — separate from mcptools_test.go's fakeCaller, since this interface is
// shaped differently (server-qualified ToolsFor/CallOn, not the
// aggregated-name Call the tool-calling loop uses).
type fakeInline struct {
	tools map[string][]mcp.RegisteredTool // by server
	calls []string
	// callResult, if set, overrides the default "sum of numeric args"
	// behavior CallOn otherwise fakes for any tool.
	callResult map[string]struct {
		text    string
		isError bool
		err     error
	}
}

func (f *fakeInline) ToolsFor(server string) []mcp.RegisteredTool {
	return f.tools[server]
}

func (f *fakeInline) CallOn(ctx context.Context, server, tool string, arguments json.RawMessage) (string, bool, error) {
	f.calls = append(f.calls, server+"__"+tool+":"+string(arguments))
	if r, ok := f.callResult[server+"__"+tool]; ok {
		return r.text, r.isError, r.err
	}
	return string(arguments), false, nil
}

func calcSchema() []byte {
	return []byte(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`)
}

func TestExpandMCPInstructions_NoInstructionLeavesContentUnchanged(t *testing.T) {
	f := &fakeInline{}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "just a plain prompt", f, secretmask.New(), &out)
	if got != "just a plain prompt" {
		t.Fatalf("got = %q, want unchanged", got)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls = %+v, want none", f.calls)
	}
}

func TestExpandMCPInstructions_ToolsListsNamesAndDescriptions(t *testing.T) {
	f := &fakeInline{tools: map[string][]mcp.RegisteredTool{
		"calc": {
			{Name: "calc__add", Description: "adds"},
			{Name: "calc__subtract", Description: "subtracts"},
		},
	}}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "Подключись к mcp calc и выведи MCP:calc->tools", f, secretmask.New(), &out)
	if got != "Подключись к mcp calc и выведи add: adds; subtract: subtracts" {
		t.Fatalf("got = %q", got)
	}
}

func TestExpandMCPInstructions_ToolsUnknownServerIsAnInlineError(t *testing.T) {
	f := &fakeInline{}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:ghost->tools", f, secretmask.New(), &out)
	if !strings.Contains(got, "MCP ошибка") || !strings.Contains(got, "ghost") {
		t.Fatalf("got = %q, want an inline error mentioning the unknown server", got)
	}
}

func TestExpandMCPInstructions_CallWithPositionalNumericArgs(t *testing.T) {
	f := &fakeInline{
		tools: map[string][]mcp.RegisteredTool{"calc": {{Name: "calc__add", InputSchema: calcSchema()}}},
		callResult: map[string]struct {
			text    string
			isError bool
			err     error
		}{"calc__add": {text: "13"}},
	}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "Посчитай MCP:calc->add(4, 9) и объясни", f, secretmask.New(), &out)
	if got != "Посчитай 13 и объясни" {
		t.Fatalf("got = %q", got)
	}
	if len(f.calls) != 1 || f.calls[0] != `calc__add:{"a":4,"b":9}` {
		t.Fatalf("calls = %+v, want one call with a=4 b=9 as JSON numbers", f.calls)
	}
}

func TestExpandMCPInstructions_CallUnknownToolIsAnInlineError(t *testing.T) {
	f := &fakeInline{tools: map[string][]mcp.RegisteredTool{"calc": {{Name: "calc__add", InputSchema: calcSchema()}}}}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:calc->nope(1)", f, secretmask.New(), &out)
	if !strings.Contains(got, "MCP ошибка") || !strings.Contains(got, "nope") {
		t.Fatalf("got = %q, want an inline error mentioning the unknown tool", got)
	}
}

func TestExpandMCPInstructions_ToolLevelErrorIsInlinedToo(t *testing.T) {
	f := &fakeInline{
		tools: map[string][]mcp.RegisteredTool{"calc": {{Name: "calc__divide", InputSchema: calcSchema()}}},
		callResult: map[string]struct {
			text    string
			isError bool
			err     error
		}{"calc__divide": {text: "division by zero", isError: true}},
	}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:calc->divide(1, 0)", f, secretmask.New(), &out)
	if !strings.Contains(got, "MCP ошибка") || !strings.Contains(got, "division by zero") {
		t.Fatalf("got = %q, want the tool's error text inlined", got)
	}
}

func TestExpandMCPInstructions_NoArgsToolCall(t *testing.T) {
	f := &fakeInline{
		tools: map[string][]mcp.RegisteredTool{"notes": {{Name: "notes__list_notes", InputSchema: []byte(`{"type":"object","properties":{}}`)}}},
		callResult: map[string]struct {
			text    string
			isError bool
			err     error
		}{"notes__list_notes": {text: "(empty)"}},
	}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:notes->list_notes()", f, secretmask.New(), &out)
	if got != "(empty)" {
		t.Fatalf("got = %q", got)
	}
}

func TestBuildToolArguments_TooManyArgumentsIsAnError(t *testing.T) {
	if _, err := buildToolArguments(calcSchema(), "1, 2, 3"); err == nil {
		t.Fatal("buildToolArguments returned nil error for too many arguments")
	}
}

func TestBuildToolArguments_StringArgumentStripsQuotes(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"text":{"type":"string"}}}`)
	got, err := buildToolArguments(schema, `"hello world"`)
	if err != nil {
		t.Fatalf("buildToolArguments: %v", err)
	}
	var obj map[string]any
	json.Unmarshal(got, &obj)
	if obj["text"] != "hello world" {
		t.Fatalf("obj = %+v, want text=\"hello world\"", obj)
	}
}

func TestOrderedObjectKeys_PreservesDeclarationOrder(t *testing.T) {
	got := orderedObjectKeys([]byte(`{"z":{"type":"number"},"a":{"type":"string"}}`))
	if len(got) != 2 || got[0] != "z" || got[1] != "a" {
		t.Fatalf("got = %+v, want [z a] (wire order, not sorted)", got)
	}
}
