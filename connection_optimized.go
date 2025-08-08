// +build linux

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"golang.org/x/sys/unix"
	"time"
	"unsafe"
)

// Optimized buffer pools for high-performance networks
var (
	// For 100Gbps networks, use much larger buffers
	hugeBufferPool = sync.Pool{
		New: func() interface{} {
			// 1MB buffers for maximum throughput
			buf := make([]byte, 1048576)
			return &buf
		},
	}
	
	megaBufferPool = sync.Pool{
		New: func() interface{} {
			// 4MB buffers for extreme throughput
			buf := make([]byte, 4194304)
			return &buf
		},
	}
)

type OptimizedConnection struct {
	*Connection
	
	// Zero-copy support
	useSendfile    bool
	useZeroCopy    bool
	
	// Ring buffer for lock-free stats
	statsRing      *RingBuffer
	
	// Multiple workers per connection
	numWorkers     int
	
	// TCP optimizations
	tcpMSS         int
	tcpCork        bool
	
	// Socket FD for advanced operations
	socketFD       int
}

// RingBuffer for lock-free statistics updates
type RingBuffer struct {
	buffer   []StatsSample
	head     atomic.Uint64
	tail     atomic.Uint64
	mask     uint64
}

type StatsSample struct {
	bytes     int64
	packets   int64
	timestamp int64
}

func NewRingBuffer(size int) *RingBuffer {
	// Ensure power of 2
	size = 1 << uint(math.Ceil(math.Log2(float64(size))))
	return &RingBuffer{
		buffer: make([]StatsSample, size),
		mask:   uint64(size - 1),
	}
}

func (rb *RingBuffer) Push(sample StatsSample) bool {
	head := rb.head.Load()
	tail := rb.tail.Load()
	
	if head-tail >= uint64(len(rb.buffer)) {
		return false // Buffer full
	}
	
	rb.buffer[head&rb.mask] = sample
	rb.head.Add(1)
	return true
}

func (rb *RingBuffer) Pop() (StatsSample, bool) {
	tail := rb.tail.Load()
	head := rb.head.Load()
	
	if tail >= head {
		return StatsSample{}, false
	}
	
	sample := rb.buffer[tail&rb.mask]
	rb.tail.Add(1)
	return sample, true
}

// GetSocketFD extracts the file descriptor from a net.Conn
func GetSocketFD(conn net.Conn) (int, error) {
	var fd int
	var err error
	
	switch c := conn.(type) {
	case *net.TCPConn:
		file, err := c.File()
		if err != nil {
			return 0, err
		}
		fd = int(file.Fd())
		file.Close() // We only need the FD
	case *net.UDPConn:
		file, err := c.File()
		if err != nil {
			return 0, err
		}
		fd = int(file.Fd())
		file.Close()
	default:
		return 0, fmt.Errorf("unsupported connection type")
	}
	
	return fd, err
}

// SetTCPOptions sets advanced TCP options for maximum performance
func SetTCPOptions(conn *net.TCPConn, throughputMode bool) error {
	// Get the file descriptor
	file, err := conn.File()
	if err != nil {
		return err
	}
	defer file.Close()
	
	fd := int(file.Fd())
	
	// Set socket buffer sizes to 16MB for 100Gbps networks
	bufSize := 16777216 // 16MB
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, bufSize)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, bufSize)
	
	if throughputMode {
		// Enable TCP_CORK for better batching
		unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_CORK, 1)
		
		// Disable TCP_NODELAY for throughput
		unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_NODELAY, 0)
		
		// Set larger MSS if possible
		unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_MAXSEG, 9000)
		
		// Enable TCP_DEFER_ACCEPT to reduce latency
		unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_DEFER_ACCEPT, 1)
	} else {
		// For packet mode, prioritize latency
		unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_NODELAY, 1)
		unix.SetsockoptInt(fd, unix.IPPROTO_TCP, unix.TCP_QUICKACK, 1)
	}
	
	// Enable SO_ZEROCOPY if available (Linux 4.14+)
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ZEROCOPY, 1)
	
	// Set CPU affinity for the socket
	cpu := runtime.NumCPU() - 1
	unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_INCOMING_CPU, cpu)
	
	return nil
}

