# GoSmesh Network Tester - API Documentation

## Overview

The NetworkTester provides both **structured data retrieval** and **HTTP API server** capabilities for getting test results programmatically. This allows you to:

- Retrieve current test statistics as Go structs
- Get final results with anomaly detection
- Query results via HTTP endpoints
- Monitor tests in real-time

## Data Structures

### TestResults (Common Structure)

The common structure for both periodic and final reports. Contains all metrics, metadata, and anomalies.

```go
type TestResults struct {
    Metadata   TestMetadata           `json:"metadata"`      // Test config & timing
    Overall    OverallStats           `json:"overall"`       // Aggregate statistics
    Targets    map[string]TargetMetrics `json:"targets"`     // Per-target metrics
    Anomalies  []string               `json:"anomalies"`     // Detected anomalies
    IsComplete bool                   `json:"is_complete"`   // Final vs periodic
    Message    string                 `json:"message"`       // Status message
}
```

### TestMetadata

Test configuration and timing information.

```go
type TestMetadata struct {
    Protocol       string      `json:"protocol"`        // "tcp" or "udp"
    PacketSize     int         `json:"packet_size"`     // Bytes per packet
    Concurrency    int         `json:"concurrency"`     // Connections per target
    Port           int         `json:"port"`            // Test port
    PPS            int         `json:"pps"`             // Packets/sec (0=throughput)
    LocalIP        string      `json:"local_ip"`        // Local binding IP
    TargetIPs      []string    `json:"target_ips"`      // Test targets
    StartTime      time.Time   `json:"start_time"`      // When test started
    EndTime        *time.Time  `json:"end_time"`        // When test ended (final)
    ElapsedMs      int64       `json:"elapsed_ms"`      // Time elapsed
    DurationMs     int64       `json:"duration_ms"`     // Test duration
    ThroughputMode bool        `json:"throughput_mode"` // Throughput vs packet mode
    PacketMode     bool        `json:"packet_mode"`     // Packet vs throughput mode
}
```

### OverallStats

Aggregate statistics across all targets.

```go
type OverallStats struct {
    // Throughput
    TotalThroughputMbps  float64 `json:"total_throughput_mbps"`
    TotalThroughputGbps  float64 `json:"total_throughput_gbps"`
    AvgThroughputMbps    float64 `json:"avg_throughput_mbps"`

    // Packets
    TotalPacketsSent     int64   `json:"total_packets_sent"`
    TotalPacketsReceived int64   `json:"total_packets_received"`
    TotalPacketsLost     int64   `json:"total_packets_lost"`
    AveragePacketLoss    float64 `json:"average_packet_loss"`  // Percentage

    // Latency
    AvgRTTMs     float64 `json:"avg_rtt_ms"`
    AvgJitterMs  float64 `json:"avg_jitter_ms"`
    MinRTTMs     float64 `json:"min_rtt_ms"`
    MaxRTTMs     float64 `json:"max_rtt_ms"`

    // Connections
    TotalConnections int   `json:"total_connections"`
    TotalReconnects  int64 `json:"total_reconnects"`
    TargetCount      int   `json:"target_count"`
}
```

### TargetMetrics

Performance metrics for a single target.

```go
type TargetMetrics struct {
    IP                string     `json:"ip"`                        // Target IP
    
    // Packets
    PacketsSent       int64      `json:"packets_sent"`
    PacketsReceived   int64      `json:"packets_received"`
    PacketsLost       int64      `json:"packets_lost"`
    PacketLossRate    float64    `json:"packet_loss_rate"`          // Percentage

    // Throughput
    BytesSent         int64      `json:"bytes_sent"`
    BytesReceived     int64      `json:"bytes_received"`
    ThroughputMbps    float64    `json:"throughput_mbps"`

    // Latency
    AvgRTTMs          float64    `json:"avg_rtt_ms"`
    MinRTTMs          float64    `json:"min_rtt_ms"`
    MaxRTTMs          float64    `json:"max_rtt_ms"`
    JitterMs          float64    `json:"jitter_ms"`

    // Connections
    ConnectionCount   int        `json:"connection_count"`
    ReconnectCount    int64      `json:"reconnect_count"`
    LastReconnectTime *time.Time `json:"last_reconnect_time"`
}
```

## Methods

### GetCurrentStats() -> TestResults

Returns current test statistics as a structured `TestResults` object.

**Characteristics:**
- Thread-safe (uses RLock)
- Returns real-time metrics
- `IsComplete` is `false`
- No anomaly detection (use GetFinalStats for that)
- Includes elapsed time

