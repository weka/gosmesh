package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/weka/gosmesh/pkg/network"
	"github.com/weka/gosmesh/pkg/version"
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
	config *TestConfig // Source of truth for all configuration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	connections []*network.Connection
	mu          sync.RWMutex

	startTime time.Time
	endTime   time.Time

	networkServer *network.Server

	// API reporting
	apiEndpoint string
	apiReportIP string
	apiTicker   *time.Ticker

	// API Server
	apiServer *http.Server

	// Server control state
	isRunning bool
	lastError string
	controlMu sync.RWMutex // For protecting control state

	// testDone signals when the controlled test has completed (including report generation)
	testDone chan struct{}
}

// ServerStats contains minimal statistics for a single networkServer for aggregated reporting
type ServerStats struct {
	IP               string           `json:"ip"`
	Throughput       float64          `json:"throughput_gbps"`
	PacketLoss       float64          `json:"packet_loss_percent"`
	Jitter           float64          `json:"jitter_ms"`
	RTT              float64          `json:"rtt_ms"`
	ReconnectCount   int64            `json:"reconnect_count"`
	TargetReconnects map[string]int64 `json:"target_reconnects"`
	UpdatedAt        time.Time        `json:"updated_at"`
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
//   - apiServerPort: port number to listen for getStats requests. If 0, no API networkServer will be initialized
//
// Returns a new NetworkTester ready to be started.
func NewNetworkTester(localIP string, ips []string, protocol string, concurrency int,
	duration, reportInterval time.Duration, packetSize, port, pps int, apiServerPort int) *NetworkTester {

	ctx, cancel := context.WithCancel(context.Background())

	config := GetDefaultTestConfig()
	config.IPs = strings.Join(ips, ",")
	config.Protocol = protocol
	config.Duration = duration
	config.ReportInterval = reportInterval
	config.PacketSize = packetSize
	config.Port = port
	config.PPS = pps
	config.LocalIP = localIP
	config.ApiServerPort = apiServerPort

	return &NetworkTester{
		config:   config,
		ctx:      ctx,
		cancel:   cancel,
		testDone: make(chan struct{}),
	}
}

// reconfigure updates the tester's configuration for the next test run.
// This allows reusing the same NetworkTester instance for multiple tests without reinstantiation.
// Must be called when no test is currently running (after Stop() and Wait() complete).
// Replicates the auto-calculation logic from runWithConfig.
func (nt *NetworkTester) reconfigure(localIP string, targetIPs []string, protocol string,
	concurrency int, duration, reportInterval time.Duration, packetSize, port, pps int) error {

	// Update basic configuration in config
	nt.config.LocalIP = localIP
	if nt.config.LocalIP == "" {
		nt.config.LocalIP = detectLocalIP(targetIPs)
	}

	nt.config.IPs = strings.Join(targetIPs, ",")
	nt.config.Protocol = protocol
	nt.config.Duration = duration
	nt.config.ReportInterval = reportInterval
	nt.config.Port = port
	nt.config.PPS = pps

	// Auto-calculate concurrency if not specified (matching runWithConfig logic)
	if concurrency == 0 {
		numTargets := len(targetIPs) - 1 // Number of other servers to connect to (excluding self)
		if numTargets > 0 {
			// Use TotalConnections as the base for auto-calculation
			totalConnections := nt.config.TotalConnections

			// Calculate connections per target to distribute evenly
			baseConnections := totalConnections / numTargets
			remainder := totalConnections % numTargets

			// If we have a remainder, round up to ensure uniform distribution
			if remainder > 0 {
				concurrency = baseConnections + 1
				actualTotal := concurrency * numTargets
				log.Printf("Auto-adjusting: increasing total connections from %d to %d for uniform distribution (%d per target)",
					totalConnections, actualTotal, concurrency)
				nt.config.TotalConnections = actualTotal
			} else {
				concurrency = baseConnections
			}

			// Ensure minimum of 2 connections per target
			minConnectionsPerTarget := 2
			if concurrency < minConnectionsPerTarget {
				concurrency = minConnectionsPerTarget
				actualTotal := concurrency * numTargets
				log.Printf("Minimum connections: increasing total connections from %d to %d to maintain minimum %d connections per target",
					totalConnections, actualTotal, minConnectionsPerTarget)
				nt.config.TotalConnections = actualTotal
			}

			log.Printf("Configuration: %d total connections, %d targets, %d connections per target",
				nt.config.TotalConnections, numTargets, concurrency)
		} else {
			// Single node (loopback testing)
			concurrency = nt.config.TotalConnections
			log.Printf("Single node configuration: %d connections", concurrency)
		}
	}
	nt.config.Concurrency = concurrency

	// Auto-detect packet size if not specified (matching runWithConfig logic)
	if packetSize == 0 {
		// Assume jumbo frame capability for modern networks
		// In production, this would call performance.GetMTU(localIP)
		mtu := 9000 // Default to jumbo frame capability

		if mtu >= 9000 {
			if protocol == "udp" {
				packetSize = 8972 // Max UDP payload
			} else {
				packetSize = mtu - 40 // Account for IP/TCP headers
			}
			log.Printf("Auto-detected packet size (jumbo frames): %d bytes", packetSize)
		} else {
			if protocol == "udp" {
				packetSize = 1472 // Standard UDP
			} else {
				packetSize = 1460 // Standard TCP
			}
			log.Printf("Auto-detected packet size (standard): %d bytes", packetSize)
		}
	}
	nt.config.PacketSize = packetSize

	// Auto-enable throughput mode if PPS not specified (matching runWithConfig logic)
	// Throughput mode is determined by pps == 0 (unlimited)
	if pps == 0 {
		log.Printf("Throughput mode enabled (unlimited PPS)")
		nt.config.ThroughputMode = true
	} else {
		log.Printf("Packet mode enabled (PPS: %d)", pps)
		nt.config.ThroughputMode = false
	}

	// Auto-calculate buffer size for throughput mode if not specified (matching runWithConfig logic)
	// Throughput mode is when pps == 0
	if pps == 0 && nt.config.BufferSize == 0 {
		if protocol == "tcp" {
			nt.config.BufferSize = 4194304 // 4MB
		} else {
			nt.config.BufferSize = 1048576 // 1MB
		}
		log.Printf("Auto-selected buffer size for throughput mode: %d bytes", nt.config.BufferSize)
	}

	// Auto-calculate num_workers if not specified
	if nt.config.NumWorkers == 0 {
		numCPU := runtime.NumCPU()
		if numCPU > 0 {
			nt.config.NumWorkers = numCPU / 2 // Use half the CPUs
			if nt.config.NumWorkers < 1 {
				nt.config.NumWorkers = 1
			}
			log.Printf("Auto-calculated num_workers: %d (based on %d CPUs)", nt.config.NumWorkers, numCPU)
		}
	}

	// Auto-calculate num_queues if not specified
	if nt.config.NumQueues == 0 {
		// Use concurrency as guide for number of queues
		nt.config.NumQueues = concurrency
		if nt.config.NumQueues > 16 {
			nt.config.NumQueues = 16 // Cap at 16 queues
		}
		if nt.config.NumQueues < 1 {
			nt.config.NumQueues = 1
		}
		log.Printf("Auto-calculated num_queues: %d", nt.config.NumQueues)
	}

	// Clear old connections
	nt.mu.Lock()
	nt.connections = nil
	nt.mu.Unlock()

	// Clear old networkServer reference
	if nt.networkServer != nil {
		nt.networkServer.Stop()
	}
	nt.networkServer = nil

	// Create new context for the next run
	nt.ctx, nt.cancel = context.WithCancel(context.Background())

	// Reset timing
	nt.startTime = time.Time{}
	nt.endTime = time.Time{}

	// Set up API reporting if configured
	if nt.config.ReportTo != "" {
		log.Printf("Reporting stats to: %s", nt.config.ReportTo)
		nt.EnableAPIReporting(nt.config.ReportTo, localIP)
	}

	isThroughputMode := pps == 0
	log.Printf("NetworkTester reconfigured: IPs=%v, Protocol=%s, Concurrency=%d, PacketSize=%d, ThroughputMode=%v, Duration=%v",
		targetIPs, protocol, nt.config.Concurrency, nt.config.PacketSize, isThroughputMode, duration)

	return nil
}

// StartHTTPServer starts a unified HTTP networkServer for both test control and results API.
// Endpoints:
//   - POST /api/start - Start a new test (control)
//   - POST /api/stop - Stop running test (control)
//   - GET /api/status - Get current test status (control)
//   - GET /api/stats - Get current test statistics (API)
//   - GET /api/stats/final - Get final test statistics with anomalies (API)
//   - GET /health - Health check
//
// The networkServer listens on the specified port and handles both remote control commands
// and results queries from the same unified interface.
func (nt *NetworkTester) StartHTTPServer() error {
	if nt != nil && nt.apiServer != nil {
		log.Printf("NetworkTester has already http networkServer on port %d", nt.config.ApiServerPort)
		return nil
	}
	mux := http.NewServeMux()

	// Control endpoints
	mux.HandleFunc("/api/start", nt.handleStart)
	mux.HandleFunc("/api/stop", nt.handleStop)
	mux.HandleFunc("/api/status", nt.handleStatus)

	// API endpoints for stats
	mux.HandleFunc("/api/stats", nt.handleStats)

	// Final stats endpoint
	mux.HandleFunc("/api/stats/final", nt.handleStatsFinal)

	// Health check endpoint
	mux.HandleFunc("/health", nt.handleHealth)

	// Create HTTP networkServer
	addr := net.JoinHostPort("0.0.0.0", fmt.Sprintf("%d", nt.config.ApiServerPort))
	nt.apiServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start networkServer in background
	nt.wg.Add(1)
	go func() {
		defer nt.wg.Done()
		log.Printf("HTTP networkServer listening on http://%s", addr)
		log.Printf("Control endpoints: POST /start, POST /stop, GET /status")
		log.Printf("API endpoints: GET /api/stats, GET /api/stats/final, GET /health")
		if err := nt.apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP networkServer error: %v", err)
		}
	}()

	return nil
}

