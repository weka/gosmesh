// Package mesh provides the MeshController for orchestrating network tests across multiple nodes.
//
// The MeshController can operate in two modes:
// 1. With SSH deployment (automatic binary deployment and service start)
// 2. Without SSH deployment (assumes servers are already running)
//
// Usage:
//
//	controller := mesh.NewController(config)
//	if err := controller.Deploy(); err != nil {
//	    log.Fatal(err)
//	}
//	if err := controller.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	controller.Wait()
//	stats := controller.GetStats()
//
// Or with automatic deployment:
//
//	controller := mesh.NewController(config)
//	controller.Run()  // Does Deploy + Start + Wait
package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/weka/gosmesh/pkg/testing"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weka/gosmesh/pkg/workers"
)

const (
	GoSmeshServiceName = "gosmesh-mesh"
	GoSmeshRemotePath  = "/opt/gosmesh"
)

// Config contains configuration for the MeshController
type Config struct {
	// Network configuration
	IPs      string
	Port     int
	Protocol string
	LocalIP  string // Optional: explicitly set local IP

	// Test configuration
	Duration         time.Duration
	ReportInterval   time.Duration
	ThroughputMode   bool
	Concurrency      int
	TotalConnections int
	PacketSize       int
	PPS              int

	// SSH deployment (optional)
	SSHHosts             string // Comma-separated SSH hosts
	DeploySystemDService bool   // Enable SSH deployment

	// Performance tuning
	BufferSize      int
	TCPNoDelay      bool
	UseOptimized    bool
	EnableIOUring   bool
	EnableHugePages bool
	EnableOffload   bool
	SendBatchSize   int
	RecvBatchSize   int
	NumQueues       int
	BusyPollUsecs   int
	TCPCork         bool
	TCPQuickAck     bool
	MemArenaSize    int
	RingSize        int
	NumWorkers      int
	CPUList         string

	// API configuration
	APIPort       int
	ReportTo      string
	WorkerAPIPort int

	// Misc
	Verbose bool
}

// MeshStats contains aggregated statistics across all nodes
type MeshStats struct {
	Timestamp     time.Time `json:"timestamp"`
	ActiveServers int       `json:"active_servers"`
	TotalServers  int       `json:"total_servers"`

	// Throughput stats
	TotalThroughput float64 `json:"total_throughput_gbps"`
	AvgThroughput   float64 `json:"avg_throughput_gbps"`
	MinThroughput   float64 `json:"min_throughput_gbps"`
	MaxThroughput   float64 `json:"max_throughput_gbps"`

	// Connection stats
	AvgPacketLoss   float64                         `json:"avg_packet_loss_percent"`
	AvgJitter       float64                         `json:"avg_jitter_ms"`
	AvgRTT          float64                         `json:"avg_rtt_ms"`
	TotalReconnects int64                           `json:"total_reconnects"`
	ServerStats     map[string]*testing.ServerStats `json:"server_stats"`
	UpdatedAt       time.Time                       `json:"updated_at"`

	// Aggregate metrics for jitter and packet loss
	TotalPacketLoss float64 `json:"total_packet_loss"`
	TotalJitter     float64 `json:"total_jitter_ms"`
	TotalRTT        float64 `json:"total_rtt_ms"`
	MinPacketLoss   float64 `json:"min_packet_loss"`
	MaxPacketLoss   float64 `json:"max_packet_loss"`
	MinJitter       float64 `json:"min_jitter_ms"`
	MaxJitter       float64 `json:"max_jitter_ms"`
	MinRTT          float64 `json:"min_rtt_ms"`
	MaxRTT          float64 `json:"max_rtt_ms"`

	// Reconnection stats
	TotalSourceReconnects int64            `json:"total_source_reconnects"`
	TotalTargetReconnects int64            `json:"total_target_reconnects"`
	MinSourceReconnects   int64            `json:"min_source_reconnects"`
	MaxSourceReconnects   int64            `json:"max_source_reconnects"`
	AvgSourceReconnects   float64          `json:"avg_source_reconnects"`
	AvgTargetReconnects   float64          `json:"avg_target_reconnects"`
	SourceReconnectStats  map[string]int64 `json:"source_reconnect_stats"`
	TargetReconnectStats  map[string]int64 `json:"target_reconnect_stats"`

	// server min/max stats
	MinThroughputServer       string `json:"min_throughput_server"`
	MaxThroughputServer       string `json:"max_throughput_server"`
	MinPacketLossServer       string `json:"min_packet_loss_server"`
	MaxPacketLossServer       string `json:"max_packet_loss_server"`
	MinJitterServer           string `json:"min_jitter_server"`
	MaxJitterServer           string `json:"max_jitter_server"`
	MinRTTServer              string `json:"min_rtt_server"`
	MaxRTTServer              string `json:"max_rtt_server"`
	MinSourceReconnectsServer string `json:"min_source_reconnects_server"`
	MaxSourceReconnectsServer string `json:"max_source_reconnects_server"`
}

// ServerStatsSlice is a sortable slice of ServerStats with metadata
type ServerStatsSlice struct {
	stats     []*testing.ServerStats
	ascending bool
}

func (s ServerStatsSlice) Len() int {
	return len(s.stats)
}

func (s ServerStatsSlice) Swap(i, j int) {
	s.stats[i], s.stats[j] = s.stats[j], s.stats[i]
}

// ReconnectEntry represents a single entry in reconnection statistics
// Contains the IP/target and its reconnection count
type ReconnectEntry struct {
	IP    string // Server IP or Target IP
	Count int64  // Reconnection count
}

// SortType defines the metric to sort by
type SortType int

const (
	SortByThroughput SortType = iota
	SortByPacketLoss
	SortByJitter
	SortByRTT
	SortByReconnectCount
)

// SortServerStats sorts ServerStats by the specified metric
// Parameters:
//   - sortType: which metric to sort by (SortByThroughput, SortByPacketLoss, etc.)
//   - ascending: if true, sorts ascending; if false, sorts descending
//   - topN: optional number of top results to return (0 = all results)
//
// Returns a sorted slice of ServerStats
func (ms *MeshStats) SortServerStats(sortType SortType, ascending bool, topN ...int) []*testing.ServerStats {
	if len(ms.ServerStats) == 0 {
		return []*testing.ServerStats{}
	}

	// Build the slice
	var statsSlice []*testing.ServerStats
	for _, stat := range ms.ServerStats {
		// Skip stale stats
		if time.Since(stat.UpdatedAt) > 30*time.Second {
			continue
		}
		statsSlice = append(statsSlice, stat)
	}

	if len(statsSlice) == 0 {
		return []*testing.ServerStats{}
	}

	// Sort based on metric
	switch sortType {
	case SortByThroughput:
		sortByThroughput(statsSlice, ascending)
	case SortByPacketLoss:
		sortByPacketLoss(statsSlice, ascending)
	case SortByJitter:
		sortByJitter(statsSlice, ascending)
	case SortByRTT:
		sortByRTT(statsSlice, ascending)
	case SortByReconnectCount:
		sortByReconnectCount(statsSlice, ascending)
	}

	// Limit to topN if specified
	if len(topN) > 0 && topN[0] > 0 && topN[0] < len(statsSlice) {
		statsSlice = statsSlice[:topN[0]]
	}

	return statsSlice
}

