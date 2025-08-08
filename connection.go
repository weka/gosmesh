package main

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

type ConnectionStats struct {
	PacketsSent     int64
	PacketsReceived int64
	PacketsLost     int64
	BytesSent       int64
	BytesReceived   int64
	LossRate        float64
	ThroughputMbps  float64
	AvgRTTMs        float64
	MinRTTMs        float64
	MaxRTTMs        float64
	JitterMs        float64
	LastUpdate      time.Time
}

type Connection struct {
	id         int
	localIP    string
	targetIP   string
	port       int
	protocol   string
	packetSize int
	pps        int
	throughputMode bool  // New: throughput mode for max performance
	bufferSize     int   // New: configurable buffer size
	numWorkers     int   // New: number of parallel workers
	
	// Optimization parameters
	sendBatchSize  int
	recvBatchSize  int
	tcpCork        bool
	tcpQuickAck    bool
	tcpNoDelay     bool
	busyPollUsecs  int
	
	conn       net.Conn
	tcpConn    *net.TCPConn  // New: keep TCP conn for socket options
	udpConn    *net.UDPConn
	
	stats      ConnectionStats
	mu         sync.RWMutex
	
	rttHistory []float64
	rttMu      sync.Mutex
	
	startTime  time.Time
	
	packetsSent     atomic.Int64
	packetsReceived atomic.Int64
	bytesSent       atomic.Int64
	bytesReceived   atomic.Int64
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
			numWorkers = 8        // Use 8 parallel workers for TCP (increased)
		} else {
			bufferSize = 1048576  // 1MB for UDP batching (increased)
			numWorkers = 4        // Use 4 parallel workers for UDP (increased)
		}
	}
	
	return &Connection{
		id:         id,
		localIP:    localIP,
		targetIP:   targetIP,
		port:       port,
		protocol:   protocol,
		packetSize: packetSize,
		pps:        pps,
		throughputMode: throughputMode,
		bufferSize:     bufferSize,
		numWorkers:     numWorkers,
		rttHistory: make([]float64, 0, 1000),
	}
}

func (c *Connection) Start(ctx context.Context) error {
	c.startTime = time.Now()
	
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
	addr := fmt.Sprintf("%s:%d", c.targetIP, c.port)
	
	// Retry connection with shorter, more aggressive retries
	var conn net.Conn
	var err error
	maxRetries := 60  // Even more retries (12 seconds worth)
	firstAttempt := true
	for i := 0; i < maxRetries; i++ {
		conn, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)  // Even shorter timeout
		if err == nil {
			if !firstAttempt {
				log.Printf("Connection %d: Connected to %s after %d retries", c.id, addr, i)
			}
			break
		}
		if firstAttempt {
			log.Printf("Connection %d: Initial connection to %s failed, retrying... (%v)", c.id, addr, err)
			firstAttempt = false
		}
		if i < maxRetries-1 {
			// Fixed 200ms delay for faster retries
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled while retrying connection")
			case <-time.After(200 * time.Millisecond):
				// Continue to next retry
			}
		}
	}
	if err != nil {
		return fmt.Errorf("failed to connect to %s after %d retries: %v", addr, maxRetries, err)
	}
	c.conn = conn
	
	// Configure TCP socket for maximum throughput
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		c.tcpConn = tcpConn
		
		// Set socket buffer sizes for 100Gbps networks
		if c.throughputMode {
			// Use large buffers based on configuration
			writeBuf := c.bufferSize * 4 // 4x buffer size for socket buffers
			if writeBuf < 16777216 {
				writeBuf = 16777216 // Minimum 16MB
			}
			if writeBuf > 134217728 {
				writeBuf = 134217728 // Max 128MB
			}
			tcpConn.SetWriteBuffer(writeBuf)
			tcpConn.SetReadBuffer(writeBuf)
			
			// Apply TCP optimizations
			if c.tcpNoDelay {
				tcpConn.SetNoDelay(true)
			} else {
				tcpConn.SetNoDelay(false) // Enable Nagle's for throughput
			}
			
			// Set TCP keepalive for long connections
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(30 * time.Second)
		} else {
			// For packet mode, prioritize latency
			tcpConn.SetNoDelay(true)         // Disable Nagle's for low latency
			tcpConn.SetWriteBuffer(4194304)  // 4MB for packet mode
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
	serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", c.targetIP, c.port))
	if err != nil {
		return err
	}
	
	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return err
	}
	c.udpConn = conn
	defer c.udpConn.Close()
	
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
	log.Printf("Starting %d parallel workers for connection %d", c.numWorkers, c.id)
	var wg sync.WaitGroup
	wg.Add(c.numWorkers)
	
	// Start multiple sender workers
	for i := 0; i < c.numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			log.Printf("Worker %d starting for connection %d", workerID, c.id)
			// Each worker runs the throughput sender
			c.tcpSenderThroughput(ctx)
		}(i)
	}
	
	wg.Wait()
	log.Printf("All workers finished for connection %d", c.id)
}

