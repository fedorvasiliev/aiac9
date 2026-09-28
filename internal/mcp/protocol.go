package mcp

import (
	"encoding/json"
	"fmt"
)

// protocolVersion is the MCP revision this client speaks during the
// "initialize" handshake — the last one using that handshake at all
// (mark3labs/mcp-go's LATEST_LEGACY_PROTOCOL_VERSION); the servers this
// app talks to (built on that same library) negotiate down to it from any
// legacy version offered.
const protocolVersion = "2025-11-25"

// clientName/clientVersion identify aiac9 to a server as its "clientInfo".
const (
	clientName    = "aiac9"
	clientVersion = "0.1.0"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcMessage is a server->client line: either a response to one of our
// requests (ID non-nil) or a notification (ID nil, Method non-empty) —
// this client only ever waits on the former; the latter (e.g. logging)
// is read and discarded.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message)
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    struct{}       `json:"capabilities"`
	ClientInfo      implementation `json:"clientInfo"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ServerInfo      implementation `json:"serverInfo"`
}

// Tool is one tool a server advertised via "tools/list".
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type listToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor"`
}

type callToolParams struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

// content is one element of a "tools/call" result's content array — only
// the "text" kind is modeled, since that's all these tools ever return;
// any other kind is skipped rather than failing the call.
type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callToolResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError"`
}
