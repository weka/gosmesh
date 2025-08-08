# GoNet - Network Performance Testing Tool

GoNet is a distributed network testing tool that measures network performance metrics in a full mesh topology. It supports both UDP and TCP protocols and provides comprehensive reporting with anomaly detection.

## Features

- **Full Mesh Testing**: Automatically establishes connections between all nodes
- **Dual Protocol Support**: Test with both UDP and TCP
- **Real-time Metrics**: 
  - Packet loss rate
  - Round-trip time (RTT)
  - Jitter (RTT variance)
  - Throughput
- **Concurrent Testing**: Multiple connections per target for stress testing
- **Anomaly Detection**: Automatic identification of performance issues
- **Periodic & Final Reports**: Regular updates during testing and comprehensive final analysis

## Installation

```bash
go build -o gonet
```

## Usage

### Basic Usage

```bash
./gonet --ips ip1,ip2,ip3
```

Each server should be started with the same IP list. The tool automatically detects which IP belongs to the local machine and uses it as the listening address.

### Command Line Options

```
--ips            Comma-separated list of IPs for full mesh testing (required)
--protocol       Protocol to use: udp or tcp (default: udp)
--concurrency    Number of concurrent connections per target (default: 1)
--duration       Test duration (default: 60s)
--report-interval   Interval for periodic reports (default: 10s)
--packet-size    Size of test packets in bytes (default: 1024)
--port           Port to use for testing (default: 9999)
```

### Examples

#### Simple 3-node test
```bash
# On each node, run:
./gonet --ips 192.168.1.10,192.168.1.11,192.168.1.12
```

#### High concurrency TCP test
```bash
./gonet --ips 10.0.0.1,10.0.0.2,10.0.0.3 --protocol tcp --concurrency 5 --duration 2m
```

#### Quick UDP test with frequent reports
```bash
./gonet --ips 172.16.0.10,172.16.0.11 --duration 30s --report-interval 5s
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