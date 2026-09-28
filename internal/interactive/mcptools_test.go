package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/llm"
	"github.com/fedorvasiliev/aiac9/internal/mcp"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

func TestMcpTools_EmptyYieldsNil(t *testing.T) {
	if got := mcpTools(nil); got != nil {
		t.Fatalf("mcpTools(nil) = %+v, want nil", got)
	}
}

func TestMcpTools_ConvertsRegisteredToolsToOpenAIShape(t *testing.T) {
	got := mcpTools([]mcp.RegisteredTool{
		{Name: "calc__add", Description: "adds two numbers", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	if len(got) != 1 {
		t.Fatalf("got %d tools, want 1", len(got))
	}
	tool := got[0]
	if tool.Type != "function" || tool.Function.Name != "calc__add" || tool.Function.Description != "adds two numbers" {
		t.Fatalf("tool = %+v, unexpected", tool)
	}
	if string(tool.Function.Parameters) != `{"type":"object"}` {
		t.Fatalf("Parameters = %s, want the schema passed through verbatim", tool.Function.Parameters)
	}
}

// fakeCaller is a toolCaller test double: dispatches by exact tool name to
// a scripted response, recording every call it received.
type fakeCaller struct {
	responses map[string]struct {
		text    string
		isError bool
		err     error
	}
	calls []string
}

func (f *fakeCaller) Call(ctx context.Context, name string, arguments json.RawMessage) (string, bool, error) {
	f.calls = append(f.calls, name+":"+string(arguments))
	r, ok := f.responses[name]
	if !ok {
		return "", true, fmt.Errorf("fakeCaller: no script for %q", name)
	}
	return r.text, r.isError, r.err
}

func TestCompleteWithTools_NoToolCallsReturnsImmediately(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	client := llm.NewClient(server.URL, "key")
	var out bytes.Buffer
	content, ex, err := completeWithTools(context.Background(), client, "kimi-k3", []llm.Message{{Role: "user", Content: "hi"}}, llm.Options{}, nil, secretmask.New(), &out)
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if content != "hello" {
		t.Fatalf("content = %q, want hello", content)
	}
	if ex == nil {
		t.Fatal("ex = nil")
	}
}

func TestCompleteWithTools_ExecutesToolCallAndSendsResultBack(t *testing.T) {
	t.Chdir(t.TempDir())
	var round int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&round, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			var req struct {
				Messages []llm.Message `json:"messages"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if len(req.Messages) != 1 {
				t.Fatalf("round 1: server saw %d messages, want 1", len(req.Messages))
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{
				"role":"assistant","content":"",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"calc__add","arguments":"{\"a\":2,\"b\":3}"}}]
			}}]}`))
			return
		}

		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Messages) != 3 {
			t.Fatalf("round 2: server saw %d messages, want 3 (user, assistant tool_call, tool result)", len(req.Messages))
		}
		last := req.Messages[2]
		if last.Role != "tool" || last.ToolCallID != "call_1" || last.Content != "5" {
			t.Fatalf("round 2: last message = %+v, want the tool result", last)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"2+3 is 5"}}]}`))
	}))
	defer server.Close()

	client := llm.NewClient(server.URL, "key")
	caller := &fakeCaller{responses: map[string]struct {
		text    string
		isError bool
		err     error
	}{
		"calc__add": {text: "5"},
	}}
	var out bytes.Buffer
	content, _, err := completeWithTools(context.Background(), client, "kimi-k3", []llm.Message{{Role: "user", Content: "2+3?"}}, llm.Options{}, caller, secretmask.New(), &out)
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if content != "2+3 is 5" {
		t.Fatalf("content = %q, want the final answer", content)
	}
	if len(caller.calls) != 1 || caller.calls[0] != `calc__add:{"a":2,"b":3}` {
		t.Fatalf("caller.calls = %+v, want one call with the model's arguments", caller.calls)
	}
	if !strings.Contains(out.String(), "calc__add") {
		t.Fatalf("output = %q, want it to mention the tool call", out.String())
	}
}

func TestCompleteWithTools_NilCallerWithToolCallsIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{
			"role":"assistant","content":"",
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"calc__add","arguments":"{}"}}]
		}}]}`))
	}))
	defer server.Close()

	client := llm.NewClient(server.URL, "key")
	var out bytes.Buffer
	_, _, err := completeWithTools(context.Background(), client, "kimi-k3", []llm.Message{{Role: "user", Content: "2+3?"}}, llm.Options{}, nil, secretmask.New(), &out)
	if err == nil {
		t.Fatal("completeWithTools returned nil error for tool_calls with no caller configured")
	}
}

func TestCompleteWithTools_ToolLevelErrorIsFedBackAsToolResult(t *testing.T) {
	t.Chdir(t.TempDir())
	var round int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&round, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{
				"role":"assistant","content":"",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"calc__divide","arguments":"{\"a\":1,\"b\":0}"}}]
			}}]}`))
			return
		}
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		last := req.Messages[len(req.Messages)-1]
		if last.Content != "division by zero" {
			t.Fatalf("tool-error content = %q, want it fed back as the tool result", last.Content)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"can't divide by zero"}}]}`))
	}))
	defer server.Close()

	client := llm.NewClient(server.URL, "key")
	caller := &fakeCaller{responses: map[string]struct {
		text    string
		isError bool
		err     error
	}{
		"calc__divide": {text: "division by zero", isError: true},
	}}
	var out bytes.Buffer
	content, _, err := completeWithTools(context.Background(), client, "kimi-k3", []llm.Message{{Role: "user", Content: "1/0?"}}, llm.Options{}, caller, secretmask.New(), &out)
	if err != nil {
		t.Fatalf("completeWithTools: %v", err)
	}
	if content != "can't divide by zero" {
		t.Fatalf("content = %q, want the model's follow-up", content)
	}
}

func TestCompleteWithTools_TooManyRoundsIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{
			"role":"assistant","content":"",
			"tool_calls":[{"id":"call_x","type":"function","function":{"name":"loop","arguments":"{}"}}]
		}}]}`))
	}))
	defer server.Close()

	client := llm.NewClient(server.URL, "key")
	caller := &fakeCaller{responses: map[string]struct {
		text    string
		isError bool
		err     error
	}{
		"loop": {text: "again"},
	}}
	var out bytes.Buffer
	_, _, err := completeWithTools(context.Background(), client, "kimi-k3", []llm.Message{{Role: "user", Content: "go"}}, llm.Options{}, caller, secretmask.New(), &out)
	if err == nil {
		t.Fatal("completeWithTools returned nil error for a model stuck calling tools forever")
	}
}
