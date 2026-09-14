package interactive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
	"github.com/fedorvasiliev/aiac9/internal/exchangelog"
	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

func TestRun_ExitsCleanlyOnImmediateEOF(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate the dialog SQLite file this run opens

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
	if !strings.Contains(out.String(), "Dialog") {
		t.Fatalf("expected the Step D select prompt to have been printed, got:\n%s", out.String())
	}
}

func TestRun_ReportsMissingAPIKeyAndLoopsBackToSelect(t *testing.T) {
	// Isolate the dialog SQLite file this run opens; the fresh temp dir
	// also has no ./prompts, so Step A only offers "no prompt".
	t.Chdir(t.TempDir())

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Step D: accept the default ("/new"); Step A: accept the default
		// (no template); Step T: a prompt. No MOONSHOT_API_KEY is
		// configured, so Complete fails locally without any network call,
		// Run reports it, and loops back to Step A — where this pipe's EOF
		// then ends the wizard.
		pw.Write([]byte("\n\nkakoy segodnya den?\n"))
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
	t.Chdir(t.TempDir()) // isolate the dialog SQLite file this run opens

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
		// Step D: accept the default ("/new"); Step A: accept the default
		// ("no prompt"); Step T: a plain prompt.
		pw.Write([]byte("\n\nhello there\n"))
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

func TestRun_PrintsPerRequestAndDialogTokenCountsAndDuration(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate the dialog SQLite file this run opens

	responses := []string{
		`{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`{"choices":[{"message":{"role":"assistant","content":"hi again"}}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`,
	}
	i := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responses[i]))
		i++
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		pw.Write([]byte("\n"))          // Step D default
		pw.Write([]byte("\n"))          // Step A default, turn 1
		pw.Write([]byte("hello\n"))     // Step T, turn 1
		pw.Write([]byte("\n"))          // Step A default, turn 2
		pw.Write([]byte("hello two\n")) // Step T, turn 2
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "prompt_tokens: 10, completion_tokens: 5") {
		t.Fatalf("expected turn 1's own prompt_tokens/completion_tokens to be printed, got:\n%s", got)
	}
	if !strings.Contains(got, "dialog prompt_tokens: 10, dialog completion_tokens: 5") {
		t.Fatalf("expected turn 1's dialog-cumulative totals to equal its own counts, got:\n%s", got)
	}
	if !strings.Contains(got, "prompt_tokens: 20, completion_tokens: 8") {
		t.Fatalf("expected turn 2's own prompt_tokens/completion_tokens to be printed, got:\n%s", got)
	}
	if !strings.Contains(got, "dialog prompt_tokens: 30, dialog completion_tokens: 13") {
		t.Fatalf("expected turn 2's dialog-cumulative totals to add turn 1's, got:\n%s", got)
	}
	// "с точностью до сотых долей секунды" — hundredths of a second, i.e.
	// exactly two decimal places, e.g. "0.00s".
	if !regexp.MustCompile(`время выполнения: \d+\.\d\ds`).MatchString(got) {
		t.Fatalf("expected the request duration printed to hundredths of a second, got:\n%s", got)
	}
	if !strings.Contains(got, "hi\n\nprompt_tokens:") {
		t.Fatalf("expected a blank line before prompt_tokens/dialog totals, got:\n%s", got)
	}
}

func TestRun_AccumulatesDialogHistoryAcrossTurns(t *testing.T) {
	t.Chdir(t.TempDir())

	var gotBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBodies = append(gotBodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply"}}]}`))
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		pw.Write([]byte("\n"))               // Step D: default ("/new")
		pw.Write([]byte("\n"))               // Step A turn 1: default (no template)
		pw.Write([]byte("first question\n")) // Step T turn 1
		pw.Write([]byte("\n"))               // Step A turn 2: default
		pw.Write([]byte("second question\n"))
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(gotBodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(gotBodies))
	}

	// The second request must fold turn 1's question and reply into one
	// system message (CLAUDE.md's Step D: "обогатить ей system prompt"),
	// ahead of the new turn's own user message.
	var req2 struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBodies[1], &req2); err != nil {
		t.Fatalf("decode second request: %v", err)
	}
	if len(req2.Messages) != 2 {
		t.Fatalf("second request messages = %+v, want exactly 2 (context system message + new user message)", req2.Messages)
	}
	if req2.Messages[0].Role != "system" || !strings.Contains(req2.Messages[0].Content, "first question") || !strings.Contains(req2.Messages[0].Content, "reply") {
		t.Fatalf("second request messages[0] = %+v, want a system message containing turn 1's question and reply", req2.Messages[0])
	}
	if req2.Messages[1].Role != "user" || req2.Messages[1].Content != "second question" {
		t.Fatalf("second request messages[1] = %+v, want {user, \"second question\"}", req2.Messages[1])
	}
}

