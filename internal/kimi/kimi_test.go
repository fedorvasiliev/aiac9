package kimi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

	c := NewClient("secret-key")
	c.BaseURL = server.URL

	content, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}})
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
	c := NewClient("")

	_, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected an error when MOONSHOT_API_KEY is not set")
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

	c := NewClient("bad-key")
	c.BaseURL = server.URL

	_, ex, err := c.Complete(context.Background(), "kimi-k3", []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("err = %v, want it to mention %q", err, "invalid key")
	}
	if ex == nil || ex.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected the exchange to still be recorded, got %+v", ex)
	}
}