// SortByThroughput returns ServerStats sorted by throughput
// ascending=true: lowest to highest, ascending=false: highest to lowest
// topN: optional, returns top N results (0 = all)
func (ms *MeshStats) SortByThroughput(ascending bool, topN ...int) []*testing.ServerStats {
	return ms.SortServerStats(SortByThroughput, ascending, topN...)
}

// SortByPacketLoss returns ServerStats sorted by packet loss
// ascending=true: lowest to highest, ascending=false: highest to lowest
func (ms *MeshStats) SortByPacketLoss(ascending bool, topN ...int) []*testing.ServerStats {
	return ms.SortServerStats(SortByPacketLoss, ascending, topN...)
}

// SortByJitter returns ServerStats sorted by jitter
// ascending=true: lowest to highest, ascending=false: highest to lowest
func (ms *MeshStats) SortByJitter(ascending bool, topN ...int) []*testing.ServerStats {
	return ms.SortServerStats(SortByJitter, ascending, topN...)
}

// SortByRTT returns ServerStats sorted by RTT (round-trip time)
// ascending=true: lowest to highest, ascending=false: highest to lowest
func (ms *MeshStats) SortByRTT(ascending bool, topN ...int) []*testing.ServerStats {
	return ms.SortServerStats(SortByRTT, ascending, topN...)
}

// SortByReconnectCount returns ServerStats sorted by reconnection count
// ascending=true: lowest to highest, ascending=false: highest to lowest
func (ms *MeshStats) SortByReconnectCount(ascending bool, topN ...int) []*testing.ServerStats {
	return ms.SortServerStats(SortByReconnectCount, ascending, topN...)
}

// Helper functions for sorting
func sortByThroughput(stats []*testing.ServerStats, ascending bool) {
	if ascending {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].Throughput < stats[i].Throughput {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	} else {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].Throughput > stats[i].Throughput {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	}
}

func sortByPacketLoss(stats []*testing.ServerStats, ascending bool) {
	if ascending {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].PacketLoss < stats[i].PacketLoss {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	} else {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].PacketLoss > stats[i].PacketLoss {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	}
}

func sortByJitter(stats []*testing.ServerStats, ascending bool) {
	if ascending {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].Jitter < stats[i].Jitter {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	} else {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].Jitter > stats[i].Jitter {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	}
}

func sortByRTT(stats []*testing.ServerStats, ascending bool) {
	if ascending {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].RTT < stats[i].RTT {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	} else {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].RTT > stats[i].RTT {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	}
}

func sortByReconnectCount(stats []*testing.ServerStats, ascending bool) {
	if ascending {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].ReconnectCount < stats[i].ReconnectCount {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	} else {
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				if stats[j].ReconnectCount > stats[i].ReconnectCount {
					stats[i], stats[j] = stats[j], stats[i]
				}
			}
		}
	}
}

// SortSourceReconnectStats sorts SourceReconnectStats by reconnection count
// Returns sorted []ReconnectEntry with IP and count for each source
// Parameters:
//   - ascending: if true, sorts ascending (lowest to highest); if false, descending
//   - topN: optional number of top results to return (0 or omitted = all results)
//
// Returns a sorted slice of ReconnectEntry
func (ms *MeshStats) SortSourceReconnectStats(ascending bool, topN ...int) []*ReconnectEntry {
	if len(ms.SourceReconnectStats) == 0 {
		return []*ReconnectEntry{}
	}

	// Build the slice
	var entries []*ReconnectEntry
	for ip, count := range ms.SourceReconnectStats {
		entries = append(entries, &ReconnectEntry{
			IP:    ip,
			Count: count,
		})
	}

	// Sort based on count
	if ascending {
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].Count < entries[i].Count {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	} else {
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].Count > entries[i].Count {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	}

	// Limit to topN if specified
	if len(topN) > 0 && topN[0] > 0 && topN[0] < len(entries) {
		entries = entries[:topN[0]]
	}

	return entries
}

// SortTargetReconnectStats sorts TargetReconnectStats by reconnection count
// Returns sorted []ReconnectEntry with target IP and count for each target
// Parameters:
//   - ascending: if true, sorts ascending (lowest to highest); if false, descending
//   - topN: optional number of top results to return (0 or omitted = all results)
//
// Returns a sorted slice of ReconnectEntry
func (ms *MeshStats) SortTargetReconnectStats(ascending bool, topN ...int) []*ReconnectEntry {
	if len(ms.TargetReconnectStats) == 0 {
		return []*ReconnectEntry{}
	}

	// Build the slice
	var entries []*ReconnectEntry
	for targetIP, count := range ms.TargetReconnectStats {
		entries = append(entries, &ReconnectEntry{
			IP:    targetIP,
			Count: count,
		})
	}

	// Sort based on count
	if ascending {
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].Count < entries[i].Count {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	} else {
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].Count > entries[i].Count {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	}

	// Limit to topN if specified
	if len(topN) > 0 && topN[0] > 0 && topN[0] < len(entries) {
		entries = entries[:topN[0]]
	}

	return entries
}

// target represents a single worker, having both IP (for network test) and SSHHost (for SSH connection, if different)
type target struct {
	IP      string
	Index   int
	SSHHost string
}

type targets []target

func (t targets) String() string {
	return ""
}

// Controller orchestrates network tests across multiple nodes
type Controller struct {
	config       *Config
	localIP      string
	stats        map[string]*testing.ServerStats
	statsLock    sync.RWMutex
	httpServer   *http.Server
	signalChan   chan os.Signal // Single channel for all signal handling
	deployHelper *SSHDeployer
	targets      targets
}

