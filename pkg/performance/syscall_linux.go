//go:build linux
// +build linux

package performance

import "github.com/weka/gosmesh/pkg/system"

// Gettid returns the thread ID on Linux
func Gettid() int {
	return system.Gettid()
}