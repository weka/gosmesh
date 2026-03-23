package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Buffer pools for different sizes to reduce allocations
var (
	smallBufferPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 2048)
			return &buf
		},
	}

	mediumBufferPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 65536) // 64KB
			return &buf
		},
	}

	largeBufferPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 4194304) // 4MB for high-speed networks
			return &buf
		},
	}

	jumboBufferPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, 134217728) // 128MB for extreme throughput
			return &buf
		},
	}
)

// getOptimalBufferPool selects the best buffer pool based on size and optimization mode
func getOptimalBufferPool(size int, useOptimized bool) (bufPtr *[]byte, poolPut func(interface{})) {
	if useOptimized {
		// In optimized mode, prefer larger buffers for maximum throughput
		if size <= 65536 {
			bufPtr = largeBufferPool.Get().(*[]byte) // Use 4MB instead of 64KB
			poolPut = largeBufferPool.Put
		} else {
			bufPtr = jumboBufferPool.Get().(*[]byte) // Use 128MB for large sizes
			poolPut = jumboBufferPool.Put
		}
	} else {
		// Standard mode - use size-appropriate pools
		if size <= 65536 {
			bufPtr = mediumBufferPool.Get().(*[]byte)
			poolPut = mediumBufferPool.Put
		} else if size <= 4194304 {
			bufPtr = largeBufferPool.Get().(*[]byte)
			poolPut = largeBufferPool.Put
		} else {
			bufPtr = jumboBufferPool.Get().(*[]byte)
			poolPut = jumboBufferPool.Put
		}
	}
	return
}

// ConnectionStats holds the performance metrics for a single connection.
// Statistics are automatically updated during testing and can be retrieved
// via Connection.GetStats().
type ConnectionStats struct {
	// PacketsSent is the number of packets transmitted
	PacketsSent int64
	// PacketsReceived is the number of echo responses received (packet mode)
	PacketsReceived int64
	// PacketsLost is the number of packets that didn't receive responses
	PacketsLost int64
	// BytesSent is the total bytes transmitted
	BytesSent int64
	// BytesReceived is the total bytes received
	BytesReceived int64
	// LossRate is the packet loss percentage (0-100)
	LossRate float64
	// ThroughputMbps is the measured throughput in megabits per second
	ThroughputMbps float64
	// AvgRTTMs is the average round-trip time in milliseconds (packet mode only)
	AvgRTTMs float64
	// MinRTTMs is the minimum RTT observed in milliseconds
	MinRTTMs float64
	// MaxRTTMs is the maximum RTT observed in milliseconds
	MaxRTTMs float64
	// JitterMs is the standard deviation of RTT in milliseconds
	JitterMs float64
	// LastUpdate is when these stats were last calculated
	LastUpdate time.Time

	// ReconnectCount is the total number of times the connection was re-established
	ReconnectCount int64
	// LastReconnectTime is when the last reconnection occurred
	LastReconnectTime time.Time
}

// Connection represents a single network connection to a target.
// It handles sending test packets and measuring performance metrics.
// Connections are typically created by NetworkTester, but can be used directly
// for more fine-grained control.
type Connection struct {
	// ID is a unique identifier for this connection within a tester
	ID int
	// TargetIP is the destination IP address
	TargetIP string

	// ...internal fields...
	localIP        string
	port           int
	protocol       string
	packetSize     int
	pps            int
	throughputMode bool // New: throughput mode for max performance
	// BufferSize sets the socket buffer size (0 = auto)
	BufferSize int
	// NumWorkers is the number of parallel worker threads
	NumWorkers int

	// Optimization parameters
	// SendBatchSize is the number of packets to batch together
	SendBatchSize int
	// RecvBatchSize is the number of receive operations to batch
	RecvBatchSize int
	// TCPCork enables TCP cork on send
	TCPCork bool
	// TCPQuickAck enables TCP quick acknowledgment
	TCPQuickAck bool
	// TCPNoDelay disables Nagle's algorithm
	TCPNoDelay bool
	// BusyPollUsecs enables busy polling (0 = disabled)
	BusyPollUsecs int
	// UseOptimized enables platform-specific optimizations
	UseOptimized bool

	conn    net.Conn
	tcpConn *net.TCPConn
	udpConn *net.UDPConn

	stats ConnectionStats
	mu    sync.RWMutex

	rttHistory []float64
	rttMu      sync.Mutex

	startTime time.Time

	packetsSent     atomic.Int64
	packetsReceived atomic.Int64
	bytesSent       atomic.Int64
	bytesReceived   atomic.Int64

	// Connection re-establishment tracking
	reconnectCount    atomic.Int64
	lastReconnectTime atomic.Int64 // Unix nano timestamp
}

