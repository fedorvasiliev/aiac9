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

// callResultMap is the exact anonymous struct shape fakeInline.callResult
// uses — named here just to keep the nesting tests below shorter.
type callResultMap = map[string]struct {
	text    string
	isError bool
	err     error
}

func TestExpandMCPInstructions_NestedCallResolvesInnermostFirst(t *testing.T) {
	f := &fakeInline{
		tools: map[string][]mcp.RegisteredTool{"calc": {
			{Name: "calc__add", InputSchema: calcSchema()},
			{Name: "calc__multiply", InputSchema: calcSchema()},
		}},
		callResult: callResultMap{
			"calc__multiply": {text: "6"},
			"calc__add":      {text: "15"},
		},
	}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "Результат: MCP:calc->add(MCP:calc->multiply(2, 3), 9)", f, secretmask.New(), &out)
	if got != "Результат: 15" {
		t.Fatalf("got = %q, want the outer call's own result", got)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %+v, want exactly 2 (inner then outer)", f.calls)
	}
	if f.calls[0] != `calc__multiply:{"a":2,"b":3}` {
		t.Fatalf("calls[0] = %q, want the inner multiply to run first", f.calls[0])
	}
	if f.calls[1] != `calc__add:{"a":6,"b":9}` {
		t.Fatalf("calls[1] = %q, want the outer add to see the inner result (6)", f.calls[1])
	}
}

func TestExpandMCPInstructions_ThreeLevelsDeep(t *testing.T) {
	f := &fakeInline{
		tools: map[string][]mcp.RegisteredTool{"calc": {
			{Name: "calc__add", InputSchema: calcSchema()},
			{Name: "calc__subtract", InputSchema: calcSchema()},
			{Name: "calc__multiply", InputSchema: calcSchema()},
		}},
		callResult: callResultMap{
			"calc__multiply": {text: "6"},  // 2*3
			"calc__subtract": {text: "1"},  // 6-5
			"calc__add":      {text: "10"}, // 1+9
		},
	}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:calc->add(MCP:calc->subtract(MCP:calc->multiply(2, 3), 5), 9)", f, secretmask.New(), &out)
	if got != "10" {
		t.Fatalf("got = %q", got)
	}
	wantCalls := []string{
		`calc__multiply:{"a":2,"b":3}`,
		`calc__subtract:{"a":6,"b":5}`,
		`calc__add:{"a":1,"b":9}`,
	}
	if len(f.calls) != len(wantCalls) {
		t.Fatalf("calls = %+v, want %+v", f.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if f.calls[i] != want {
			t.Fatalf("calls[%d] = %q, want %q", i, f.calls[i], want)
		}
	}
}

func TestExpandMCPInstructions_NestedAcrossDifferentServers(t *testing.T) {
	f := &fakeInline{
		tools: map[string][]mcp.RegisteredTool{
			"calc":  {{Name: "calc__add", InputSchema: calcSchema()}},
			"notes": {{Name: "notes__count", InputSchema: []byte(`{"type":"object","properties":{}}`)}},
		},
		callResult: callResultMap{
			"notes__count": {text: "3"},
			"calc__add":    {text: "13"},
		},
	}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:calc->add(MCP:notes->count(), 10)", f, secretmask.New(), &out)
	if got != "13" {
		t.Fatalf("got = %q", got)
	}
	if len(f.calls) != 2 || f.calls[0] != "notes__count:{}" || f.calls[1] != `calc__add:{"a":3,"b":10}` {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestExpandMCPInstructions_UnbalancedParensIsLeftUnexpanded(t *testing.T) {
	f := &fakeInline{tools: map[string][]mcp.RegisteredTool{"calc": {{Name: "calc__add", InputSchema: calcSchema()}}}}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:calc->add(1, 2", f, secretmask.New(), &out)
	if got != "MCP:calc->add(1, 2" {
		t.Fatalf("got = %q, want the malformed instruction left untouched", got)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls = %+v, want none for an unbalanced call", f.calls)
	}
}

func TestExpandMCPInstructions_BareCommandWithoutParensIsNotACall(t *testing.T) {
	// "MCP:scheduler->get_summary" with no "()" doesn't match either
	// recognized shape ("tools", or "name(args)") and is left as plain
	// text — CLAUDE.md's examples always show a call with parentheses,
	// even for a zero-argument tool ("notes->list_notes()").
	f := &fakeInline{tools: map[string][]mcp.RegisteredTool{"scheduler": {{Name: "scheduler__get_summary"}}}}
	var out bytes.Buffer
	got := expandMCPInstructions(context.Background(), "MCP:scheduler->get_summary", f, secretmask.New(), &out)
	if got != "MCP:scheduler->get_summary" {
		t.Fatalf("got = %q, want it left unchanged", got)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls = %+v, want none", f.calls)
	}
}
