package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/exchangelog"
	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

func TestRun_ExitsCleanlyOnImmediateEOF(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	pw.Close() // nothing written: the read end sees EOF right away
	defer pr.Close()

	var out bytes.Buffer
	if err := Run(context.Background(), &config.Config{}, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Use prompt") {
		t.Fatalf("expected the Step A select prompt to have been printed, got:\n%s", out.String())
	}
}

func TestRun_ReportsMissingAPIKeyAndLoopsBackToSelect(t *testing.T) {
	// No ./prompts directory here (t.Chdir isn't used), so Step A only
	// offers the synthetic "no prompt" option.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Step A: accept the default (empty line = "no prompt"); Step T: a
		// prompt. No MOONSHOT_API_KEY is configured, so Complete fails
		// locally without any network call, Run reports it, and loops back
		// to Step A — where this pipe's EOF then ends the wizard.
		pw.Write([]byte("\nkakoy segodnya den?\n"))
	}()

	var out bytes.Buffer
	if err := Run(context.Background(), &config.Config{}, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "MOONSHOT_API_KEY is not set") {
		t.Fatalf("expected the missing-API-key error to be printed, got:\n%s", out.String())
	}
}

func TestRun_NoPromptUsesDefaultModel(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Step A: accept the default ("no prompt"); Step T: a plain prompt.
		pw.Write([]byte("\nhello there\n"))
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode sent request: %v\nbody: %s", err, gotBody)
	}
	if req.Model != defaultModel {
		t.Fatalf("model = %q, want the built-in default %q", req.Model, defaultModel)
	}
}

func TestRun_PromptFilterRestrictsStepAList(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := os.Mkdir(promptfile.Dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", promptfile.Dir, err)
	}
	for _, name := range []string{"any-w1d4.prompt.md", "w1d2-1-pure.prompt.md"} {
		if err := os.WriteFile(filepath.Join(promptfile.Dir, name), []byte("hi"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()
	pw.Close() // just inspect the printed Step A list, then EOF

	var out bytes.Buffer
	if err := Run(context.Background(), &config.Config{}, pr, &out, Options{PromptFilter: "w1d4"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "any-w1d4.prompt.md") {
		t.Fatalf("expected the matching file to be listed, got:\n%s", got)
	}
	if strings.Contains(got, "w1d2-1-pure.prompt.md") {
		t.Fatalf("expected the non-matching file to be filtered out, got:\n%s", got)
	}
}

func TestRun_ResponseTimeoutOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"too late"}}]}`))
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		pw.Write([]byte("\nhello\nexit\n"))
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	timeout := 50 * time.Millisecond
	var out bytes.Buffer
	start := time.Now()
	if err := Run(context.Background(), cfg, pr, &out, Options{ResponseTimeout: &timeout}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 200*time.Millisecond {
		t.Fatalf("Run took %v, want it to time out well before the server's 200ms reply", elapsed)
	}
	if !strings.Contains(out.String(), "Client.Timeout") && !strings.Contains(strings.ToLower(out.String()), "timeout") {
		t.Fatalf("expected a timeout error to be printed, got:\n%s", out.String())
	}
}

func TestRun_DeepSeekModelRoutesToDeepSeekAndLogsBothSections(t *testing.T) {
	t.Chdir(t.TempDir())

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"deep reply"}}]}`))
	}))
	defer server.Close()

	if err := os.Mkdir(promptfile.Dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", promptfile.Dir, err)
	}
	tmpl := "### Model\ndeepseek-chat\n\n### User Prompt\nhello\n"
	if err := os.WriteFile(filepath.Join(promptfile.Dir, "ds.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Step A: options are ["(без промпта)", "ds.md"] — pick "2"; Step T: empty.
		pw.Write([]byte("2\n\n"))
	}()

	cfg := &config.Config{DeepSeekAPIKey: "deep-secret", DeepSeekBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if gotAuth != "Bearer deep-secret" {
		t.Fatalf("Authorization header = %q, want the DeepSeek key", gotAuth)
	}
	if !strings.Contains(out.String(), "deep reply") {
		t.Fatalf("expected the reply to be printed, got:\n%s", out.String())
	}

	entries, err := os.ReadDir(exchangelog.Dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one log file, got %v (err %v)", entries, err)
	}
	logData, err := os.ReadFile(filepath.Join(exchangelog.Dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	log := string(logData)
	if !strings.Contains(log, "=== REQUEST ===") || !strings.Contains(log, "=== RESPONSE (HTTP 200) ===") {
		t.Fatalf("expected both request and response sections in the log, got:\n%s", log)
	}
}

func TestRun_StepAAppliesPromptTemplate(t *testing.T) {
	t.Chdir(t.TempDir())

	if err := os.Mkdir(promptfile.Dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", promptfile.Dir, err)
	}
	tmpl := "### Model\nkimi-k2.7-code\n\n### User Prompt\nhello\n\n### Words limit\n5\n"
	if err := os.WriteFile(filepath.Join(promptfile.Dir, "greet.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi back"}}]}`))
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Step A: options are ["(без промпта)", "greet.md"] — pick "2"
		// (its Model heading overrides the built-in default model); Step T:
		// accept empty (nothing to add on top of the template).
		pw.Write([]byte("2\n\n"))
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode sent request: %v\nbody: %s", err, gotBody)
	}
	if req.Model != "kimi-k2.7-code" {
		t.Fatalf("model = %q, want the template's override %q", req.Model, "kimi-k2.7-code")
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages = %+v, want exactly one user message", req.Messages)
	}
	if !strings.Contains(req.Messages[0].Content, "hello") {
		t.Fatalf("message content = %q, want it to contain the template's user prompt", req.Messages[0].Content)
	}
	if !strings.Contains(req.Messages[0].Content, "должно быть 5") {
		t.Fatalf("message content = %q, want the Words limit sentence appended", req.Messages[0].Content)
	}

	if !strings.Contains(out.String(), "hi back") {
		t.Fatalf("expected the reply to be printed, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Параметр") {
		t.Fatalf("expected the parsed-template table to be printed, got:\n%s", out.String())
	}
}