func (c *Connection) tcpSenderThroughput(ctx context.Context) {
	// Use the configured buffer size for writes
	writeBufferSize := c.bufferSize
	if writeBufferSize > 4194304 {
		writeBufferSize = 4194304 // Cap at 4MB for safety
	}
	
	log.Printf("Connection %d: Starting TCP sender in throughput mode, write buffer: %d", c.id, writeBufferSize)
	
	// Get appropriate buffer from pool based on size
	var bufPtr *[]byte
	var poolPut func(interface{})
	
	if writeBufferSize <= 65536 {
		bufPtr = mediumBufferPool.Get().(*[]byte)
		poolPut = mediumBufferPool.Put
	} else {
		bufPtr = largeBufferPool.Get().(*[]byte)
		poolPut = largeBufferPool.Put
	}
	
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
	batchSize := int64(c.sendBatchSize)
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
			log.Printf("Connection %d: Sender stopping, total writes: %d, total bytes: %d", c.id, writeCount, c.bytesSent.Load())
			return
		default:
			// Send large buffer at once
			n, err := c.conn.Write(buffer)
			if err != nil {
				log.Printf("Connection %d: Write error: %v", c.id, err)
				// On error, flush stats and continue
				if localBytes > 0 {
					c.bytesSent.Add(localBytes)
					c.packetsSent.Add(localPackets)
					localBytes = 0
					localPackets = 0
				}
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
					log.Printf("Connection %d: First write successful, n=%d, localBytes=%d", c.id, n, localBytes)
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
			
			// Prepare packet with timestamp for RTT measurement
			binary.BigEndian.PutUint64(packet[0:8], sequenceNum)
			binary.BigEndian.PutUint64(packet[8:16], uint64(time.Now().UnixNano()))
			
			// Send packet
			n, err := c.conn.Write(packet)
			if err != nil {
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
		log.Printf("Connection %d: TCP receiver in throughput mode - no echo expected", c.id)
		<-ctx.Done()
	} else {
		// Packet mode: process individual packets for RTT
		c.tcpReceiverPacket(ctx)
	}
}

func (c *Connection) tcpReceiverThroughput(ctx context.Context) {
	log.Printf("Connection %d: Starting TCP receiver in throughput mode, buffer size: %d", c.id, c.bufferSize)
	
	// Get appropriate buffer from pool based on size
	var bufPtr *[]byte
	var poolPut func(interface{})
	
	if c.bufferSize <= 65536 {
		bufPtr = mediumBufferPool.Get().(*[]byte)
		poolPut = mediumBufferPool.Put
	} else if c.bufferSize <= 4194304 {
		bufPtr = largeBufferPool.Get().(*[]byte)
		poolPut = largeBufferPool.Put
	} else {
		bufPtr = jumboBufferPool.Get().(*[]byte)
		poolPut = jumboBufferPool.Put
	}
	
	buffer := (*bufPtr)[:c.bufferSize]
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
	batchSize := int64(c.recvBatchSize)
	if batchSize <= 0 {
		batchSize = 100 // Default batch size
	}
	
	// Remove deadline for maximum throughput
	c.conn.SetReadDeadline(time.Time{})
	
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
				return
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
			c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := c.conn.Read(buffer)
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
	if c.bufferSize > maxPacketSize {
		maxPacketSize = c.bufferSize
	}
	if maxPacketSize > 65507 { // Max UDP payload
		maxPacketSize = 65507
	}
	
	// Get buffer from pool
	bufPtr := largeBufferPool.Get().(*[]byte)
	packet := (*bufPtr)[:maxPacketSize]
	defer func() {
		largeBufferPool.Put(bufPtr)
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
	batchSize := int64(c.sendBatchSize)
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
	bufPtr := largeBufferPool.Get().(*[]byte)
	buffer := (*bufPtr)[:maxPacketSize]
	defer func() {
		largeBufferPool.Put(bufPtr)
	}()
	
	// Use local counters
	var localBytes int64
	var localPackets int64
	var batchCount int64
	batchSize := int64(c.recvBatchSize)
	if batchSize <= 0 {
		batchSize = 1000 // Default batch size for UDP
	}
	
	// Remove deadline for maximum throughput
	c.udpConn.SetReadDeadline(time.Time{})
	
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
	c.stats.PacketsLost = sent - received
	c.stats.BytesSent = bytesSent
	c.stats.BytesReceived = bytesReceived
	c.stats.ThroughputMbps = throughput
	if sent > 0 {
		c.stats.LossRate = float64(sent-received) / float64(sent) * 100
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
	c.stats.PacketsLost = sent - received
	c.stats.BytesSent = bytesSent
	c.stats.BytesReceived = bytesReceived
	
	if sent > 0 {
		c.stats.LossRate = float64(sent-received) / float64(sent) * 100
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
	
	c.stats.LastUpdate = time.Now()
}

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