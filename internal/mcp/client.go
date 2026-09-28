// Package mcp is a minimal client for the Model Context Protocol —
// CLAUDE.md's "## Работа с MCP": local MCP servers, launched as an
// ordinary process over stdio, or (CLAUDE.md's later config shape)
// reached over its "Streamable HTTP" transport — no auth either way. Only
// the subset needed to discover and call tools is implemented:
// "initialize", "notifications/initialized", "tools/list", "tools/call".
// transport.go's stdioTransport and httpTransport are the two ways a
// Client can be wired to a server; both speak the exact same JSON-RPC 2.0
// envelope, just delivered differently (newline-delimited stdin/stdout
// lines vs. HTTP POSTs), so everything below this point is transport
// agnostic.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// transport is how a Client exchanges JSON-RPC 2.0 messages with a
// server, whatever the underlying delivery mechanism.
type transport interface {
	// call sends data — a marshaled {"jsonrpc","id","method","params"}
	// request object — and returns the matching response's "result" and
	// "error" members (at most one non-nil), blocking until it arrives,
	// ctx is done, or the connection is lost.
	call(ctx context.Context, id int64, data []byte) (result json.RawMessage, rpcErr *rpcError, err error)
	// notify sends data — a marshaled {"jsonrpc","method","params"}
	// notification object — with no response expected.
	notify(ctx context.Context, data []byte) error
	close() error
}

// Client is a connection to one MCP server, over whichever transport it
// was constructed with (Start for stdio, StartHTTP for Streamable HTTP).
type Client struct {
	transport transport
	nextID    atomic.Int64
}

// call sends method/params as a request and decodes its result into
// result (skipped if nil).
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	data, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("mcp: marshal %s request: %w", method, err)
	}

	res, rpcErr, err := c.transport.call(ctx, id, data)
	if err != nil {
		return err
	}
	if rpcErr != nil {
		return rpcErr
	}
	if result != nil && len(res) > 0 {
		if err := json.Unmarshal(res, result); err != nil {
			return fmt.Errorf("mcp: decode %s result: %w", method, err)
		}
	}
	return nil
}

// notify sends method/params as a one-way notification (no response
// expected) — only ever "notifications/initialized" here.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	data, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("mcp: marshal %s notification: %w", method, err)
	}
	if err := c.transport.notify(ctx, data); err != nil {
		return fmt.Errorf("mcp: %s notification: %w", method, err)
	}
	return nil
}

// initialize runs CLAUDE.md's connection setup: the MCP "initialize"
// request followed by the "notifications/initialized" notification,
// after which tools/list and tools/call are usable.
func (c *Client) initialize(ctx context.Context) error {
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      implementation{Name: clientName, Version: clientVersion},
	}
	var result initializeResult
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	if err := c.notify(ctx, "notifications/initialized", struct{}{}); err != nil {
		return fmt.Errorf("mcp: initialized notification: %w", err)
	}
	return nil
}

// ListTools returns every tool the server advertises, following
// nextCursor pages until the server stops sending one.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	cursor := ""
	for {
		var params any
		if cursor != "" {
			params = struct {
				Cursor string `json:"cursor"`
			}{cursor}
		}
		var result listToolsResult
		if err := c.call(ctx, "tools/list", params, &result); err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
}

// CallTool invokes name with arguments (a JSON object, or nil for none),
// returning its text content joined together and whether the server
// flagged the call itself as having failed ("isError" — CLAUDE.md/MCP:
// tool-level errors are reported this way, not as a protocol error, so
// the model can see and react to them).
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (text string, isError bool, err error) {
	var args any
	if len(arguments) > 0 {
		args = json.RawMessage(arguments)
	}
	var result callToolResult
	if err := c.call(ctx, "tools/call", callToolParams{Name: name, Arguments: args}, &result); err != nil {
		return "", false, fmt.Errorf("mcp: tools/call %s: %w", name, err)
	}
	var b []byte
	for _, part := range result.Content {
		if part.Type != "text" {
			continue
		}
		if len(b) > 0 {
			b = append(b, '\n')
		}
		b = append(b, part.Text...)
	}
	return string(b), result.IsError, nil
}

// Close disconnects from the server.
func (c *Client) Close() error {
	return c.transport.close()
}