// NewController creates a new MeshController
func NewController(config *Config) *Controller {
	ipList := parseIPs(config.IPs)

	// Parse SSH hosts
	var sshHosts []string
	config.SSHHosts = strings.TrimSpace(config.SSHHosts)
	if config.SSHHosts != "" {
		sshHosts = strings.Split(config.SSHHosts, ",")
	}
	if len(sshHosts) > 0 && len(sshHosts) != len(ipList) {
		log.Fatalf("Number of SSH hosts (%d) must match number of IPs (%d)", len(sshHosts), len(ipList))
	}
	var tt targets
	for i, ip := range ipList {
		h := ip
		if len(sshHosts) != 0 {
			h = sshHosts[i]
		}
		// Determine SSH host
		tt = append(tt, target{
			IP:      ip,
			Index:   i,
			SSHHost: strings.TrimSpace(h),
		})
	}

	// Determine local IP
	localIP := config.LocalIP
	if localIP == "" {
		localIP = detectLocalIP(ipList)
		if localIP == "" {
			localIP = getLocalIPAddress()
			if localIP == "" {
				log.Fatal("Could not detect any local IP address")
			}
		}
		if config.Verbose {
			log.Printf("Detected local IP from mesh list: %s", localIP)
		}
	}

	ret := &Controller{
		config:       config,
		localIP:      localIP,
		stats:        make(map[string]*testing.ServerStats),
		signalChan:   make(chan os.Signal, 1),
		deployHelper: NewSSHDeployer(config.Verbose),
		targets:      tt,
	}

	// Register for OS signals
	signal.Notify(ret.signalChan, os.Interrupt, syscall.SIGTERM)

	return ret
}

// GetStats returns current aggregated statistics across all nodes
// This is the public API method that returns structured data instead of printing
func (mc *Controller) GetStats() *MeshStats {
	mc.statsLock.RLock()
	defer mc.statsLock.RUnlock()

	if len(mc.stats) == 0 {
		return &MeshStats{
			Timestamp:   time.Now(),
			ServerStats: make(map[string]*testing.ServerStats),
		}
	}

	meshStats := &MeshStats{
		Timestamp:    time.Now(),
		TotalServers: len(mc.targets),
		ServerStats:  make(map[string]*testing.ServerStats),
	}

	var activeCount int

	// Aggregate metrics for throughput
	var totalThroughput, minThroughput, maxThroughput float64

	// Aggregate metrics for jitter and packet loss
	var totalPacketLoss, totalJitter, totalRTT float64
	var minPacketLoss, maxPacketLoss float64 = 999999, 0
	var minJitter, maxJitter float64 = 999999, 0
	var minRTT, maxRTT float64 = 999999, 0

	// Reconnection server
	var totalSourceReconnects, totalTargetReconnects int64
	var minSourceReconnects, maxSourceReconnects int64 = 999999, 0

	// Metrics for min / max servers
	var minThroughputServer, maxThroughputServer string
	var minPacketLossServer, maxPacketLossServer string
	var minJitterServer, maxJitterServer string
	var minRTTServer, maxRTTServer string
	var minSourceReconnectsServer, maxSourceReconnectsServer string
	sourceReconnectStats := make(map[string]int64)
	targetReconnectStats := make(map[string]int64) // Total reconnections TO each target

	first := true
	rttMeasuredCount := 0        // Count servers with actual RTT measurements
	packetLossMeasuredCount := 0 // Count servers with actual packet loss measurements

	for ip, serverStats := range mc.stats {
		// Skip stale serverStats
		if time.Since(serverStats.UpdatedAt) > 30*time.Second {
			continue
		}

		activeCount++

		// Copy server serverStats to make atomic calculations
		stats := &testing.ServerStats{
			IP:               serverStats.IP,
			Throughput:       serverStats.Throughput,
			PacketLoss:       serverStats.PacketLoss,
			Jitter:           serverStats.Jitter,
			RTT:              serverStats.RTT,
			ReconnectCount:   serverStats.ReconnectCount,
			TargetReconnects: serverStats.TargetReconnects,
			UpdatedAt:        serverStats.UpdatedAt,
		}

		meshStats.ServerStats[ip] = stats
		totalThroughput += stats.Throughput

		if first || stats.Throughput < minThroughput {
			minThroughput = stats.Throughput
			minThroughputServer = stats.IP
			first = false
		}

		if stats.Throughput > maxThroughput {
			maxThroughput = stats.Throughput
			maxThroughputServer = stats.IP
		}

		// Only process packet loss if actually measured (not -1)
		if stats.PacketLoss >= 0 {
			totalPacketLoss += stats.PacketLoss
			packetLossMeasuredCount++

			// Track packet loss min/max
			if stats.PacketLoss < minPacketLoss {
				minPacketLoss = stats.PacketLoss
				minPacketLossServer = ip
			}
			if stats.PacketLoss > maxPacketLoss {
				maxPacketLoss = stats.PacketLoss
				maxPacketLossServer = ip
			}
		}

		// Only process RTT/jitter if actually measured (not -1)
		if stats.RTT >= 0 && stats.Jitter >= 0 {
			totalJitter += stats.Jitter
			totalRTT += stats.RTT
			rttMeasuredCount++

			// Track jitter min/max
			if stats.Jitter < minJitter {
				minJitter = stats.Jitter
				minJitterServer = ip
			}
			if stats.Jitter > maxJitter {
				maxJitter = stats.Jitter
				maxJitterServer = ip
			}

			// Track RTT min/max
			if stats.RTT < minRTT {
				minRTT = stats.RTT
				minRTTServer = ip
			}
			if stats.RTT > maxRTT {
				maxRTT = stats.RTT
				maxRTTServer = ip
			}
		}

		// Collect reconnection stats
		sourceReconnects := stats.ReconnectCount
		sourceReconnectStats[ip] = sourceReconnects
		totalSourceReconnects += sourceReconnects

		// Track source reconnection min/max
		if sourceReconnects < minSourceReconnects {
			minSourceReconnects = sourceReconnects
			minSourceReconnectsServer = ip
		}
		if sourceReconnects > maxSourceReconnects {
			maxSourceReconnects = sourceReconnects
			maxSourceReconnectsServer = ip
		}

		// Aggregate target reconnection stats
		for targetIP, reconnectCount := range stats.TargetReconnects {
			targetReconnectStats[targetIP] += reconnectCount
			totalTargetReconnects += reconnectCount
		}
	}

	if activeCount == 0 {
		return &MeshStats{
			Timestamp:   time.Now(),
			ServerStats: make(map[string]*testing.ServerStats),
		}
	}

	meshStats.ActiveServers = activeCount
	meshStats.MinThroughput = minThroughput
	meshStats.MinThroughputServer = minThroughputServer

	meshStats.MaxThroughput = maxThroughput
	meshStats.MaxThroughputServer = maxThroughputServer

	meshStats.MinPacketLoss = minPacketLoss
	meshStats.MinPacketLossServer = minPacketLossServer

	meshStats.MaxPacketLoss = maxPacketLoss
	meshStats.MaxPacketLossServer = maxPacketLossServer

	meshStats.MinJitter = minJitter
	meshStats.MinJitterServer = minJitterServer

	meshStats.MaxJitter = maxJitter
	meshStats.MaxJitterServer = maxJitterServer

	meshStats.MinRTT = minRTT
	meshStats.MinRTTServer = minRTTServer

	meshStats.MaxRTT = maxRTT
	meshStats.MaxRTTServer = maxRTTServer

	meshStats.MinSourceReconnects = minSourceReconnects
	meshStats.MinSourceReconnectsServer = minSourceReconnectsServer

	meshStats.MaxSourceReconnects = maxSourceReconnects
	meshStats.MaxSourceReconnectsServer = maxSourceReconnectsServer

	meshStats.TotalThroughput = totalThroughput
	meshStats.TotalSourceReconnects = totalSourceReconnects
	meshStats.TotalTargetReconnects = totalTargetReconnects
	meshStats.TotalRTT = totalRTT
	meshStats.TotalJitter = totalJitter
	meshStats.TotalPacketLoss = totalPacketLoss

	// Calculate averages
	meshStats.AvgThroughput = totalThroughput / float64(activeCount)
	meshStats.AvgSourceReconnects = float64(totalSourceReconnects) / float64(activeCount)
	meshStats.AvgTargetReconnects = float64(totalTargetReconnects) / float64(activeCount)

	if packetLossMeasuredCount > 0 {
		meshStats.AvgPacketLoss = totalPacketLoss / float64(packetLossMeasuredCount)
	} else {
		meshStats.AvgPacketLoss = -1
	}

	if rttMeasuredCount > 0 {
		meshStats.AvgRTT = totalRTT / float64(rttMeasuredCount)
		meshStats.AvgJitter = totalJitter / float64(rttMeasuredCount)
	} else {
		meshStats.AvgRTT = -1
		meshStats.AvgJitter = -1
	}

	meshStats.UpdatedAt = time.Now()
	return meshStats
}

