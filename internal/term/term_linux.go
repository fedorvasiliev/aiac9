//go:build linux

package term

import (
	"syscall"
	"time"
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

// WaitReadable blocks until fd has data available to read or timeout
// elapses, reporting which, without consuming any bytes itself. TTYs on
// this platform reject *os.File.SetReadDeadline ("file type does not
// support deadline"), so a read against them cannot be given a deadline
// directly; select(2) on the raw fd is the way to poll one with a timeout
// instead, letting a caller (runWithConsole) alternate between animating a
// spinner and checking whether a background request has finished, without
// ever leaving a blocking Read in flight when it stops polling.
func WaitReadable(fd uintptr, timeout time.Duration) (bool, error) {
	var set syscall.FdSet
	fdSet(&set, fd)
	tv := syscall.NsecToTimeval(timeout.Nanoseconds())
	if _, err := syscall.Select(int(fd)+1, &set, nil, nil, &tv); err != nil {
		return false, err
	}
	return fdIsSet(&set, fd), nil
}

func fdSet(set *syscall.FdSet, fd uintptr) {
	set.Bits[fd/64] |= 1 << (fd % 64)
}

func fdIsSet(set *syscall.FdSet, fd uintptr) bool {
	return set.Bits[fd/64]&(1<<(fd%64)) != 0
}