type Packet struct {
	SequenceNum uint64
	Timestamp   int64
	Payload     []byte
}

func NewConnection(localIP, targetIP string, port int, protocol string, packetSize, pps, id int) *Connection {
	bufferSize := packetSize
	throughputMode := false
	numWorkers := 1

	// Enable throughput mode for unlimited PPS
	if pps <= 0 {
		throughputMode = true
		// Use much larger buffers for 100Gbps networks
		if protocol == "tcp" {
			bufferSize = 4194304 // 4MB for TCP on high-speed networks (increased)
			numWorkers = 8       // Use 8 parallel workers for TCP (increased)
		} else {
			bufferSize = 1048576 // 1MB for UDP batching (increased)
			numWorkers = 4       // Use 4 parallel workers for UDP (increased)
		}
	}

	return &Connection{
		ID:             id,
		localIP:        localIP,
		TargetIP:       targetIP,
		port:           port,
		protocol:       protocol,
		packetSize:     packetSize,
		pps:            pps,
		throughputMode: throughputMode,
		BufferSize:     bufferSize,
		NumWorkers:     numWorkers,
		rttHistory:     make([]float64, 0, 1000),
	}
}

func (c *Connection) Start(ctx context.Context) error {
	c.startTime = time.Now()

	// Log optimization status for debugging
	if c.UseOptimized {
		log.Printf("Connection %d->%s: Using optimized mode", c.ID, c.TargetIP)
	} else {
		log.Printf("Connection %d->%s: Using standard mode", c.ID, c.TargetIP)
	}

	// Start periodic stats updater for throughput mode
	if c.throughputMode {
		go c.periodicStatsUpdater(ctx)
	}

	if c.protocol == "tcp" {
		return c.startTCP(ctx)
	}
	return c.startUDP(ctx)
}

func (c *Connection) startTCP(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.TargetIP, c.port)

	// Retry connection indefinitely until context cancellation
	var conn net.Conn
	var err error
	firstAttempt := true
	retryCount := 0
	baseDelay := 200 * time.Millisecond
	maxDelay := 2 * time.Second

	for {
		conn, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			if !firstAttempt {
				log.Printf("Connection %d: Connected to %s after %d retries", c.ID, addr, retryCount)
			}
			break
		}

		if firstAttempt {
			log.Printf("Connection %d: Initial connection to %s failed, retrying indefinitely... (%v)", c.ID, addr, err)
			firstAttempt = false
		}

		retryCount++

		// Exponential backoff with jitter to avoid thundering herd
		delay := baseDelay
		if retryCount > 10 {
			// After 10 attempts, use exponential backoff up to maxDelay
			backoffDelay := time.Duration(min(int64(baseDelay)*(1<<min(retryCount-10, 4)), int64(maxDelay)))
			delay = backoffDelay
		}

		// Log periodic status for long-running connection attempts
		if retryCount%50 == 0 {
			log.Printf("Connection %d: Still retrying %s (attempt %d)", c.ID, addr, retryCount)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while retrying connection to %s after %d attempts", addr, retryCount)
		case <-time.After(delay):
			// Continue to next retry
		}
	}
	c.conn = conn

	// Configure TCP socket for maximum throughput
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		c.tcpConn = tcpConn

		// Apply Linux-specific optimizations in optimized mode
		if c.UseOptimized {
			if err := c.applyOptimizedTCPOptions(tcpConn); err != nil {
				log.Printf("Warning: Could not apply optimized TCP options: %v", err)
			} else {
				log.Printf("Connection %d->%s: Applied optimized TCP socket options", c.ID, c.TargetIP)
			}
		}

		// Set socket buffer sizes for 100Gbps networks
		if c.throughputMode {
			// Use large buffers based on configuration
			writeBuf := c.BufferSize * 4 // 4x buffer size for socket buffers
			if writeBuf < 16777216 {
				writeBuf = 16777216 // Minimum 16MB
			}
			if writeBuf > 134217728 {
				writeBuf = 134217728 // Max 128MB
			}
			tcpConn.SetWriteBuffer(writeBuf)
			tcpConn.SetReadBuffer(writeBuf)

			// Apply TCP optimizations
			if c.TCPNoDelay {
				tcpConn.SetNoDelay(true)
			} else {
				tcpConn.SetNoDelay(false) // Enable Nagle's for throughput
			}

			// Set TCP keepalive for long connections
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(30 * time.Second)
		} else {
			// For packet mode, prioritize latency
			tcpConn.SetNoDelay(true)        // Disable Nagle's for low latency
			tcpConn.SetWriteBuffer(4194304) // 4MB for packet mode
			tcpConn.SetReadBuffer(4194304)
		}
	}

	defer c.conn.Close()

	// Start sender and receiver
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		c.tcpSender(ctx)
	}()

	go func() {
		defer wg.Done()
		c.tcpReceiver(ctx)
	}()

	wg.Wait()
	return nil
}

