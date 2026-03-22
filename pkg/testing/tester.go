package testing

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

	"github.com/weka/gosmesh/pkg/network"
)

// NetworkTester orchestrates network performance testing across multiple targets.
// It creates connections to all target IPs and measures performance metrics.
//
// Usage:
//
//	tester := testing.NewNetworkTester(
//	    "192.168.1.1",                          // local IP
//	    []string{"192.168.1.2", "192.168.1.3"}, // target IPs
//	    "tcp",                                   // protocol
//	    2,                                       // concurrency per target
//	    5*time.Minute,                          // duration
//	    5*time.Second,                          // report interval
//	    64000,                                   // packet size
//	    9999,                                    // port
//	    0,                                       // pps (0 = throughput mode)
//	)
//	if err := tester.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer tester.Stop()
//	tester.Wait()
//	report := tester.GenerateFinalReport()
//	fmt.Println(report)
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

	connections []*network.Connection
	mu          sync.RWMutex

	startTime time.Time
	endTime   time.Time

	// Optimization flags
	UseOptimized    bool
	EnableIOUring   bool
	EnableHugePages bool
	EnableOffload   bool

	// Performance tuning parameters
	BufferSize    int
	SendBatchSize int
	RecvBatchSize int
	NumQueues     int
	BusyPollUsecs int
	TCPCork       bool
	TCPQuickAck   bool
	TCPNoDelay    bool
	MemArenaSize  int
	RingSize      int
	NumWorkers    int
	CPUList       string

	// API reporting
	apiEndpoint string
	apiReportIP string
	apiTicker   *time.Ticker
}

// NewNetworkTester creates a new NetworkTester instance.
// Parameters:
//   - localIP: The local IP address to bind to for testing
//   - ips: List of target IP addresses to test against
//   - protocol: "tcp" or "udp"
//   - concurrency: Number of connections per target IP
//   - duration: How long the test should run
//   - reportInterval: How often to print periodic reports
//   - packetSize: Size of test packets in bytes
//   - port: Port to use for testing
//   - pps: Packets per second per connection (0 = unlimited throughput mode)
//
// Returns a new NetworkTester ready to be started.
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

