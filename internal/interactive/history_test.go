package interactive

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/config"
	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
)

func TestGroupIntoTurns_PairsRequestWithReply(t *testing.T) {
	history := []dialogstore.Message{
		{ID: 1, Role: "user", Content: "q1"},
		{ID: 2, Role: "assistant", Content: "a1"},
		{ID: 3, Role: "system", Content: "sys"},
		{ID: 4, Role: "user", Content: "q2"},
		{ID: 5, Role: "assistant", Content: "a2"},
	}

	turns := groupIntoTurns(history)
	if len(turns) != 2 {
		t.Fatalf("groupIntoTurns() = %+v, want 2 turns", turns)
	}
	if len(turns[0].request) != 1 || turns[0].request[0].Content != "q1" || turns[0].reply.Content != "a1" {
		t.Fatalf("turn 0 = %+v, want request=[q1] reply=a1", turns[0])
	}
	if len(turns[1].request) != 2 || turns[1].reply.Content != "a2" {
		t.Fatalf("turn 1 = %+v, want request=[sys,q2] reply=a2", turns[1])
	}
}

func TestGroupIntoTurns_DropsDanglingRequestWithNoReply(t *testing.T) {
	history := []dialogstore.Message{
		{ID: 1, Role: "user", Content: "q1"},
		{ID: 2, Role: "assistant", Content: "a1"},
		{ID: 3, Role: "user", Content: "q2, no reply yet"},
	}

	turns := groupIntoTurns(history)
	if len(turns) != 1 {
		t.Fatalf("groupIntoTurns() = %+v, want 1 complete turn (dangling request dropped)", turns)
	}
}

func TestTurn_MessageIDs(t *testing.T) {
	tn := turn{
		request: []dialogstore.Message{{ID: 1}, {ID: 2}},
		reply:   dialogstore.Message{ID: 3},
	}
	got := tn.messageIDs()
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("messageIDs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("messageIDs() = %v, want %v", got, want)
		}
	}
}

func TestTurn_UserText_JoinsOnlyUserMessages(t *testing.T) {
	tn := turn{request: []dialogstore.Message{
		{Role: "system", Content: "ignored"},
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
	}}
	got := tn.userText()
	want := "first\nsecond"
	if got != want {
		t.Fatalf("userText() = %q, want %q", got, want)
	}
}

func makeHistory(n int) []dialogstore.Message {
	var history []dialogstore.Message
	var id int64
	for i := 0; i < n; i++ {
		id++
		history = append(history, dialogstore.Message{ID: id, Role: "user", Content: "q"})
		id++
		history = append(history, dialogstore.Message{ID: id, Role: "assistant", Content: "a"})
	}
	return history
}

func TestEnforceHistoryLimit_NonPositiveCountMeansNoCap(t *testing.T) {
	history := makeHistory(5)
	var out bytes.Buffer

	summary, kept := enforceHistoryLimit(context.Background(), &config.Config{HistoryMsgCount: 0}, nil, "dlg-1", history, &out)
	if summary != "" || len(kept) != 5 {
		t.Fatalf("enforceHistoryLimit(count=0) = (%q, %d turns), want all 5 turns kept", summary, len(kept))
	}
}

func TestEnforceHistoryLimit_UnderLimitKeepsEverything(t *testing.T) {
	history := makeHistory(3)
	var out bytes.Buffer

	_, kept := enforceHistoryLimit(context.Background(), &config.Config{HistoryMsgCount: 10}, nil, "dlg-1", history, &out)
	if len(kept) != 3 {
		t.Fatalf("kept = %d turns, want all 3", len(kept))
	}
}

