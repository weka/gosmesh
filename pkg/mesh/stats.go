package mesh

import (
	"encoding/json"
	"fmt"
	"github.com/weka/gosmesh/pkg/testing"
	"io"
	"log"
	"net/http"
	"time"
)

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
		TotalServers: mc.config.Targets.Len(),
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

func (mc *Controller) handleWorkerStats(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")

	// Marshal to JSON with indentation for readability
	jsonData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		if mc.config.Verbose {
			log.Printf("Failed to marshal stats: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		errorJSON := fmt.Sprintf(`{"error": "%s"}`, err.Error())
		_, _ = w.Write([]byte(errorJSON))
		return
	}

	// Write response
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jsonData)

	if mc.config.Verbose {
		log.Printf("[API] Stats reported: %d bytes", len(jsonData))
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