// Start begins the network testing.
// It starts the echo server, creates connections to all targets, and starts periodic reporting.
// This is non-blocking; call Wait() to wait for completion or Stop() to end the test early.
func (nt *NetworkTester) Start() error {
	nt.startTime = time.Now()

	// Start server
	server := network.NewServer(nt.localIP, nt.port, nt.protocol, nt.packetSize)
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
			// Create connection
			conn := network.NewConnection(nt.localIP, targetIP, nt.port, nt.protocol, nt.packetSize, nt.pps, i)

			// Set optimization flag
			conn.UseOptimized = nt.UseOptimized

			// Apply performance tuning parameters
			if nt.BufferSize > 0 {
				conn.BufferSize = nt.BufferSize
			}
			if nt.NumWorkers > 0 {
				conn.NumWorkers = nt.NumWorkers
			}
			conn.SendBatchSize = nt.SendBatchSize
			conn.RecvBatchSize = nt.RecvBatchSize
			conn.TCPCork = nt.TCPCork
			conn.TCPQuickAck = nt.TCPQuickAck
			conn.TCPNoDelay = nt.TCPNoDelay
			conn.BusyPollUsecs = nt.BusyPollUsecs
			nt.connections = append(nt.connections, conn)

			// Start connection goroutine
			nt.wg.Add(1)
			go func(c *network.Connection) {
				defer nt.wg.Done()
				if err := c.Start(nt.ctx); err != nil {
					log.Printf("Connection to %s failed: %v", c.TargetIP, err)
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
		log.Printf("Test will stop automatically after %v", nt.duration)
		time.AfterFunc(nt.duration, func() {
			log.Printf("Test duration (%v) reached, stopping...", nt.duration)
			nt.Stop()
		})
	}

	return nil
}

// periodicReporter prints periodic reports at the configured interval.
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

// generatePeriodicReport creates a human-readable report of current statistics.
func (nt *NetworkTester) generatePeriodicReport() string {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	elapsed := time.Since(nt.startTime)
	report := fmt.Sprintf("\n=== Periodic Report (Elapsed: %v) ===\n", elapsed.Round(time.Second))

	throughputMode := nt.pps <= 0 // Check if in throughput mode

	for _, conn := range nt.connections {
		stats := conn.GetStats()
		report += fmt.Sprintf("Target: %s (conn-%d) | Protocol: %s\n", conn.TargetIP, conn.ID, nt.protocol)

		// Show packet loss only for UDP in packet mode
		if throughputMode {
			report += fmt.Sprintf("  Sent: %d | Packet Loss: Not applicable (throughput mode)\n", stats.PacketsSent)
		} else if nt.protocol == "tcp" {
			report += fmt.Sprintf("  Sent: %d | Packet Loss: Not applicable (use UDP for packet loss testing)\n", stats.PacketsSent)
		} else {
			report += fmt.Sprintf("  Sent: %d | Received: %d | Lost: %d (%.2f%%)\n",
				stats.PacketsSent, stats.PacketsReceived, stats.PacketsLost, stats.LossRate)
		}

		// Show RTT/Jitter only in packet mode
		if throughputMode {
			report += fmt.Sprintf("  Throughput: %.2f Mbps | RTT/Jitter: Not measured (throughput mode)\n", stats.ThroughputMbps)
		} else {
			report += fmt.Sprintf("  Throughput: %.2f Mbps | Avg RTT: %.2f ms | Jitter: %.2f ms\n",
				stats.ThroughputMbps, stats.AvgRTTMs, stats.JitterMs)
			report += fmt.Sprintf("  Min RTT: %.2f ms | Max RTT: %.2f ms\n",
				stats.MinRTTMs, stats.MaxRTTMs)
		}

		// Show reconnection stats
		if stats.ReconnectCount > 0 {
			timeSinceReconnect := time.Since(stats.LastReconnectTime).Round(time.Second)
			report += fmt.Sprintf("  Reconnections: %d (last: %v ago)\n", stats.ReconnectCount, timeSinceReconnect)
		} else {
			report += fmt.Sprintf("  Reconnections: 0\n")
		}
	}

	return report
}

// EnableAPIReporting configures the tester to send stats to an API endpoint.
// endpoint: HTTP endpoint to POST stats to
// reportIP: IP address to include in the report
func (nt *NetworkTester) EnableAPIReporting(endpoint, reportIP string) {
	nt.apiEndpoint = endpoint
	nt.apiReportIP = reportIP
}

// startAPIReporter starts the goroutine that sends periodic API reports.
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

// sendAPIReport sends current statistics to the configured API endpoint.
func (nt *NetworkTester) sendAPIReport() {
	// Calculate aggregate stats
	var totalThroughput float64
	var totalPacketsSent, totalPacketsReceived int64
	var avgRTT, avgJitter float64
	var connCount int
	var rttCount int // Count connections that actually have RTT data

	// Reconnection stats
	var totalReconnects int64
	targetReconnects := make(map[string]int64)

	nt.mu.RLock()
	throughputMode := nt.pps <= 0 // Check if we're in throughput mode

	for _, conn := range nt.connections {
		stats := conn.GetStats()
		totalThroughput += stats.ThroughputMbps
		totalPacketsSent += stats.PacketsSent
		totalPacketsReceived += stats.PacketsReceived

		// Collect reconnection stats
		totalReconnects += stats.ReconnectCount
		if stats.ReconnectCount > 0 {
			targetReconnects[conn.TargetIP] = stats.ReconnectCount
		}

		// Only include RTT/jitter if we have valid data (not in throughput mode)
		if !throughputMode && stats.AvgRTTMs > 0 {
			avgRTT += stats.AvgRTTMs
			avgJitter += stats.JitterMs
			rttCount++
		}
		connCount++
	}
	nt.mu.RUnlock()

	if rttCount > 0 {
		avgRTT /= float64(rttCount)
		avgJitter /= float64(rttCount)
	} else {
		// In throughput mode, these metrics aren't calculated
		avgRTT = -1 // Use -1 to indicate "not measured"
		avgJitter = -1
	}

	// Convert Mbps to Gbps
	throughputGbps := totalThroughput / 1000.0

	// Calculate packet loss percentage
	var lossPercent float64
	if throughputMode {
		// Check if loss rate is applicable for this protocol/mode
		nt.mu.RLock()
		validLossCount := 0
		var totalValidLossRate float64

		for _, conn := range nt.connections {
			stats := conn.GetStats()
			// Only include valid loss rates (not -1 which means "not applicable")
			if stats.LossRate >= 0 {
				totalValidLossRate += stats.LossRate
				validLossCount++
			}
		}
		nt.mu.RUnlock()

		if validLossCount > 0 {
			lossPercent = totalValidLossRate / float64(validLossCount)
		} else {
			// All connections report -1 (not applicable, e.g., TCP throughput mode)
			lossPercent = -1
		}
	} else {
		// Packet mode - calculate based on actual packet counts
		if totalPacketsSent > 0 {
			lossPercent = float64(totalPacketsSent-totalPacketsReceived) / float64(totalPacketsSent) * 100.0
		}
	}

	// Create stats payload
	stats := map[string]interface{}{
		"ip":                  nt.apiReportIP,
		"throughput_gbps":     throughputGbps,
		"packet_loss_percent": lossPercent,
		"jitter_ms":           avgJitter,
		"rtt_ms":              avgRTT,
		"reconnect_count":     totalReconnects,
		"target_reconnects":   targetReconnects,
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

// Stop terminates the test immediately.
func (nt *NetworkTester) Stop() {
	nt.cancel()
	nt.endTime = time.Now()
	if nt.apiTicker != nil {
		nt.apiTicker.Stop()
	}
}

// Wait blocks until all test goroutines have completed.
func (nt *NetworkTester) Wait() {
	nt.wg.Wait()
}

// GenerateFinalReport produces a comprehensive report of the test results.
// Returns a formatted string with per-target statistics and anomaly detection.
func (nt *NetworkTester) GenerateFinalReport() string {
	if nt.endTime.IsZero() {
		nt.endTime = time.Now()
	}

	testDuration := nt.endTime.Sub(nt.startTime)

	report := "\n\n" + strings.Repeat("=", 60) + "\n"
	report += "                    FINAL REPORT\n"
	report += strings.Repeat("=", 60) + "\n"
	report += fmt.Sprintf("Test Duration: %v\n", testDuration.Round(time.Second))
	report += fmt.Sprintf("Protocol: %s | Packet Size: %d bytes\n", nt.protocol, nt.packetSize)
	report += fmt.Sprintf("Concurrency: %d connections per target\n\n", nt.concurrency)

	// Collect stats from all connections
	type targetStats struct {
		ip    string
		conns []*network.ConnectionStats
	}

	targetMap := make(map[string]*targetStats)
	for _, conn := range nt.connections {
		stats := conn.GetStats()
		if _, exists := targetMap[conn.TargetIP]; !exists {
			targetMap[conn.TargetIP] = &targetStats{
				ip:    conn.TargetIP,
				conns: []*network.ConnectionStats{},
			}
		}
		targetMap[conn.TargetIP].conns = append(targetMap[conn.TargetIP].conns, &stats)
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
