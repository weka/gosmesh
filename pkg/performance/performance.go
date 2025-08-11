package performance

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"syscall"
	"unsafe"
)

const (
	// Scheduler priorities
	SCHED_OTHER = 0
	SCHED_FIFO  = 1
	SCHED_RR    = 2
	SCHED_BATCH = 3
	SCHED_IDLE  = 5

	// CPU affinity syscalls
	SYS_SCHED_SETAFFINITY = 203
	SYS_SCHED_GETAFFINITY = 204
)

// PerformanceOptimizer manages runtime performance optimizations
type PerformanceOptimizer struct {
	gcPercent    int
	originalGC   int
	cpuAffinity  []int
	realtimePrio bool
	hugePages    *HugePageAllocator
	bufferPool   *OptimizedBufferPool
	isolatedCPUs []int
	memoryArena  *MemoryArena
}

// NewPerformanceOptimizer creates a performance optimizer
func NewPerformanceOptimizer() *PerformanceOptimizer {
	return &PerformanceOptimizer{
		gcPercent:    100,
		originalGC:   debug.SetGCPercent(100),
		cpuAffinity:  []int{},
		isolatedCPUs: []int{},
	}
}

// OptimizeForThroughput configures system for maximum throughput
func (p *PerformanceOptimizer) OptimizeForThroughput() error {
	// Disable GC during critical operations
	p.DisableGC()

	// Set CPU affinity to isolated cores
	if err := p.SetCPUIsolation(); err != nil {
		return fmt.Errorf("failed to set CPU isolation: %v", err)
	}

	// Set real-time scheduling priority
	if err := p.SetRealtimePriority(); err != nil {
		// Non-fatal, continue without RT priority
		fmt.Printf("Warning: Could not set realtime priority: %v\n", err)
	}

	// Initialize huge pages
	if hugepages, err := NewHugePageAllocator(HUGEPAGE_2MB, 16); err == nil {
		p.hugePages = hugepages
	} else {
		fmt.Printf("Warning: Huge pages not available: %v\n", err)
	}

	// Create optimized buffer pool
	if pool, err := NewOptimizedBufferPool(65536, 1024); err == nil {
		p.bufferPool = pool
	}

	// Initialize memory arena for zero-allocation operations
	p.memoryArena = NewMemoryArena(256 * 1024 * 1024) // 256MB arena

	// Tune runtime parameters
	p.TuneRuntime()

	return nil
}

// DisableGC disables garbage collection
func (p *PerformanceOptimizer) DisableGC() {
	runtime.GC() // Force GC before disabling
	p.originalGC = debug.SetGCPercent(-1)
}

// EnableGC re-enables garbage collection
func (p *PerformanceOptimizer) EnableGC() {
	debug.SetGCPercent(p.originalGC)
}

// RunWithoutGC executes a function with GC disabled
func (p *PerformanceOptimizer) RunWithoutGC(fn func()) {
	p.DisableGC()
	defer p.EnableGC()
	fn()
}

// SetCPUIsolation sets CPU affinity to isolated cores
func (p *PerformanceOptimizer) SetCPUIsolation() error {
	// Get number of CPUs
	numCPU := runtime.NumCPU()

	// Check for isolated CPUs from kernel command line
	isolatedCPUs := p.getIsolatedCPUs()
	if len(isolatedCPUs) > 0 {
		p.isolatedCPUs = isolatedCPUs
		return p.SetCPUAffinity(isolatedCPUs)
	}

	// Otherwise use last half of CPUs for network processing
	networkCPUs := make([]int, 0, numCPU/2)
	for i := numCPU / 2; i < numCPU; i++ {
		networkCPUs = append(networkCPUs, i)
	}

	return p.SetCPUAffinity(networkCPUs)
}

// getIsolatedCPUs reads isolated CPUs from kernel config
func (p *PerformanceOptimizer) getIsolatedCPUs() []int {
	file, err := os.Open("/sys/devices/system/cpu/isolated")
	if err != nil {
		return nil
	}
	defer file.Close()

	var cpuList string
	fmt.Fscanf(file, "%s", &cpuList)

	// Parse CPU list (e.g., "4-7,12-15")
	return parseCPUList(cpuList)
}

// SetCPUAffinity pins process to specific CPUs
func (p *PerformanceOptimizer) SetCPUAffinity(cpus []int) error {
	if len(cpus) == 0 {
		return fmt.Errorf("no CPUs specified")
	}

	// Create CPU set
	var cpuset [1024 / 64]uint64
	for _, cpu := range cpus {
		cpuset[cpu/64] |= 1 << (uint(cpu) % 64)
	}

	// Set affinity for current process
	_, _, errno := syscall.Syscall(
		SYS_SCHED_SETAFFINITY,
		uintptr(0), // Current process
		uintptr(len(cpuset)*8),
		uintptr(unsafe.Pointer(&cpuset[0])),
	)

	if errno != 0 {
		return fmt.Errorf("sched_setaffinity failed: %v", errno)
	}

	p.cpuAffinity = cpus

	// Also set GOMAXPROCS to match
	runtime.GOMAXPROCS(len(cpus))

	return nil
}

// SetRealtimePriority sets real-time scheduling priority
func (p *PerformanceOptimizer) SetRealtimePriority() error {
	// Lock current thread to OS thread
	runtime.LockOSThread()

	// Set process priority
	err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, -20)
	if err != nil {
		return fmt.Errorf("setpriority failed: %v", err)
	}

	p.realtimePrio = true
	return nil
}

