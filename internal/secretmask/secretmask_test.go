package secretmask

import "testing"

func TestMask_ReplacesRegisteredSecrets(t *testing.T) {
	m := New("sk-abc123", "other-secret")

	got := m.Mask("Authorization: Bearer sk-abc123, note: other-secret too")
	want := "Authorization: Bearer ***, note: *** too"
	if got != want {
		t.Fatalf("Mask() = %q, want %q", got, want)
	}
}

func TestMask_LeavesUnrelatedTextAlone(t *testing.T) {
	m := New("sk-abc123")

	got := m.Mask("nothing secret here")
	if got != "nothing secret here" {
		t.Fatalf("Mask() = %q, want unchanged text", got)
	}
}

func TestNew_IgnoresEmptySecrets(t *testing.T) {
	m := New("", "sk-abc123")

	// An empty registered secret must not turn into "insert *** between
	// every character" via strings.ReplaceAll(s, "", "***").
	got := m.Mask("hello world")
	if got != "hello world" {
		t.Fatalf("Mask() = %q, want unchanged text (empty secret must be ignored)", got)
	}
}
