package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// stdioTransport speaks newline-delimited JSON-RPC 2.0 over a local
// subprocess's stdin/stdout — unlike LSP, there is no Content-Length
// framing.
type stdioTransport struct {
	stdin   io.WriteCloser
	cmd     *exec.Cmd // nil when built directly from pipes (tests)
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan rpcMessage
	closed  bool
}

// Start launches command (with args) and completes the MCP initialize
// handshake against it over its stdio, returning a ready client. ctx
// bounds only the handshake — the subprocess itself keeps running (as a
// persistent connection tools/list and tools/call reuse) until Close.
func Start(ctx context.Context, command string, args []string) (*Client, error) {
	cmd := exec.Command(command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", command, err)
	}

	tr := newStdioTransport(stdin, stdout)
	tr.cmd = cmd
	go io.Copy(io.Discard, stderr) // drain so the server never blocks writing diagnostics; aiac9 has nowhere useful to surface them today

	c := &Client{transport: tr}
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// newStdioTransport wires a transport directly to a pair of pipes —
// Start's path for a real subprocess, and how tests connect to an
// in-process fake server.
func newStdioTransport(stdin io.WriteCloser, stdout io.Reader) *stdioTransport {
	t := &stdioTransport{stdin: stdin, pending: make(map[int64]chan rpcMessage)}
	go t.readLoop(bufio.NewReader(stdout))
	return t
}

// readLoop is the only reader of stdout; it dispatches each response line
// to whichever call() is waiting on that id, and discards anything else
// (server->client notifications, e.g. logging — this client never issues
// requests a server would need to notify about further).
func (t *stdioTransport) readLoop(r *bufio.Reader) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr == nil && msg.ID != nil {
				t.mu.Lock()
				ch, ok := t.pending[*msg.ID]
				if ok {
					delete(t.pending, *msg.ID)
				}
				t.mu.Unlock()
				if ok {
					ch <- msg
				}
			}
		}
		if err != nil {
			t.mu.Lock()
			t.closed = true
			for id, ch := range t.pending {
				close(ch)
				delete(t.pending, id)
			}
			t.mu.Unlock()
			return
		}
	}
}

func (t *stdioTransport) call(ctx context.Context, id int64, data []byte) (json.RawMessage, *rpcError, error) {
	ch := make(chan rpcMessage, 1)

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, nil, fmt.Errorf("mcp: connection closed")
	}
	t.pending[id] = ch
	t.mu.Unlock()

	t.writeMu.Lock()
	_, werr := t.stdin.Write(append(data, '\n'))
	t.writeMu.Unlock()
	if werr != nil {
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, nil, fmt.Errorf("mcp: write request: %w", werr)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, nil, fmt.Errorf("mcp: connection closed while waiting for a response")
		}
		return msg.Result, msg.Error, nil
	case <-ctx.Done():
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, nil, ctx.Err()
	}
}

func (t *stdioTransport) notify(ctx context.Context, data []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if _, err := t.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// close closes the subprocess's stdin (its usual signal to exit), then
// waits for it to actually exit if this transport owns one (Start, not
// newStdioTransport's test-only path).
func (t *stdioTransport) close() error {
	closeErr := t.stdin.Close()
	if t.cmd != nil {
		_ = t.cmd.Wait()
	}
	return closeErr
}
