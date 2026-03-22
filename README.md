# GoSmesh - Network Performance Testing Tool

GoSmesh is a distributed network testing tool that measures network performance metrics in a full mesh topology. It supports both UDP and TCP protocols and provides comprehensive reporting with anomaly detection.

Note: This software is not affiliated with WEKA and meant as a smoke testing tool provided As-Is with MIT license. 

## Features

- **Full Mesh Testing**: Automatically establishes connections between all nodes
- **Dual Protocol Support**: Test with both UDP and TCP
- **Optimized for 100Gbps Networks**: Achieves 92.8 Gbps (99.2% of iperf performance)
- **Real-time Metrics**:
  - **Throughput** (always measured)
  - **Packet loss rate** (only in packet mode with `--pps`)
  - **Round-trip time (RTT)** (only in packet mode with `--pps`)
  - **Jitter (RTT variance)** (only in packet mode with `--pps`)
- **Smart Connection Distribution**: Automatically distributes 64 connections across mesh for optimal performance
- **Concurrent Testing**: Multiple connections per target for stress testing
- **Anomaly Detection**: Automatic identification of performance issues
- **Periodic & Final Reports**: Regular updates during testing and comprehensive final analysis

## Installation

```bash
go build -o gosmesh
```

## Usage

GoSmesh has three main commands:

### Commands

- **`run`** - Run network test directly (used by systemd on individual nodes)
- **`mesh`** - Deploy and orchestrate mesh testing across multiple nodes from a single controller
- **`uninstall`** - Remove gosmesh deployment from specified nodes

### Basic Usage

#### Direct Node Testing
```bash
gosmesh run --ips ip1,ip2,ip3
```

#### Mesh Controller (Recommended)
```bash
gosmesh mesh --ips ip1,ip2,ip3
```

The mesh controller automatically deploys, starts, and monitors tests across all nodes from a single control point.

### Command Line Options

#### Common Options (both run and mesh)
```
--ips               Comma-separated list of IPs for full mesh testing (required)
--protocol          Protocol to use: udp or tcp (default: tcp)
--total-connections Total connections to distribute across mesh (default: 64)
--concurrency       Connections per target (0=auto from total-connections)
--duration          Test duration (default: 5m)
--report-interval   Interval for periodic reports (default: 5s)
--packet-size       Size of test packets (0=auto-detect, uses jumbo frames)
--port              Port to use for testing (default: 9999)
--pps               Packets per second per connection (0=unlimited throughput mode)
--buffer-size       Buffer size for throughput mode (0=auto, 4MB for TCP)
```

#### Mesh-specific Options
```
--ssh-hosts         SSH hosts (user@host1,user@host2,...) - if not provided, uses root@IP
--api-port          Port for API server (default: 8080)
--verbose           Enable verbose logging
```

### Examples

#### Mesh Controller (Recommended)
```bash
# High-Performance 2-node test (100Gbps networks)
gosmesh mesh --ips 10.200.6.28,10.200.6.240 --duration 2m

# 3-node full mesh test
gosmesh mesh --ips 192.168.1.10,192.168.1.11,192.168.1.12

# 4-node mesh with custom connection count
gosmesh mesh --ips 10.0.0.1,10.0.0.2,10.0.0.3,10.0.0.4 --total-connections 128

# UDP test with verbose logging
gosmesh mesh --ips 172.16.0.10,172.16.0.11 --protocol udp --verbose
```

#### Direct Node Testing
```bash
# Run directly on each node (must start simultaneously)
gosmesh run --ips 10.200.6.28,10.200.6.240

# UDP test with packet rate limiting
gosmesh run --ips 172.16.0.10,172.16.0.11 --protocol udp --pps 1000
```

#### Cleanup
```bash
# Remove deployment from all nodes
gosmesh uninstall --ips 10.200.6.28,10.200.6.240,10.200.6.25
```

## Using GoSmesh as a Library

GoSmesh can be imported and used as a Go library for programmatic network testing.

### Installation

```bash
go get github.com/weka/gosmesh
```

### Basic Usage

