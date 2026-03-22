# GoSmesh as a Library

GoSmesh can be used as a Go library to integrate network performance testing into your applications. This document covers how to use GoSmesh programmatically.

## Installation

```bash
go get github.com/weka/gosmesh
```

## Quick Start

The simplest way to run a network test:

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/weka/gosmesh/pkg/testing"
)

func main() {
	// Create a tester
	tester := testing.NewNetworkTester(
		"192.168.1.1",                          // local IP
		[]string{"192.168.1.2", "192.168.1.3"}, // targets
		"tcp",                                   // protocol
		2,                                       // concurrency per target
		5*time.Minute,                          // duration
		5*time.Second,                          // report interval
		64000,                                   // packet size
		9999,                                    // port
		0,                                       // pps (0=throughput mode)
	)

	// Start testing
	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	// Wait for completion
	tester.Wait()

	// Print results
	fmt.Println(tester.GenerateFinalReport())
}
```

## Core Types

### NetworkTester

The main orchestration class for network testing.

**Constructor:**
```go
func NewNetworkTester(localIP string, ips []string, protocol string, concurrency int,
	duration, reportInterval time.Duration, packetSize, port, pps int) *NetworkTester
```

**Parameters:**
- `localIP`: The local IP address to bind to for testing
- `ips`: Slice of target IP addresses to test against
- `protocol`: Either "tcp" or "udp"
- `concurrency`: Number of connections per target IP
- `duration`: How long the test should run
- `reportInterval`: Interval for periodic reports
- `packetSize`: Size of test packets in bytes
- `port`: Port to use for testing
- `pps`: Packets per second per connection (0 = unlimited throughput mode)

**Key Methods:**

```go
// Start begins the test (non-blocking)
func (nt *NetworkTester) Start() error

// Stop terminates the test immediately
func (nt *NetworkTester) Stop()

// Wait blocks until all test goroutines complete
func (nt *NetworkTester) Wait()

// GenerateFinalReport produces a comprehensive report
func (nt *NetworkTester) GenerateFinalReport() string

// EnableAPIReporting configures stats reporting to an HTTP endpoint
func (nt *NetworkTester) EnableAPIReporting(endpoint, reportIP string)
```

**Configuration Fields:**

Set these before calling `Start()`:

```go
// Performance optimization
tester.UseOptimized = true    // Enable optimized/high-performance mode
tester.EnableIOUring = true   // Enable Linux io_uring (Linux only)
tester.EnableHugePages = true // Use huge pages (Linux only)
tester.EnableOffload = true   // Enable hardware offload (if available)

// Tuning parameters
tester.BufferSize = 4194304   // 4MB buffers
tester.SendBatchSize = 64     // Batch multiple packets
tester.RecvBatchSize = 64
tester.NumWorkers = 4         // Worker threads
tester.BusyPollUsecs = 0      // Busy poll timeout (0=disabled)

// TCP options
tester.TCPNoDelay = true      // Disable Nagle's algorithm
tester.TCPCork = false        // Don't cork packets
tester.TCPQuickAck = true     // Enable quick ACK
```

### Connection

Represents a single test connection (advanced use).

**Constructor:**
```go
func NewConnection(localIP, targetIP string, port int, protocol string,
	packetSize, pps, id int) *Connection
```

**Methods:**

```go
// Start begins the connection (must provide context)
func (c *Connection) Start(ctx context.Context) error

// GetStats returns current performance metrics
func (c *Connection) GetStats() ConnectionStats
```

**Fields:**
```go
ID        int    // Unique identifier
TargetIP  string // Destination IP
```

### ConnectionStats

Performance metrics for a single connection.

```go
type ConnectionStats struct {
	PacketsSent     int64     // Packets transmitted
	PacketsReceived int64     // Echo responses received (packet mode)
	PacketsLost     int64     // Unreturned packets
	BytesSent       int64     // Total bytes sent
	BytesReceived   int64     // Total bytes received
	LossRate        float64   // Packet loss percentage (0-100)
	ThroughputMbps  float64   // Throughput in Mbps
	AvgRTTMs        float64   // Average RTT in milliseconds (packet mode)
	MinRTTMs        float64   // Minimum RTT
	MaxRTTMs        float64   // Maximum RTT
	JitterMs        float64   // RTT standard deviation
	LastUpdate      time.Time // When stats were last updated

	ReconnectCount    int64     // Number of reconnections
	LastReconnectTime time.Time // When last reconnect occurred
}
```

## Testing Modes

### Throughput Mode (Default)

Enabled when `pps` is 0 or not specified. Maximizes network speed.

```go
tester := testing.NewNetworkTester(
	"192.168.1.1",
	[]string{"192.168.1.2"},
	"tcp",
	64,           // more concurrency for throughput
	5*time.Minute,
	5*time.Second,
	9000,         // jumbo frames
	9999,
	0,            // pps=0 for throughput mode
)
```

**Available Metrics:**
- ✅ Throughput (Mbps/Gbps)
- ❌ Packet Loss (not measured)
- ❌ RTT/Jitter (not measured)

### Packet Mode

Enabled when `pps > 0`. Measures quality and reliability.

```go
tester := testing.NewNetworkTester(
	"192.168.1.1",
	[]string{"192.168.1.2"},
	"udp",        // use UDP for packet loss
	2,            // lower concurrency
	5*time.Minute,
	5*time.Second,
	1024,         // smaller packets
	9999,
	100,          // 100 pps per connection
)
```

**Available Metrics:**
- ✅ Throughput (Mbps)
- ✅ Packet Loss (UDP only - accurate via echo protocol)
- ✅ RTT/Jitter (both TCP and UDP)

## Complete Example

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/weka/gosmesh/pkg/testing"
)

func main() {
	// Create tester for 100Gbps network
	tester := testing.NewNetworkTester(
		"10.200.6.28",
		[]string{"10.200.6.240"},
		"tcp",
		64,           // optimal for 100Gbps
		2*time.Minute,
		10*time.Second,
		9000,         // jumbo frames
		9999,
		0,            // throughput mode
	)

	// Optimize for high performance
	tester.UseOptimized = true
	tester.BufferSize = 4194304
	tester.TCPNoDelay = true
	tester.NumWorkers = 4

	// Optional: send results to API
	tester.EnableAPIReporting("http://monitoring:8080/metrics", "10.200.6.28")

	// Start test
	if err := tester.Start(); err != nil {
		log.Fatalf("Failed to start test: %v", err)
	}

	// Wait for completion
	tester.Wait()

	// Print results
	report := tester.GenerateFinalReport()
	fmt.Println(report)
}
```

