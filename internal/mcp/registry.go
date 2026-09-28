package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// RegisteredTool is one tool aggregated across every connected server,
// under its exposed Name — server-qualified ("<server>__<tool>", since
// two servers could otherwise advertise the same tool name and OpenAI-style
// function names can't contain a "." to namespace them any other way).
type RegisteredTool struct {
	Name        string // exposed, e.g. "calc__add"
	Description string
	InputSchema json.RawMessage
}

// Registry holds every server connected at startup (CLAUDE.md's "##
// Работа с MCP") and dispatches a tool call to whichever one owns it.
type Registry struct {
	clients map[string]*Client // by server name, e.g. "calc"
	tools   []RegisteredTool
	owner   map[string]ownedTool // exposed name -> (server, original tool name)
}

type ownedTool struct {
	server string
	name   string
}

// Connect launches and initializes every server in cfg, then discovers
// its tools — best-effort per server: one server failing to start or
// answer tools/list is reported to stdout and simply has no tools
// registered, rather than failing the whole app (the same graceful
// degradation already used for a missing dialog store or prompts
// directory). A Config with no servers yields an empty, harmless Registry.
func Connect(ctx context.Context, cfg *Config, stdout io.Writer) *Registry {
	r := &Registry{clients: make(map[string]*Client), owner: make(map[string]ownedTool)}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic connection order/output

	for _, name := range names {
		sc := cfg.Servers[name]
		var client *Client
		var err error
		if sc.Type == "http" {
			client, err = StartHTTP(ctx, sc.URL)
		} else {
			client, err = Start(ctx, sc.Command, sc.Args)
		}
		if err != nil {
			fmt.Fprintf(stdout, "MCP: не удалось подключить сервер %q: %v\n", name, err)
			continue
		}

		tools, err := client.ListTools(ctx)
		if err != nil {
			fmt.Fprintf(stdout, "MCP: сервер %q подключён, но список инструментов получить не удалось: %v\n", name, err)
			client.Close()
			continue
		}

		r.clients[name] = client
		toolNames := make([]string, 0, len(tools))
		for _, t := range tools {
			exposed := name + "__" + t.Name
			r.tools = append(r.tools, RegisteredTool{Name: exposed, Description: t.Description, InputSchema: t.InputSchema})
			r.owner[exposed] = ownedTool{server: name, name: t.Name}
			toolNames = append(toolNames, t.Name)
		}
		fmt.Fprintf(stdout, "MCP: подключён сервер %q, инструменты: %v\n", name, toolNames)
	}

	return r
}

// Tools returns every tool aggregated across every connected server, for
// attaching to an outgoing LLM request. Empty (nil) when no server
// connected successfully, so a request with no configured/working MCP
// servers is byte-identical to one built before MCP support existed.
func (r *Registry) Tools() []RegisteredTool {
	return r.tools
}

// Call dispatches one tool call by its exposed Name to whichever server
// owns it.
func (r *Registry) Call(ctx context.Context, exposedName string, arguments json.RawMessage) (text string, isError bool, err error) {
	owned, ok := r.owner[exposedName]
	if !ok {
		return "", true, fmt.Errorf("mcp: unknown tool %q", exposedName)
	}
	client, ok := r.clients[owned.server]
	if !ok {
		return "", true, fmt.Errorf("mcp: server %q for tool %q is not connected", owned.server, exposedName)
	}
	return client.CallTool(ctx, owned.name, arguments)
}

// ToolsFor returns the tools server (its config name, e.g. "calc")
// advertises, or nil if no such server is connected — CLAUDE.md's inline
// "MCP:<server>->tools" instruction ("найди инструмент calc среди
// подключенных mcpServers и выполни команду tools на нем").
func (r *Registry) ToolsFor(server string) []RegisteredTool {
	prefix := server + "__"
	var out []RegisteredTool
	for _, t := range r.tools {
		if strings.HasPrefix(t.Name, prefix) {
			out = append(out, t)
		}
	}
	return out
}

// CallOn invokes tool on server directly, by their config/tool names as
// advertised (not Call's aggregated/namespaced exposed name) — CLAUDE.md's
// inline "MCP:<server>-><tool>(...)" instruction.
func (r *Registry) CallOn(ctx context.Context, server, tool string, arguments json.RawMessage) (text string, isError bool, err error) {
	client, ok := r.clients[server]
	if !ok {
		return "", true, fmt.Errorf("mcp: server %q is not connected", server)
	}
	return client.CallTool(ctx, tool, arguments)
}

// Close disconnects every connected server.
func (r *Registry) Close() {
	for _, c := range r.clients {
		c.Close()
	}
}