// StopHTTPServer gracefully stops the HTTP server
func (nt *NetworkTester) StopHTTPServer() {
	if nt.apiServer != nil {
		if err := nt.apiServer.Close(); err != nil {
			log.Printf("Error closing HTTP server: %v", err)
		}
		nt.apiServer = nil
	}
}

// StartWithConfig accepts a TestConfig and attempts to start the test.
// If a test is already running, it returns an error.
// The test runs asynchronously in the background.
// Missing/zero fields in config are filled with defaults.
func (nt *NetworkTester) StartWithConfig(config *TestConfig) error {
	nt.controlMu.Lock()
	defer nt.controlMu.Unlock()

	// Check if test already running
	if nt.isRunning {
		return fmt.Errorf("test is already running")
	}

	// Wait for any previous test goroutines to complete
	// This prevents WaitGroup counter issues when reusing the tester
	// Create a channel to signal when all goroutines are done
	done := make(chan struct{})
	go func() {
		nt.wg.Wait()
		close(done)
	}()

	// Wait with timeout to prevent hanging
	select {
	case <-done:
		// All previous goroutines completed
		log.Printf("Previous test goroutines completed, starting new test")
	case <-time.After(1 * time.Second):
		// Timeout - some goroutines may still be running
		log.Printf("Warning: timeout waiting for previous test to complete, proceeding anyway")
	}

	// Merge provided config with defaults - any zero values use defaults
	defaults := GetDefaultTestConfig()
	mergedConfig := MergeTestConfig(defaults, config)

	// Apply merged config to the tester
	nt.config = mergedConfig

	// Reconfigure the tester with the merged config
	if err := nt.reconfigure(
		mergedConfig.LocalIP,
		strings.Split(mergedConfig.IPs, ","),
		mergedConfig.Protocol,
		mergedConfig.Concurrency,
		mergedConfig.Duration,
		mergedConfig.ReportInterval,
		mergedConfig.PacketSize,
		mergedConfig.Port,
		mergedConfig.PPS,
	); err != nil {
		return fmt.Errorf("failed to reconfigure tester: %v", err)
	}

	nt.isRunning = true
	nt.startTime = time.Now()
	nt.lastError = ""

	// Run test in background
	go nt.runControlledTest()

	return nil
}