func TestRun_ClearCommandResetsDialogHistory(t *testing.T) {
	t.Chdir(t.TempDir())

	var gotBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBodies = append(gotBodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply"}}]}`))
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		pw.Write([]byte("\n"))               // Step D (dialog 1): default ("/new")
		pw.Write([]byte("\n"))               // Step A (dialog 1): default
		pw.Write([]byte("first question\n")) // Step T (dialog 1)
		pw.Write([]byte("\n"))               // Step A (dialog 1), 2nd turn: default
		pw.Write([]byte("/clear\n"))         // Step T (dialog 1), 2nd turn: reset
		pw.Write([]byte("\n"))               // Step D (dialog 2): default ("/new")
		pw.Write([]byte("\n"))               // Step A (dialog 2): default
		pw.Write([]byte("second question\n"))
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if !strings.Contains(out.String(), "начат новый диалог") {
		t.Fatalf("expected a confirmation that a new dialog started, got:\n%s", out.String())
	}
	if len(gotBodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(gotBodies))
	}

	var req2 struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBodies[1], &req2); err != nil {
		t.Fatalf("decode second request: %v", err)
	}
	if len(req2.Messages) != 1 || req2.Messages[0].Content != "second question" {
		t.Fatalf("expected /clear to reset the dialog context, second request messages = %+v", req2.Messages)
	}
}

func TestRun_StickyFactsExtractsAndPersistsFacts(t *testing.T) {
	t.Chdir(t.TempDir())

	var gotBodies [][]byte
	responses := []string{
		`{"choices":[{"message":{"role":"assistant","content":"Привет, Федя!\n\nФакты:\nимя: Федя"}}]}`,
		`{"choices":[{"message":{"role":"assistant","content":"Тебе 41."}}]}`,
	}
	i := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBodies = append(gotBodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responses[i]))
		i++
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		pw.Write([]byte("\n"))                 // Step D default
		pw.Write([]byte("\n"))                 // Step A turn 1
		pw.Write([]byte("меня зовут Федя\n"))  // Step T turn 1
		pw.Write([]byte("\n"))                 // Step A turn 2
		pw.Write([]byte("сколько мне лет?\n")) // Step T turn 2
	}()

	cfg := &config.Config{
		MoonshotAPIKey:  "secret",
		MoonshotBaseURL: server.URL,
		ContextStrategy: "STICKY_FACTS",
	}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Привет, Федя!") {
		t.Fatalf("expected the reply's body (facts section stripped) to be printed, got:\n%s", got)
	}
	if strings.Contains(got, "Факты:") {
		t.Fatalf("expected the facts section to be stripped from the printed reply, got:\n%s", got)
	}

	if len(gotBodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(gotBodies))
	}
	var req2 struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBodies[1], &req2); err != nil {
		t.Fatalf("decode second request: %v", err)
	}

	var sawFactsContext, sawFactsInstruction bool
	for _, m := range req2.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "имя: Федя") {
			sawFactsContext = true
		}
		if m.Role == "system" && strings.Contains(m.Content, "Факты:") {
			sawFactsInstruction = true
		}
	}
	if !sawFactsContext {
		t.Fatalf("expected the second request to include the extracted fact as system context, got messages: %+v", req2.Messages)
	}
	if !sawFactsInstruction {
		t.Fatalf("expected every request to include the facts-extraction instruction, got messages: %+v", req2.Messages)
	}

	db, err := sql.Open("sqlite", dialogstore.DefaultPath)
	if err != nil {
		t.Fatalf("open dialog db: %v", err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM facts WHERE key = 'имя'`).Scan(&value); err != nil {
		t.Fatalf("query saved fact: %v", err)
	}
	if value != "Федя" {
		t.Fatalf("saved fact value = %q, want %q", value, "Федя")
	}
}

func TestRun_PersistsMessagesToDialogStore(t *testing.T) {
	t.Chdir(t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply"}}]}`))
	}))
	defer server.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		pw.Write([]byte("\n\nhello\nexit\n")) // Step D default, Step A default, Step T, then "exit" at Step A
	}()

	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, pr, &out, Options{}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	db, err := sql.Open("sqlite", dialogstore.DefaultPath)
	if err != nil {
		t.Fatalf("open dialog db: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT role, content FROM dialog ORDER BY id`)
	if err != nil {
		t.Fatalf("query dialog table: %v", err)
	}
	defer rows.Close()

	var got [][2]string
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, [2]string{role, content})
	}
	want := [][2]string{{"user", "hello"}, {"assistant", "reply"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("dialog rows = %v, want %v", got, want)
	}
}

