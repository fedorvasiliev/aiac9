// Package kimi is a client for the Moonshot AI chat completions API, used
// by all "kimi-*" models. The request/response contract it implements is
// documented in ./api-examples/kimi-completions.txt at the repo root.
package kimi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultBaseURL is the Moonshot AI chat completions endpoint.
const DefaultBaseURL = "https://api.moonshot.ai/v1/chat/completions"

// Message is one chat turn, OpenAI-compatible ("system"/"user"/"assistant").
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client calls the Moonshot AI chat completions API.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewClient returns a Client authenticating with apiKey. apiKey is normally
// read from the MOONSHOT_API_KEY environment variable (see internal/config)
// and must never be logged or printed verbatim.
func NewClient(apiKey string) *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

type request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
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
}

// Complete sends messages to model and returns the assistant's reply text
// along with the raw exchange for logging. ex is populated whenever a
// request actually reached the network, even if the API returned an error
// status, so callers can still log what happened.
func (c *Client) Complete(ctx context.Context, model string, messages []Message) (content string, ex *Exchange, err error) {
	if c.APIKey == "" {
		return "", nil, fmt.Errorf("MOONSHOT_API_KEY is not set")
	}

	reqBody, err := json.Marshal(request{Model: model, Messages: messages})
	if err != nil {
		return "", nil, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	httpResp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("call moonshot api: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	ex = &Exchange{
		Model:        model,
		RequestBody:  reqBody,
		ResponseBody: respBody,
		StatusCode:   httpResp.StatusCode,
	}

	var resp response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", ex, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return "", ex, fmt.Errorf("moonshot api error: %s", resp.Error.Message)
	}
	if httpResp.StatusCode != http.StatusOK {
		return "", ex, fmt.Errorf("moonshot api returned status %d", httpResp.StatusCode)
	}
	if len(resp.Choices) == 0 {
		return "", ex, fmt.Errorf("moonshot api returned no choices")
	}

	return resp.Choices[0].Message.Content, ex, nil
}