// handleStart handles POST /start requests to start a new test
func (nt *NetworkTester) handleStats(w http.ResponseWriter, r *http.Request) {
	_ = r // Unused parameter
	results := nt.GetCurrentStats()
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Printf("Failed to encode current stats: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"Failed to encode stats: %v"}`, err)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jsonData)
}

// handleStatsFinal handles GET /api/stats/final requests for final test results with anomalies
func (nt *NetworkTester) handleStatsFinal(w http.ResponseWriter, r *http.Request) {
	_ = r // Unused parameter
	w.Header().Set("Content-Type", "application/json")
	results := nt.GetFinalStats()
	if err := json.NewEncoder(w).Encode(results); err != nil {
		log.Printf("Failed to encode final stats: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// handleHealth handles GET /health requests for health check
func (nt *NetworkTester) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = r // Unused parameter
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"status":"ok","versionHash":"%s"}`, version.GetVersionHash())
}

// handleStart handles POST /start requests to start a new test
func (nt *NetworkTester) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var config TestConfig
	// Parse configuration from request body
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, fmt.Sprintf("Invalid config: %v", err), http.StatusBadRequest)
		return
	}

	// Use the new StartWithConfig function
	if err := nt.StartWithConfig(&config); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "Test started",
	})
}

// handleStop handles POST /stop requests to stop the running test
func (nt *NetworkTester) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nt.controlMu.Lock()
	if !nt.isRunning {
		nt.controlMu.Unlock()
		http.Error(w, "No test is currently running", http.StatusBadRequest)
		return
	}
	nt.controlMu.Unlock()

	log.Printf("Stopping test via control networkServer...")
	nt.StopAndFinalize()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "Test stopped",
	})
}

// handleStatus handles GET /status requests for test status
func (nt *NetworkTester) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	status := map[string]interface{}{
		"running": nt.isRunning,
	}

	locked := nt.controlMu.TryRLock()
	if !locked {
		_ = json.NewEncoder(w).Encode(status)
		http.Error(w, "Unable to acquire status lock, try again later", http.StatusServiceUnavailable)
		return
	}
	defer nt.controlMu.RUnlock()

	if nt.isRunning {
		status["uptime_seconds"] = time.Since(nt.startTime).Seconds()
		if nt.config != nil {
			status["config"] = nt.config
		}
	}

	if nt.lastError != "" {
		status["last_error"] = nt.lastError
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

// runControlledTest executes a test controlled via HTTP API
func (nt *NetworkTester) runControlledTest() {
	defer func() {
		nt.controlMu.Lock()
		wasRunning := nt.isRunning
		nt.isRunning = false
		nt.controlMu.Unlock()

		if wasRunning {
			log.Printf("Controlled test completed")
		}

		// Signal that the test is done
		close(nt.testDone)
	}()

	// Start test
	if err := nt.Start(); err != nil {
		nt.controlMu.Lock()
		nt.lastError = fmt.Sprintf("Failed to start test: %v", err)
		nt.controlMu.Unlock()
		log.Printf("Error: Failed to start test: %v", err)
		return
	}

	log.Printf("Controlled test started, waiting for completion...")

	// Wait for test completion
	nt.Wait()
}

// Start begins the network testing.
// It starts the echo networkServer, creates connections to all targets, and starts periodic reporting.
// This is non-blocking; call Wait() to wait for completion or Stop() to end the test early.
func (nt *NetworkTester) Start() error {
	nt.startTime = time.Now()

	if nt.config.ApiServerPort > 0 {
		if err := nt.StartHTTPServer(); err != nil {
			log.Printf("Failed to start API networkServer: %v\n", err)
		}
	}
	// Start networkServer
	nt.networkServer = network.NewServer(nt.config.LocalIP, nt.config.Port, nt.config.Protocol, nt.config.PacketSize)
	if err := nt.networkServer.Start(); err != nil {
		return fmt.Errorf("failed to start networkServer: %v", err)
	}

	nt.wg.Add(1)
	go func() {
		defer nt.wg.Done()
		nt.networkServer.Run(nt.ctx)
	}()

	// Create connections to all targets (excluding self)
	connectionCount := 0
	targetIPs := strings.Split(nt.config.IPs, ",")
	for _, targetIP := range targetIPs {
		if targetIP == nt.config.LocalIP {
			continue // Skip self
		}

		log.Printf("Creating %d connections to target %s", nt.config.Concurrency, targetIP)
		for i := 0; i < nt.config.Concurrency; i++ {
			// Create connection
			conn := network.NewConnection(nt.config.LocalIP, targetIP, nt.config.Port, nt.config.Protocol, nt.config.PacketSize, nt.config.PPS, i)

			// Set optimization flag
			conn.UseOptimized = nt.config.UseOptimized

			// Apply performance tuning parameters
			if nt.config.BufferSize > 0 {
				conn.BufferSize = nt.config.BufferSize
			}
			if nt.config.NumWorkers > 0 {
				conn.NumWorkers = nt.config.NumWorkers
			}
			conn.SendBatchSize = nt.config.SendBatchSize
			conn.RecvBatchSize = nt.config.RecvBatchSize
			conn.TCPCork = nt.config.TCPCork
			conn.TCPQuickAck = nt.config.TCPQuickAck
			conn.TCPNoDelay = nt.config.TCPNoDelay
			conn.BusyPollUsecs = nt.config.BusyPollUsecs
			nt.connections = append(nt.connections, conn)
			connectionCount++

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
	log.Printf("Created %d total connections", connectionCount)

	// Start periodic reporting
	nt.wg.Add(1)
	go nt.periodicReporter()

	// Start API reporting if configured
	if nt.apiEndpoint != "" {
		nt.startAPIReporter()
	}

	// Schedule test end
	if nt.config.Duration > 0 {
		log.Printf("Test will stop automatically after %v", nt.config.Duration)
		time.AfterFunc(nt.config.Duration, func() {
			log.Printf("Test duration (%v) reached, stopping...", nt.config.Duration)
			nt.StopAndFinalize()
		})
	}

	return nil
}

// periodicReporter prints periodic reports at the configured interval.
func (nt *NetworkTester) periodicReporter() {
	defer nt.wg.Done()
	ticker := time.NewTicker(nt.config.ReportInterval)
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

	throughputMode := nt.config.PPS <= 0 // Check if in throughput mode

	for _, conn := range nt.connections {
		stats := conn.GetStats()
		report += fmt.Sprintf("Target: %s (conn-%d) | Protocol: %s\n", conn.TargetIP, conn.ID, nt.config.Protocol)

		// Show packet loss only for UDP in packet mode
		if throughputMode {
			report += fmt.Sprintf("  Sent: %d | Packet Loss: Not applicable (throughput mode)\n", stats.PacketsSent)
		} else if nt.config.Protocol == "tcp" {
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
	throughputMode := nt.config.PPS <= 0 // Check if we're in throughput mode

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
	stats := &ServerStats{
		IP:               nt.config.LocalIP,
		Throughput:       throughputGbps,
		PacketLoss:       lossPercent,
		Jitter:           avgJitter,
		RTT:              avgRTT,
		ReconnectCount:   totalReconnects,
		TargetReconnects: targetReconnects,
		UpdatedAt:        time.Now(),
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
	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to send stats to API: %d (%s)", resp.StatusCode, resp.Status)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
}

// finalizTest generates and logs the final report when a test completes
func (nt *NetworkTester) finalizeTest() {
	// wait 1 second to make sure all connections are closed so it is printed last
	time.Sleep(1 * time.Second)
	report := nt.GenerateFinalReport()
	log.Printf("Test completed. Final Report:\n%s", report)
}

// Stop terminates the test immediately.
func (nt *NetworkTester) Stop() {
	nt.cancel()
	nt.endTime = time.Now()

	// Mark as no longer running (safe to do without lock since we're cancelling)
	nt.controlMu.Lock()
	nt.isRunning = false
	nt.controlMu.Unlock()

	if nt.apiTicker != nil {
		nt.apiTicker.Stop()
	}
	if nt.networkServer != nil {
		log.Printf("Stopping network networkServer...")
		nt.networkServer.Stop()
		nt.mu.Lock()
		nt.networkServer = nil
		nt.mu.Unlock()
	}
}

// StopAndFinalize stops the test and generates the final report
func (nt *NetworkTester) StopAndFinalize() {
	nt.Stop()
	nt.finalizeTest()
}

// Wait blocks until all test goroutines have completed.
func (nt *NetworkTester) Wait() {
	nt.wg.Wait()
}

// GetTestDone returns a channel that closes when the controlled test completes
// (including test execution and final report generation).
// This is useful for waiting for the entire test lifecycle to complete.
func (nt *NetworkTester) GetTestDone() <-chan struct{} {
	return nt.testDone
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
	report += fmt.Sprintf("Protocol: %s | Packet Size: %d bytes\n", nt.config.Protocol, nt.config.PacketSize)
	report += fmt.Sprintf("Concurrency: %d connections per target, total %d \n\n", nt.config.Concurrency, nt.config.TotalConnections)

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
		if avgThroughput < 10.0 && nt.config.Protocol == "tcp" {
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
	throughputMode := nt.config.PPS <= 0

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
			Protocol:       nt.config.Protocol,
			PacketSize:     nt.config.PacketSize,
			Concurrency:    nt.config.Concurrency,
			Port:           nt.config.Port,
			PPS:            nt.config.PPS,
			LocalIP:        nt.config.LocalIP,
			TargetIPs:      strings.Split(nt.config.IPs, ","),
			StartTime:      nt.startTime,
			ElapsedMs:      int64(elapsed.Milliseconds()),
			DurationMs:     int64(nt.config.Duration.Milliseconds()),
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
		if metrics.ThroughputMbps < 10.0 && nt.config.Protocol == "tcp" {
			anomalies = append(anomalies, fmt.Sprintf("LOW THROUGHPUT: %s has only %.2f Mbps", ip, metrics.ThroughputMbps))
		}
	}

	results.Anomalies = anomalies
	return results
}

// StopAPIServer gracefully shuts down the HTTP networkServer.
func (nt *NetworkTester) StopAPIServer() error {
	if nt.apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := nt.apiServer.Shutdown(ctx)
		if err == nil {
			nt.apiServer = nil
		}
		return err
	}
	return nil
}

func detectLocalIP(ipList []string) string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				for _, targetIP := range ipList {
					if ip.String() == targetIP {
						return targetIP
					}
				}
			}
		}
	}

	// Try binding to each IP
	for _, ip := range ipList {
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:0", ip))
		if err != nil {
			continue
		}
		conn, err := net.ListenUDP("udp", addr)
		if err == nil {
			_ = conn.Close()
			return ip
		}
	}

	return ""
}
