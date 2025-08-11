//go:build linux
// +build linux

package performance

import (
	"fmt"
	"syscall"
	"unsafe"
)

const MSG_ZEROCOPY = 0x4000000

// SendBatchLinux sends multiple buffers in a single syscall using sendmmsg on Linux
func (z *ZeroCopySender) SendBatchLinux(buffers [][]byte) (int, error) {
	if z.fd < 0 {
		return 0, fmt.Errorf("invalid file descriptor")
	}

	// Prepare iovecs for each buffer
	var iovecs []syscall.Iovec
	for _, buf := range buffers {
		if len(buf) > 0 {
			iovecs = append(iovecs, syscall.Iovec{
				Base: &buf[0],
				Len:  uint64(len(buf)),
			})
		}
	}

	// Prepare message header - Linux uses uint64 for Iovlen
	msg := syscall.Msghdr{
		Iov:    &iovecs[0],
		Iovlen: uint64(len(iovecs)),
	}

	// Send with MSG_ZEROCOPY flag
	n, _, errno := syscall.Syscall6(
		syscall.SYS_SENDMSG,
		uintptr(z.fd),
		uintptr(unsafe.Pointer(&msg)),
		MSG_ZEROCOPY,
		0, 0, 0,
	)

	if errno != 0 {
		return int(n), errno
	}
	return int(n), nil
}
