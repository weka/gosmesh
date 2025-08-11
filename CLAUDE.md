# CLAUDE.md - AI Development Assistant Guide

## Project Overview
GoSmesh is a network testing tool designed to measure network performance metrics (jitter, packet loss, throughput) in a full mesh topology. It supports both UDP and TCP protocols and provides real-time and final reports with anomaly detection.

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

### Build System
The project uses [Taskfile](https://taskfile.dev/) for build automation. Install with:
```bash
# macOS
brew install go-task/tap/go-task

# Linux
sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d
```

### Common Build Commands
```bash
# Build for current platform (builds to .temp/ directory)
task build

# Cross-compile for Linux (builds to .temp/ directory)
task build-linux

# Deploy to a single server (uses binary from .temp/)
task deploy-linux SERVER=root@h1-4-c

# Deploy to multiple servers (uses binary from .temp/)
task deploy-all SERVERS="root@h1 root@h2 root@h3"

# Run local test (uses binary from .temp/)
task test-local

# Run mesh test with custom parameters (uses binary from .temp/)
task run-mesh IPS="10.0.0.1,10.0.0.2" DURATION=60s CONCURRENCY=4

# Clean build artifacts (removes entire .temp/ directory)
task clean
```

### Adding New Features
When adding features, consider:
1. Metrics are calculated per-connection in `connection.go`
2. Aggregation happens in `tester.go` for reporting
3. Protocol-specific code is separated (TCP vs UDP methods)

### Testing Modifications
To test changes locally:
```bash
# Quick build and test (uses .temp/ directory)
task build
task test-local

# Or manually (note: binary is in .temp/ directory)
./.temp/gosmesh --ips <ip1>,<ip2>,<ip3> --duration 30s --concurrency 2
```

### Code Validation
**ALWAYS validate code changes using go vet before committing:**
```bash
# Run go vet to check for common issues
task vet
```
This should be run after every code change to catch potential issues like:
- Unreachable code
- Incorrect format strings
- Missing imports
- Type checking errors

For multi-node testing:
```bash
# Build and deploy to test servers
task deploy-linux SERVER=root@server1
task deploy-linux SERVER=root@server2
task deploy-linux SERVER=root@server3

# Or deploy to all at once
task deploy-all SERVERS="root@server1 root@server2 root@server3"
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
- **Build and Temporary Files**: ALWAYS use .temp/ directory for all build artifacts, temporary files, and test outputs
  - All binaries are built into `.temp/` directory (configured in Taskfile.yml)
  - Never place build artifacts or temporary files in project root
  - Use `task clean` to remove all build artifacts from .temp/

## Testing Strategy
- Local testing: Use loopback and local network IPs
- Multi-node: Deploy binary to multiple servers with same IP list
- Stress testing: Increase concurrency and packet size gradually

## Performance Notes
- **Default Configuration**: 64 total connections distributed across mesh
- **Connection Distribution**: With N nodes, each connects to N-1 others with 64/(N-1) connections
- **Achieved Performance**: 92.8 Gbps on 100Gbps networks (99.2% of iperf)
- **Optimal Settings**: 64 connections, 4MB buffers, TCP protocol, jumbo frames

## Known Limitations
- Assumes symmetric network paths (echo-based)
- No encryption or authentication
- Single port per instance
- No persistent storage of results