## Advanced: Low-Level Connection Control

For fine-grained control, use the `Connection` type directly:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/weka/gosmesh/pkg/network"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a single connection
	conn := network.NewConnection(
		"192.168.1.1",
		"192.168.1.2",
		9999,
		"tcp",
		64000,
		0,
		0,
	)

	// Start it
	go func() {
		if err := conn.Start(ctx); err != nil {
			fmt.Printf("Connection error: %v\n", err)
		}
	}()

	// Collect stats over time
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for i := 0; i < 10; i++ {
		<-ticker.C
		stats := conn.GetStats()
		fmt.Printf("Throughput: %.2f Mbps\n", stats.ThroughputMbps)
	}

	cancel()
}
```

## API Reporting

Send test results to an HTTP endpoint:

```go
tester.EnableAPIReporting("http://stats.example.com/api/report", "192.168.1.1")

// Stats are sent automatically every second as JSON:
// {
//   "ip": "192.168.1.1",
//   "throughput_gbps": 92.8,
//   "packet_loss_percent": -1,
//   "jitter_ms": -1,
//   "rtt_ms": -1,
//   "reconnect_count": 0,
//   "target_reconnects": {}
// }
```

## Performance Tuning

### For Maximum Throughput (100Gbps Networks)

```go
tester.UseOptimized = true
tester.BufferSize = 4194304        // 4MB
tester.TCPNoDelay = true
tester.NumWorkers = 4
tester.SendBatchSize = 64
tester.RecvBatchSize = 64

// Also configure:
tester := testing.NewNetworkTester(
	localIP, targets,
	"tcp",
	64,                // 64 connections
	duration,
	interval,
	9000,              // jumbo frames
	port,
	0,                 // throughput mode
)
```

### For Latency/Jitter Measurement

```go
tester := testing.NewNetworkTester(
	localIP, targets,
	"tcp",
	2,                 // lower concurrency
	duration,
	interval,
	1024,              // smaller packets
	port,
	10000,             // 10k pps for good RTT samples
)
```

### For Packet Loss Testing

```go
tester := testing.NewNetworkTester(
	localIP, targets,
	"udp",             // UDP only
	2,
	duration,
	interval,
	1024,
	port,
	1000,              // moderate packet rate
)
```

## Error Handling

```go
tester := testing.NewNetworkTester(...)

if err := tester.Start(); err != nil {
	// Handle initialization errors
	switch err.Error() {
	case "failed to start server":
		// Server binding failed - check port/IP
	case "failed to resolve IP":
		// IP address invalid
	default:
		log.Printf("Unexpected error: %v", err)
	}
}
```

## Concurrency

The `NetworkTester` is thread-safe for calling `GetStats()` from multiple goroutines, but you should not call `Start()` or `Stop()` from multiple goroutines concurrently.

## Platform-Specific Optimizations

### Linux

```go
tester.EnableIOUring = true   // Use io_uring for high-performance I/O
tester.EnableHugePages = true // Use huge pages for better TLB hit rates
tester.EnableOffload = true   // Use hardware offload if available
```

### macOS

Platform-specific optimizations are automatically disabled on unsupported platforms.

## Metrics Interpretation

### Throughput

- **Throughput mode**: Maximum achievable bandwidth
- **Packet mode**: Actual bandwidth with echo overhead
- **High throughput**: Close to line rate (e.g., 90+ Gbps on 100G networks)

### Packet Loss (UDP Packet Mode Only)

- **0% loss**: Perfect connectivity
- **0-1% loss**: Acceptable for most networks
- **>5% loss**: Indicates network problems

### RTT/Jitter (Packet Mode)

- **RTT**: Round-trip time in milliseconds
- **Jitter**: Standard deviation of RTT
- **Typical LAN**: RTT < 1ms, Jitter < 0.1ms
- **Typical WAN**: RTT 10-100ms, Jitter < 10ms

## See Also

- `examples.go` - More complete examples
- `API.md` - Quick reference guide
- `CLAUDE.md` - Architecture and development guide

