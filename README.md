# GosMesh - Network Performance Testing Tool

GosMesh is a distributed network testing tool that measures network performance metrics in a full mesh topology. It supports both UDP and TCP protocols and provides comprehensive reporting with anomaly detection.

## Features

- **Full Mesh Testing**: Automatically establishes connections between all nodes
- **Dual Protocol Support**: Test with both UDP and TCP
- **Optimized for 100Gbps Networks**: Achieves 92.8 Gbps (99.2% of iperf performance)
- **Real-time Metrics**: 
  - Packet loss rate
  - Round-trip time (RTT)
  - Jitter (RTT variance)
  - Throughput
- **Smart Connection Distribution**: Automatically distributes 64 connections across mesh for optimal performance
- **Concurrent Testing**: Multiple connections per target for stress testing
- **Anomaly Detection**: Automatic identification of performance issues
- **Periodic & Final Reports**: Regular updates during testing and comprehensive final analysis

## Installation

```bash
go build -o gosmesh
```

## Usage

GosMesh has three main commands:

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

## Output

### Periodic Reports
Shows real-time statistics for each connection:
- Packets sent/received/lost
- Loss percentage
- Current throughput
- Average RTT and jitter
- Min/Max RTT values

### Final Report
Comprehensive analysis including:
- Per-target aggregated statistics
- Total packet counts
- Average performance metrics
- **Anomaly detection** highlighting:
  - High packet loss (>5%)
  - High latency (>100ms average RTT)
  - High jitter (>20ms)
  - Low throughput (<10 Mbps for TCP)

## Deployment

1. Copy the binary to all test nodes
2. Ensure the test port (default 9999) is open between all nodes
3. Start the tool on all nodes simultaneously with the same IP list
4. Each node will generate its own report from its perspective

## Performance on 100Gbps Networks

Based on extensive testing, GosMesh achieves:
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

### Good Performance Indicators
- Packet loss < 1%
- RTT < 50ms (varies by network distance)
- Jitter < 10ms
- Consistent throughput across connections

### Common Issues
- **100% packet loss**: Check firewall rules or network connectivity
- **High jitter**: Indicates network congestion or unstable path
- **Asymmetric performance**: Different metrics to/from same node suggests routing issues
- **Low throughput**: May indicate bandwidth limitations or TCP tuning issues

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