func (c *Connection) startUDP(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.TargetIP, c.port)

	serverAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}

	// Retry UDP connection indefinitely until context cancellation
	var conn *net.UDPConn
	firstAttempt := true
	retryCount := 0
	baseDelay := 200 * time.Millisecond
	maxDelay := 2 * time.Second

	for {
		conn, err = net.DialUDP("udp", nil, serverAddr)
		if err == nil {
			if !firstAttempt {
				log.Printf("Connection %d: UDP connected to %s after %d retries", c.ID, addr, retryCount)
			}
			break
		}

		if firstAttempt {
			log.Printf("Connection %d: Initial UDP connection to %s failed, retrying indefinitely... (%v)", c.ID, addr, err)
			firstAttempt = false
		}

		retryCount++

		// Exponential backoff with jitter to avoid thundering herd
		delay := baseDelay
		if retryCount > 10 {
			// After 10 attempts, use exponential backoff up to maxDelay
			backoffDelay := time.Duration(min(int64(baseDelay)*(1<<min(retryCount-10, 4)), int64(maxDelay)))
			delay = backoffDelay
		}

		// Log periodic status for long-running connection attempts
		if retryCount%50 == 0 {
			log.Printf("Connection %d: Still retrying UDP %s (attempt %d)", c.ID, addr, retryCount)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while retrying UDP connection to %s after %d attempts", addr, retryCount)
		case <-time.After(delay):
			// Continue to next retry
		}
	}
	c.udpConn = conn
	defer c.udpConn.Close()

	// Configure UDP socket buffers for high-throughput mode
	if c.throughputMode {
		// Set large receive buffer to prevent packet drops
		if err := c.udpConn.SetReadBuffer(16777216); err != nil { // 16MB
			log.Printf("Connection %d: Warning - could not set UDP read buffer: %v", c.ID, err)
		}
		// Set large send buffer
		if err := c.udpConn.SetWriteBuffer(16777216); err != nil { // 16MB
			log.Printf("Connection %d: Warning - could not set UDP write buffer: %v", c.ID, err)
		}
		log.Printf("Connection %d: Set UDP socket buffers to 16MB", c.ID)
	}

	// Start sender and receiver
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		c.udpSender(ctx)
	}()

	go func() {
		defer wg.Done()
		c.udpReceiver(ctx)
	}()

	wg.Wait()
	return nil
}

func (c *Connection) tcpSender(ctx context.Context) {
	if c.throughputMode {
		// Throughput mode: send continuously for maximum speed
		// NOTE: Parallel workers disabled due to socket contention
		c.tcpSenderThroughput(ctx)
	} else {
		// Packet mode: precise packet-based sending for metrics
		c.tcpSenderPacket(ctx)
	}
}

func (c *Connection) tcpSenderThroughputParallel(ctx context.Context) {
	log.Printf("Starting %d parallel workers for connection %d", c.NumWorkers, c.ID)
	var wg sync.WaitGroup
	wg.Add(c.NumWorkers)

	// Start multiple sender workers
	for i := 0; i < c.NumWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			log.Printf("Worker %d starting for connection %d", workerID, c.ID)
			// Each worker runs the throughput sender
			c.tcpSenderThroughput(ctx)
		}(i)
	}

	wg.Wait()
	log.Printf("All workers finished for connection %d", c.ID)
}

