//go:build linux
// +build linux

package performance

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/weka/gosmesh/pkg/system"
)

const (
	// Huge page sizes
	HUGEPAGE_2MB = 2 * 1024 * 1024
	HUGEPAGE_1GB = 1024 * 1024 * 1024

	// mmap flags for huge pages
	MAP_HUGETLB   = 0x40000
	MAP_HUGE_2MB  = 21 << 26
	MAP_HUGE_1GB  = 30 << 26
	// MAP_POPULATE  = 0x8000  // Commented out - defined in syscall_linux.go
	MAP_LOCKED    = 0x2000
	
	// NUMA memory policies
	MPOL_DEFAULT  = 0
	MPOL_BIND     = 2
	MPOL_INTERLEAVE = 3
	MPOL_LOCAL    = 4
)

// HugePageAllocator manages huge page allocations
type HugePageAllocator struct {
	pageSize   int
	totalPages int
	usedPages  int
	mappings   [][]byte
}

// CacheAlignedStats prevents false sharing with cache line padding
type CacheAlignedStats struct {
	// First cache line (64 bytes)
	bytesSent    uint64
	packetsSent  uint64
	bytesRecv    uint64
	packetsRecv  uint64
	errors       uint64
	latencySum   uint64
	latencyCount uint64
	maxLatency   uint64
	
	// Padding to next cache line
	_ [64 - 8*8]byte
	
	// Second cache line for less frequently accessed stats
	minLatency   uint64
	jitterSum    uint64
	jitterCount  uint64
	lostPackets  uint64
	duplicates   uint64
	outOfOrder   uint64
	throughput   uint64
	timestamp    uint64
	
	// Padding to next cache line
	_ [64 - 8*8]byte
}

// NewHugePageAllocator creates a new huge page allocator
func NewHugePageAllocator(pageSize int, numPages int) (*HugePageAllocator, error) {
	allocator := &HugePageAllocator{
		pageSize:   pageSize,
		totalPages: numPages,
		mappings:   make([][]byte, 0, numPages),
	}

	// Check if huge pages are available
	if err := allocator.checkHugePages(); err != nil {
		return nil, err
	}

	// Pre-allocate huge pages
	for i := 0; i < numPages; i++ {
		page, err := allocator.allocateHugePage()
		if err != nil {
			// Clean up already allocated pages
			for _, p := range allocator.mappings {
				syscall.Munmap(p)
			}
			return nil, fmt.Errorf("failed to allocate huge page %d: %v", i, err)
		}
		allocator.mappings = append(allocator.mappings, page)
	}

	return allocator, nil
}

// checkHugePages verifies huge pages are available
func (h *HugePageAllocator) checkHugePages() error {
	// Check 2MB huge pages
	if file, err := os.Open("/sys/kernel/mm/hugepages/hugepages-2048kB/nr_hugepages"); err == nil {
		defer file.Close()
		var count int
		fmt.Fscanf(file, "%d", &count)
		if count == 0 {
			return fmt.Errorf("no 2MB huge pages available, configure with: echo 1024 > /sys/kernel/mm/hugepages/hugepages-2048kB/nr_hugepages")
		}
	}
	return nil
}

// allocateHugePage allocates a single huge page
func (h *HugePageAllocator) allocateHugePage() ([]byte, error) {
	flags := syscall.MAP_PRIVATE | syscall.MAP_ANONYMOUS | MAP_HUGETLB | system.MAP_POPULATE | MAP_LOCKED
	
	// Add huge page size flag
	if h.pageSize == HUGEPAGE_2MB {
		flags |= MAP_HUGE_2MB
	} else if h.pageSize == HUGEPAGE_1GB {
		flags |= MAP_HUGE_1GB
	}

	// Allocate huge page
	data, err := syscall.Mmap(-1, 0, h.pageSize, syscall.PROT_READ|syscall.PROT_WRITE, flags)
	if err != nil {
		return nil, fmt.Errorf("mmap huge page failed: %v", err)
	}

	// Touch all pages to ensure they're allocated
	for i := 0; i < len(data); i += 4096 {
		data[i] = 0
	}

	h.usedPages++
	return data, nil
}

// GetBuffer returns a buffer from huge pages
func (h *HugePageAllocator) GetBuffer(size int) []byte {
	if size > h.pageSize {
		return nil
	}
	
	// Simple allocation from first available page
	// In production, implement proper memory management
	if h.usedPages > 0 && len(h.mappings) > 0 {
		page := h.mappings[0]
		if len(page) >= size {
			return page[:size]
		}
	}
	
	return nil
}

