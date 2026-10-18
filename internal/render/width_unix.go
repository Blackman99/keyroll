//go:build linux || darwin || freebsd || netbsd || openbsd

package render

import (
	"os"
	"syscall"
	"unsafe"
)

// termWidth asks the terminal behind f for its width (0 if f is not one).
func termWidth(f *os.File) int {
	var ws struct{ Row, Col, X, Y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