func (c *Connection) tcpSenderThroughput(ctx context.Context) {
	// Use the configured buffer size for writes
	writeBufferSize := c.BufferSize
	if writeBufferSize > 4194304 {
		writeBufferSize = 4194304 // Cap at 4MB for safety
	}

	log.Printf("Connection %d: Starting TCP sender in throughput mode, write buffer: %d", c.ID, writeBufferSize)

	// Get appropriate buffer from pool based on size
	var bufPtr *[]byte
	var poolPut func(interface{})

	bufPtr, poolPut = getOptimalBufferPool(writeBufferSize, c.UseOptimized)

	buffer := (*bufPtr)[:writeBufferSize]
	defer func() {
		poolPut(bufPtr)
	}()

	// Fill buffer with pattern once
	for i := range buffer {
		buffer[i] = byte(i % 256)
	}

	// Pre-calculate packet estimate divisor
	packetSizeInv := int64(c.packetSize)
	if packetSizeInv == 0 {
		packetSizeInv = 1
	}

	// Use local counters to reduce atomic operations
	var localBytes int64
	var localPackets int64
	var batchCount int64
	batchSize := int64(c.SendBatchSize)
	if batchSize <= 0 {
		batchSize = 100 // Default batch size
	}

	writeCount := 0
	for {
		select {
		case <-ctx.Done():
			// Flush remaining stats
			if localBytes > 0 {
				c.bytesSent.Add(localBytes)
				c.packetsSent.Add(localPackets)
			}
			log.Printf("Connection %d: Sender stopping, total writes: %d, total bytes: %d", c.ID, writeCount, c.bytesSent.Load())
			return
		default:
			// Check if connection is still valid
			if c.conn == nil {
				return // Connection was closed
			}

			// Send large buffer at once
			n, err := c.conn.Write(buffer)
			if err != nil {
				log.Printf("Connection %d: Write error: %v", c.ID, err)
				// On error, flush stats
				if localBytes > 0 {
					c.bytesSent.Add(localBytes)
					c.packetsSent.Add(localPackets)
					localBytes = 0
					localPackets = 0
				}

				// Attempt to reconnect on connection errors
				if reconnectErr := c.reconnectTCP(ctx); reconnectErr != nil {
					log.Printf("Connection %d: Failed to reconnect: %v", c.ID, reconnectErr)
					return // Exit on context cancellation or unrecoverable error
				}

				// After successful reconnection, continue with next iteration
				continue
			}
			writeCount++

			// Accumulate local stats
			localBytes += int64(n)
			localPackets += int64(n) / packetSizeInv
			batchCount++

			// Batch atomic updates (or immediately on first write for debugging)
			if batchCount >= batchSize || batchCount == 1 {
				c.bytesSent.Add(localBytes)
				c.packetsSent.Add(localPackets)
				if writeCount == 1 {
					log.Printf("Connection %d: First write successful, n=%d, localBytes=%d", c.ID, n, localBytes)
				}
				if batchCount >= batchSize {
					localBytes = 0
					localPackets = 0
					batchCount = 0
				}

				// Only update full stats occasionally
				c.updateStats()
			}
		}
	}
}

func (c *Connection) tcpSenderPacket(ctx context.Context) {
	sequenceNum := uint64(0)
	packet := make([]byte, c.packetSize)

	// Rate-limited mode for precise packet testing
	interval := time.Second / time.Duration(c.pps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sequenceNum++

			// Check if connection is still valid
			if c.conn == nil {
				return // Connection was closed
			}

			// Prepare packet with timestamp for RTT measurement
			binary.BigEndian.PutUint64(packet[0:8], sequenceNum)
			binary.BigEndian.PutUint64(packet[8:16], uint64(time.Now().UnixNano()))

			// Send packet
			n, err := c.conn.Write(packet)
			if err != nil {
				log.Printf("Connection %d: Packet send error: %v", c.ID, err)
				// Attempt to reconnect on connection errors
				if reconnectErr := c.reconnectTCP(ctx); reconnectErr != nil {
					log.Printf("Connection %d: Failed to reconnect in packet mode: %v", c.ID, reconnectErr)
					return // Exit on context cancellation or unrecoverable error
				}
				continue
			}

			c.packetsSent.Add(1)
			c.bytesSent.Add(int64(n))
		}
	}
}

func (c *Connection) tcpReceiver(ctx context.Context) {
	if c.throughputMode {
		// In throughput mode, we don't receive echo - server just consumes
		// So receiver does nothing (like iperf client)
		log.Printf("Connection %d: TCP receiver in throughput mode - no echo expected", c.ID)
		<-ctx.Done()
	} else {
		// Packet mode: process individual packets for RTT
		log.Printf("Connection %d: TCP receiver in packet mode - expecting echo", c.ID)
		c.tcpReceiverPacket(ctx)
	}
}

