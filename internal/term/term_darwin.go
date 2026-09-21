//go:build darwin

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
	return ioctl(fd, syscall.TIOCGETA, unsafe.Pointer(&t)) == nil
}

// MakeRaw switches fd into cbreak mode: canonical (line-buffered) input and
// local echo are disabled, so individual keystrokes — including the escape
// sequences arrow keys send — can be read as they are typed. ISIG is left
// enabled, so Ctrl+C still raises SIGINT rather than arriving as a literal
// byte. It returns the previous state so the caller can restore it with
// Restore, which callers must always do before the program exits.
func MakeRaw(fd uintptr) (*State, error) {
	var oldState syscall.Termios
	if err := ioctl(fd, syscall.TIOCGETA, unsafe.Pointer(&oldState)); err != nil {
		return nil, err
	}

	newState := oldState
	newState.Lflag &^= syscall.ICANON | syscall.ECHO
	newState.Cc[syscall.VMIN] = 1
	newState.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&newState)); err != nil {
		return nil, err
	}

	return &State{termios: oldState}, nil
}

// Restore restores the terminal mode captured by MakeRaw.
func Restore(fd uintptr, state *State) error {
	return ioctl(fd, syscall.TIOCSETA, unsafe.Pointer(&state.termios))
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// WaitAny blocks until at least one of fds has data available to read, or
// timeout elapses, reporting per-fd readiness without consuming any bytes
// itself. TTYs on this platform reject *os.File.SetReadDeadline ("file
// type does not support deadline"), so a read against them cannot be given
// a deadline directly; select(2) on the raw fds is the way to poll them
// with a timeout instead. runPhase watches stdin plus a private wake-up
// pipe it closes when the phase's own work finishes, so it reacts to
// either immediately — no periodic timer needed — while guaranteeing no
// blocking Read is ever left in flight once it stops polling.
func WaitAny(fds []uintptr, timeout time.Duration) ([]bool, error) {
	var set syscall.FdSet
	var maxFd uintptr
	for _, fd := range fds {
		fdSet(&set, fd)
		if fd > maxFd {
			maxFd = fd
		}
	}
	tv := syscall.NsecToTimeval(timeout.Nanoseconds())
	if err := syscall.Select(int(maxFd)+1, &set, nil, nil, &tv); err != nil {
		return nil, err
	}
	ready := make([]bool, len(fds))
	for i, fd := range fds {
		ready[i] = fdIsSet(&set, fd)
	}
	return ready, nil
}

// WaitReadable is WaitAny for a single fd.
func WaitReadable(fd uintptr, timeout time.Duration) (bool, error) {
	ready, err := WaitAny([]uintptr{fd}, timeout)
	if err != nil {
		return false, err
	}
	return ready[0], nil
}

func fdSet(set *syscall.FdSet, fd uintptr) {
	set.Bits[fd/32] |= 1 << (fd % 32)
}

func fdIsSet(set *syscall.FdSet, fd uintptr) bool {
	return set.Bits[fd/32]&(1<<(fd%32)) != 0
}
