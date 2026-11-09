package cli

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether r is an interactive terminal: an *os.File for
// which the TCGETS ioctl succeeds (a plain character device such as
// /dev/null is not one).
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