func (c *Connection) tcpReceiverThroughput(ctx context.Context) {
	log.Printf("Connection %d: Starting TCP receiver in throughput mode, buffer size: %d", c.ID, c.BufferSize)

	// Get appropriate buffer from pool based on size
	var bufPtr *[]byte
	var poolPut func(interface{})

	bufPtr, poolPut = getOptimalBufferPool(c.BufferSize, c.UseOptimized)

	buffer := (*bufPtr)[:c.BufferSize]
	defer func() {
		poolPut(bufPtr)
	}()

	// Pre-calculate packet estimate divisor
	packetSizeInv := int64(c.packetSize)
	if packetSizeInv == 0 {
		packetSizeInv = 1
	}

	// Use local counters
	var localBytes int64
	var localPackets int64
	var batchCount int64
	batchSize := int64(c.RecvBatchSize)
	if batchSize <= 0 {
		batchSize = 100 // Default batch size
	}

	// Remove deadline for maximum throughput (check for nil first)
	if c.conn != nil {
		c.conn.SetReadDeadline(time.Time{})
	}

	for {
		select {
		case <-ctx.Done():
			// Flush remaining stats
			if localBytes > 0 {
				c.bytesReceived.Add(localBytes)
				c.packetsReceived.Add(localPackets)
			}
			return
		default:
			// Check if connection is still valid
			if c.conn == nil {
				log.Printf("Connection %d: Connection is nil in throughput receiver", c.ID)
				// Attempt to reconnect
				if reconnectErr := c.reconnectTCP(ctx); reconnectErr != nil {
					log.Printf("Connection %d: Failed to reconnect in throughput receiver (conn was nil): %v", c.ID, reconnectErr)
					return
				}
				// After reconnect, re-set the deadline
				if c.conn != nil {
					c.conn.SetReadDeadline(time.Time{})
				}
				continue
			}

			n, err := c.conn.Read(buffer)
			if err != nil {
				// Flush and continue on error
				if localBytes > 0 {
					c.bytesReceived.Add(localBytes)
					c.packetsReceived.Add(localPackets)
					localBytes = 0
					localPackets = 0
				}
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				log.Printf("Connection %d: Read error in throughput mode: %v", c.ID, err)
				// Attempt to reconnect on connection errors
				if reconnectErr := c.reconnectTCP(ctx); reconnectErr != nil {
					log.Printf("Connection %d: Failed to reconnect in throughput receiver: %v", c.ID, reconnectErr)
					return // Exit on context cancellation or unrecoverable error
				}
				continue
			}

			// Accumulate local stats
			localBytes += int64(n)
			localPackets += int64(n) / packetSizeInv
			batchCount++

			// Batch atomic updates
			if batchCount >= batchSize {
				c.bytesReceived.Add(localBytes)
				c.packetsReceived.Add(localPackets)
				localBytes = 0
				localPackets = 0
				batchCount = 0

				// Update full stats occasionally
				c.updateStats()
			}
		}
	}
}

func (c *Connection) tcpReceiverPacket(ctx context.Context) {
	buffer := make([]byte, c.packetSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Check if connection is still valid
			if c.conn == nil {
				log.Printf("Connection %d: Connection is nil in packet receiver", c.ID)
				// Attempt to reconnect
				if reconnectErr := c.reconnectTCP(ctx); reconnectErr != nil {
					log.Printf("Connection %d: Failed to reconnect in packet receiver (conn was nil): %v", c.ID, reconnectErr)
					return
				}
				continue
			}

			c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := c.conn.Read(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				log.Printf("Connection %d: Read error in packet mode: %v", c.ID, err)
				// Attempt to reconnect on connection errors
				if reconnectErr := c.reconnectTCP(ctx); reconnectErr != nil {
					log.Printf("Connection %d: Failed to reconnect in packet receiver: %v", c.ID, reconnectErr)
					return // Exit on context cancellation or unrecoverable error
				}
				continue
			}

			if n >= 16 {
				c.processReceivedPacket(buffer[:n])
			}
		}
	}
}

func (c *Connection) udpSender(ctx context.Context) {
	if c.throughputMode {
		// Throughput mode: send larger UDP packets
		c.udpSenderThroughput(ctx)
	} else {
		// Packet mode: precise packet-based sending
		c.udpSenderPacket(ctx)
	}
}

