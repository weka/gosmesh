# CLAUDE.md - AI Development Assistant Guide

## Project Overview
GoNet is a network testing tool designed to measure network performance metrics (jitter, packet loss, throughput) in a full mesh topology. It supports both UDP and TCP protocols and provides real-time and final reports with anomaly detection.

## Architecture

### Core Components
1. **main.go** - Entry point, CLI parsing, local IP detection
2. **tester.go** - Orchestrates full mesh testing, manages connections, generates reports
3. **connection.go** - Handles individual connections, packet sending/receiving, metrics collection
4. **server.go** - Echo server implementation for both TCP and UDP

### Key Design Decisions
- **Full Mesh Topology**: Each node connects to all other nodes (excluding itself)
- **Echo-based Testing**: Server echoes packets back for RTT measurement
- **Concurrent Connections**: Multiple connections per target for stress testing
- **Real-time Metrics**: Continuous calculation of jitter, loss, throughput

## Development Flow

### Adding New Features
When adding features, consider:
1. Metrics are calculated per-connection in `connection.go`
2. Aggregation happens in `tester.go` for reporting
3. Protocol-specific code is separated (TCP vs UDP methods)

### Testing Modifications
To test changes:
```bash
go build -o gonet
./gonet --ips <ip1>,<ip2>,<ip3> --duration 30s --concurrency 2
```

### Common Tasks

#### Add New Metric
1. Add field to `ConnectionStats` struct in connection.go
2. Update `updateStats()` method to calculate metric
3. Include in report generation in tester.go

#### Modify Packet Format
1. Update packet structure in `connection.go`
2. Ensure both sender and receiver handle new format
3. Update server echo logic if needed

#### Change Report Format
1. Modify `generatePeriodicReport()` for periodic reports
2. Modify `GenerateFinalReport()` for final report
3. Anomaly detection logic is in `GenerateFinalReport()`

### Performance Considerations
- Packet sending rate: 100 pps (10ms interval)
- RTT history limited to 1000 samples to prevent memory growth
- Use atomic operations for packet counters to avoid lock contention

### Debugging Tips
- Check local IP detection if connections fail
- Verify firewall rules for the testing port (default 9999)
- Use `--protocol tcp` for more reliable testing in lossy networks
- Lower concurrency if seeing resource issues

## Code Standards
- Error handling: Return errors up the stack, log at top level
- Concurrency: Use context for cancellation, sync.WaitGroup for coordination
- Metrics: Use atomic operations for counters, mutex for complex stats

## Testing Strategy
- Local testing: Use loopback and local network IPs
- Multi-node: Deploy binary to multiple servers with same IP list
- Stress testing: Increase concurrency and packet size gradually

## Known Limitations
- Assumes symmetric network paths (echo-based)
- No encryption or authentication
- Single port per instance
- No persistent storage of results