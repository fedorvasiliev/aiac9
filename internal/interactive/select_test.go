package interactive

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

var testOptions = []string{"a", "b", "c"}

const testDefaultIndex = 2 // "c"

func TestSelectFallback_EmptyLineAcceptsDefault(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer

	got, ok := selectFallback(reader, &out, testOptions, testDefaultIndex)
	if !ok || got != "c" {
		t.Fatalf("selectFallback() = (%q, %v), want (\"c\", true)", got, ok)
	}
}

func TestSelectFallback_ByNumber(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("2\n"))
	var out bytes.Buffer

	got, ok := selectFallback(reader, &out, testOptions, testDefaultIndex)
	if !ok || got != "b" {
		t.Fatalf("selectFallback() = (%q, %v), want (\"b\", true)", got, ok)
	}
}

func TestSelectFallback_ByName(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("A\n")) // case-insensitive match
	var out bytes.Buffer

	got, ok := selectFallback(reader, &out, testOptions, testDefaultIndex)
	if !ok || got != "a" {
		t.Fatalf("selectFallback() = (%q, %v), want (\"a\", true)", got, ok)
	}
}

func TestSelectFallback_ReprocessesAfterInvalidInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("nope\n1\n"))
	var out bytes.Buffer

	got, ok := selectFallback(reader, &out, testOptions, testDefaultIndex)
	if !ok || got != "a" {
		t.Fatalf("selectFallback() = (%q, %v), want (\"a\", true)", got, ok)
	}
	if !strings.Contains(out.String(), "не понял выбор") {
		t.Fatalf("expected an error message for invalid input, got:\n%s", out.String())
	}
}

func TestSelectFallback_EOFWithNoInputStopsWizard(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	var out bytes.Buffer

	_, ok := selectFallback(reader, &out, testOptions, testDefaultIndex)
	if ok {
		t.Fatal("expected ok = false on immediate EOF")
	}
}
