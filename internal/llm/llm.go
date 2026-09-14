// Package llm is a generic client for OpenAI-compatible chat completions
// APIs — shared by every provider this app talks to (see internal/kimi and
// internal/deepseek for their base URLs/contracts). Each provider is just a
// different (baseURL, apiKey) pair; the request/response shape is the same.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Timeouts from CLAUDE.md's "Техническая конфигурация": connectTimeout
// bounds establishing the connection (TCP dial + TLS handshake);
// responseTimeout bounds the whole round trip, including waiting for the
// response.
const (
	connectTimeout  = 30 * time.Second
	responseTimeout = 180 * time.Second
)

// Message is one chat turn, OpenAI-compatible ("system"/"user"/"assistant").
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client calls one provider's chat completions endpoint.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient returns a Client posting to baseURL, authenticating with
// apiKey. apiKey is normally read from a provider-specific environment
// variable (see internal/config) and must never be logged or printed
// verbatim.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: responseTimeout,
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: connectTimeout}).DialContext,
				TLSHandshakeTimeout: connectTimeout,
			},
		},
	}
}

type request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stop           string          `json:"stop,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

// Options carries optional per-request parameters beyond model/messages —
// populated from a selected prompt template's "Response format", "Stop"
// and "Temperature" headings (see internal/promptfile). The zero value
// sends none of them.
type Options struct {
	ResponseFormat string   // -> response_format.type, if non-empty
	Stop           string   // -> stop, if non-empty
	Temperature    *float64 // -> temperature, if non-nil
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Exchange records the raw wire bodies of one request/response pair, for
// callers that need to log it (see internal/exchangelog). Bodies are the
// literal bytes sent/received — mask them before writing to a log file or
// the terminal.
type Exchange struct {
	Model        string
	RequestBody  []byte
	ResponseBody []byte
	StatusCode   int

	// Duration is how long the HTTP round trip took (request sent to
	// response body fully read) — CLAUDE.md requires it printed and
	// logged.
	Duration time.Duration

	// PromptTokens, CompletionTokens and TotalTokens mirror the response's
	// usage object, if present — CLAUDE.md requires the first two printed
	// alongside the reply (and summed per-dialog).
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Complete sends messages to model and returns the assistant's reply text
// along with the raw exchange for logging. ex is populated whenever a
// request actually reached the network, even if the API returned an error
// status, so callers can still log what happened.
//
// onRequest, if non-nil, is called with the marshaled request body right
// before it is sent — CLAUDE.md requires the request to be logged before
// the call goes out, not after the response comes back.
func (c *Client) Complete(ctx context.Context, model string, messages []Message, opts Options, onRequest func(reqBody []byte)) (content string, ex *Exchange, err error) {
	if c.APIKey == "" {
		return "", nil, fmt.Errorf("API key is not set")
	}

	req := request{Model: model, Messages: messages, Stop: opts.Stop, Temperature: opts.Temperature}
	if opts.ResponseFormat != "" {
		req.ResponseFormat = &responseFormat{Type: opts.ResponseFormat}
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("encode request: %w", err)
	}

	if onRequest != nil {
		onRequest(reqBody)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	start := time.Now()
	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("call %s: %w", c.BaseURL, err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	duration := time.Since(start)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	ex = &Exchange{
		Model:        model,
		RequestBody:  reqBody,
		ResponseBody: respBody,
		StatusCode:   httpResp.StatusCode,
		Duration:     duration,
	}

	var resp response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", ex, fmt.Errorf("decode response: %w", err)
	}
	if resp.Usage != nil {
		ex.PromptTokens = resp.Usage.PromptTokens
		ex.CompletionTokens = resp.Usage.CompletionTokens
		ex.TotalTokens = resp.Usage.TotalTokens
	}
	if resp.Error != nil {
		return "", ex, fmt.Errorf("api error: %s", resp.Error.Message)
	}
	if httpResp.StatusCode != http.StatusOK {
		return "", ex, fmt.Errorf("api returned status %d", httpResp.StatusCode)
	}
	if len(resp.Choices) == 0 {
		return "", ex, fmt.Errorf("api returned no choices")
	}

	return resp.Choices[0].Message.Content, ex, nil
}
