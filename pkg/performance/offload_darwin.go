//go:build darwin
// +build darwin

package performance

import (
	"fmt"
	"syscall"
	"unsafe"
)

// SendBatchLinux sends multiple buffers (stub for Darwin)
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

	// Prepare message header - Darwin uses int32 for Iovlen
	msg := syscall.Msghdr{
		Iov:    &iovecs[0],
		Iovlen: int32(len(iovecs)),
	}

	// Send with MSG_ZEROCOPY flag (not supported on Darwin, use 0)
	n, _, errno := syscall.Syscall6(
		syscall.SYS_SENDMSG,
		uintptr(z.fd),
		uintptr(unsafe.Pointer(&msg)),
		0, // MSG_ZEROCOPY not supported on Darwin
		0, 0, 0,
	)

	if errno != 0 {
		return int(n), errno
	}
	return int(n), nil
}
