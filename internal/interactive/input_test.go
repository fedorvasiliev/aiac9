package interactive

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestInputStep_ReturnsTrimmedLine(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("  hello there  \n"))
	var out bytes.Buffer

	got, ok := inputStep(reader, &out, "Label")
	if !ok || got != "hello there" {
		t.Fatalf("inputStep() = (%q, %v), want (\"hello there\", true)", got, ok)
	}
}

func TestInputStep_SkipsEmptyLines(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n\nfinally\n"))
	var out bytes.Buffer

	got, ok := inputStep(reader, &out, "Label")
	if !ok || got != "finally" {
		t.Fatalf("inputStep() = (%q, %v), want (\"finally\", true)", got, ok)
	}
}

func TestInputStep_ExitStopsWizard(t *testing.T) {
	for _, word := range []string{"exit", "EXIT", "quit"} {
		reader := bufio.NewReader(strings.NewReader(word + "\n"))
		var out bytes.Buffer

		_, ok := inputStep(reader, &out, "Label")
		if ok {
			t.Fatalf("%q: expected ok = false", word)
		}
	}
}

func TestInputStep_EOFStopsWizard(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	var out bytes.Buffer

	_, ok := inputStep(reader, &out, "Label")
	if ok {
		t.Fatal("expected ok = false on immediate EOF")
	}
}
