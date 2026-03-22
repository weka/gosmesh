package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/weka/gosmesh/pkg/network"
)

// TargetMetrics contains performance metrics for a single target.
// This structure is used in both periodic and final reports via the API.
type TargetMetrics struct {
	// IP address of the target
	IP string `json:"ip"`

	// Packet statistics
	PacketsSent     int64   `json:"packets_sent"`
	PacketsReceived int64   `json:"packets_received"`
	PacketsLost     int64   `json:"packets_lost"`
	PacketLossRate  float64 `json:"packet_loss_rate"` // Percentage

	// Throughput
	BytesSent      int64   `json:"bytes_sent"`
	BytesReceived  int64   `json:"bytes_received"`
	ThroughputMbps float64 `json:"throughput_mbps"`

	// Latency (not measured in throughput mode)
	AvgRTTMs float64 `json:"avg_rtt_ms"`
	MinRTTMs float64 `json:"min_rtt_ms"`
	MaxRTTMs float64 `json:"max_rtt_ms"`
	JitterMs float64 `json:"jitter_ms"`

	// Connection statistics
	ConnectionCount   int        `json:"connection_count"`
	ReconnectCount    int64      `json:"reconnect_count"`
	LastReconnectTime *time.Time `json:"last_reconnect_time,omitempty"`
}

// TestMetadata contains metadata about the test configuration.
type TestMetadata struct {
	// Test configuration
	Protocol    string   `json:"protocol"`
	PacketSize  int      `json:"packet_size"`
	Concurrency int      `json:"concurrency"`
	Port        int      `json:"port"`
	PPS         int      `json:"pps"` // Packets per second (0 = throughput mode)
	LocalIP     string   `json:"local_ip"`
	TargetIPs   []string `json:"target_ips"`

	// Test timing
	StartTime  time.Time  `json:"start_time"`
	EndTime    *time.Time `json:"end_time,omitempty"`
	ElapsedMs  int64      `json:"elapsed_ms"`
	DurationMs int64      `json:"duration_ms"`

	// Mode information
	ThroughputMode bool `json:"throughput_mode"` // true if pps <= 0
	PacketMode     bool `json:"packet_mode"`     // true if pps > 0
}

// OverallStats contains aggregate statistics across all targets.
type OverallStats struct {
	// Aggregate throughput
	TotalThroughputMbps float64 `json:"total_throughput_mbps"`
	TotalThroughputGbps float64 `json:"total_throughput_gbps"`
	AvgThroughputMbps   float64 `json:"avg_throughput_mbps"`

	// Aggregate packet stats
	TotalPacketsSent     int64   `json:"total_packets_sent"`
	TotalPacketsReceived int64   `json:"total_packets_received"`
	TotalPacketsLost     int64   `json:"total_packets_lost"`
	AveragePacketLoss    float64 `json:"average_packet_loss"` // Percentage

	// Aggregate latency
	AvgRTTMs    float64 `json:"avg_rtt_ms"`
	AvgJitterMs float64 `json:"avg_jitter_ms"`
	MinRTTMs    float64 `json:"min_rtt_ms"`
	MaxRTTMs    float64 `json:"max_rtt_ms"`

	// Connection stats
	TotalConnections int   `json:"total_connections"`
	TotalReconnects  int64 `json:"total_reconnects"`

	// Target summary
	TargetCount int `json:"target_count"`
}

// TestResults is the common structure for both periodic and final reports via API.
// It contains all metrics, metadata, and aggregated statistics.
type TestResults struct {
	// Test metadata and configuration
	Metadata TestMetadata `json:"metadata"`

	// Overall aggregate statistics
	Overall OverallStats `json:"overall"`

	// Per-target detailed metrics
	Targets map[string]TargetMetrics `json:"targets"`

	// Anomalies detected (only in final results, empty in periodic)
	Anomalies []string `json:"anomalies,omitempty"`

	// Status indicator
	IsComplete bool   `json:"is_complete"` // true for final, false for periodic
	Message    string `json:"message,omitempty"`
}

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

	// API Server
	apiServer     *http.Server
	apiServerPort int
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

