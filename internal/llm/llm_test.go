package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient_ConfiguresTimeoutsPerCLAUDEmd(t *testing.T) {
	c := NewClient("https://example.invalid", "secret-key")

	if c.HTTPClient.Timeout != responseTimeout {
		t.Fatalf("HTTPClient.Timeout = %v, want %v", c.HTTPClient.Timeout, responseTimeout)
	}
	transport, ok := c.HTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTPClient.Transport = %T, want *http.Transport", c.HTTPClient.Transport)
	}
	if transport.TLSHandshakeTimeout != connectTimeout {
		t.Fatalf("TLSHandshakeTimeout = %v, want %v", transport.TLSHandshakeTimeout, connectTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("DialContext is nil, want a dialer enforcing the connect timeout")
	}
}

func TestComplete_Success(t *testing.T) {
	var gotAuth, gotModel string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = req.Model

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi there"}}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret-key")

	content, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}}, Options{}, nil)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if content != "hi there" {
		t.Fatalf("content = %q, want %q", content, "hi there")
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer secret-key")
	}
	if gotModel != "kimi-k3" {
		t.Fatalf("request model = %q, want %q", gotModel, "kimi-k3")
	}
	if ex == nil || ex.StatusCode != http.StatusOK || ex.Model != "kimi-k3" {
		t.Fatalf("unexpected exchange: %+v", ex)
	}
}

func TestComplete_MissingAPIKey(t *testing.T) {
	c := NewClient("https://example.invalid", "")

	_, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}}, Options{}, nil)
	if err == nil {
		t.Fatal("expected an error when the API key is empty")
	}
	if ex != nil {
		t.Fatalf("expected no exchange when the request never reached the network, got %+v", ex)
	}
}

func TestComplete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key","type":"auth_error"}}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "bad-key")

	_, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}}, Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("err = %v, want it to mention %q", err, "invalid key")
	}
	if ex == nil || ex.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected the exchange to still be recorded, got %+v", ex)
	}
}

func TestComplete_SendsResponseFormatStopAndTemperature(t *testing.T) {
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret-key")

	temp := 0.3
	_, _, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}}, Options{
		ResponseFormat: "json_object",
		Stop:           "###",
		Temperature:    &temp,
	}, nil)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	var req request
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
		t.Fatalf("response_format = %+v, want type %q", req.ResponseFormat, "json_object")
	}
	if req.Stop != "###" {
		t.Fatalf("stop = %q, want %q", req.Stop, "###")
	}
	if req.Temperature == nil || *req.Temperature != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", req.Temperature)
	}
}

func TestComplete_CallsOnRequestBeforeSending(t *testing.T) {
	var serverSawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverSawRequest = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "secret-key")

	var gotBody []byte
	var sawRequestBeforeNetworkCall bool
	onRequest := func(reqBody []byte) {
		gotBody = reqBody
		sawRequestBeforeNetworkCall = !serverSawRequest
	}

	_, _, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}}, Options{}, onRequest)
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if gotBody == nil {
		t.Fatal("onRequest was not called")
	}
	if !sawRequestBeforeNetworkCall {
		t.Fatal("onRequest must run before the HTTP call, so the request can be logged first")
	}
}