func TestRun_ResumingExistingDialogSeedsContextAndPrintsHistory(t *testing.T) {
	t.Chdir(t.TempDir()) // shared aiac9.db across both Run calls below

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply-1"}}],"usage":{"prompt_tokens":10,"completion_tokens":4}}`))
	}))
	defer server.Close()
	cfg := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server.URL}

	// First run: a fresh dialog with one turn, then exit — leaves one row
	// behind in ./aiac9.db for Step D to list next time.
	pr1, pw1, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	go func() {
		defer pw1.Close()
		pw1.Write([]byte("\n"))           // Step D: default ("/new")
		pw1.Write([]byte("\n"))           // Step A: default
		pw1.Write([]byte("question-1\n")) // Step T
		pw1.Write([]byte("exit\n"))       // Step A, next turn: exit
	}()
	var out1 bytes.Buffer
	if err := Run(context.Background(), cfg, pr1, &out1, Options{}); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	pr1.Close()

	// Second run: Step D now lists that dialog as option "2" (after
	// "/new"); resuming it should print its last Q&A and seed context for
	// the next request.
	var gotBody []byte
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply-2"}}],"usage":{"prompt_tokens":6,"completion_tokens":3}}`))
	}))
	defer server2.Close()
	cfg2 := &config.Config{MoonshotAPIKey: "secret", MoonshotBaseURL: server2.URL}

	pr2, pw2, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr2.Close()
	go func() {
		defer pw2.Close()
		pw2.Write([]byte("2\n"))          // Step D: resume the existing dialog
		pw2.Write([]byte("\n"))           // Step A: default
		pw2.Write([]byte("question-2\n")) // Step T
	}()

	var out2 bytes.Buffer
	if err := Run(context.Background(), cfg2, pr2, &out2, Options{}); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	got := out2.String()
	if !strings.Contains(got, "Вопрос:") || !strings.Contains(got, "question-1") {
		t.Fatalf("expected the resumed dialog's last question to be printed, got:\n%s", got)
	}
	if !strings.Contains(got, "Ответ:") || !strings.Contains(got, "reply-1") {
		t.Fatalf("expected the resumed dialog's last answer to be printed, got:\n%s", got)
	}

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode request: %v\nbody: %s", err, gotBody)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v, want a leading system message plus the new user message", req.Messages)
	}
	if !strings.Contains(req.Messages[0].Content, "question-1") || !strings.Contains(req.Messages[0].Content, "reply-1") {
		t.Fatalf("system message = %q, want it to contain the resumed dialog's full history", req.Messages[0].Content)
	}
	if req.Messages[1].Content != "question-2" {
		t.Fatalf("messages[1] = %+v, want the new turn's own question", req.Messages[1])
	}

	// The dialog-cumulative totals must be seeded from the resumed
	// dialog's persisted history (10/4 from turn 1) before adding this
	// turn's own counts (6/3) — not start back at 0.
	if !strings.Contains(got, "dialog prompt_tokens: 16, dialog completion_tokens: 7") {
		t.Fatalf("expected dialog token totals seeded from the resumed history, got:\n%s", got)
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

	go func() {
		defer pw.Close()
		pw.Write([]byte("\n")) // Step D: default ("/new"), then EOF at Step A — just inspect its printed list
	}()

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
	t.Chdir(t.TempDir()) // isolate the dialog SQLite file this run opens

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
		pw.Write([]byte("\n\nhello\nexit\n")) // Step D default, Step A default, Step T, then "exit" at Step A
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
		pw.Write([]byte("\n2\n\n")) // Step D default, Step A pick #2, Step T empty
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
		pw.Write([]byte("\n2\n\n")) // Step D default, Step A pick #2, Step T empty
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
