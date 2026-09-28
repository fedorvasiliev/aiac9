package interactive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/fedorvasiliev/aiac9/internal/exchangelog"
	"github.com/fedorvasiliev/aiac9/internal/llm"
	"github.com/fedorvasiliev/aiac9/internal/mcp"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

// maxToolRounds bounds how many tool-call round trips one Step T turn can
// make, so a model that keeps calling tools can't hang the wizard
// indefinitely.
const maxToolRounds = 8

// toolCaller is the one method completeWithTools needs from a connected
// MCP registry (CLAUDE.md's "## Работа с MCP") — narrowed to an interface
// so tests can supply a fake without spawning a real MCP server
// subprocess. *mcp.Registry satisfies it; Run always constructs one via
// mcp.Connect, which never returns nil, so the real code path never needs
// to pass a nil toolCaller.
type toolCaller interface {
	Call(ctx context.Context, name string, arguments json.RawMessage) (text string, isError bool, err error)
}

// mcpTools converts a registry's aggregated tools into the OpenAI-style
// "tools" a request attaches. No tools (no MCP servers configured, or none
// connected successfully) yields nil — so a request built with MCP
// unavailable is unaffected, byte-for-byte, by this feature existing at
// all.
func mcpTools(regTools []mcp.RegisteredTool) []llm.Tool {
	if len(regTools) == 0 {
		return nil
	}
	tools := make([]llm.Tool, len(regTools))
	for i, t := range regTools {
		tools[i] = llm.Tool{Type: "function", Function: llm.ToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		}}
	}
	return tools
}

// completeWithTools calls client.Complete, and for as long as the model's
// reply asks for tool invocations (CLAUDE.md's "## Работа с MCP") instead
// of a final answer, executes each one via registry, folds its result
// back in as a "tool" message, and asks again — until a reply carries no
// more tool calls, or maxToolRounds is hit. Every round is logged as its
// own request/response pair (CLAUDE.md: "Каждая пара запроса и ответа
// логируется"), printed as soon as that round's response arrives, rather
// than deferred to the caller.
func completeWithTools(ctx context.Context, client *llm.Client, model string, messages []llm.Message, opts llm.Options, caller toolCaller, masker *secretmask.Masker, stdout io.Writer) (content string, ex *llm.Exchange, err error) {
	for round := 0; ; round++ {
		var logPath string
		onRequest := func(reqBody []byte) {
			path, logErr := exchangelog.Start(exchangelog.Dir, model, reqBody, masker)
			if logErr != nil {
				fmt.Fprintln(stdout, "не удалось записать лог:", masker.Mask(logErr.Error()))
				return
			}
			logPath = path
		}

		content, ex, err = client.Complete(ctx, model, messages, opts, onRequest)
		if logPath != "" && ex != nil {
			if ferr := exchangelog.Finish(logPath, ex.StatusCode, ex.ResponseBody, ex.Duration, masker); ferr != nil {
				fmt.Fprintln(stdout, "не удалось дописать лог:", masker.Mask(ferr.Error()))
			} else {
				fmt.Fprintln(stdout, "лог:", logPath)
			}
		}
		if err != nil {
			return "", ex, err
		}

		if len(ex.Message.ToolCalls) == 0 {
			return content, ex, nil
		}
		if caller == nil {
			return "", ex, fmt.Errorf("модель запросила вызов инструмента, но MCP не подключён")
		}
		if round >= maxToolRounds {
			return "", ex, fmt.Errorf("превышено число обращений к инструментам за один ход (%d)", maxToolRounds)
		}

		messages = append(messages, llm.Message{Role: "assistant", Content: ex.Message.Content, ToolCalls: ex.Message.ToolCalls})
		for _, call := range ex.Message.ToolCalls {
			resultText, isError, callErr := caller.Call(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments))
			label := colorize(stdout, ansiYellow, "MCP:")
			switch {
			case callErr != nil:
				resultText = callErr.Error()
				fmt.Fprintln(stdout, label, call.Function.Name, "-> ошибка:", masker.Mask(resultText))
			case isError:
				fmt.Fprintln(stdout, label, call.Function.Name, "-> ошибка инструмента:", masker.Mask(resultText))
			default:
				fmt.Fprintln(stdout, label, call.Function.Name, "->", masker.Mask(resultText))
			}
			messages = append(messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: resultText})
		}
	}
}