**Example:**
```go
results := tester.GetCurrentStats()
fmt.Printf("Total throughput: %.2f Gbps\n", results.Overall.TotalThroughputGbps)
fmt.Printf("Avg packet loss: %.2f%%\n", results.Overall.AveragePacketLoss)

// Per-target details
for ip, metrics := range results.Targets {
    fmt.Printf("%s: %.2f Mbps\n", ip, metrics.ThroughputMbps)
}
```

### GetFinalStats() -> TestResults

Returns final test statistics with anomaly detection.

**Characteristics:**
- Thread-safe (uses RLock)
- Includes `IsComplete: true`
- Contains `Anomalies` array
- Uses end time instead of elapsed
- Only call after test has completed

**Example:**
```go
tester.Wait()  // Wait for test to finish
results := tester.GetFinalStats()

if len(results.Anomalies) > 0 {
    fmt.Println("Issues detected:")
    for _, anomaly := range results.Anomalies {
        fmt.Println("  -", anomaly)
    }
} else {
    fmt.Println("All connections performing normally")
}
```

### StartAPIServer(port int) error

Starts an HTTP API server serving test results.

**Endpoints:**
- `GET /api/stats` - Returns current statistics
- `GET /api/stats/final` - Returns final statistics with anomalies
- `GET /health` - Health check endpoint

**Returns:**
- `nil` if server started successfully
- error if port binding failed

**Example:**
```go
if err := tester.StartAPIServer(8080); err != nil {
    log.Fatal(err)
}

// Later, query the API:
// curl http://localhost:8080/api/stats
// curl http://localhost:8080/api/stats/final
// curl http://localhost:8080/health
```

### StopAPIServer() error

Gracefully shuts down the API server.

**Returns:**
- `nil` if shutdown successful
- error if shutdown failed

**Example:**
```go
defer tester.StopAPIServer()
```

## HTTP API Endpoints

### GET /api/stats

Returns current test statistics as JSON.

**Response:**
```json
{
  "metadata": {
    "protocol": "tcp",
    "packet_size": 64000,
    "concurrency": 2,
    "port": 9999,
    "pps": 0,
    "local_ip": "192.168.1.1",
    "target_ips": ["192.168.1.2", "192.168.1.3"],
    "start_time": "2024-01-15T14:30:45Z",
    "elapsed_ms": 30000,
    "duration_ms": 300000,
    "throughput_mode": true,
    "packet_mode": false
  },
  "overall": {
    "total_throughput_mbps": 92800.0,
    "total_throughput_gbps": 92.8,
    "avg_throughput_mbps": 46400.0,
    "total_packets_sent": 5000000,
    "total_packets_received": 5000000,
    "total_packets_lost": 0,
    "average_packet_loss": 0.0,
    "avg_rtt_ms": -1,
    "avg_jitter_ms": -1,
    "min_rtt_ms": -1,
    "max_rtt_ms": -1,
    "total_connections": 4,
    "total_reconnects": 0,
    "target_count": 2
  },
  "targets": {
    "192.168.1.2": {
      "ip": "192.168.1.2",
      "packets_sent": 2500000,
      "packets_received": 2500000,
      "packets_lost": 0,
      "packet_loss_rate": 0.0,
      "bytes_sent": 160000000000,
      "throughput_mbps": 46400.0,
      "avg_rtt_ms": -1,
      "min_rtt_ms": -1,
      "max_rtt_ms": -1,
      "jitter_ms": -1,
      "connection_count": 2,
      "reconnect_count": 0
    },
    "192.168.1.3": {
      "ip": "192.168.1.3",
      "packets_sent": 2500000,
      "packets_received": 2500000,
      "packets_lost": 0,
      "packet_loss_rate": 0.0,
      "bytes_sent": 160000000000,
      "throughput_mbps": 46400.0,
      "avg_rtt_ms": -1,
      "min_rtt_ms": -1,
      "max_rtt_ms": -1,
      "jitter_ms": -1,
      "connection_count": 2,
      "reconnect_count": 0
    }
  },
  "is_complete": false,
  "message": "Current test statistics"
}
```

### GET /api/stats/final

Returns final test statistics with anomaly detection.

**Response (same as /api/stats plus anomalies):**
```json
{
  "metadata": { ... },
  "overall": { ... },
  "targets": { ... },
  "anomalies": [
    "HIGH LOSS: 192.168.1.4 has 5.5% packet loss",
    "HIGH JITTER: 192.168.1.5 has 25.3 ms jitter"
  ],
  "is_complete": true,
  "message": "Final test results"
}
```

