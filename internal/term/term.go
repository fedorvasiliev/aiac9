// Package term puts a terminal into "cbreak" mode (no line buffering, no
// echo, but signals like Ctrl+C still work) so a program can read individual
// keystrokes — needed by the interactive select step to react to arrow
// keys. State/MakeRaw/Restore/IsTerminal are implemented per-OS in
// term_darwin.go, term_linux.go and, for any other platform, term_other.go
// (where raw mode is simply reported as unsupported).
package term