// Free releases all huge page mappings
func (h *HugePageAllocator) Free() error {
	var lastErr error
	for _, mapping := range h.mappings {
		if err := syscall.Munmap(mapping); err != nil {
			lastErr = err
		}
	}
	h.mappings = nil
	h.usedPages = 0
	return lastErr
}

// NUMAMemory manages NUMA-aware memory allocation
type NUMAMemory struct {
	node int
	size int
	data []byte
}

// NewNUMAMemory creates NUMA-aware memory allocation
func NewNUMAMemory(size int, node int) (*NUMAMemory, error) {
	numa := &NUMAMemory{
		node: node,
		size: size,
	}

	// Set NUMA policy for this thread
	if err := numa.setNUMAPolicy(); err != nil {
		return nil, err
	}

	// Allocate memory on specific NUMA node
	data := make([]byte, size)
	
	// Touch pages to ensure allocation on correct node
	for i := 0; i < len(data); i += 4096 {
		data[i] = 0
	}

	numa.data = data
	return numa, nil
}

// setNUMAPolicy sets the NUMA memory policy
func (n *NUMAMemory) setNUMAPolicy() error {
	// Lock thread to CPU
	runtime.LockOSThread()
	
	// Create node mask
	var nodemask uint64 = 1 << uint(n.node)
	
	// Set memory policy to bind to specific node
	_, _, errno := syscall.Syscall6(
		syscall.SYS_SET_MEMPOLICY,
		MPOL_BIND,
		uintptr(unsafe.Pointer(&nodemask)),
		8, // maxnode
		0, 0, 0,
	)
	
	if errno != 0 {
		return fmt.Errorf("set_mempolicy failed: %v", errno)
	}
	
	return nil
}

// GetNUMANode returns the current NUMA node for a CPU
func GetNUMANode(cpu int) (int, error) {
	path := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/node", cpu)
	file, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer file.Close()

	var node int
	_, err = fmt.Fscanf(file, "%d", &node)
	return node, err
}

// OptimizedBufferPool uses huge pages for buffer allocation
type OptimizedBufferPool struct {
	hugePage *HugePageAllocator
	bufSize  int
	buffers  chan []byte
}

// NewOptimizedBufferPool creates a buffer pool using huge pages
func NewOptimizedBufferPool(bufSize int, poolSize int) (*OptimizedBufferPool, error) {
	// Calculate how many buffers fit in a huge page
	buffersPerPage := HUGEPAGE_2MB / bufSize
	pagesNeeded := (poolSize + buffersPerPage - 1) / buffersPerPage

	hugePage, err := NewHugePageAllocator(HUGEPAGE_2MB, pagesNeeded)
	if err != nil {
		// Fall back to regular memory if huge pages not available
		return &OptimizedBufferPool{
			bufSize: bufSize,
			buffers: make(chan []byte, poolSize),
		}, nil
	}

	pool := &OptimizedBufferPool{
		hugePage: hugePage,
		bufSize:  bufSize,
		buffers:  make(chan []byte, poolSize),
	}

	// Pre-allocate buffers from huge pages
	for i := 0; i < poolSize; i++ {
		if i < len(hugePage.mappings)*buffersPerPage {
			pageIdx := i / buffersPerPage
			bufIdx := i % buffersPerPage
			if pageIdx < len(hugePage.mappings) {
				page := hugePage.mappings[pageIdx]
				offset := bufIdx * bufSize
				if offset+bufSize <= len(page) {
					pool.buffers <- page[offset : offset+bufSize]
				}
			}
		}
	}

	return pool, nil
}

// Get retrieves a buffer from the pool
func (p *OptimizedBufferPool) Get() []byte {
	select {
	case buf := <-p.buffers:
		return buf
	default:
		// Fallback to regular allocation if pool is empty
		return make([]byte, p.bufSize)
	}
}

// Put returns a buffer to the pool
func (p *OptimizedBufferPool) Put(buf []byte) {
	if len(buf) != p.bufSize {
		return
	}
	
	select {
	case p.buffers <- buf:
		// Buffer returned to pool
	default:
		// Pool is full, let GC handle it
	}
}

// Close releases huge page resources
func (p *OptimizedBufferPool) Close() error {
	if p.hugePage != nil {
		return p.hugePage.Free()
	}
	return nil
}

// AlignedAlloc allocates cache-aligned memory
func AlignedAlloc(size int) []byte {
	// Allocate with extra space for alignment
	buf := make([]byte, size+64)
	
	// Find aligned offset
	ptr := uintptr(unsafe.Pointer(&buf[0]))
	offset := int((64 - (ptr % 64)) % 64)
	
	// Return aligned slice
	return buf[offset : offset+size]
}