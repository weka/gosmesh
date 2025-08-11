//go:build darwin
// +build darwin

package performance

import "syscall"

// Stub implementations for Darwin (macOS)

type HugePageAllocator struct{}
type OptimizedBufferPool struct{}
type IOUringConnection struct{}
type IOUring struct{}
type IOResult struct {
	Bytes int32
	Error error
}
type CacheAlignedStats struct {
	bytesSent   uint64
	packetsSent uint64
	bytesRecv   uint64
	packetsRecv uint64
}

const HUGEPAGE_2MB = 2 * 1024 * 1024

func NewHugePageAllocator(pageSize int, numPages int) (*HugePageAllocator, error) {
	// Not supported on Darwin
	return nil, nil
}

func (h *HugePageAllocator) Free() error {
	return nil
}

func NewOptimizedBufferPool(bufSize int, poolSize int) (*OptimizedBufferPool, error) {
	// Use regular buffers on Darwin
	return &OptimizedBufferPool{}, nil
}

func (p *OptimizedBufferPool) Get() []byte {
	return make([]byte, 65536)
}

func (p *OptimizedBufferPool) Put(buf []byte) {
	// No-op on Darwin
}

func (p *OptimizedBufferPool) Close() error {
	return nil
}

func NewIOUringConnection(conn interface{}) (*IOUringConnection, error) {
	// Not supported on Darwin
	return nil, nil
}

func (c *IOUringConnection) SendAsync(data []byte) (chan IOResult, error) {
	return nil, nil
}

func (c *IOUringConnection) RecvAsync(buf []byte) (chan IOResult, error) {
	return nil, nil
}

func (c *IOUringConnection) Close() error {
	return nil
}

// Gettid returns the thread ID (stub for Darwin)
func Gettid() int {
	// On Darwin, use process ID as a fallback
	return syscall.Getpid()
}

// SetCPUAffinity sets CPU affinity (stub for Darwin)
func SetCPUAffinity(cpus []int) error {
	// Not supported on Darwin
	return nil
}

// EnableBusyPolling enables busy polling (stub for Darwin)
func EnableBusyPolling(fd int, usecs int) error {
	// Not supported on Darwin
	return nil
}

// SetTCPCork sets TCP_CORK option (stub for Darwin)
func SetTCPCork(fd int, enabled bool) error {
	// Not supported on Darwin
	return nil
}

// SetTCPQuickAck sets TCP_QUICKACK option (stub for Darwin)
func SetTCPQuickAck(fd int, enabled bool) error {
	// Not supported on Darwin
	return nil
}