func TestEnforceHistoryLimit_SlidingWindowHardDeletesOverflowNoSummarization(t *testing.T) {
	dir := t.TempDir()
	store, err := dialogstore.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var summarizationCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		summarizationCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"should not be called"}}]}`))
	}))
	defer server.Close()

	for i := 0; i < 5; i++ {
		store.AppendMessage("dlg-1", "user", "q", "kimi-k2.6", nil, nil)
		store.AppendMessage("dlg-1", "assistant", "a", "kimi-k2.6", nil, nil)
	}
	history, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	var out bytes.Buffer
	cfg := &config.Config{
		HistoryMsgCount:      2,
		ContextStrategy:      config.ContextStrategySlidingWindow,
		HistorySummarization: true, // must be ignored: Sliding_Window never summarizes
		MoonshotAPIKey:       "secret",
		MoonshotBaseURL:      server.URL,
	}
	summary, kept := enforceHistoryLimit(context.Background(), cfg, store, "dlg-1", history, &out)

	if summary != "" {
		t.Fatalf("summary = %q, want empty (Sliding_Window never summarizes)", summary)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d turns, want 2 (the cap)", len(kept))
	}
	if summarizationCalls != 0 {
		t.Fatalf("summarizationCalls = %d, want 0 (no LLM call for Sliding_Window)", summarizationCalls)
	}

	remaining, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History (after): %v", err)
	}
	if len(remaining) != 4 { // 2 kept turns * 2 rows each
		t.Fatalf("remaining rows = %d, want 4 (overflow hard-deleted)", len(remaining))
	}
}

func TestEnforceHistoryLimit_StickyFactsAlsoHardDeletesOverflowNoSummarization(t *testing.T) {
	dir := t.TempDir()
	store, err := dialogstore.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var summarizationCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		summarizationCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"should not be called"}}]}`))
	}))
	defer server.Close()

	for i := 0; i < 5; i++ {
		store.AppendMessage("dlg-1", "user", "q", "kimi-k2.6", nil, nil)
		store.AppendMessage("dlg-1", "assistant", "a", "kimi-k2.6", nil, nil)
	}
	history, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	var out bytes.Buffer
	cfg := &config.Config{
		HistoryMsgCount:      2,
		ContextStrategy:      config.ContextStrategyStickyFacts,
		HistorySummarization: true, // must be ignored, same as Sliding_Window
		MoonshotAPIKey:       "secret",
		MoonshotBaseURL:      server.URL,
	}
	summary, kept := enforceHistoryLimit(context.Background(), cfg, store, "dlg-1", history, &out)

	if summary != "" {
		t.Fatalf("summary = %q, want empty (STICKY_FACTS never summarizes)", summary)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d turns, want 2 (the cap)", len(kept))
	}
	if summarizationCalls != 0 {
		t.Fatalf("summarizationCalls = %d, want 0 (no LLM call for STICKY_FACTS trimming)", summarizationCalls)
	}

	remaining, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History (after): %v", err)
	}
	if len(remaining) != 4 { // 2 kept turns * 2 rows each
		t.Fatalf("remaining rows = %d, want 4 (overflow hard-deleted)", len(remaining))
	}
}

func TestEnforceHistoryLimit_SlidingWindowIsCaseInsensitive(t *testing.T) {
	history := makeHistory(5)
	var out bytes.Buffer

	_, kept := enforceHistoryLimit(context.Background(), &config.Config{
		HistoryMsgCount: 2,
		ContextStrategy: "sliding_window", // lowercase, per CLAUDE.md's own inconsistent casing across sections
	}, nil, "dlg-1", history, &out)
	if len(kept) != 2 {
		t.Fatalf("kept = %d turns, want 2", len(kept))
	}
}

func TestEnforceHistoryLimit_OverLimitWithoutSummarizationJustTrims(t *testing.T) {
	dir := t.TempDir()
	store, err := dialogstore.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	for i := 0; i < 5; i++ {
		promptTokens := 1
		store.AppendMessage("dlg-1", "user", "q", "kimi-k2.6", &promptTokens, nil)
		completionTokens := 1
		store.AppendMessage("dlg-1", "assistant", "a", "kimi-k2.6", nil, &completionTokens)
	}
	history, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	var out bytes.Buffer
	cfg := &config.Config{HistoryMsgCount: 2, HistorySummarization: false}
	summary, kept := enforceHistoryLimit(context.Background(), cfg, store, "dlg-1", history, &out)

	if summary != "" {
		t.Fatalf("summary = %q, want empty (summarization is off)", summary)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d turns, want 2 (the cap)", len(kept))
	}

	// Nothing should have been deleted from the database.
	remaining, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History (after): %v", err)
	}
	if len(remaining) != len(history) {
		t.Fatalf("remaining rows = %d, want all %d still present (summarization is off, so nothing is deleted)", len(remaining), len(history))
	}
}

