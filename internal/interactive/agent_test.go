package interactive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/promptfile"
)

func promptWithSections(sections ...promptfile.Section) *promptfile.Prompt {
	return &promptfile.Prompt{Sections: sections}
}

func TestSaveAgentFields_SavesProfileAndInvariants(t *testing.T) {
	store := openTestStore(t)
	tmpl := promptWithSections(
		promptfile.Section{Header: promptfile.HeaderProfile, Value: "Ты полезный ассистент."},
		promptfile.Section{Header: promptfile.HeaderInvariants, Value: "Всегда отвечай на русском."},
	)

	var out bytes.Buffer
	saveAgentFields(store, tmpl, &out)

	profile, invariants, err := store.Agent()
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if profile != "Ты полезный ассистент." {
		t.Fatalf("profile = %q", profile)
	}
	if invariants != "Всегда отвечай на русском." {
		t.Fatalf("invariants = %q", invariants)
	}
}

func TestSaveAgentFields_NoClearsProfile(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetAgentProfile("старый профиль"); err != nil {
		t.Fatalf("SetAgentProfile: %v", err)
	}

	tmpl := promptWithSections(promptfile.Section{Header: promptfile.HeaderProfile, Value: "no"})
	var out bytes.Buffer
	saveAgentFields(store, tmpl, &out)

	profile, _, err := store.Agent()
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if profile != "" {
		t.Fatalf("profile = %q, want cleared", profile)
	}
	if !strings.Contains(out.String(), "стёрт") {
		t.Fatalf("output = %q, want a note that the profile was cleared", out.String())
	}
}

func TestSaveAgentFields_NoClearsInvariantsCaseInsensitively(t *testing.T) {
	store := openTestStore(t)
	if err := store.SetAgentInvariants("старые инварианты"); err != nil {
		t.Fatalf("SetAgentInvariants: %v", err)
	}

	tmpl := promptWithSections(promptfile.Section{Header: promptfile.HeaderInvariants, Value: "  NO  "})
	var out bytes.Buffer
	saveAgentFields(store, tmpl, &out)

	_, invariants, err := store.Agent()
	if err != nil {
		t.Fatalf("Agent: %v", err)
	}
	if invariants != "" {
		t.Fatalf("invariants = %q, want cleared", invariants)
	}
}

func TestSaveAgentFields_NilStoreOrTemplateIsANoOp(t *testing.T) {
	var out bytes.Buffer
	tmpl := promptWithSections(promptfile.Section{Header: promptfile.HeaderProfile, Value: "x"})
	saveAgentFields(nil, tmpl, &out) // must not panic
	saveAgentFields(openTestStore(t), nil, &out)
}
