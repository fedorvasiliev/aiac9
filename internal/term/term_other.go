//go:build !darwin && !linux

package term

import (
	"errors"
	"time"
)

// State is unused on platforms without raw-mode support.
type State struct{}

// IsTerminal always reports false: without raw-mode support there is no way
// to read arrow-key escape sequences, so the select step falls back to its
// plain, non-tty prompt.
func IsTerminal(fd uintptr) bool { return false }

// MakeRaw is not implemented for this platform.
func MakeRaw(fd uintptr) (*State, error) {
	return nil, errors.New("raw terminal mode is not supported on this platform")
}

// Restore is a no-op counterpart to MakeRaw on this platform.
func Restore(fd uintptr, state *State) error { return nil }

// WaitAny is not implemented for this platform; IsTerminal already reports
// false here, so callers never reach it.
func WaitAny(fds []uintptr, timeout time.Duration) ([]bool, error) {
	return nil, errors.New("polling stdin is not supported on this platform")
}

// WaitReadable is not implemented for this platform; IsTerminal already
// reports false here, so callers never reach it.
func WaitReadable(fd uintptr, timeout time.Duration) (bool, error) {
	return false, errors.New("polling stdin is not supported on this platform")
}