// Start begins the test by starting services on all nodes
// Assumes deployment has already been done (either via Deploy() or servers already running)
func (mc *Controller) Start() error {
	if mc.config.Verbose {
		log.Println("\n========== PHASE 2: STARTING SERVICES ==========")
	} else {
		fmt.Printf("🚀 Starting services... ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	numWorkers := min(len(mc.targets), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.targets, numWorkers,
		func(ctx context.Context, target target, i int) error {
			return mc.startService(target)
		})

	mc.handleServiceStartResults(results)

	if !results.AllSucceeded() {
		if mc.config.Verbose {
			log.Printf("⚠️  Some services failed to start")
		}
		if err := results.AsError(); err != nil {
			return fmt.Errorf("service start errors: %v", err)
		}
	}

	if !mc.config.Verbose {
		fmt.Printf("Done\n\n")
	}

	// Give services time to initialize
	if mc.config.Verbose {
		log.Println("Waiting for services to initialize...")
	}
	time.Sleep(3 * time.Second)

	return nil
}

// Wait blocks until the test duration completes or a signal is received
// Continuously monitors and displays statistics
func (mc *Controller) Wait() {
	if mc.config.Verbose {
		log.Println("Waiting for test completion...")
	} else {
		fmt.Printf("📊 Monitoring mesh performance:\n")
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	durationTimer := time.NewTimer(mc.config.Duration)
	defer durationTimer.Stop()

	// Wait for initial stats
	time.Sleep(10 * time.Second)

	for {
		select {
		case <-ticker.C:
			mc.displayStats()
		case sig := <-mc.signalChan:
			log.Printf("\n⚠️  Received signal: %v", sig)
			mc.displayStats()
			return
		case <-durationTimer.C:
			log.Println("\n✓ Test duration completed")
			return
		}
	}
}

// Run performs the complete workflow: Deploy -> Start -> Wait
// This is the main entry point for simple usage
func (mc *Controller) Run() {
	if !mc.config.Verbose {
		log.Printf("🚀 GoSmesh Mesh Controller\n")
		log.Printf("Controller: %s | Nodes: %d | Duration: %v\n\n", mc.localIP, len(mc.targets), mc.config.Duration)
	} else {
		log.Printf("Starting mesh controller on %s", mc.localIP)
		log.Printf("Deploying to nodes: %v", mc.targets)
	}

	mc.startAPIServer()
	// Start API server
	defer mc.cleanup()
	// Deploy (if SSH is enabled)
	if mc.config.DeploySystemDService {
		deployDone := make(chan error)
		go func() {
			deployDone <- mc.Deploy()
		}()

		select {
		case err := <-deployDone:
			if err != nil {
				log.Printf("Deployment failed: %v", err)
				return
			}
		case sig := <-mc.signalChan:
			log.Printf("\n⚠️  Received signal: %v", sig)
			return
		}
	}

	// Start services
	if err := mc.Start(); err != nil {
		log.Printf("Failed to start services: %v", err)
		mc.cleanup()
		return
	}

	// Wait for completion
	mc.Wait()
	mc.displayStats()
}

// updateStats updates the statistics from a reporting node
func (mc *Controller) updateStats(ip string, throughput, packetLoss, jitter, rtt float64, reconnects int64, targetReconnects map[string]int64) {
	mc.statsLock.Lock()
	defer mc.statsLock.Unlock()

	mc.stats[ip] = &testing.ServerStats{
		IP:               ip,
		Throughput:       throughput,
		PacketLoss:       packetLoss,
		Jitter:           jitter,
		RTT:              rtt,
		ReconnectCount:   reconnects,
		TargetReconnects: targetReconnects,
		UpdatedAt:        time.Now(),
	}
}

// statsHTTPHandler handles incoming stats reports from nodes
func (mc *Controller) statsHTTPHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var stats map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&stats); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Extract IP from stats
	ip, ok := stats["ip"].(string)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Extract metrics
	throughput, _ := stats["throughput_gbps"].(float64)
	packetLoss, _ := stats["packet_loss_percent"].(float64)
	jitter, _ := stats["jitter_ms"].(float64)
	rtt, _ := stats["rtt_ms"].(float64)
	reconnectCount, _ := stats["reconnect_count"].(float64)

	targetReconnects := make(map[string]int64)
	if tr, ok := stats["target_reconnects"].(map[string]interface{}); ok {
		for k, v := range tr {
			if val, ok := v.(float64); ok {
				targetReconnects[k] = int64(val)
			}
		}
	}

	// Update stats
	mc.updateStats(ip, throughput, packetLoss, jitter, rtt, int64(reconnectCount), targetReconnects)

	w.WriteHeader(http.StatusOK)
}

