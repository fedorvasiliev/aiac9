// Package secretmask hides values that came from environment variables
// before they reach a log file or the terminal — per CLAUDE.md, any such
// value must be masked with asterisks wherever it is logged or displayed.
package secretmask

import "strings"

// Masker replaces a fixed set of secret values with "***" in arbitrary text.
type Masker struct {
	secrets []string
}

// New returns a Masker that hides every non-empty value in secrets.
func New(secrets ...string) *Masker {
	m := &Masker{}
	for _, s := range secrets {
		if s != "" {
			m.secrets = append(m.secrets, s)
		}
	}
	return m
}

// Mask returns s with every occurrence of a registered secret replaced by
// "***". Text with no registered secrets is returned unchanged.
func (m *Masker) Mask(s string) string {
	for _, secret := range m.secrets {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}
