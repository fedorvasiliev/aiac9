package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/llm"
	"github.com/fedorvasiliev/aiac9/internal/secretmask"
)

func TestValidateInvariants_NoInvariantsSkipsRequest(t *testing.T) {
	t.Chdir(t.TempDir())
	store := openTestStore(t)

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	var out bytes.Buffer
	client := llm.NewClient(server.URL, "key")
	validateInvariants(context.Background(), store, client, "kimi-k3", "some answer", secretmask.New(), &out)

	if called {
		t.Fatal("validateInvariants made an HTTP request with no invariants set, want none")
	}
	if !strings.Contains(out.String(), "не заданы") {
		t.Fatalf("output = %q, want a note that invariants aren't set", out.String())
	}
}

func TestValidateInvariants_SendsRequestAndPrintsVerdict(t *testing.T) {
	t.Chdir(t.TempDir())
	store := openTestStore(t)
	if err := store.SetAgentInvariants("никогда не выдумывай факты"); err != nil {
		t.Fatalf("SetAgentInvariants: %v", err)
	}

	var gotMessages []llm.Message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMessages = req.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	client := llm.NewClient(server.URL, "key")
	validateInvariants(context.Background(), store, client, "kimi-k3", "some answer", secretmask.New(), &out)

	if len(gotMessages) != 2 {
		t.Fatalf("sent %d messages, want 2 (system + user)", len(gotMessages))
	}
	if !strings.Contains(gotMessages[0].Content, "никогда не выдумывай факты") {
		t.Fatalf("system message = %q, want it to contain the saved invariants", gotMessages[0].Content)
	}
	if !strings.Contains(gotMessages[1].Content, "some answer") {
		t.Fatalf("user message = %q, want it to contain the response being checked", gotMessages[1].Content)
	}
	if !strings.Contains(out.String(), "OK") {
		t.Fatalf("output = %q, want the verdict printed", out.String())
	}
}

func TestConsoleCommand_InvariantsPrintsSavedValue(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetAgentInvariants("всегда отвечай на русском"); err != nil {
		t.Fatalf("SetAgentInvariants: %v", err)
	}

	var out bytes.Buffer
	if !consoleCommand(store, &out, secretmask.New(), "/invariants") {
		t.Fatal("consoleCommand(\"/invariants\") = false, want true")
	}
	if !strings.Contains(out.String(), "всегда отвечай на русском") {
		t.Fatalf("output = %q, want it to contain the saved invariants", out.String())
	}
}

func TestConsoleCommand_InvariantsUnsetReportsSo(t *testing.T) {
	store := openTestStore(t)
	var out bytes.Buffer

	if !consoleCommand(store, &out, secretmask.New(), "/invariants") {
		t.Fatal("consoleCommand(\"/invariants\") = false, want true")
	}
	if !strings.Contains(out.String(), "не заданы") {
		t.Fatalf("output = %q, want a note that no invariants are set", out.String())
	}
}