func TestLastUsedModel_ReturnsMostRecentTurnsModel(t *testing.T) {
	history := []dialogstore.Message{
		{Role: "user", Content: "q1", Model: "kimi-k2.6"},
		{Role: "assistant", Content: "a1", Model: "kimi-k2.6"},
		{Role: "user", Content: "q2", Model: "deepseek-chat"},
		{Role: "assistant", Content: "a2", Model: "deepseek-chat"},
	}
	if got := lastUsedModel(history); got != "deepseek-chat" {
		t.Fatalf("lastUsedModel() = %q, want %q (the most recent turn's model)", got, "deepseek-chat")
	}
}

func TestLastUsedModel_FallsBackToDefaultForEmptyOrModellessHistory(t *testing.T) {
	if got := lastUsedModel(nil); got != defaultModel {
		t.Fatalf("lastUsedModel(nil) = %q, want the default %q", got, defaultModel)
	}
	legacy := []dialogstore.Message{{Role: "user", Content: "q"}} // Model == "" (pre-upgrade row)
	if got := lastUsedModel(legacy); got != defaultModel {
		t.Fatalf("lastUsedModel(legacy row with no Model) = %q, want the default %q", got, defaultModel)
	}
}

func TestEnforceHistoryLimit_SummarizesWithDialogsLastUsedModelNotTheDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := dialogstore.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var gotModels []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		if err := json.Unmarshal(body, &req); err == nil {
			gotModels = append(gotModels, req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"summary"}}]}`))
	}))
	defer server.Close()

	// This dialog's turns all used a non-default model.
	for i := 0; i < 5; i++ {
		store.AppendMessage("dlg-1", "user", "q", "deepseek-chat", nil, nil)
		store.AppendMessage("dlg-1", "assistant", "a", "deepseek-chat", nil, nil)
	}
	history, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	var out bytes.Buffer
	cfg := &config.Config{
		HistoryMsgCount:      2,
		HistorySummarization: true,
		DeepSeekAPIKey:       "secret",
		DeepSeekBaseURL:      server.URL,
	}
	enforceHistoryLimit(context.Background(), cfg, store, "dlg-1", history, &out)

	if len(gotModels) != 3 {
		t.Fatalf("got %d summarization calls, want 3", len(gotModels))
	}
	for _, m := range gotModels {
		if m != "deepseek-chat" {
			t.Fatalf("summarization request model = %q, want the dialog's own last-used model %q, not the built-in default", m, "deepseek-chat")
		}
	}
}

func TestEnforceHistoryLimit_OverLimitWithSummarizationFoldsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	store, err := dialogstore.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var gotPrompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotPrompts = append(gotPrompts, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"a compact summary"}}]}`))
	}))
	defer server.Close()

	for i := 0; i < 5; i++ {
		store.AppendMessage("dlg-1", "user", "q", "kimi-k2.6", nil, nil)
		store.AppendMessage("dlg-1", "assistant", "a", "kimi-k2.6", nil, nil)
	}
	history, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}

	var out bytes.Buffer
	cfg := &config.Config{
		HistoryMsgCount:      2,
		HistorySummarization: true,
		MoonshotAPIKey:       "secret",
		MoonshotBaseURL:      server.URL,
	}
	summary, kept := enforceHistoryLimit(context.Background(), cfg, store, "dlg-1", history, &out)

	if summary != "a compact summary" {
		t.Fatalf("summary = %q, want the LLM's reply", summary)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d turns, want 2 (the cap)", len(kept))
	}
	if len(gotPrompts) != 3 {
		t.Fatalf("got %d summarization calls, want 3 (one per overflow turn: 5-2)", len(gotPrompts))
	}
	if !bytes.Contains([]byte(gotPrompts[0]), []byte("user message - q")) {
		t.Fatalf("summarization prompt = %q, want it to follow CLAUDE.md's template", gotPrompts[0])
	}

	// The overflow turns must be gone from the database; only the kept
	// ones remain.
	remaining, err := store.History("dlg-1")
	if err != nil {
		t.Fatalf("History (after): %v", err)
	}
	if len(remaining) != 4 { // 2 kept turns * 2 rows each
		t.Fatalf("remaining rows = %d, want 4 (only the 2 kept turns)", len(remaining))
	}

	// The summary must also have been persisted, so a later resume picks
	// it back up.
	saved, err := store.Summary("dlg-1")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if saved != "a compact summary" {
		t.Fatalf("persisted summary = %q, want it saved to dialog_summary", saved)
	}
}
