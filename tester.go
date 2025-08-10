package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type NetworkTester struct {
	localIP        string
	targetIPs      []string
	protocol       string
	concurrency    int
	duration       time.Duration
	reportInterval time.Duration
	packetSize     int
	port           int
	pps            int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	connections []*Connection
	mu          sync.RWMutex
	
	startTime time.Time
	endTime   time.Time
	
	// Optimization flags
	UseOptimized    bool
	EnableIOUring   bool
	EnableHugePages bool
	EnableOffload   bool
	
	// Performance tuning parameters
	BufferSize     int
	SendBatchSize  int
	RecvBatchSize  int
	NumQueues      int
	BusyPollUsecs  int
	TCPCork        bool
	TCPQuickAck    bool
	TCPNoDelay     bool
	MemArenaSize   int
	RingSize       int
	NumWorkers     int
	CPUList        string
	
	// API reporting
	apiEndpoint    string
	apiReportIP    string
	apiTicker      *time.Ticker
}

func NewNetworkTester(localIP string, ips []string, protocol string, concurrency int, 
	duration, reportInterval time.Duration, packetSize, port, pps int) *NetworkTester {
	
	ctx, cancel := context.WithCancel(context.Background())
	
	return &NetworkTester{
		localIP:        localIP,
		targetIPs:      ips,
		protocol:       protocol,
		concurrency:    concurrency,
		duration:       duration,
		reportInterval: reportInterval,
		packetSize:     packetSize,
		port:           port,
		pps:            pps,
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (nt *NetworkTester) Start() error {
	nt.startTime = time.Now()
	
	// Start server
	server := NewServer(nt.localIP, nt.port, nt.protocol, nt.packetSize)
	if err := server.Start(); err != nil {
		return fmt.Errorf("failed to start server: %v", err)
	}
	
	nt.wg.Add(1)
	go func() {
		defer nt.wg.Done()
		server.Run(nt.ctx)
	}()

	// Give servers time to start on all nodes
	// This is critical - without enough delay, clients will exhaust retries
	// In mesh mode, ALL nodes must have their servers ready before ANY connections start
	log.Printf("Waiting 10 seconds for all servers to start across the mesh...")
	time.Sleep(10 * time.Second)

	// Create connections to all targets (excluding self)
	for _, targetIP := range nt.targetIPs {
		if targetIP == nt.localIP {
			continue // Skip self
		}
		
		for i := 0; i < nt.concurrency; i++ {
			conn := NewConnection(nt.localIP, targetIP, nt.port, nt.protocol, nt.packetSize, nt.pps, i)
			// Apply performance tuning parameters
			if nt.BufferSize > 0 {
				conn.bufferSize = nt.BufferSize
			}
			if nt.NumWorkers > 0 {
				conn.numWorkers = nt.NumWorkers
			}
			conn.sendBatchSize = nt.SendBatchSize
			conn.recvBatchSize = nt.RecvBatchSize
			conn.tcpCork = nt.TCPCork
			conn.tcpQuickAck = nt.TCPQuickAck
			conn.tcpNoDelay = nt.TCPNoDelay
			conn.busyPollUsecs = nt.BusyPollUsecs
			nt.connections = append(nt.connections, conn)
			
			nt.wg.Add(1)
			go func(c *Connection) {
				defer nt.wg.Done()
				if err := c.Start(nt.ctx); err != nil {
					log.Printf("Connection to %s failed: %v", c.targetIP, err)
				}
			}(conn)
		}
	}

	// Start periodic reporting
	nt.wg.Add(1)
	go nt.periodicReporter()
	
	// Start API reporting if configured
	if nt.apiEndpoint != "" {
		nt.startAPIReporter()
	}

	// Schedule test end
	if nt.duration > 0 {
		time.AfterFunc(nt.duration, func() {
			log.Println("Test duration reached, stopping...")
			nt.Stop()
		})
	}

	return nil
}

func (nt *NetworkTester) periodicReporter() {
	defer nt.wg.Done()
	ticker := time.NewTicker(nt.reportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			report := nt.generatePeriodicReport()
			fmt.Println(report)
		case <-nt.ctx.Done():
			return
		}
	}
}

func (nt *NetworkTester) generatePeriodicReport() string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	elapsed := time.Since(nt.startTime)
	report := fmt.Sprintf("\n=== Periodic Report (Elapsed: %v) ===\n", elapsed.Round(time.Second))
	
	for _, conn := range nt.connections {
		stats := conn.GetStats()
		report += fmt.Sprintf("Target: %s (conn-%d) | Protocol: %s\n", conn.targetIP, conn.id, nt.protocol)
		report += fmt.Sprintf("  Sent: %d | Received: %d | Lost: %d (%.2f%%)\n", 
			stats.PacketsSent, stats.PacketsReceived, stats.PacketsLost, stats.LossRate)
		report += fmt.Sprintf("  Throughput: %.2f Mbps | Avg RTT: %.2f ms | Jitter: %.2f ms\n",
			stats.ThroughputMbps, stats.AvgRTTMs, stats.JitterMs)
		report += fmt.Sprintf("  Min RTT: %.2f ms | Max RTT: %.2f ms\n", 
			stats.MinRTTMs, stats.MaxRTTMs)
	}
	
	return report
}