// OptimizedTCPSender uses zero-copy and advanced techniques
func (c *OptimizedConnection) OptimizedTCPSender(ctx context.Context) {
	// Pin to CPU
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	
	// Get huge buffer from pool
	bufPtr := megaBufferPool.Get().(*[]byte)
	buffer := *bufPtr
	defer megaBufferPool.Put(bufPtr)
	
	// Fill buffer once with pattern
	for i := range buffer {
		buffer[i] = byte(i & 0xFF)
	}
	
	// Use multiple goroutines for sending
	numWorkers := c.numWorkers
	if numWorkers == 0 {
		numWorkers = runtime.NumCPU()
	}
	
	var wg sync.WaitGroup
	wg.Add(numWorkers)
	
	// Create a channel for coordinating sends
	sendChan := make(chan []byte, numWorkers*2)
	
	// Start sender workers
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			runtime.LockOSThread() // Pin each worker to a thread
			
			for {
				select {
				case <-ctx.Done():
					return
				case data := <-sendChan:
					if data == nil {
						return
					}
					
					// Try zero-copy send first
					n, err := c.sendZeroCopy(data)
					if err != nil {
						// Fallback to regular send
						n, err = c.conn.Write(data)
					}
					
					if err == nil {
						// Update stats using ring buffer (lock-free)
						c.statsRing.Push(StatsSample{
							bytes:     int64(n),
							packets:   int64(n / c.packetSize),
							timestamp: time.Now().UnixNano(),
						})
					}
				}
			}
		}(i)
	}
	
	// Feed data to workers
	for {
		select {
		case <-ctx.Done():
			close(sendChan)
			wg.Wait()
			return
		default:
			// Send full buffer to a worker
			sendChan <- buffer
		}
	}
}

// sendZeroCopy attempts to use sendfile or MSG_ZEROCOPY
func (c *OptimizedConnection) sendZeroCopy(data []byte) (int, error) {
	if c.socketFD <= 0 {
		return 0, fmt.Errorf("invalid socket FD")
	}
	
	// Use MSG_ZEROCOPY flag (Linux 4.14+)
	err := unix.Send(c.socketFD, data, unix.MSG_ZEROCOPY|unix.MSG_DONTWAIT)
	if err != nil {
		return 0, err
	}
	
	return len(data), nil
}

// OptimizedUDPSender uses sendmmsg for batched sending
func (c *OptimizedConnection) OptimizedUDPSender(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	
	// Maximum UDP packet size (jumbo frames)
	packetSize := 9000
	if c.packetSize > packetSize {
		packetSize = c.packetSize
	}
	
	// Prepare multiple messages for sendmmsg
	const batchSize = 64
	messages := make([]Mmsghdr, batchSize)
	iovecs := make([]unix.Iovec, batchSize)
	buffers := make([][]byte, batchSize)
	
	for i := 0; i < batchSize; i++ {
		buffers[i] = make([]byte, packetSize)
		// Fill with pattern
		for j := range buffers[i] {
			buffers[i][j] = byte((i + j) & 0xFF)
		}
		
		iovecs[i] = unix.Iovec{
			Base: &buffers[i][0],
			Len:  uint64(len(buffers[i])),
		}
		
		messages[i].Msgvec.Iov = &iovecs[i]
		messages[i].Msgvec.Iovlen = 1
	}
	
	sequenceNum := uint64(0)
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Update sequence numbers
			for i := 0; i < batchSize; i++ {
				sequenceNum++
				binary.BigEndian.PutUint64(buffers[i][0:8], sequenceNum)
			}
			
			// Send batch of messages
			n, err := sendmmsg(c.socketFD, messages, 0)
			if err == nil && n > 0 {
				totalBytes := int64(n * packetSize)
				c.statsRing.Push(StatsSample{
					bytes:     totalBytes,
					packets:   int64(n),
					timestamp: time.Now().UnixNano(),
				})
			}
		}
	}
}

// Define Mmsghdr structure for sendmmsg
type Mmsghdr struct {
	Msgvec unix.Msghdr
	Msglen uint32
}

// sendmmsg syscall wrapper
func sendmmsg(fd int, messages []Mmsghdr, flags int) (int, error) {
	n, _, err := unix.Syscall6(
		unix.SYS_SENDMMSG,
		uintptr(fd),
		uintptr(unsafe.Pointer(&messages[0])),
		uintptr(len(messages)),
		uintptr(flags),
		0,
		0,
	)
	
	if err != 0 {
		return 0, err
	}
	
	return int(n), nil
}

// recvmmsg syscall wrapper  
func recvmmsg(fd int, messages []Mmsghdr, flags int, timeout *unix.Timespec) (int, error) {
	n, _, err := unix.Syscall6(
		unix.SYS_RECVMMSG,
		uintptr(fd),
		uintptr(unsafe.Pointer(&messages[0])),
		uintptr(len(messages)),
		uintptr(flags),
		uintptr(unsafe.Pointer(timeout)),
		0,
	)
	
	if err != 0 {
		return 0, err
	}
	
	return int(n), nil
}

// ProcessStatsRing aggregates stats from the ring buffer
func (c *OptimizedConnection) ProcessStatsRing() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	
	for range ticker.C {
		var totalBytes int64
		var totalPackets int64
		
		// Drain the ring buffer
		for {
			sample, ok := c.statsRing.Pop()
			if !ok {
				break
			}
			
			totalBytes += sample.bytes
			totalPackets += sample.packets
		}
		
		if totalBytes > 0 {
			c.bytesSent.Add(totalBytes)
			c.packetsSent.Add(totalPackets)
			c.updateStats()
		}
	}
}