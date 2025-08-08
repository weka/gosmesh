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