// CleanupNode performs thorough cleanup on a single node
// Stops services, kills processes, removes binaries and directories
// This uses the controller's configuration for service name and remote directory
// killEvenIfNoSystemD means kill the process even if it was deployed auxiliary (not just from systemd), to ensure cleanup is complete
func (mc *Controller) cleanupNode(ctx context.Context, target target, killEvenIfNoSystemD bool) error {
	remoteBinary := fmt.Sprintf("%s/gosmesh", GoSmeshRemotePath)

	// Determine SSH host
	sshHost := target.SSHHost
	ip := target.IP

	// only if deployed service and no auxiliary installation was performed
	if mc.config.DeploySystemDService {
		if mc.config.Verbose {
			log.Printf("[%s] Stopping existing service...", ip)
		}

		stopCmd := fmt.Sprintf("systemctl stop %s || true", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, stopCmd, "stop service"); err != nil {
			return fmt.Errorf("[%s] stop service failed: %v", ip, err)
		}
		// Disable service if it exists
		if mc.config.Verbose {
			log.Printf("[%s] Disabling existing service...", ip)
		}
		disableCmd := fmt.Sprintf("systemctl disable %s || true", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, disableCmd, "disable service"); err != nil {
			return fmt.Errorf("[%s] disable service failed: %v", ip, err)
		}
		// Reset failed state (redirect stderr to avoid hanging)
		if mc.config.Verbose {
			log.Printf("[%s] Resetting failed state...", ip)
		}
		resetCmd := fmt.Sprintf("systemctl reset-failed %s 2>/dev/null || true", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, resetCmd, "reset failed state"); err != nil {
			return fmt.Errorf("[%s] reset failed state failed: %v", ip, err)
		}
	}

	// kill processes only if we spawned them OR if explicitly required
	if mc.config.DeploySystemDService || killEvenIfNoSystemD {
		// Kill processes running specifically from the remote directory path using their executable location
		if mc.config.Verbose {
			log.Printf("[%s] Killing processes from %s...", ip, GoSmeshRemotePath)
		}
		killCmd := fmt.Sprintf(`
killed=false
for pid in $(pgrep gosmesh 2>/dev/null || true); do
    if [ -n "$pid" ]; then
        exe_path=$(readlink /proc/$pid/exe 2>/dev/null || echo "")
        if [ "$exe_path" = "%s" ]; then
            echo "Killing process $pid from %s"
            kill $pid && echo "Killed $pid" || echo "Failed to kill $pid"
            killed=true
        fi
    fi
done
if [ "$killed" = "false" ]; then
    echo "No processes found from %s"
fi
`, remoteBinary, remoteBinary, GoSmeshRemotePath)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, killCmd, "kill processes"); err != nil {
			return fmt.Errorf("[%s] kill processes failed: %v", ip, err)
		}

		// Force kill any remaining processes
		if mc.config.Verbose {
			log.Printf("[%s] Force killing any remaining processes from %s...", ip, GoSmeshRemotePath)
		}
		forceKillCmd := fmt.Sprintf(`
killed=false
for pid in $(pgrep gosmesh 2>/dev/null || true); do
    if [ -n "$pid" ]; then
        exe_path=$(readlink /proc/$pid/exe 2>/dev/null || echo "")
        if [ "$exe_path" = "%s" ]; then
            echo "Force killing process $pid from %s"
            kill -9 $pid && echo "Force killed $pid" || echo "Failed to force kill $pid"
            killed=true
        fi
    fi
done
if [ "$killed" = "false" ]; then
    echo "No processes from %s to force kill"
fi
`, remoteBinary, remoteBinary, GoSmeshRemotePath)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, forceKillCmd, "force kill processes"); err != nil {
			return fmt.Errorf("[%s] force kill processes failed: %v", ip, err)
		}
	}

	// Remove service file
	if mc.config.DeploySystemDService {
		if mc.config.Verbose {
			log.Printf("[%s] Removing service file...", ip)
		}
		removeServiceCmd := fmt.Sprintf("rm -f /etc/systemd/system/%s.service", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, removeServiceCmd, "remove service file"); err != nil {
			return fmt.Errorf("[%s] remove service file failed: %v", ip, err)
		}

		// Remove binary and directory
		if mc.config.Verbose {
			log.Printf("[%s] Removing binary and directory...", ip)
		}
		removeCmd := fmt.Sprintf("rm -rf %s", GoSmeshRemotePath)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, removeCmd, "remove directory"); err != nil {
			return fmt.Errorf("[%s] remove directory failed: %v", ip, err)
		}

		// Reload systemd
		if mc.config.Verbose {
			log.Printf("[%s] Reloading systemd...", ip)
		}
		reloadCmd := "systemctl daemon-reload"
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, reloadCmd, "reload systemd"); err != nil {
			return fmt.Errorf("[%s] reload systemd failed: %v", ip, err)
		}

		if mc.config.Verbose {
			log.Printf("[%s] Cleanup completed successfully", ip)
		}
	}

	return nil
}