```go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/weka/gosmesh/pkg/testing"
)

func main() {
	// Create a new tester
	tester := testing.NewNetworkTester(
		"192.168.1.1",                          // local IP
		[]string{"192.168.1.2", "192.168.1.3"}, // target IPs
		"tcp",                                   // protocol
		2,                                       // concurrency per target
		5*time.Minute,                          // duration
		5*time.Second,                          // report interval
		64000,                                   // packet size
		9999,                                    // port
		0,                                       // pps (0 = throughput mode)
	)

	// Optional: configure optimization
	tester.UseOptimized = true
	tester.TCPNoDelay = true
	tester.BufferSize = 4194304 // 4MB

	// Start the test (non-blocking)
	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	// Wait for completion or stop early
	tester.Wait()

	// Get and print the final report
	report := tester.GenerateFinalReport()
	fmt.Println(report)
}
```

### Advanced Configuration

```go
// Enable API reporting
tester.EnableAPIReporting("http://api.example.com/stats", "192.168.1.1")

// Configure for high performance
tester.UseOptimized = true
tester.BufferSize = 4194304           // 4MB buffers
tester.NumWorkers = 4                 // Worker threads
tester.SendBatchSize = 64             // Batch packets
tester.RecvBatchSize = 64
tester.TCPNoDelay = true              // Disable Nagle
tester.TCPCork = false                // Don't cork packets
```

### Accessing Real-time Metrics

```go
// Start the test
tester.Start()

// Periodically check metrics in your own code
// Access low-level metrics through the testing package API
```

For complete API documentation, see the `pkg/testing` and `pkg/network` packages.

## Measurement Modes

GoSmesh operates in two distinct modes that provide different types of measurements:

### Throughput Mode (Default)
**Enabled when**: No `--pps` specified or `--pps 0`
**Purpose**: Maximum network speed measurement
**Metrics available**:
- ✅ **Throughput** - Maximum achievable bandwidth
- ❌ **Packet Loss** - Not applicable (prioritizes speed over echo reliability)
- ❌ **RTT/Jitter** - Not measured (no packet timing)

**Use cases**:
- Bandwidth testing (like iperf)
- Performance benchmarking
- Maximum speed validation

### Packet Mode  
**Enabled when**: `--pps X` where X > 0
**Purpose**: Network quality and reliability measurement
**Metrics available**:
- ✅ **Throughput** - Based on actual packet delivery
- ✅ **Packet Loss** - **UDP only** - Accurate loss percentage via echo protocol
- ✅ **RTT/Jitter** - Round-trip time statistics

**Protocol considerations**:
- **UDP**: All metrics available - recommended for quality testing
- **TCP**: Packet loss not measured (TCP hides retransmissions from application)

**Use cases**:
- Network quality assessment (use UDP)
- Latency-sensitive application testing
- Packet loss troubleshooting (UDP only)

### PPS (Packets Per Second) Calculation

`--pps` specifies packets per second **per individual connection**. Total network load scales with mesh size:

```
Total PPS from each server = --pps × (target servers) × (connections per target)
```

**Examples**:
- **2 servers, --pps 100**: Each server sends 100 × 1 × 64 = 6,400 pps total
- **4 servers, --pps 50**: Each server sends 50 × 3 × 21 = 3,150 pps total

## Output

### Output Format by Mode

#### Throughput Mode Output
```bash
=== Mesh Statistics [2025-01-15 14:30:45] ===
Active Servers: 4/4

Throughput:
  Total: 345.67 Gbps | Avg: 86.42 Gbps
  Best: 10.0.0.1 (92.34 Gbps) | Worst: 10.0.0.4 (78.90 Gbps)

Packet Loss: Not applicable (throughput mode)

RTT/Jitter: Not measured (throughput mode)
```

