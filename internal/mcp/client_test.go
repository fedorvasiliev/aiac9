package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// handler decides how a fake server responds to one incoming line: result
// (marshaled into the response's "result") or rpcErr, and whether the line
// was a notification (no response expected at all).
type handler func(method string, params json.RawMessage) (result any, rpcErr *rpcError)

// newTestClient wires a Client to an in-process fake server driven by
// handle, via a pair of io.Pipe — exercising the exact same wire format
// (newline-delimited JSON-RPC 2.0) a real subprocess would.
func newTestClient(t *testing.T, handle handler) (*Client, func()) {
	t.Helper()

	inR, inW := io.Pipe()   // client -> server
	outR, outW := io.Pipe() // server -> client

	c := &Client{transport: newStdioTransport(inW, outR)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r := bufio.NewReader(inR)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				var req struct {
					ID     *int64          `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if jerr := json.Unmarshal(line, &req); jerr == nil {
					result, rpcErr := handle(req.Method, req.Params)
					if req.ID != nil {
						msg := struct {
							JSONRPC string    `json:"jsonrpc"`
							ID      int64     `json:"id"`
							Result  any       `json:"result,omitempty"`
							Error   *rpcError `json:"error,omitempty"`
						}{JSONRPC: "2.0", ID: *req.ID, Result: result, Error: rpcErr}
						data, _ := json.Marshal(msg)
						outW.Write(append(data, '\n'))
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	cleanup := func() {
		inW.Close()
		outW.Close()
		<-done
	}
	return c, cleanup
}

func echoInitializeHandler(extra handler) handler {
	return func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "initialize":
			return initializeResult{ProtocolVersion: protocolVersion, ServerInfo: implementation{Name: "fake", Version: "1.0"}}, nil
		case "notifications/initialized":
			return nil, nil
		default:
			if extra != nil {
				return extra(method, params)
			}
			return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
		}
	}
}

func TestClient_InitializeHandshake(t *testing.T) {
	var gotParams initializeParams
	c, cleanup := newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "initialize":
			json.Unmarshal(params, &gotParams)
			return initializeResult{ProtocolVersion: protocolVersion, ServerInfo: implementation{Name: "fake", Version: "1.0"}}, nil
		case "notifications/initialized":
			return nil, nil
		default:
			return nil, &rpcError{Code: -32601, Message: "unexpected: " + method}
		}
	})
	defer cleanup()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if gotParams.ProtocolVersion != protocolVersion {
		t.Fatalf("protocolVersion sent = %q, want %q", gotParams.ProtocolVersion, protocolVersion)
	}
	if gotParams.ClientInfo.Name != clientName {
		t.Fatalf("clientInfo.name sent = %q, want %q", gotParams.ClientInfo.Name, clientName)
	}
}

func TestClient_ListTools_SinglePage(t *testing.T) {
	c, cleanup := newTestClient(t, echoInitializeHandler(func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/list" {
			t.Fatalf("unexpected method %q", method)
		}
		return listToolsResult{Tools: []Tool{
			{Name: "add", Description: "adds", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "subtract", Description: "subtracts"},
		}}, nil
	}))
	defer cleanup()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "add" || tools[1].Name != "subtract" {
		t.Fatalf("tools = %+v, want [add subtract]", tools)
	}
}

func TestClient_ListTools_FollowsPagination(t *testing.T) {
	calls := 0
	c, cleanup := newTestClient(t, echoInitializeHandler(func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/list" {
			t.Fatalf("unexpected method %q", method)
		}
		calls++
		if calls == 1 {
			return listToolsResult{Tools: []Tool{{Name: "first"}}, NextCursor: "page2"}, nil
		}
		var p struct {
			Cursor string `json:"cursor"`
		}
		json.Unmarshal(params, &p)
		if p.Cursor != "page2" {
			t.Fatalf("second page cursor = %q, want page2", p.Cursor)
		}
		return listToolsResult{Tools: []Tool{{Name: "second"}}}, nil
	}))
	defer cleanup()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "first" || tools[1].Name != "second" {
		t.Fatalf("tools = %+v, want [first second]", tools)
	}
	if calls != 2 {
		t.Fatalf("tools/list called %d times, want 2", calls)
	}
}

func TestClient_CallTool_JoinsTextContentAndReportsError(t *testing.T) {
	var gotName string
	var gotArgs json.RawMessage
	c, cleanup := newTestClient(t, echoInitializeHandler(func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			t.Fatalf("unexpected method %q", method)
		}
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(params, &p)
		gotName, gotArgs = p.Name, p.Arguments
		return callToolResult{Content: []content{
			{Type: "text", Text: "line one"},
			{Type: "text", Text: "line two"},
		}}, nil
	}))
	defer cleanup()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	text, isErr, err := c.CallTool(context.Background(), "add", json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if isErr {
		t.Fatal("isError = true, want false")
	}
	if text != "line one\nline two" {
		t.Fatalf("text = %q, want joined lines", text)
	}
	if gotName != "add" {
		t.Fatalf("server saw tool name %q, want add", gotName)
	}
	if string(gotArgs) != `{"a":1,"b":2}` {
		t.Fatalf("server saw arguments %s, want the original JSON", gotArgs)
	}
}

func TestClient_CallTool_ToolLevelErrorIsNotAProtocolError(t *testing.T) {
	c, cleanup := newTestClient(t, echoInitializeHandler(func(method string, params json.RawMessage) (any, *rpcError) {
		return callToolResult{Content: []content{{Type: "text", Text: "division by zero"}}, IsError: true}, nil
	}))
	defer cleanup()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	text, isErr, err := c.CallTool(context.Background(), "divide", nil)
	if err != nil {
		t.Fatalf("CallTool returned a protocol error for a tool-level failure: %v", err)
	}
	if !isErr {
		t.Fatal("isError = false, want true")
	}
	if text != "division by zero" {
		t.Fatalf("text = %q, want the error text", text)
	}
}

func TestClient_Call_ProtocolErrorResponseIsReturnedAsError(t *testing.T) {
	c, cleanup := newTestClient(t, echoInitializeHandler(func(method string, params json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32602, Message: "unknown tool"}
	}))
	defer cleanup()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_, _, err := c.CallTool(context.Background(), "nope", nil)
	if err == nil {
		t.Fatal("CallTool returned nil error for a JSON-RPC error response")
	}
}

func TestClient_Call_ContextCanceledUnblocks(t *testing.T) {
	block := make(chan struct{})
	c, cleanup := newTestClient(t, echoInitializeHandler(func(method string, params json.RawMessage) (any, *rpcError) {
		<-block // never respond
		return nil, nil
	}))
	defer func() { close(block); cleanup() }()

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := c.CallTool(ctx, "slow", nil)
	if err == nil {
		t.Fatal("CallTool returned nil error, want a context-deadline error")
	}
}

func TestClient_Call_ConnectionClosedUnblocksPendingCalls(t *testing.T) {
	// Deliberately bypasses newTestClient's shared fake server: this test
	// simulates the server side vanishing mid-call (e.g. the subprocess
	// died) by closing its write end directly, without needing the fake
	// server's own goroutine to acknowledge or unwind first.
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &Client{transport: newStdioTransport(inW, outR)}

	go io.Copy(io.Discard, inR) // drain the client's requests; never respond to any of them

	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.CallTool(context.Background(), "slow", nil)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the call actually reach the "server" first
	outW.Close()                      // simulate the connection/subprocess going away

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("CallTool returned nil error after the connection closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool never returned after the connection closed")
	}
}