// Deploy performs SSH deployment to all nodes (if SSH is enabled)
// This is optional - you can also assume servers are already running
func (mc *Controller) Deploy() error {
	if !mc.config.DeploySystemDService {
		if mc.config.Verbose {
			log.Println("SSH deployment disabled, assuming servers are already running")
		}
		return nil
	}

	if mc.config.Verbose {
		log.Println("\n========== PHASE 1: DEPLOYMENT ==========")
	} else {
		fmt.Printf("📦 Deploying to %d nodes... ", len(mc.targets))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	numWorkers := min(len(mc.targets), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.targets, numWorkers,
		func(ctx context.Context, target target, i int) error {
			return mc.deployToNode(target, false)
		})

	mc.handleDeploymentResults(results)

	if !results.AllSucceeded() {
		if mc.config.Verbose {
			log.Printf("⚠️  Some deployments failed, but continuing with successful nodes")
		}
		if err := results.AsError(); err != nil {
			return fmt.Errorf("deployment errors: %v", err)
		}
	}

	if !mc.config.Verbose {
		fmt.Printf("Done\n")
	}

	return nil
}

// Uninstall performs cleanup and uninstallation on all nodes
// Stops services, removes binaries, and cleans up directories in parallel
func (mc *Controller) Uninstall() error {
	if mc.config.Verbose {
		log.Println("\n========== PHASE: UNINSTALL ==========")
	} else {
		fmt.Printf("🧹 Uninstalling from %d nodes... ", len(mc.targets))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	numWorkers := min(len(mc.targets), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.targets, numWorkers,
		func(ctx context.Context, target target, i int) error {
			return mc.cleanupNode(ctx, target, false)
		})

	successCount := 0
	failCount := 0

	for _, result := range results.Items {
		if result.Err != nil {
			if mc.config.Verbose {
				log.Printf("❌ Failed to uninstall from %s: %v", result.Object.IP, result.Err)
			}
			failCount++
		} else {
			if mc.config.Verbose {
				log.Printf("✅ Successfully uninstalled from %s", result.Object.IP)
			}
			successCount++
		}
	}

	if mc.config.Verbose {
		log.Println("\n========== UNINSTALL COMPLETE ==========")
		log.Printf("Successful uninstalls: %d/%d", successCount, len(results.Items))
		if failCount > 0 {
			log.Printf("Failed uninstalls: %d", failCount)
		}
	} else {
		if failCount == 0 {
			fmt.Printf("Done (✅ %d/%d)\n", successCount, len(results.Items))
		} else {
			fmt.Printf("Done (✅ %d, ❌ %d)\n", successCount, failCount)
		}
	}

	if !results.AllSucceeded() {
		if err := results.AsError(); err != nil {
			return fmt.Errorf("uninstall errors: %v", err)
		}
	}

	return nil
}

// startService starts the gosmesh service on a node
// Routes to either startServiceWithDeployment or startServiceNoDeployment based on config
func (mc *Controller) startService(target target) error {
	if mc.config.DeploySystemDService {
		return mc.startServiceWithDeployment(target)
	}
	return mc.startServiceNoDeployment(target)
}

// startServiceWithDeployment starts and verifies a deployed service
// Checks that the service is active, process is running, and port is listening
func (mc *Controller) startServiceWithDeployment(target target) error {
	// Determine SSH host
	sshHost := target.SSHHost
	ip := target.IP
	if mc.config.Verbose {
		log.Printf("[%s] 🚀 Starting service...", ip)
	}

	if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("systemctl start %s", GoSmeshServiceName), "start service"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Failed to start service: %v", ip, err)
		}
		return fmt.Errorf("[%s] failed to start service: %v", ip, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] ⏳ Service start command completed, verifying...", ip)
	}

	// Verify service is running
	if mc.config.Verbose {
		log.Printf("[%s] 🔍 Checking service health...", ip)
	}

	var ssCmd string
	if mc.config.Protocol == "udp" {
		ssCmd = fmt.Sprintf("ss -uln | grep ':%d ' >/dev/null 2>&1", mc.config.Port)
	} else {
		ssCmd = fmt.Sprintf("ss -tln | grep ':%d ' >/dev/null 2>&1", mc.config.Port)
	}

	verifyCmd := fmt.Sprintf(`
		echo "[VERIFY] Quick service check..."
		if systemctl is-active %s >/dev/null 2>&1 && pgrep -f 'gosmesh run' >/dev/null 2>&1 && %s; then
			echo "[SUCCESS] Service active, process running, port listening"
			exit 0
		fi
		
		echo "[ERROR] Service verification failed"
		echo "Service active: $(systemctl is-active %s 2>/dev/null || echo 'inactive')"
		echo "Process running: $(pgrep -f 'gosmesh run' >/dev/null 2>&1 && echo 'yes' || echo 'no')"
		echo "Port listening: $(%s && echo 'yes' || echo 'no')"
		
		echo "Recent logs:"
		journalctl -u %s --no-pager --since "30 seconds ago" --lines=5 || true
		exit 1
	`, GoSmeshServiceName, ssCmd, GoSmeshServiceName, ssCmd, GoSmeshServiceName)

	if err := mc.deployHelper.Exec(sshHost, verifyCmd, "verify service health"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Service verification failed", ip)
		}
		return fmt.Errorf("[%s] service verification failed: %v", ip, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] ✅ SUCCESS: Service started and verified", ip)
	}

	return nil
}

// startServiceNoDeployment checks if a service is running by verifying port connectivity
// Assumes service is already deployed and just checks if the port is open
func (mc *Controller) startServiceNoDeployment(target target) error {
	// Determine SSH host
	sshHost := target.SSHHost
	ip := target.IP

	if mc.config.Verbose {
		log.Printf("[%s] 🔍 Checking if service is running on port %d...", ip, mc.config.Port)
	}

	// Build the port check command based on protocol
	var portCheckCmd string
	if mc.config.Protocol == "udp" {
		portCheckCmd = fmt.Sprintf("ss -uln | grep -q ':%d ' && echo 'Port listening' || (echo 'Port not listening'; exit 1)", mc.config.Port)
	} else {
		portCheckCmd = fmt.Sprintf("ss -tln | grep -q ':%d ' && echo 'Port listening' || (echo 'Port not listening'; exit 1)", mc.config.Port)
	}

	if err := mc.deployHelper.Exec(sshHost, portCheckCmd, "check port connectivity"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Port %d is not listening", ip, mc.config.Port)
		}
		return fmt.Errorf("[%s] port %d not listening: %v", ip, mc.config.Port, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] ✅ SUCCESS: Service is running (port %d is listening)", ip, mc.config.Port)
	}

	return nil
}

func (mc *Controller) generateSystemdUnit(binaryPath string) string {
	apiEndpoint := fmt.Sprintf("http://%s:%d/stats", mc.localIP, mc.config.APIPort)
	if mc.config.Verbose {
		log.Printf("Generated API endpoint for reporting: %s", apiEndpoint)
	}

	// Build command arguments
	cmd := fmt.Sprintf("%s run --ips %s --duration %s --port %d --protocol %s --report-to %s",
		binaryPath, mc.config.IPs, mc.config.Duration, mc.config.Port, mc.config.Protocol, apiEndpoint)

	// Add optional flags if they differ from defaults
	if mc.config.TotalConnections != 64 {
		cmd += fmt.Sprintf(" --total-connections %d", mc.config.TotalConnections)
	}
	if mc.config.Concurrency != 0 {
		cmd += fmt.Sprintf(" --concurrency %d", mc.config.Concurrency)
	}
	if mc.config.ReportInterval != 5*time.Second {
		cmd += fmt.Sprintf(" --report-interval %s", mc.config.ReportInterval)
	}
	if mc.config.PacketSize != 0 {
		cmd += fmt.Sprintf(" --packet-size %d", mc.config.PacketSize)
	}
	if mc.config.PPS != 0 {
		cmd += fmt.Sprintf(" --pps %d", mc.config.PPS)
	}
	if !mc.config.ThroughputMode {
		cmd += " --throughput-mode=false"
	}
	if mc.config.BufferSize != 0 {
		cmd += fmt.Sprintf(" --buffer-size %d", mc.config.BufferSize)
	}
	if mc.config.TCPNoDelay {
		cmd += " --tcp-nodelay"
	}
	if !mc.config.UseOptimized {
		cmd += " --optimized=false"
	}
	if !mc.config.EnableIOUring {
		cmd += " --io-uring=false"
	}
	if !mc.config.EnableHugePages {
		cmd += " --huge-pages=false"
	}
	if !mc.config.EnableOffload {
		cmd += " --hw-offload=false"
	}
	if mc.config.SendBatchSize != 64 {
		cmd += fmt.Sprintf(" --send-batch-size %d", mc.config.SendBatchSize)
	}
	if mc.config.RecvBatchSize != 64 {
		cmd += fmt.Sprintf(" --recv-batch-size %d", mc.config.RecvBatchSize)
	}
	if mc.config.NumQueues != 0 {
		cmd += fmt.Sprintf(" --num-queues %d", mc.config.NumQueues)
	}
	if mc.config.BusyPollUsecs != 50 {
		cmd += fmt.Sprintf(" --busy-poll-usecs %d", mc.config.BusyPollUsecs)
	}
	if mc.config.TCPCork {
		cmd += " --tcp-cork"
	}
	if mc.config.TCPQuickAck {
		cmd += " --tcp-quickack"
	}
	if mc.config.MemArenaSize != 268435456 {
		cmd += fmt.Sprintf(" --memory-arena-size %d", mc.config.MemArenaSize)
	}
	if mc.config.RingSize != 4096 {
		cmd += fmt.Sprintf(" --ring-size %d", mc.config.RingSize)
	}
	if mc.config.NumWorkers != 0 {
		cmd += fmt.Sprintf(" --num-workers %d", mc.config.NumWorkers)
	}
	if mc.config.CPUList != "" {
		cmd += fmt.Sprintf(" --cpu-list %s", mc.config.CPUList)
	}
	if mc.config.WorkerAPIPort != 0 {
		cmd += fmt.Sprintf(" --api-server-port %d", mc.config.WorkerAPIPort)
	}

	return fmt.Sprintf(`[Unit]
Description=GoSmesh Mesh Testing Service
After=network.target

[Service]
Type=simple
ExecStart=%s
StandardOutput=journal
StandardError=journal
Restart=no
KillMode=mixed
TimeoutStartSec=10
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`, cmd)
}