func (nt *NetworkTester) EnableAPIReporting(endpoint, reportIP string) {
	nt.apiEndpoint = endpoint
	nt.apiReportIP = reportIP
}

func (nt *NetworkTester) startAPIReporter() {
	nt.apiTicker = time.NewTicker(1 * time.Second)
	nt.wg.Add(1)
	
	go func() {
		defer nt.wg.Done()
		for {
			select {
			case <-nt.apiTicker.C:
				nt.sendAPIReport()
			case <-nt.ctx.Done():
				if nt.apiTicker != nil {
					nt.apiTicker.Stop()
				}
				return
			}
		}
	}()
}

func (nt *NetworkTester) sendAPIReport() {
	// Calculate aggregate stats
	var totalThroughput float64
	var totalPacketsSent, totalPacketsReceived int64
	var avgRTT, avgJitter float64
	var connCount int
	
	nt.mu.RLock()
	for _, conn := range nt.connections {
		stats := conn.GetStats()
		totalThroughput += stats.ThroughputMbps
		totalPacketsSent += stats.PacketsSent
		totalPacketsReceived += stats.PacketsReceived
		avgRTT += stats.AvgRTTMs
		avgJitter += stats.JitterMs
		connCount++
	}
	nt.mu.RUnlock()
	
	if connCount > 0 {
		avgRTT /= float64(connCount)
		avgJitter /= float64(connCount)
	}
	
	// Convert Mbps to Gbps
	throughputGbps := totalThroughput / 1000.0
	
	// Calculate packet loss percentage
	var lossPercent float64
	if totalPacketsSent > 0 {
		lossPercent = float64(totalPacketsSent-totalPacketsReceived) / float64(totalPacketsSent) * 100.0
	}
	
	// Create stats payload
	stats := map[string]interface{}{
		"ip":                   nt.apiReportIP,
		"throughput_gbps":      throughputGbps,
		"packet_loss_percent":  lossPercent,
		"jitter_ms":           avgJitter,
		"rtt_ms":              avgRTT,
	}
	
	jsonData, err := json.Marshal(stats)
	if err != nil {
		log.Printf("Failed to marshal stats: %v", err)
		return
	}
	
	// Send to API endpoint
	resp, err := http.Post(nt.apiEndpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to send stats to API: %v", err)
		return
	}
	defer resp.Body.Close()
}

func (nt *NetworkTester) Stop() {
	nt.cancel()
	nt.endTime = time.Now()
	if nt.apiTicker != nil {
		nt.apiTicker.Stop()
	}
}

func (nt *NetworkTester) Wait() {
	nt.wg.Wait()
}