#### Packet Mode Output (UDP)
```bash
=== Mesh Statistics [2025-01-15 14:30:45] ===
Active Servers: 4/4

Throughput:
  Total: 45.67 Gbps | Avg: 11.42 Gbps
  Best: 10.0.0.1 (12.34 Gbps) | Worst: 10.0.0.4 (10.23 Gbps)

Packet Loss:
  Avg: 0.12% | Best: 10.0.0.2 (0.01%) | Worst: 10.0.0.3 (0.34%)

Jitter:
  Avg: 2.45 ms | Best: 10.0.0.1 (1.23 ms) | Worst: 10.0.0.4 (4.56 ms)

RTT:
  Avg: 15.67 ms | Best: 10.0.0.2 (12.34 ms) | Worst: 10.0.0.3 (18.90 ms)
```

#### Packet Mode Output (TCP)
```bash
=== Mesh Statistics [2025-01-15 14:30:45] ===
Active Servers: 4/4

Throughput:
  Total: 45.67 Gbps | Avg: 11.42 Gbps
  Best: 10.0.0.1 (12.34 Gbps) | Worst: 10.0.0.4 (10.23 Gbps)

Packet Loss: Not applicable (use UDP for packet loss testing)

Jitter:
  Avg: 2.45 ms | Best: 10.0.0.1 (1.23 ms) | Worst: 10.0.0.4 (4.56 ms)

RTT:
  Avg: 15.67 ms | Best: 10.0.0.2 (12.34 ms) | Worst: 10.0.0.3 (18.90 ms)
```

### Anomaly Detection (Packet Mode Only)
When using `--pps`, the system automatically detects:
- **High packet loss** (>5%) - UDP only
- **High latency** (>100ms average RTT)  
- **High jitter** (>20ms)
- **Low throughput** (<10 Mbps for TCP)

## Deployment

1. Copy the binary to all test nodes
2. Ensure the test port (default 9999) is open between all nodes
3. Start the tool on all nodes simultaneously with the same IP list
4. Each node will generate its own report from its perspective

## Performance on 100Gbps Networks

Based on extensive testing, GoSmesh achieves:
- **92.8 Gbps** with 64 connections (99.2% of iperf baseline)
- **75.6 Gbps** with 32 connections
- **47.5 Gbps** with 16 connections

### Optimization Tips for Maximum Performance
- Use default 64 total connections for best results
- Enable jumbo frames (MTU 9000) if available
- Use TCP protocol for highest throughput
- Ensure buffer size is at least 4MB (auto-configured)

## Network Requirements

- All nodes must be reachable from each other
- Firewall rules must allow the test port (default 9999)
- For UDP testing, ensure UDP traffic is not blocked
- For TCP testing, ensure TCP connections can be established

## Interpreting Results

### Performance Indicators by Mode

#### Throughput Mode
- **High throughput**: Close to line rate (e.g., 90+ Gbps on 100G networks)
- **Consistent performance**: Similar throughput across all servers
- **Stable connections**: No connection drops or errors

#### Packet Mode  
- **Low packet loss**: < 1% in most networks
- **Low latency**: RTT < 50ms (varies by distance)
- **Low jitter**: < 10ms for stable networks
- **Predictable performance**: Consistent metrics across connections

### Common Issues

#### Both Modes
- **Low throughput**: Check bandwidth limits, CPU usage, buffer sizes
- **Connection failures**: Verify firewall rules and port accessibility
- **Asymmetric performance**: May indicate routing or hardware issues

#### Packet Mode Only
- **High packet loss** (UDP): Network congestion, buffer overruns, or connectivity issues
- **High jitter**: Network instability, congestion, or competing traffic
- **Variable RTT**: Routing changes or network congestion

#### TCP vs UDP in Packet Mode
- **TCP packet loss always shows "Not applicable"**: This is correct - TCP hides retransmissions from the application
- **Use UDP for packet loss testing**: TCP's built-in reliability makes application-level loss measurement meaningless
- **RTT/Jitter available for both**: Both protocols can measure round-trip times accurately

## Troubleshooting

### Local IP not detected
- Ensure one of the provided IPs is assigned to a local network interface
- Check IP formatting (no spaces, proper comma separation)

### No packets received
- Verify firewall rules on all nodes
- Check if the port is already in use
- Try with a different port using `--port` flag

### High packet loss
- Reduce concurrency if system resources are limited
- Check network capacity and congestion
- Try TCP mode for more reliable delivery

## License

MIT