// deployToNode deploys gosmesh binary and service to a node
func (mc *Controller) deployToNode(target target, start bool) error {
	// This is a stub - would contain actual deployment logic from cmd_mesh.go
	// For now, just log that we would deploy
	if mc.config.Verbose {
		log.Printf("[%s] Deploying gosmesh...", target.IP)
	}
	return mc.deployToNodeWithStart(target, start)
}

func (mc *Controller) deployToNodeWithStart(target target, startService bool) error {
	sshHost := target.SSHHost
	ip := target.IP
	if mc.config.Verbose {
		log.Printf("[%s] (%s) Starting deployment", ip, sshHost)
	}

	remoteBinary := filepath.Join(GoSmeshRemotePath, "gosmesh")

	// Get the path of the currently executing binary
	currentBinary, err := os.Executable()
	if os.Getenv("GOSMESH_EXECUTABLE_PATH") != "" {
		currentBinary = os.Getenv("GOSMESH_EXECUTABLE_PATH")
	}

	if err != nil {
		return fmt.Errorf("[%s] failed to get current binary path: %v", ip, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] Deployment via SSH to %s", ip, sshHost)
	}

	// Step 0: Test SSH connectivity first
	if mc.config.Verbose {
		log.Printf("[%s] Testing SSH connectivity...", sshHost)
	}
	if err := mc.deployHelper.Exec(sshHost, "echo 'SSH connection OK'", "connectivity test"); err != nil {
		return fmt.Errorf("[%s] SSH connectivity failed: %v", sshHost, err)
	}
	if mc.config.Verbose {
		log.Printf("[%s] SSH connectivity confirmed", ip)
	}

	// Step 1: Thorough cleanup of old processes and services
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := mc.cleanupNode(cleanupCtx, target, false); err != nil {
		return err
	}

	// Step 2: Create remote directory
	if mc.config.Verbose {
		log.Printf("[%s] Creating remote directory %s...", ip, GoSmeshRemotePath)
	}
	if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("mkdir -p %s", GoSmeshRemotePath), "create directory"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 3: Copy binary (use the currently executing binary)
	if mc.config.Verbose {
		log.Printf("[%s] Copying binary to remote...", ip)
	}
	if err := mc.deployHelper.SCP(currentBinary, fmt.Sprintf("%s:%s", sshHost, remoteBinary), "copy binary"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 4: Make executable
	if mc.config.Verbose {
		log.Printf("[%s] Making binary executable...", ip)
	}
	if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("chmod +x %s", remoteBinary), "make executable"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 5: Create systemd service file
	if mc.config.Verbose {
		log.Printf("[%s] Creating systemd service...", ip)
	}
	serviceContent := mc.generateSystemdUnit(remoteBinary)
	serviceTempPath := fmt.Sprintf("/tmp/gosmesh-mesh-%s.service", strings.ReplaceAll(ip, ".", "_"))
	if err := os.WriteFile(serviceTempPath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("[%s] failed to write service file: %v", ip, err)
	}
	defer os.Remove(serviceTempPath)

	// Copy service file to remote
	if err := mc.deployHelper.SCP(serviceTempPath, fmt.Sprintf("%s:/etc/systemd/system/%s.service", sshHost, GoSmeshServiceName), "copy service file"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 6: Reload systemd
	if mc.config.Verbose {
		log.Printf("[%s] Reloading systemd...", ip)
	}
	if err := mc.deployHelper.Exec(sshHost, "systemctl daemon-reload", "reload systemd"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 7: Start service (optional)
	if startService {
		if mc.config.Verbose {
			log.Printf("[%s] Starting gosmesh service...", ip)
		}
		if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("systemctl start %s", GoSmeshServiceName), "start service"); err != nil {
			return fmt.Errorf("[%s] %v", ip, err)
		}
		if mc.config.Verbose {
			log.Printf("[%s] ✓ Deployment and service start completed successfully", ip)
		}
	} else {
		if mc.config.Verbose {
			log.Printf("[%s] ✓ Deployment completed successfully (service not started)", ip)
		}
	}
	return nil
}

// startAPIServer starts the HTTP API server for stats reporting
func (mc *Controller) startAPIServer() {
	if mc.config.Verbose {
		log.Printf("Starting API server on port %d", mc.config.APIPort)
	}

	mux := http.NewServeMux()

	// Stats endpoint
	mux.HandleFunc("/stats", mc.handleStats)

	// Get stats (for external monitoring)
	mux.HandleFunc("/getStats", mc.handleGetStats)
	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok"}`)
	})

	mc.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", mc.config.APIPort),
		Handler: mux,
	}

	go func() {
		if err := mc.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
			return
		}
		if mc.config.Verbose {
			log.Printf("API server listening on :%d", mc.config.APIPort)
		}
	}()
}

func (mc *Controller) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var stats testing.ServerStats
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	if err := json.Unmarshal(body, &stats); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	stats.UpdatedAt = time.Now()

	// Initialize TargetReconnects map if nil
	if stats.TargetReconnects == nil {
		stats.TargetReconnects = make(map[string]int64)
	}

	mc.statsLock.Lock()
	mc.stats[stats.IP] = &stats
	mc.statsLock.Unlock()

	w.WriteHeader(http.StatusOK)
}