func (nt *NetworkTester) GenerateFinalReport() string {
	if nt.endTime.IsZero() {
		nt.endTime = time.Now()
	}
	
	testDuration := nt.endTime.Sub(nt.startTime)
	
	report := fmt.Sprintf("\n\n" + strings.Repeat("=", 60) + "\n")
	report += fmt.Sprintf("                    FINAL REPORT\n")
	report += fmt.Sprintf(strings.Repeat("=", 60) + "\n")
	report += fmt.Sprintf("Test Duration: %v\n", testDuration.Round(time.Second))
	report += fmt.Sprintf("Protocol: %s | Packet Size: %d bytes\n", nt.protocol, nt.packetSize)
	report += fmt.Sprintf("Concurrency: %d connections per target\n\n", nt.concurrency)
	
	// Collect stats from all connections
	type targetStats struct {
		ip    string
		conns []*ConnectionStats
	}
	
	targetMap := make(map[string]*targetStats)
	for _, conn := range nt.connections {
		stats := conn.GetStats()
		if _, exists := targetMap[conn.targetIP]; !exists {
			targetMap[conn.targetIP] = &targetStats{
				ip:    conn.targetIP,
				conns: []*ConnectionStats{},
			}
		}
		targetMap[conn.targetIP].conns = append(targetMap[conn.targetIP].conns, &stats)
	}
	
	// Generate per-target summary
	report += "PER-TARGET SUMMARY:\n"
	report += strings.Repeat("-", 60) + "\n"
	
	var anomalies []string
	
	for ip, target := range targetMap {
		var totalSent, totalReceived, totalLost int64
		var avgThroughput, avgRTT, avgJitter float64
		var minRTT float64 = 999999
		var maxRTT float64 = 0
		
		for _, stats := range target.conns {
			totalSent += stats.PacketsSent
			totalReceived += stats.PacketsReceived
			totalLost += stats.PacketsLost
			avgThroughput += stats.ThroughputMbps
			avgRTT += stats.AvgRTTMs
			avgJitter += stats.JitterMs
			
			if stats.MinRTTMs < minRTT {
				minRTT = stats.MinRTTMs
			}
			if stats.MaxRTTMs > maxRTT {
				maxRTT = stats.MaxRTTMs
			}
		}
		
		numConns := float64(len(target.conns))
		// Total throughput is the sum of all connections, not average
		totalThroughput := avgThroughput
		avgRTT /= numConns
		avgJitter /= numConns
		lossRate := 0.0
		if totalSent > 0 {
			lossRate = float64(totalLost) / float64(totalSent) * 100
		}
		
		report += fmt.Sprintf("\nTarget: %s (%d connections)\n", ip, len(target.conns))
		report += fmt.Sprintf("  Total Packets: Sent=%d | Received=%d | Lost=%d\n", 
			totalSent, totalReceived, totalLost)
		report += fmt.Sprintf("  Loss Rate: %.2f%%\n", lossRate)
		report += fmt.Sprintf("  Total Throughput: %.2f Mbps (%.2f Gbps)\n", totalThroughput, totalThroughput/1000)
		report += fmt.Sprintf("  RTT: Min=%.2f ms | Avg=%.2f ms | Max=%.2f ms\n", 
			minRTT, avgRTT, maxRTT)
		report += fmt.Sprintf("  Avg Jitter: %.2f ms\n", avgJitter)
		
		// Detect anomalies
		if lossRate > 5.0 {
			anomalies = append(anomalies, fmt.Sprintf("HIGH LOSS: %s has %.2f%% packet loss", ip, lossRate))
		}
		if avgRTT > 100.0 {
			anomalies = append(anomalies, fmt.Sprintf("HIGH LATENCY: %s has %.2f ms average RTT", ip, avgRTT))
		}
		if avgJitter > 20.0 {
			anomalies = append(anomalies, fmt.Sprintf("HIGH JITTER: %s has %.2f ms jitter", ip, avgJitter))
		}
		if avgThroughput < 10.0 && nt.protocol == "tcp" {
			anomalies = append(anomalies, fmt.Sprintf("LOW THROUGHPUT: %s has only %.2f Mbps", ip, avgThroughput))
		}
	}
	
	// Report anomalies
	if len(anomalies) > 0 {
		report += "\n" + strings.Repeat("!", 60) + "\n"
		report += "                    ANOMALIES DETECTED\n"
		report += strings.Repeat("!", 60) + "\n"
		for _, anomaly := range anomalies {
			report += fmt.Sprintf("  • %s\n", anomaly)
		}
	} else {
		report += "\n" + strings.Repeat("✓", 60) + "\n"
		report += "           All connections performing normally\n"
		report += strings.Repeat("✓", 60) + "\n"
	}
	
	return report
}