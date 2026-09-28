package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newRegistryForTest builds a Registry directly from in-process fake
// clients (white-box: same package), bypassing Connect's real subprocess
// launching — Connect itself is a thin, hard-to-fake-safely wrapper
// around Start+ListTools already exercised by client_test.go.
func newRegistryForTest(t *testing.T, servers map[string]handler) (*Registry, func()) {
	t.Helper()
	r := &Registry{clients: make(map[string]*Client), owner: make(map[string]ownedTool)}

	var cleanups []func()
	for name, h := range servers {
		c, cleanup := newTestClient(t, echoInitializeHandler(h))
		cleanups = append(cleanups, cleanup)
		if err := c.initialize(context.Background()); err != nil {
			t.Fatalf("initialize %s: %v", name, err)
		}
		tools, err := c.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools %s: %v", name, err)
		}
		r.clients[name] = c
		for _, tool := range tools {
			exposed := name + "__" + tool.Name
			r.tools = append(r.tools, RegisteredTool{Name: exposed, Description: tool.Description, InputSchema: tool.InputSchema})
			r.owner[exposed] = ownedTool{server: name, name: tool.Name}
		}
	}

	return r, func() {
		for _, c := range cleanups {
			c()
		}
	}
}

func TestRegistry_NamespacesToolsPerServer(t *testing.T) {
	r, cleanup := newRegistryForTest(t, map[string]handler{
		"calc": func(method string, params json.RawMessage) (any, *rpcError) {
			if method == "tools/list" {
				return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
		"notes": func(method string, params json.RawMessage) (any, *rpcError) {
			if method == "tools/list" {
				return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil // same tool name, different server
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
	})
	defer cleanup()

	got := map[string]bool{}
	for _, tool := range r.Tools() {
		got[tool.Name] = true
	}
	if !got["calc__add"] || !got["notes__add"] {
		t.Fatalf("Tools() = %+v, want both calc__add and notes__add", r.Tools())
	}
}

func TestRegistry_CallDispatchesToOwningServer(t *testing.T) {
	r, cleanup := newRegistryForTest(t, map[string]handler{
		"calc": func(method string, params json.RawMessage) (any, *rpcError) {
			switch method {
			case "tools/list":
				return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil
			case "tools/call":
				return callToolResult{Content: []content{{Type: "text", Text: "3"}}}, nil
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
		"notes": func(method string, params json.RawMessage) (any, *rpcError) {
			switch method {
			case "tools/list":
				return listToolsResult{Tools: []Tool{{Name: "list_notes"}}}, nil
			case "tools/call":
				t.Fatal("notes server received a call meant for calc")
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
	})
	defer cleanup()

	text, isErr, err := r.Call(context.Background(), "calc__add", json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if isErr || text != "3" {
		t.Fatalf("text=%q isErr=%v, want \"3\", false", text, isErr)
	}
}

func TestRegistry_CallUnknownToolIsAnError(t *testing.T) {
	r, cleanup := newRegistryForTest(t, map[string]handler{
		"calc": func(method string, params json.RawMessage) (any, *rpcError) {
			if method == "tools/list" {
				return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
	})
	defer cleanup()

	if _, _, err := r.Call(context.Background(), "calc__nope", nil); err == nil {
		t.Fatal("Call returned nil error for an unregistered tool name")
	}
}

func TestRegistry_ToolsForFiltersByServer(t *testing.T) {
	r, cleanup := newRegistryForTest(t, map[string]handler{
		"calc": func(method string, params json.RawMessage) (any, *rpcError) {
			if method == "tools/list" {
				return listToolsResult{Tools: []Tool{{Name: "add"}, {Name: "subtract"}}}, nil
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
		"notes": func(method string, params json.RawMessage) (any, *rpcError) {
			if method == "tools/list" {
				return listToolsResult{Tools: []Tool{{Name: "list_notes"}}}, nil
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
	})
	defer cleanup()

	got := r.ToolsFor("calc")
	if len(got) != 2 || got[0].Name != "calc__add" || got[1].Name != "calc__subtract" {
		t.Fatalf("ToolsFor(calc) = %+v, want [calc__add calc__subtract]", got)
	}
	if got := r.ToolsFor("nope"); got != nil {
		t.Fatalf("ToolsFor(nope) = %+v, want nil", got)
	}
}

func TestRegistry_CallOnDispatchesByServerAndToolName(t *testing.T) {
	var gotName string
	r, cleanup := newRegistryForTest(t, map[string]handler{
		"calc": func(method string, params json.RawMessage) (any, *rpcError) {
			switch method {
			case "tools/list":
				return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil
			case "tools/call":
				var p struct {
					Name string `json:"name"`
				}
				json.Unmarshal(params, &p)
				gotName = p.Name
				return callToolResult{Content: []content{{Type: "text", Text: "13"}}}, nil
			}
			return nil, &rpcError{Code: -32601, Message: method}
		},
	})
	defer cleanup()

	text, isErr, err := r.CallOn(context.Background(), "calc", "add", json.RawMessage(`{"a":4,"b":9}`))
	if err != nil {
		t.Fatalf("CallOn: %v", err)
	}
	if isErr || text != "13" {
		t.Fatalf("text=%q isErr=%v, want \"13\", false", text, isErr)
	}
	if gotName != "add" {
		t.Fatalf("server saw tool name %q, want add", gotName)
	}
}

func TestRegistry_CallOnUnknownServerIsAnError(t *testing.T) {
	r, cleanup := newRegistryForTest(t, map[string]handler{})
	defer cleanup()

	if _, _, err := r.CallOn(context.Background(), "nope", "add", nil); err == nil {
		t.Fatal("CallOn returned nil error for an unconnected server")
	}
}

func TestConnect_DispatchesHTTPServersByType(t *testing.T) {
	server := fakeHTTPServer(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "initialize":
			return initializeResult{ProtocolVersion: protocolVersion}, nil
		case "tools/list":
			return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil
		}
		return nil, &rpcError{Code: -32601, Message: method}
	})
	defer server.Close()

	var out bytes.Buffer
	cfg := &Config{Servers: map[string]ServerConfig{
		"calc": {Type: "http", URL: server.URL},
	}}
	r := Connect(context.Background(), cfg, &out)
	defer r.Close()

	tools := r.ToolsFor("calc")
	if len(tools) != 1 || tools[0].Name != "calc__add" {
		t.Fatalf("ToolsFor(calc) = %+v, want [calc__add]", tools)
	}
	if !strings.Contains(out.String(), `подключён сервер "calc"`) {
		t.Fatalf("output = %q, want a connection confirmation", out.String())
	}
}