// GetCurrentStats returns a structured TestResults object containing current test statistics.
// This can be used programmatically or returned via an API endpoint.
// The results include current metrics for all targets and overall aggregates.
func (nt *NetworkTester) GetCurrentStats() TestResults {
	nt.mu.RLock()
	defer nt.mu.RUnlock()

	elapsed := time.Since(nt.startTime)
	throughputMode := nt.pps <= 0

	// Build targets map
	targetsMap := make(map[string]TargetMetrics)
	connsByTarget := make(map[string][]*network.ConnectionStats)

	for _, conn := range nt.connections {
		stats := conn.GetStats()
		connsByTarget[conn.TargetIP] = append(connsByTarget[conn.TargetIP], &stats)
	}

	var totalThroughput, avgRTT, avgJitter, totalPacketsSent, totalPacketsReceived, totalPacketsLost float64
	var minRTT float64 = 999999
	var maxRTT float64 = 0
	var rttCount int
	var totalReconnects int64

	for targetIP, conns := range connsByTarget {
		var targetThroughput, targetAvgRTT, targetAvgJitter, targetBytes float64
		var targetPacketsSent, targetPacketsReceived, targetPacketsLost int64
		var targetMinRTT float64 = 999999
		var targetMaxRTT float64 = 0
		var targetReconnects int64

		for _, stats := range conns {
			targetPacketsSent += stats.PacketsSent
			targetPacketsReceived += stats.PacketsReceived
			targetPacketsLost += stats.PacketsLost
			targetThroughput += stats.ThroughputMbps
			targetBytes += float64(stats.BytesSent)
			targetAvgRTT += stats.AvgRTTMs
			targetAvgJitter += stats.JitterMs
			targetReconnects += stats.ReconnectCount

			if stats.MinRTTMs > 0 && stats.MinRTTMs < targetMinRTT {
				targetMinRTT = stats.MinRTTMs
			}
			if stats.MaxRTTMs > targetMaxRTT {
				targetMaxRTT = stats.MaxRTTMs
			}
		}

		numConns := float64(len(conns))
		targetAvgRTT /= numConns
		targetAvgJitter /= numConns

		// Calculate packet loss rate
		targetLossRate := 0.0
		if targetPacketsSent > 0 {
			targetLossRate = float64(targetPacketsSent-targetPacketsReceived) / float64(targetPacketsSent) * 100
		}

		// Determine last reconnect time if any
		var lastReconnectTime *time.Time
		for _, stats := range conns {
			if stats.ReconnectCount > 0 && (lastReconnectTime == nil || stats.LastReconnectTime.After(*lastReconnectTime)) {
				lastReconnectTime = &stats.LastReconnectTime
			}
		}

		targetsMap[targetIP] = TargetMetrics{
			IP:                targetIP,
			PacketsSent:       targetPacketsSent,
			PacketsReceived:   targetPacketsReceived,
			PacketsLost:       targetPacketsLost,
			PacketLossRate:    targetLossRate,
			BytesSent:         int64(targetBytes),
			ThroughputMbps:    targetThroughput,
			AvgRTTMs:          targetAvgRTT,
			MinRTTMs:          targetMinRTT,
			MaxRTTMs:          targetMaxRTT,
			JitterMs:          targetAvgJitter,
			ConnectionCount:   len(conns),
			ReconnectCount:    targetReconnects,
			LastReconnectTime: lastReconnectTime,
		}

		// Aggregate for overall stats
		totalThroughput += targetThroughput
		totalPacketsSent += float64(targetPacketsSent)
		totalPacketsReceived += float64(targetPacketsReceived)
		totalPacketsLost += float64(targetPacketsLost)
		totalReconnects += targetReconnects

		if targetMinRTT < minRTT {
			minRTT = targetMinRTT
		}
		if targetMaxRTT > maxRTT {
			maxRTT = targetMaxRTT
		}

		if !throughputMode && targetAvgRTT > 0 {
			avgRTT += targetAvgRTT
			avgJitter += targetAvgJitter
			rttCount++
		}
	}

	if rttCount > 0 {
		avgRTT /= float64(rttCount)
		avgJitter /= float64(rttCount)
	} else {
		avgRTT = -1
		avgJitter = -1
		minRTT = -1
		maxRTT = -1
	}

	// Calculate average loss rate
	avgLoss := 0.0
	if totalPacketsSent > 0 {
		avgLoss = totalPacketsLost / totalPacketsSent * 100.0
	}

	overallStats := OverallStats{
		TotalThroughputMbps:  totalThroughput,
		TotalThroughputGbps:  totalThroughput / 1000.0,
		AvgThroughputMbps:    totalThroughput / float64(len(targetsMap)),
		TotalPacketsSent:     int64(totalPacketsSent),
		TotalPacketsReceived: int64(totalPacketsReceived),
		TotalPacketsLost:     int64(totalPacketsLost),
		AveragePacketLoss:    avgLoss,
		AvgRTTMs:             avgRTT,
		AvgJitterMs:          avgJitter,
		MinRTTMs:             minRTT,
		MaxRTTMs:             maxRTT,
		TotalConnections:     len(nt.connections),
		TotalReconnects:      totalReconnects,
		TargetCount:          len(targetsMap),
	}

	return TestResults{
		Metadata: TestMetadata{
			Protocol:       nt.protocol,
			PacketSize:     nt.packetSize,
			Concurrency:    nt.concurrency,
			Port:           nt.port,
			PPS:            nt.pps,
			LocalIP:        nt.localIP,
			TargetIPs:      nt.targetIPs,
			StartTime:      nt.startTime,
			ElapsedMs:      int64(elapsed.Milliseconds()),
			DurationMs:     int64(nt.duration.Milliseconds()),
			ThroughputMode: throughputMode,
			PacketMode:     !throughputMode,
		},
		Overall:    overallStats,
		Targets:    targetsMap,
		IsComplete: false,
		Message:    "Current test statistics",
	}
}

