package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
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
	
	conn       net.Conn
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

func NewConnection(localIP, targetIP string, port int, protocol string, packetSize int, id int) *Connection {
	return &Connection{
		id:         id,
		localIP:    localIP,
		targetIP:   targetIP,
		port:       port,
		protocol:   protocol,
		packetSize: packetSize,
		rttHistory: make([]float64, 0, 1000),
	}
}

func (c *Connection) Start(ctx context.Context) error {
	c.startTime = time.Now()
	
	if c.protocol == "tcp" {
		return c.startTCP(ctx)
	}
	return c.startUDP(ctx)
}

func (c *Connection) startTCP(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.targetIP, c.port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %v", addr, err)
	}
	c.conn = conn
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
	ticker := time.NewTicker(10 * time.Millisecond) // 100 pps
	defer ticker.Stop()
	
	sequenceNum := uint64(0)
	packet := make([]byte, c.packetSize)
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sequenceNum++
			
			// Prepare packet
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
	ticker := time.NewTicker(10 * time.Millisecond) // 100 pps
	defer ticker.Stop()
	
	sequenceNum := uint64(0)
	packet := make([]byte, c.packetSize)
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sequenceNum++
			
			// Prepare packet
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
	
	// Calculate throughput
	elapsed := time.Since(c.startTime).Seconds()
	if elapsed > 0 {
		c.stats.ThroughputMbps = (float64(bytesReceived) * 8) / (elapsed * 1000000)
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