func (c *Connection) udpSenderThroughput(ctx context.Context) {
	// Use jumbo frames if available (9000 bytes)
	// Otherwise use maximum standard UDP packet
	maxPacketSize := 9000
	if c.BufferSize > maxPacketSize {
		maxPacketSize = c.BufferSize
	}
	if maxPacketSize > 65507 { // Max UDP payload
		maxPacketSize = 65507
	}

	// Get buffer from pool
	bufPtr, poolPut := getOptimalBufferPool(maxPacketSize, c.UseOptimized)
	packet := (*bufPtr)[:maxPacketSize]
	defer func() {
		poolPut(bufPtr)
	}()

	// Fill with pattern once
	for i := range packet {
		packet[i] = byte(i % 256)
	}

	sequenceNum := uint64(0)

	// Use local counters
	var localBytes int64
	var localPackets int64
	var batchCount int64
	batchSize := int64(c.SendBatchSize)
	if batchSize <= 0 {
		batchSize = 1000 // Default batch size for UDP
	}

	for {
		select {
		case <-ctx.Done():
			// Flush remaining stats
			if localBytes > 0 {
				c.bytesSent.Add(localBytes)
				c.packetsSent.Add(localPackets)
			}
			return
		default:
			// Check if connection is still valid
			if c.udpConn == nil {
				return // Connection was closed
			}

			sequenceNum++

			// Add minimal header for tracking (first 8 bytes only)
			binary.BigEndian.PutUint64(packet[0:8], sequenceNum)

			// Send large UDP packet
			n, err := c.udpConn.Write(packet)
			if err != nil {
				// Flush stats on error
				if localBytes > 0 {
					c.bytesSent.Add(localBytes)
					c.packetsSent.Add(localPackets)
					localBytes = 0
					localPackets = 0
				}
				continue
			}

			// Accumulate local stats
			localBytes += int64(n)
			localPackets++
			batchCount++

			// Batch atomic updates
			if batchCount >= batchSize {
				c.bytesSent.Add(localBytes)
				c.packetsSent.Add(localPackets)
				localBytes = 0
				localPackets = 0
				batchCount = 0

				// Update full stats occasionally
				c.updateStats()
			}
		}
	}
}

func (c *Connection) udpSenderPacket(ctx context.Context) {
	sequenceNum := uint64(0)
	packet := make([]byte, c.packetSize)

	// Rate-limited mode
	interval := time.Second / time.Duration(c.pps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sequenceNum++

			// Check if connection is still valid
			if c.udpConn == nil {
				return // Connection was closed
			}

			// Prepare packet with timestamp
			binary.BigEndian.PutUint64(packet[0:8], sequenceNum)
			binary.BigEndian.PutUint64(packet[8:16], uint64(time.Now().UnixNano()))

			// Send packet
			n, err := c.udpConn.Write(packet)
			if err != nil {
				continue
			}

			c.packetsSent.Add(1)
			c.bytesSent.Add(int64(n))
		}
	}
}

func (c *Connection) udpReceiver(ctx context.Context) {
	if c.throughputMode {
		// Throughput mode: receive larger packets
		c.udpReceiverThroughput(ctx)
	} else {
		// Packet mode: process individual packets for RTT
		c.udpReceiverPacket(ctx)
	}
}

func (c *Connection) udpReceiverThroughput(ctx context.Context) {

	// Use large buffer for receiving
	maxPacketSize := 65507 // Max UDP payload

	// Get buffer from pool
	bufPtr, poolPut := getOptimalBufferPool(maxPacketSize, c.UseOptimized)
	buffer := (*bufPtr)[:maxPacketSize]
	defer func() {
		poolPut(bufPtr)
	}()

	// Use local counters
	var localBytes int64
	var localPackets int64
	var batchCount int64
	batchSize := int64(c.RecvBatchSize)
	if batchSize <= 0 {
		batchSize = 1000 // Default batch size for UDP
	}

	// Set a short read timeout to prevent blocking forever
	c.udpConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))

	for {
		select {
		case <-ctx.Done():
			// Flush remaining stats
			if localBytes > 0 {
				c.bytesReceived.Add(localBytes)
				c.packetsReceived.Add(localPackets)
			}
			return
		default:
			// Reset deadline for each read attempt
			c.udpConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, err := c.udpConn.Read(buffer)
			if err != nil {
				// Flush stats on error
				if localBytes > 0 {
					c.bytesReceived.Add(localBytes)
					c.packetsReceived.Add(localPackets)
					localBytes = 0
					localPackets = 0
				}
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}

			// Only count if we got data
			if n > 0 {
				// Accumulate local stats
				localBytes += int64(n)
				localPackets++
				batchCount++

				// Batch atomic updates
				if batchCount >= batchSize {
					c.bytesReceived.Add(localBytes)
					c.packetsReceived.Add(localPackets)
					localBytes = 0
					localPackets = 0
					batchCount = 0

					// Update full stats occasionally
					c.updateStats()
				}
			}
		}
	}
}

func (c *Connection) udpReceiverPacket(ctx context.Context) {
	buffer := make([]byte, c.packetSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			c.udpConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := c.udpConn.Read(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}

			if n >= 16 {
				c.processReceivedPacket(buffer[:n])
			}
		}
	}
}

