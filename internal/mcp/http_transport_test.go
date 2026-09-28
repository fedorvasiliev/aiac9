package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeHTTPServer mimics the exact wire behavior of a real mark3labs/mcp-go
// Streamable HTTP server, as observed with curl against the real
// calc-mcp-server binary run with --transport http: initialize responds
// with a Content-Type: application/json body and a fresh Mcp-Session-Id
// response header; every later request must echo that header back or gets
// 404 "Invalid session ID"; a notification (no "id") gets a bare 202
// Accepted with no body.
func fakeHTTPServer(t *testing.T, handle handler) *httptest.Server {
	t.Helper()
	const sessionID = "mcp-session-test-1234"

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.Method != "initialize" {
			if r.Header.Get(sessionIDHeader) != sessionID {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("Invalid session ID"))
				return
			}
		}

		if req.ID == nil { // notification
			w.WriteHeader(http.StatusAccepted)
			return
		}

		result, rpcErr := handle(req.Method, req.Params)
		if req.Method == "initialize" {
			w.Header().Set(sessionIDHeader, sessionID)
		}

		msg := struct {
			JSONRPC string    `json:"jsonrpc"`
			ID      int64     `json:"id"`
			Result  any       `json:"result,omitempty"`
			Error   *rpcError `json:"error,omitempty"`
		}{JSONRPC: "2.0", ID: *req.ID, Result: result, Error: rpcErr}
		data, _ := json.Marshal(msg)
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
}

func TestHTTPTransport_InitializeCapturesSessionIDAndEchoesIt(t *testing.T) {
	server := fakeHTTPServer(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "initialize":
			return initializeResult{ProtocolVersion: protocolVersion, ServerInfo: implementation{Name: "fake", Version: "1.0"}}, nil
		case "tools/list":
			return listToolsResult{Tools: []Tool{{Name: "add"}}}, nil
		}
		return nil, &rpcError{Code: -32601, Message: method}
	})
	defer server.Close()

	c, err := StartHTTP(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer c.Close()

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "add" {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestHTTPTransport_MissingSessionIsAnError(t *testing.T) {
	server := fakeHTTPServer(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method == "initialize" {
			return initializeResult{ProtocolVersion: protocolVersion}, nil
		}
		return listToolsResult{}, nil
	})
	defer server.Close()

	// Bypass StartHTTP's own initialize so the session is never captured,
	// then call directly — mirrors the real server's 404 behavior when a
	// request arrives without a valid Mcp-Session-Id.
	tr := &httpTransport{url: server.URL, httpClient: server.Client()}
	c := &Client{transport: tr}
	if _, err := c.ListTools(context.Background()); err == nil {
		t.Fatal("ListTools returned nil error with no session established")
	}
}

func TestHTTPTransport_CallToolRoundTrip(t *testing.T) {
	server := fakeHTTPServer(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "initialize":
			return initializeResult{ProtocolVersion: protocolVersion}, nil
		case "tools/call":
			var p callToolParams
			json.Unmarshal(params, &p)
			return callToolResult{Content: []content{{Type: "text", Text: "13"}}}, nil
		}
		return nil, &rpcError{Code: -32601, Message: method}
	})
	defer server.Close()

	c, err := StartHTTP(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer c.Close()

	text, isErr, err := c.CallTool(context.Background(), "add", json.RawMessage(`{"a":4,"b":9}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if isErr || text != "13" {
		t.Fatalf("text=%q isErr=%v", text, isErr)
	}
}

func TestHTTPTransport_NonJSONErrorBodyIsReturnedAsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer server.Close()

	if _, err := StartHTTP(context.Background(), server.URL); err == nil {
		t.Fatal("StartHTTP returned nil error for a 500 response")
	}
}

func TestHTTPTransport_NotificationExpects202(t *testing.T) {
	notified := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "initialize" {
			w.Header().Set(sessionIDHeader, "s1")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25"}}`))
			return
		}
		if req.Method == "notifications/initialized" && req.ID == nil {
			notified = true
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	c, err := StartHTTP(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	defer c.Close()
	if !notified {
		t.Fatal("server never saw the notifications/initialized notification")
	}
}