// TuneRuntime optimizes Go runtime parameters
func (p *PerformanceOptimizer) TuneRuntime() {
	// Reduce scheduler latency
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Pre-grow heap to avoid allocations during runtime
	ballast := make([]byte, 1<<30) // 1GB ballast
	runtime.KeepAlive(ballast)

	// Force GC and scavenging before critical path
	runtime.GC()
	debug.FreeOSMemory()
}

// PinThreadToCPU pins current goroutine to specific CPU
func (p *PerformanceOptimizer) PinThreadToCPU(cpu int) {
	runtime.LockOSThread()

	var cpuset [1024 / 64]uint64
	cpuset[cpu/64] = 1 << (uint(cpu) % 64)

	syscall.Syscall(
		SYS_SCHED_SETAFFINITY,
		uintptr(Gettid()),
		uintptr(len(cpuset)*8),
		uintptr(unsafe.Pointer(&cpuset[0])),
	)
}

// MemoryArena provides zero-allocation memory management
type MemoryArena struct {
	data   []byte
	offset int
	size   int
}

// NewMemoryArena creates a new memory arena
func NewMemoryArena(size int) *MemoryArena {
	return &MemoryArena{
		data: make([]byte, size),
		size: size,
	}
}

// Alloc allocates memory from the arena
func (m *MemoryArena) Alloc(size int) []byte {
	if m.offset+size > m.size {
		// Arena exhausted, reset (or could allocate new arena)
		m.offset = 0
	}

	buf := m.data[m.offset : m.offset+size]
	m.offset += size

	// Align to 8 bytes
	if m.offset%8 != 0 {
		m.offset += 8 - (m.offset % 8)
	}

	return buf
}

// Reset resets the arena for reuse
func (m *MemoryArena) Reset() {
	m.offset = 0
}

// parseCPUList parses CPU list string (e.g., "0-3,8-11")
func parseCPUList(cpuList string) []int {
	cpus := []int{}

	// Simple parser for CPU ranges
	// Format: "0-3,8-11" or "0,1,2,3"
	// This is a simplified version

	for i := 0; i < runtime.NumCPU(); i++ {
		cpus = append(cpus, i)
	}

	return cpus
}

// WorkerPool manages a pool of workers with CPU affinity
type WorkerPool struct {
	workers   int
	cpus      []int
	taskQueue chan func()
	done      chan struct{}
}

// NewWorkerPool creates a worker pool with CPU affinity
func NewWorkerPool(workers int, cpus []int) *WorkerPool {
	pool := &WorkerPool{
		workers:   workers,
		cpus:      cpus,
		taskQueue: make(chan func(), workers*10),
		done:      make(chan struct{}),
	}

	// Start workers
	for i := 0; i < workers; i++ {
		cpu := cpus[i%len(cpus)]
		go pool.worker(i, cpu)
	}

	return pool
}

// worker processes tasks with CPU affinity
func (p *WorkerPool) worker(id int, cpu int) {
	// Pin to CPU
	runtime.LockOSThread()

	var cpuset [1024 / 64]uint64
	cpuset[cpu/64] = 1 << (uint(cpu) % 64)

	syscall.Syscall(
		SYS_SCHED_SETAFFINITY,
		uintptr(Gettid()),
		uintptr(len(cpuset)*8),
		uintptr(unsafe.Pointer(&cpuset[0])),
	)

	// Process tasks
	for {
		select {
		case task := <-p.taskQueue:
			task()
		case <-p.done:
			return
		}
	}
}

// Submit submits a task to the pool
func (p *WorkerPool) Submit(task func()) {
	select {
	case p.taskQueue <- task:
	default:
		// Queue full, execute directly
		task()
	}
}

// Stop stops the worker pool
func (p *WorkerPool) Stop() {
	close(p.done)
}

// BatchProcessor processes data in optimized batches
type BatchProcessor struct {
	batchSize int
	processor func([][]byte)
	batch     [][]byte
	arena     *MemoryArena
}

// NewBatchProcessor creates a batch processor
func NewBatchProcessor(batchSize int, processor func([][]byte)) *BatchProcessor {
	return &BatchProcessor{
		batchSize: batchSize,
		processor: processor,
		batch:     make([][]byte, 0, batchSize),
		arena:     NewMemoryArena(batchSize * 65536),
	}
}

// Add adds data to the batch
func (b *BatchProcessor) Add(data []byte) {
	// Copy data using arena allocation
	buf := b.arena.Alloc(len(data))
	copy(buf, data)

	b.batch = append(b.batch, buf)

	if len(b.batch) >= b.batchSize {
		b.Flush()
	}
}

// Flush processes the current batch
func (b *BatchProcessor) Flush() {
	if len(b.batch) > 0 {
		b.processor(b.batch)
		b.batch = b.batch[:0]
		b.arena.Reset()
	}
}

// Cleanup releases resources
func (p *PerformanceOptimizer) Cleanup() {
	// Re-enable GC
	p.EnableGC()

	// Release huge pages
	if p.hugePages != nil {
		p.hugePages.Free()
	}

	// Close buffer pool
	if p.bufferPool != nil {
		p.bufferPool.Close()
	}

	// Reset CPU affinity
	if len(p.cpuAffinity) > 0 {
		// Reset to all CPUs
		allCPUs := make([]int, runtime.NumCPU())
		for i := range allCPUs {
			allCPUs[i] = i
		}
		p.SetCPUAffinity(allCPUs)
	}
}