### GET /health

Health check endpoint.

**Response:**
```json
{
  "status": "ok",
  "message": "API server is running"
}
```

## Usage Examples

### Example 1: Get Current Stats Programmatically

```go
package main

import (
    "fmt"
    "log"
    "time"
    "github.com/weka/gosmesh/pkg/testing"
)

func main() {
    tester := testing.NewNetworkTester(
        "192.168.1.1",
        []string{"192.168.1.2", "192.168.1.3"},
        "tcp", 2, 5*time.Minute, 5*time.Second,
        64000, 9999, 0,
    )

    if err := tester.Start(); err != nil {
        log.Fatal(err)
    }

    // Get stats periodically
    ticker := time.NewTicker(10 * time.Second)
    for range ticker.C {
        stats := tester.GetCurrentStats()
        fmt.Printf("Throughput: %.2f Gbps, Loss: %.2f%%\n",
            stats.Overall.TotalThroughputGbps,
            stats.Overall.AveragePacketLoss)
    }
}
```

### Example 2: Query Results via HTTP

```bash
# Get current stats
curl http://localhost:8080/api/stats | jq .overall.total_throughput_gbps

# Get final stats with anomalies
curl http://localhost:8080/api/stats/final | jq .anomalies

# Check health
curl http://localhost:8080/health
```

### Example 3: API Server with Graceful Shutdown

```go
package main

import (
    "log"
    "time"
    "github.com/weka/gosmesh/pkg/testing"
)

func main() {
    tester := testing.NewNetworkTester(...)
    
    // Start test
    if err := tester.Start(); err != nil {
        log.Fatal(err)
    }
    
    // Start API server
    if err := tester.StartAPIServer(8080); err != nil {
        log.Fatal(err)
    }
    
    // Cleanup
    defer func() {
        tester.StopAPIServer()
        tester.Stop()
        tester.Wait()
    }()
    
    // Test runs for configured duration, API available at http://localhost:8080
    tester.Wait()
}
```

### Example 4: Access Per-Target Metrics

```go
results := tester.GetCurrentStats()

for ip, metrics := range results.Targets {
    fmt.Printf("Target: %s\n", ip)
    fmt.Printf("  Throughput: %.2f Mbps\n", metrics.ThroughputMbps)
    fmt.Printf("  Loss Rate: %.2f%%\n", metrics.PacketLossRate)
    fmt.Printf("  Avg RTT: %.2f ms\n", metrics.AvgRTTMs)
    fmt.Printf("  Jitter: %.2f ms\n", metrics.JitterMs)
    fmt.Printf("  Reconnects: %d\n", metrics.ReconnectCount)
}
```

## Thread Safety

- `GetCurrentStats()` - Thread-safe, uses RLock
- `GetFinalStats()` - Thread-safe, uses RLock  
- `StartAPIServer()` - Thread-safe
- `StopAPIServer()` - Thread-safe

All methods can be called concurrently from multiple goroutines.

## Special Values

### Not Applicable (-1)

Some metrics return `-1` when not applicable:
- `AvgRTTMs`, `MinRTTMs`, `MaxRTTMs`, `JitterMs` return `-1` in **throughput mode**
- `PacketLossRate` returns `-1` for **TCP connections**

## Anomaly Detection

Anomalies are detected in `GetFinalStats()` based on thresholds:
- **High packet loss**: > 5%
- **High latency**: > 100ms average RTT
- **High jitter**: > 20ms
- **Low throughput**: < 10 Mbps for TCP

## Best Practices

1. **Call GetCurrentStats() periodically** for real-time monitoring
2. **Use GetFinalStats() after test completion** for anomaly detection
3. **Use StartAPIServer()** for external monitoring/dashboards
4. **Handle -1 values** when metrics aren't applicable
5. **Check IsComplete flag** to differentiate periodic vs final results
6. **Use Message field** for status information

## Integration Examples

### With Monitoring System

```go
// Send results to monitoring API every 10 seconds
ticker := time.NewTicker(10 * time.Second)
for range ticker.C {
    results := tester.GetCurrentStats()
    sendToMonitoring(results)
}
```

### With Dashboard

```go
// Expose via HTTP for dashboard visualization
tester.StartAPIServer(8080)
// Dashboard can query http://server:8080/api/stats regularly
```

### With Logging

```go
results := tester.GetFinalStats()
log.Printf("Test completed: %.2f Gbps, %d anomalies",
    results.Overall.TotalThroughputGbps,
    len(results.Anomalies))
```