func (c *Connection) processReceivedPacket(data []byte) {
	// Extract timestamp
	sentTime := int64(binary.BigEndian.Uint64(data[8:16]))
	now := time.Now().UnixNano()
	rttNanos := now - sentTime
	rttMs := float64(rttNanos) / 1e6

	// Sanity check RTT - reject impossible values
	if rttMs < 0 || rttMs > 10000 { // Reject RTT > 10 seconds or negative
		// Log first few bad RTT values for debugging
		if len(c.rttHistory) < 5 {
			log.Printf("Connection %d: Bad RTT calculation: sent=%d, now=%d, rtt=%f ms",
				c.ID, sentTime, now, rttMs)
		}
		return // Don't process this packet
	}

	c.packetsReceived.Add(1)
	c.bytesReceived.Add(int64(len(data)))

	// Update RTT history
	c.rttMu.Lock()
	c.rttHistory = append(c.rttHistory, rttMs)
	if len(c.rttHistory) > 1000 {
		c.rttHistory = c.rttHistory[1:]
	}
	c.rttMu.Unlock()

	// Update stats
	c.updateStats()
}

func (c *Connection) updateStats() {
	// In throughput mode, skip expensive RTT calculations
	if c.throughputMode {
		c.updateStatsThroughput()
	} else {
		c.updateStatsFull()
	}
}

func (c *Connection) updateStatsThroughput() {
	// Fast path for throughput mode - no locks on hot path
	sent := c.packetsSent.Load()
	received := c.packetsReceived.Load()
	bytesSent := c.bytesSent.Load()
	bytesReceived := c.bytesReceived.Load()

	// Calculate throughput based on sent bytes (unidirectional like iperf)
	elapsed := time.Since(c.startTime).Seconds()
	var throughput float64
	if elapsed > 0 {
		// Always use sent bytes for throughput in throughput mode
		throughput = (float64(bytesSent) * 8) / (elapsed * 1000000)
	}

	// Single lock for stats update
	c.mu.Lock()
	c.stats.PacketsSent = sent
	c.stats.PacketsReceived = received
	c.stats.BytesSent = bytesSent
	c.stats.BytesReceived = bytesReceived
	c.stats.ThroughputMbps = throughput

	// Packet loss calculation - only meaningful for UDP in packet mode
	// TCP hides retransmissions from application, making loss measurement unreliable
	if c.throughputMode || c.protocol == "tcp" {
		c.stats.PacketsLost = -1 // Not applicable in throughput mode or TCP
		c.stats.LossRate = -1    // Not applicable in throughput mode or TCP
	} else {
		// UDP packet mode only: calculate packet loss
		c.stats.PacketsLost = sent - received
		if sent > 0 {
			c.stats.LossRate = float64(sent-received) / float64(sent) * 100
		} else {
			c.stats.LossRate = 0
		}
	}

	// Update reconnection stats
	c.stats.ReconnectCount = c.reconnectCount.Load()
	lastReconnectNano := c.lastReconnectTime.Load()
	if lastReconnectNano > 0 {
		c.stats.LastReconnectTime = time.Unix(0, lastReconnectNano)
	}

	c.stats.LastUpdate = time.Now()
	c.mu.Unlock()
}

func (c *Connection) updateStatsFull() {
	c.mu.Lock()
	defer c.mu.Unlock()

	sent := c.packetsSent.Load()
	received := c.packetsReceived.Load()
	bytesSent := c.bytesSent.Load()
	bytesReceived := c.bytesReceived.Load()

	c.stats.PacketsSent = sent
	c.stats.PacketsReceived = received
	c.stats.BytesSent = bytesSent
	c.stats.BytesReceived = bytesReceived

	// Packet loss calculation - only meaningful for UDP in packet mode
	// TCP hides retransmissions from application, making loss measurement unreliable
	if c.throughputMode || c.protocol == "tcp" {
		c.stats.PacketsLost = -1 // Not applicable in throughput mode or TCP
		c.stats.LossRate = -1    // Not applicable in throughput mode or TCP
	} else {
		// UDP packet mode only: calculate packet loss
		c.stats.PacketsLost = sent - received
		if sent > 0 {
			c.stats.LossRate = float64(sent-received) / float64(sent) * 100
		} else {
			c.stats.LossRate = 0
		}
	}

	// Calculate throughput based on received bytes (what actually got through)
	elapsed := time.Since(c.startTime).Seconds()
	if elapsed > 0 {
		// For received throughput (more meaningful for network testing)
		if bytesReceived > 0 {
			c.stats.ThroughputMbps = (float64(bytesReceived) * 8) / (elapsed * 1000000)
		} else {
			// If nothing received, show send rate
			c.stats.ThroughputMbps = (float64(bytesSent) * 8) / (elapsed * 1000000)
		}
	}

	// Calculate RTT statistics
	c.rttMu.Lock()
	if len(c.rttHistory) > 0 {
		sum := 0.0
		c.stats.MinRTTMs = c.rttHistory[0]
		c.stats.MaxRTTMs = c.rttHistory[0]

		for _, rtt := range c.rttHistory {
			sum += rtt
			if rtt < c.stats.MinRTTMs {
				c.stats.MinRTTMs = rtt
			}
			if rtt > c.stats.MaxRTTMs {
				c.stats.MaxRTTMs = rtt
			}
		}

		c.stats.AvgRTTMs = sum / float64(len(c.rttHistory))

		// Calculate jitter (standard deviation of RTT)
		if len(c.rttHistory) > 1 {
			variance := 0.0
			for _, rtt := range c.rttHistory {
				diff := rtt - c.stats.AvgRTTMs
				variance += diff * diff
			}
			c.stats.JitterMs = math.Sqrt(variance / float64(len(c.rttHistory)))
		}
	}
	c.rttMu.Unlock()

	// Update reconnection stats
	c.stats.ReconnectCount = c.reconnectCount.Load()
	lastReconnectNano := c.lastReconnectTime.Load()
	if lastReconnectNano > 0 {
		c.stats.LastReconnectTime = time.Unix(0, lastReconnectNano)
	}

	c.stats.LastUpdate = time.Now()
}

