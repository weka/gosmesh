//go:build darwin
// +build darwin

package system

import "syscall"

const (
	MAP_ANONYMOUS     = syscall.MAP_ANON
	MAP_POPULATE      = 0 // Not supported on Darwin
	SYS_SET_MEMPOLICY = 0 // Not supported on Darwin
)
