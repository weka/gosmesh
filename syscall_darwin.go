//go:build darwin
// +build darwin

package main

import "syscall"

const (
	MAP_ANONYMOUS = syscall.MAP_ANON
	MAP_POPULATE  = 0 // Not supported on Darwin
	SYS_SET_MEMPOLICY = 0 // Not supported on Darwin
)

// Gettid returns the thread ID on Darwin (macOS)
func Gettid() int {
	// On Darwin, we can use the current PID as a fallback
	return syscall.Getpid()
}