// GetStats returns a snapshot of the current performance metrics for this connection.
// The statistics include throughput, packet loss, latency, and jitter depending
// on the protocol and mode. This method is safe to call concurrently.
func (c *Connection) GetStats() ConnectionStats {
	c.updateStats()
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

func (c *Connection) periodicStatsUpdater(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.updateStats()
		}
	}
}

// reconnectTCP handles reconnection when the current TCP connection fails
func (c *Connection) reconnectTCP(ctx context.Context) error {
	// Close the existing broken connection
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
		c.tcpConn = nil
	}

	log.Printf("Connection %d: Attempting to reconnect to %s after connection failure", c.ID, c.TargetIP)

	addr := fmt.Sprintf("%s:%d", c.TargetIP, c.port)

	// Use the same indefinite retry logic as initial connection
	var conn net.Conn
	var err error
	firstAttempt := true
	retryCount := 0
	baseDelay := 200 * time.Millisecond
	maxDelay := 2 * time.Second

	for {
		conn, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			// Track successful reconnection
			c.reconnectCount.Add(1)
			c.lastReconnectTime.Store(time.Now().UnixNano())

			reconnectNum := c.reconnectCount.Load()
			if !firstAttempt {
				log.Printf("Connection %d: Reconnected to %s after %d retries (reconnection #%d)", c.ID, addr, retryCount, reconnectNum)
			} else {
				log.Printf("Connection %d: Reconnected to %s on first attempt (reconnection #%d)", c.ID, addr, reconnectNum)
			}
			break
		}

		if firstAttempt {
			log.Printf("Connection %d: Reconnection to %s failed, retrying indefinitely... (%v)", c.ID, addr, err)
			firstAttempt = false
		}

		retryCount++

		// Exponential backoff with jitter to avoid thundering herd
		delay := baseDelay
		if retryCount > 10 {
			// After 10 attempts, use exponential backoff up to maxDelay
			backoffDelay := time.Duration(min(int64(baseDelay)*(1<<min(retryCount-10, 4)), int64(maxDelay)))
			delay = backoffDelay
		}

		// Log periodic status for long-running reconnection attempts
		if retryCount%25 == 0 {
			log.Printf("Connection %d: Still retrying reconnection to %s (attempt %d)", c.ID, addr, retryCount)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while retrying reconnection to %s after %d attempts", addr, retryCount)
		case <-time.After(delay):
			// Continue to next retry
		}
	}

	// Update connection references
	c.conn = conn

	// Configure TCP socket for maximum throughput
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		c.tcpConn = tcpConn

		// Apply Linux-specific optimizations in optimized mode
		if c.UseOptimized {
			if err := c.applyOptimizedTCPOptions(tcpConn); err != nil {
				log.Printf("Warning: Could not apply optimized TCP options on reconnect: %v", err)
			}
		}

		// Set socket buffer sizes for 100Gbps networks
		if c.throughputMode {
			// Use large buffers based on configuration
			writeBuf := c.BufferSize * 4 // 4x buffer size for socket buffers
			if writeBuf < 16777216 {
				writeBuf = 16777216 // Minimum 16MB
			}
			if writeBuf > 134217728 {
				writeBuf = 134217728 // Max 128MB
			}
			tcpConn.SetWriteBuffer(writeBuf)
			tcpConn.SetReadBuffer(writeBuf)

			// Configure TCP options for high throughput
			tcpConn.SetNoDelay(c.TCPNoDelay)
		}
	}

	return nil
}
