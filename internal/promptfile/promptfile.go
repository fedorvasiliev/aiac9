// Package promptfile discovers and parses pre-filled prompt templates from
// ./prompts, at runtime (never at compile time) — CLAUDE.md's "Шаг A" and
// "Парсинг заготовленных промптов".
//
// A template is a plain-text file with Markdown-style headings ("# Header",
// "## Header", ...); everything up to the next heading (or EOF) is that
// heading's value. Recognized headings: Model, System prompt, User prompt,
// Response format, Words limit, Stop, Temperature — matched case-insensitively, so
// "### User Prompt" and "### user prompt" are equivalent. A file with no
// heading at all is treated as a single implicit User prompt holding the
// whole file.
package promptfile

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

// Dir is the default directory prompt templates are discovered in.
const Dir = "prompts"

// Recognized heading names, in their canonical display casing.
const (
	HeaderModel          = "Model"
	HeaderSystemPrompt   = "System prompt"
	HeaderUserPrompt     = "User prompt"
	HeaderResponseFormat = "Response format"
	HeaderWordsLimit     = "Words limit"
	HeaderStop           = "Stop"
	HeaderTemperature    = "Temperature"
)

// Section is one heading and its value, in the order parsed from the file.
type Section struct {
	Header string // exactly as written in the file, e.g. "User Prompt"
	Value  string // trimmed; may be multi-line for System/User prompt
}

// Prompt is a parsed template file.
type Prompt struct {
	Sections []Section
}

// List returns the file names found directly under dir, sorted, skipping
// subdirectories and dotfiles. A missing directory is not an error — it
// yields an empty list, since having no prompts folder at all is valid.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

var headingRe = regexp.MustCompile(`^#+\s*(.+?)\s*$`)

// Parse reads and parses the prompt template at path.
func Parse(path string) (*Prompt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(string(data)), nil
}

func parse(text string) *Prompt {
	var sections []Section
	var header string
	var buf strings.Builder
	haveHeading := false

	flush := func() {
		if haveHeading {
			sections = append(sections, Section{Header: header, Value: strings.TrimSpace(buf.String())})
		}
		buf.Reset()
	}

	for _, line := range strings.Split(text, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			flush()
			header = m[1]
			haveHeading = true
			continue
		}
		if haveHeading {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()

	if len(sections) == 0 {
		// No headings at all: treat the whole file as a plain user prompt.
		if v := strings.TrimSpace(text); v != "" {
			sections = append(sections, Section{Header: HeaderUserPrompt, Value: v})
		}
	}

	return &Prompt{Sections: sections}
}

// isScalar reports whether header holds a single value rather than
// free-form message text — everything except System/User prompt.
func isScalar(header string) bool {
	return !strings.EqualFold(header, HeaderSystemPrompt) && !strings.EqualFold(header, HeaderUserPrompt)
}

// firstLine returns s up to its first newline, trimmed.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// display returns a section's usable value: scalar headings (Model,
// Response format, Words limit, Stop) keep only their first non-empty
// line — they hold one setting, not a paragraph, so any further lines are
// almost certainly leftover placeholder text. System/User prompt keep the
// full paragraph.
func display(header, value string) string {
	if isScalar(header) {
		return firstLine(value)
	}
	return value
}

// Values returns every non-empty value for sections matching header
// (case-insensitively), in file order.
func (p *Prompt) Values(header string) []string {
	var vals []string
	for _, s := range p.Sections {
		if !strings.EqualFold(s.Header, header) {
			continue
		}
		if v := display(s.Header, s.Value); v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}

// Value returns the first non-empty value for header, if any.
func (p *Prompt) Value(header string) (string, bool) {
	vals := p.Values(header)
	if len(vals) == 0 {
		return "", false
	}
	return vals[0], true
}

// Rows returns one (heading, value) pair per section, in file order,
// skipping sections whose usable value is empty — for rendering the
// "что получилось после парсинга" table.
func (p *Prompt) Rows() [][2]string {
	var rows [][2]string
	for _, s := range p.Sections {
		if v := display(s.Header, s.Value); v != "" {
			rows = append(rows, [2]string{s.Header, v})
		}
	}
	return rows
}