// GetFinalStats returns a structured TestResults object with final test results and anomalies.
// This includes the same structure as GetCurrentStats but marks the results as complete
// and includes anomaly detection.
func (nt *NetworkTester) GetFinalStats() TestResults {
	if nt.endTime.IsZero() {
		nt.endTime = time.Now()
	}

	// Get current stats as base
	results := nt.GetCurrentStats()

	// Mark as final
	results.IsComplete = true
	results.Message = "Final test results"
	endTime := nt.endTime
	results.Metadata.EndTime = &endTime

	// Detect anomalies
	var anomalies []string
	for ip, metrics := range results.Targets {
		if metrics.PacketLossRate > 5.0 {
			anomalies = append(anomalies, fmt.Sprintf("HIGH LOSS: %s has %.2f%% packet loss", ip, metrics.PacketLossRate))
		}
		if metrics.AvgRTTMs > 100.0 && metrics.AvgRTTMs > 0 {
			anomalies = append(anomalies, fmt.Sprintf("HIGH LATENCY: %s has %.2f ms average RTT", ip, metrics.AvgRTTMs))
		}
		if metrics.JitterMs > 20.0 && metrics.JitterMs > 0 {
			anomalies = append(anomalies, fmt.Sprintf("HIGH JITTER: %s has %.2f ms jitter", ip, metrics.JitterMs))
		}
		if metrics.ThroughputMbps < 10.0 && nt.protocol == "tcp" {
			anomalies = append(anomalies, fmt.Sprintf("LOW THROUGHPUT: %s has only %.2f Mbps", ip, metrics.ThroughputMbps))
		}
	}

	results.Anomalies = anomalies
	return results
}

// StartAPIServer starts an HTTP API server that serves test results.
// The server listens on localhost:port and provides endpoints:
//   - GET /api/stats - Returns current test statistics as JSON
//   - GET /api/stats/final - Returns final test statistics with anomalies as JSON
//
// Returns the port the server is listening on or an error if startup failed.
func (nt *NetworkTester) StartAPIServer(port int) error {
	nt.apiServerPort = port

	// Create router/mux
	mux := http.NewServeMux()

	// Endpoint for current stats
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := nt.GetCurrentStats()
		if err := json.NewEncoder(w).Encode(results); err != nil {
			log.Printf("Failed to encode current stats: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	// Endpoint for final stats
	mux.HandleFunc("/api/stats/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := nt.GetFinalStats()
		if err := json.NewEncoder(w).Encode(results); err != nil {
			log.Printf("Failed to encode final stats: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok","message":"API server is running"}`)
	})

	// Create server
	addr := net.JoinHostPort("localhost", fmt.Sprintf("%d", port))
	nt.apiServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in background
	nt.wg.Add(1)
	go func() {
		defer nt.wg.Done()
		log.Printf("Starting API server on http://%s", addr)
		if err := nt.apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
		}
	}()

	return nil
}

// StopAPIServer gracefully shuts down the API server.
func (nt *NetworkTester) StopAPIServer() error {
	if nt.apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return nt.apiServer.Shutdown(ctx)
	}
	return nil
}
