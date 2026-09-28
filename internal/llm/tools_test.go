package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComplete_SendsTools(t *testing.T) {
	var gotTools []Tool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotTools = req.Tools
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret-key")
	tool := Tool{Type: "function", Function: ToolFunction{
		Name:        "calc__add",
		Description: "adds two numbers",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"}}}`),
	}}

	_, _, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "2+2"}}, Options{Tools: []Tool{tool}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(gotTools) != 1 || gotTools[0].Function.Name != "calc__add" {
		t.Fatalf("gotTools = %+v, want one tool named calc__add", gotTools)
	}
}

func TestComplete_NoToolsOmitsFieldEntirely(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret-key")
	if _, _, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}}, Options{}, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.Contains(string(gotBody), `"tools"`) {
		t.Fatalf("request body contains \"tools\" with no tools configured: %s", gotBody)
	}
}

func TestComplete_ParsesToolCallsIntoExchangeMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{
			"role":"assistant",
			"content":"",
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"calc__add","arguments":"{\"a\":2,\"b\":3}"}}]
		}}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret-key")
	content, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "2+3"}}, Options{}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if content != "" {
		t.Fatalf("content = %q, want empty for a tool-call-only reply", content)
	}
	if len(ex.Message.ToolCalls) != 1 {
		t.Fatalf("ex.Message.ToolCalls = %+v, want 1 entry", ex.Message.ToolCalls)
	}
	call := ex.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "calc__add" || call.Function.Arguments != `{"a":2,"b":3}` {
		t.Fatalf("call = %+v, unexpected", call)
	}
}

func TestMessage_ToolResultMarshalsExpectedFields(t *testing.T) {
	msg := Message{Role: "tool", Content: "5", ToolCallID: "call_1"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["role"] != "tool" || got["content"] != "5" || got["tool_call_id"] != "call_1" {
		t.Fatalf("got = %+v", got)
	}
	if _, ok := got["tool_calls"]; ok {
		t.Fatalf("got = %+v, want no tool_calls field on a tool-result message", got)
	}
	if _, ok := got["name"]; ok {
		t.Fatalf("got = %+v, want no name field when unset", got)
	}
}
