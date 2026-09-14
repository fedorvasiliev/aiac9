package interactive

import (
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/dialogstore"
)

func TestExtractFacts_SplitsBodyAndFacts(t *testing.T) {
	content := "Привет, Федя!\n\nФакты:\nимя: Федя\nвозраст: 41"

	body, facts := extractFacts(content)
	if body != "Привет, Федя!" {
		t.Fatalf("body = %q, want %q", body, "Привет, Федя!")
	}
	want := []dialogstore.Fact{{Key: "имя", Value: "Федя"}, {Key: "возраст", Value: "41"}}
	if len(facts) != len(want) || facts[0] != want[0] || facts[1] != want[1] {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
}

func TestExtractFacts_HeaderMatchIsCaseInsensitive(t *testing.T) {
	content := "ответ\n\nФАКТЫ:\nключ: значение"

	body, facts := extractFacts(content)
	if body != "ответ" {
		t.Fatalf("body = %q, want %q", body, "ответ")
	}
	if len(facts) != 1 || facts[0] != (dialogstore.Fact{Key: "ключ", Value: "значение"}) {
		t.Fatalf("facts = %+v, want one fact {ключ значение}", facts)
	}
}

func TestExtractFacts_NoSectionReturnsContentUnchanged(t *testing.T) {
	content := "just a plain reply, no facts section"

	body, facts := extractFacts(content)
	if body != content {
		t.Fatalf("body = %q, want unchanged content", body)
	}
	if facts != nil {
		t.Fatalf("facts = %+v, want nil", facts)
	}
}

func TestExtractFacts_SkipsMalformedLines(t *testing.T) {
	content := "reply\n\nФакты:\nvalid: yes\nno colon here\nempty-value: "

	_, facts := extractFacts(content)
	if len(facts) != 1 || facts[0] != (dialogstore.Fact{Key: "valid", Value: "yes"}) {
		t.Fatalf("facts = %+v, want only the one well-formed fact", facts)
	}
}
