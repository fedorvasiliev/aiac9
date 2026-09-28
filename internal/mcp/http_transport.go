package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// sessionIDHeader is the MCP Streamable HTTP transport's session header:
// the server mints one in the "initialize" response, and every later
// request on the same logical connection must echo it back.
const sessionIDHeader = "Mcp-Session-Id"

// httpTransportTimeout bounds one HTTP round trip to a Streamable HTTP
// MCP server — these are local/trusted, per CLAUDE.md's "## Работа с
// MCP", so this is generous rather than tightly tuned.
const httpTransportTimeout = 30 * time.Second

// httpTransport speaks the MCP "Streamable HTTP" transport: JSON-RPC 2.0
// messages POSTed to a single URL, correlated by the HTTP request/response
// itself (no separate read loop needed, unlike stdio) and carried across
// calls by an opaque session ID the server assigns on "initialize".
// Responses are expected as a single "application/json" body — this
// client never requests (and so never needs to handle) the transport's
// optional "text/event-stream" streaming upgrade, since every call here
// is a plain, synchronous request/response.
type httpTransport struct {
	url        string
	httpClient *http.Client

	mu        sync.Mutex
	sessionID string
}

// StartHTTP connects to url (a Streamable HTTP MCP server, CLAUDE.md's
// config `{"type": "http", "url": "..."}`) and completes the initialize
// handshake, returning a ready client.
func StartHTTP(ctx context.Context, url string) (*Client, error) {
	tr := &httpTransport{url: url, httpClient: &http.Client{Timeout: httpTransportTimeout}}
	c := &Client{transport: tr}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (t *httpTransport) call(ctx context.Context, id int64, data []byte) (json.RawMessage, *rpcError, error) {
	respBody, status, err := t.post(ctx, data)
	if err != nil {
		return nil, nil, err
	}
	if status == http.StatusAccepted || len(respBody) == 0 {
		return nil, nil, fmt.Errorf("mcp: server accepted the request without returning a response")
	}

	var msg rpcMessage
	if err := json.Unmarshal(respBody, &msg); err != nil {
		return nil, nil, fmt.Errorf("mcp: decode response: %w", err)
	}
	return msg.Result, msg.Error, nil
}

func (t *httpTransport) notify(ctx context.Context, data []byte) error {
	_, _, err := t.post(ctx, data)
	return err
}

// post sends one JSON-RPC message and returns the raw response body (nil
// for a 202 Accepted, per the transport spec's handling of notifications
// and responses-to-server-requests, neither of which carries a body).
func (t *httpTransport) post(ctx context.Context, data []byte) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		req.Header.Set(sessionIDHeader, sid)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("mcp: http request: %w", err)
	}
	defer resp.Body.Close()

	if newSID := resp.Header.Get(sessionIDHeader); newSID != "" {
		t.mu.Lock()
		t.sessionID = newSID
		t.mu.Unlock()
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("mcp: read response: %w", err)
	}

	if resp.StatusCode == http.StatusAccepted {
		return nil, resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("mcp: http %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return nil, resp.StatusCode, fmt.Errorf("mcp: unexpected content type %q (streamed text/event-stream responses aren't supported)", ct)
	}
	return respBody, resp.StatusCode, nil
}

func (t *httpTransport) close() error {
	return nil // stateless HTTP: nothing to tear down beyond letting the server's session expire on its own
}
