package promptfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestList_SortsAndSkipsDotfiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.md", "a.md", ".hidden.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a.md", "b.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
}

func TestList_MissingDirIsNotAnError(t *testing.T) {
	got, err := List(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %v, want empty", got)
	}
}

func TestParse_SimpleTemplate(t *testing.T) {
	// Mirrors prompts/w1d1.prompt.md.
	p := parse("### Model\nkimi-k2.6\n\n### User Prompt\nпочему гусь свинье не товарищ?")

	if v, ok := p.Value(HeaderModel); !ok || v != "kimi-k2.6" {
		t.Fatalf("Model = (%q, %v), want (\"kimi-k2.6\", true)", v, ok)
	}
	users := p.Values(HeaderUserPrompt)
	if len(users) != 1 || users[0] != "почему гусь свинье не товарищ?" {
		t.Fatalf("User prompt = %v", users)
	}
}

func TestParse_ScalarHeadingsKeepOnlyFirstLine(t *testing.T) {
	// Mirrors prompts/w1d2-1-pure.prompt.md: "Response format" has two lines, and
	// "Words limit"/"Stop" are present but empty.
	text := "### Model\nkimi-k2.6\n\n### User Prompt\nвыведи 20 первых чисел фибоначчи\n\n" +
		"### Response format\ntext\njson_object\n\n### Words limit\n\n### Stop\n"
	p := parse(text)

	if v, ok := p.Value(HeaderResponseFormat); !ok || v != "text" {
		t.Fatalf("Response format = (%q, %v), want (\"text\", true)", v, ok)
	}
	if _, ok := p.Value(HeaderWordsLimit); ok {
		t.Fatal("Words limit should be absent when its section is empty")
	}
	if _, ok := p.Value(HeaderStop); ok {
		t.Fatal("Stop should be absent when its section is empty")
	}
}

func TestParse_NoHeadingsBecomesPlainUserPrompt(t *testing.T) {
	p := parse("just a raw prompt, no headings at all")

	users := p.Values(HeaderUserPrompt)
	if len(users) != 1 || users[0] != "just a raw prompt, no headings at all" {
		t.Fatalf("User prompt = %v", users)
	}
}

func TestParse_HeaderMatchingIsCaseInsensitive(t *testing.T) {
	p := parse("### user prompt\nhello")

	if v, ok := p.Value(HeaderUserPrompt); !ok || v != "hello" {
		t.Fatalf("Value(HeaderUserPrompt) = (%q, %v), want (\"hello\", true)", v, ok)
	}
}

func TestPrompt_Rows_SkipsEmptyValues(t *testing.T) {
	p := parse("### Model\nkimi-k2.6\n\n### Stop\n\n### User Prompt\nhi")

	rows := p.Rows()
	want := [][2]string{{"Model", "kimi-k2.6"}, {"User Prompt", "hi"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("Rows() = %v, want %v", rows, want)
	}
}

func TestParse_RealFixtureFiles(t *testing.T) {
	// Sanity check against the actual files checked into ./prompts.
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	fixturesDir := filepath.Join(root, "..", "..", "prompts")

	names, err := List(fixturesDir)
	if err != nil {
		t.Fatalf("List(%q): %v", fixturesDir, err)
	}
	if len(names) == 0 {
		t.Skip("no fixture files present under ./prompts")
	}

	for _, name := range names {
		p, err := Parse(filepath.Join(fixturesDir, name))
		if err != nil {
			t.Fatalf("Parse(%q): %v", name, err)
		}
		if len(p.Sections) == 0 {
			t.Fatalf("Parse(%q): expected at least one section", name)
		}
	}
}
