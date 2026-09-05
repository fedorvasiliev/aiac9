//go:build linux

package term

import (
	"syscall"
	"unsafe"
)

// State holds the terminal mode captured by MakeRaw, to be restored later.
type State struct {
	termios syscall.Termios
}

// IsTerminal reports whether fd is connected to a terminal.
func IsTerminal(fd uintptr) bool {
	var t syscall.Termios
	return ioctl(fd, syscall.TCGETS, unsafe.Pointer(&t)) == nil
}

// MakeRaw switches fd into cbreak mode: canonical (line-buffered) input and
// local echo are disabled, so individual keystrokes — including the escape
// sequences arrow keys send — can be read as they are typed. ISIG is left
// enabled, so Ctrl+C still raises SIGINT rather than arriving as a literal
// byte. It returns the previous state so the caller can restore it with
// Restore, which callers must always do before the program exits.
func MakeRaw(fd uintptr) (*State, error) {
	var oldState syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&oldState)); err != nil {
		return nil, err
	}

	newState := oldState
	newState.Lflag &^= syscall.ICANON | syscall.ECHO
	newState.Cc[syscall.VMIN] = 1
	newState.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&newState)); err != nil {
		return nil, err
	}

	return &State{termios: oldState}, nil
}

// Restore restores the terminal mode captured by MakeRaw.
func Restore(fd uintptr, state *State) error {
	return ioctl(fd, syscall.TCSETS, unsafe.Pointer(&state.termios))
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}