// displayStats displays current statistics (used by Wait loop)
func (mc *Controller) displayStats() {
	stats := mc.GetStats()

	if stats.ActiveServers == 0 {
		fmt.Println("No active servers reporting yet...")
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("\n=== Mesh Statistics [%s] ===\n", timestamp)
	fmt.Printf("Active Servers: %d/%d\n", stats.ActiveServers, stats.TotalServers)

	fmt.Printf("\nThroughput:\n")
	fmt.Printf("  Total: %.2f Gbps | Avg: %.2f Gbps\n", stats.TotalThroughput, stats.AvgThroughput)
	fmt.Printf("  Best: %s (%.2f Gbps) | Worst: %s (%.2f Gbps)\n",
		stats.MaxThroughputServer, stats.MaxThroughput,
		stats.MinThroughputServer, stats.MinThroughput,
	)

	sortedThroughput := stats.SortByThroughput(true, 10)
	worstCount := len(sortedThroughput)

	if worstCount > 1 {
		fmt.Printf("  Worst %d: ", worstCount)
		for i := 0; i < worstCount; i++ {
			if i > 0 {
				fmt.Printf(", ")
			}
			fmt.Printf("%s (%.2f)", sortedThroughput[i].IP, sortedThroughput[i].Throughput)
		}
		fmt.Printf(" Gbps\n")
	}

	if stats.TotalPacketLoss > 0 {
		fmt.Printf("\nPacket Loss:\n")
		fmt.Printf("  Avg: %.2f%% | Best: %s (%.2f%%) | Worst: %s (%.2f%%)\n",
			stats.AvgPacketLoss,
			stats.MinPacketLossServer, stats.MinPacketLoss,
			stats.MaxPacketLossServer, stats.MaxPacketLoss,
		)
	}

	if stats.TotalRTT > 0 {
		// Jitter stats
		fmt.Printf("\nJitter:\n")
		fmt.Printf("  Avg: %.2f ms | Best: %s (%.2f ms) | Worst: %s (%.2f ms)\n",
			stats.AvgJitter,
			stats.MinJitterServer, stats.MinJitter,
			stats.MaxJitterServer, stats.MaxJitter,
		)

		// RTT stats
		fmt.Printf("\nRTT:\n")
		fmt.Printf("  Avg: %.2f ms | Best: %s (%.2f ms) | Worst: %s (%.2f ms)\n",
			stats.AvgRTT,
			stats.MinRTTServer, stats.MinRTT,
			stats.MaxRTTServer, stats.MaxRTT,
		)
	} else {
		// Throughput mode - RTT/jitter not measured
		fmt.Printf("\nRTT/Jitter: Not measured (throughput mode)\n")
	}

	if stats.AvgRTT >= 0 && stats.AvgJitter >= 0 {
		fmt.Printf("\nLatency:\n")
		fmt.Printf("  Avg RTT: %.2f ms | Avg Jitter: %.2f ms\n", stats.AvgRTT, stats.AvgJitter)
	}

	// Reconnection stats - always show, even if zero
	fmt.Printf("\nReconnections (Sources):\n")
	if stats.TotalSourceReconnects > 0 {
		fmt.Printf("  Total: %d | Avg: %.1f per server\n",
			stats.TotalSourceReconnects, stats.AvgSourceReconnects)
		fmt.Printf("  Best: %s (%d) | Worst: %s (%d)\n",
			stats.MinSourceReconnectsServer, stats.MinSourceReconnects,
			stats.MaxSourceReconnectsServer, stats.MaxSourceReconnects)

		sortedSources := stats.SortSourceReconnectStats(true, 3)
		topCount := len(sortedSources)
		fmt.Printf("  Top 3 Re-establishers: ")
		for i := 0; i < topCount; i++ {
			if i > 0 {
				fmt.Printf(", ")
			}
			fmt.Printf("%s (%d)", sortedSources[i].IP, sortedSources[i].Count)
		}
		fmt.Printf("\n")
	}
	if stats.TotalTargetReconnects > 0 {
		// Top 3 targets (servers being reconnected to most)
		sortedTargets := stats.SortTargetReconnectStats(true, 3)
		topTargetCount := len(sortedTargets)
		if len(sortedTargets) > 0 {
			fmt.Printf("\nReconnections (targets):\n")
			fmt.Printf("  Total: %d | Avg: %.1f per target\n", stats.TotalTargetReconnects, stats.AvgTargetReconnects)

			fmt.Printf("  Top 3 Problematic targets: ")
			if len(sortedTargets) < topTargetCount {
				topTargetCount = len(sortedTargets)
			}
			for i := 0; i < topTargetCount; i++ {
				if i > 0 {
					fmt.Printf(", ")
				}
				fmt.Printf("%s (%d)", sortedTargets[i].IP, sortedTargets[i].Count)
			}
			fmt.Printf("\n")
		}
	} else {
		// Show zero stats when no reconnections
		fmt.Printf("  Total: 0 | Avg: 0.0 per server\n")
		fmt.Printf("  All connections stable - no reconnections detected\n")
	}

	fmt.Printf("=======================\n")
}

// handleGetStats creates a JSON representation of all stats from GetStats() and reports it.
func (mc *Controller) handleGetStats(w http.ResponseWriter, _ *http.Request) {
	// Get current stats
	stats := mc.GetStats()

	// Marshal to JSON
	jsonData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		if w != nil {
			http.Error(w, fmt.Sprintf("Failed to marshal stats: %v", err), http.StatusInternalServerError)
		}
		if mc.config.Verbose {
			log.Printf("Failed to marshal stats: %v", err)
		}
		return
	}

	// If this is being used as HTTP handler
	if w != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jsonData)

		if mc.config.Verbose {
			log.Printf("[API] Stats reported: %d bytes", len(jsonData))
		}
	}
}

// GetStatsJSON returns all stats as formatted JSON string
// This is a convenience method that can be used without HTTP context
func (mc *Controller) GetStatsJSON() (string, error) {
	stats := mc.GetStats()

	jsonData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal stats: %w", err)
	}

	return string(jsonData), nil
}

// ReportAllStats writes all stats to stdout as formatted JSON
// Useful for logging or displaying all stats
func (mc *Controller) ReportAllStats() error {
	stats := mc.GetStats()

	jsonData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal stats: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}

// cleanup performs cleanup operations
func (mc *Controller) cleanup() {
	if mc.httpServer != nil {
		_ = mc.httpServer.Close()
	}

	if mc.config.Verbose {
		log.Println("Cleanup complete")
	}
}
