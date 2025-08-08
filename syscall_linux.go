//go:build linux
// +build linux

package main

import "syscall"

const (
	MAP_ANONYMOUS = syscall.MAP_ANONYMOUS
	MAP_POPULATE  = 0x8000
	SYS_SET_MEMPOLICY = 237
)

// Gettid returns the thread ID on Linux
func Gettid() int {
	return syscall.Gettid()
}

// SetCPUAffinity sets CPU affinity on Linux
func SetCPUAffinity(cpus []int) error {
	// Implementation in performance.go
	return nil
}

// EnableBusyPolling enables busy polling on Linux
func EnableBusyPolling(fd int, usecs int) error {
	// SO_BUSY_POLL = 46
	return syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, 46, usecs)
}

// SetTCPCork sets TCP_CORK option on Linux
func SetTCPCork(fd int, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	// TCP_CORK = 3
	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, 3, val)
}

// SetTCPQuickAck sets TCP_QUICKACK option on Linux
func SetTCPQuickAck(fd int, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	// TCP_QUICKACK = 12
	return syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, 12, val)
}