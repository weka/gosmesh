# Quick Reference: NetworkTester Results API

## At a Glance

| What | Method | Returns | Use When |
|------|--------|---------|----------|
| Current stats | `GetCurrentStats()` | `TestResults` | Monitoring during test |
| Final stats | `GetFinalStats()` | `TestResults` | After test completes |
| HTTP API | `StartAPIServer(port)` | error | Need REST endpoints |
| Stop API | `StopAPIServer()` | error | Shutting down |

## Quick Start

### Get Current Stats
```go
stats := tester.GetCurrentStats()
fmt.Println(stats.Overall.TotalThroughputGbps)
fmt.Println(stats.Targets["192.168.1.2"].ThroughputMbps)
```

### Get Final Stats with Anomalies
```go
tester.Wait()
final := tester.GetFinalStats()
if len(final.Anomalies) > 0 {
    fmt.Println(final.Anomalies)
}
```

### Start HTTP API
```go
tester.StartAPIServer(8080)
// Query: curl http://localhost:8080/api/stats
// Query: curl http://localhost:8080/api/stats/final
```

## Data Structure Map

```
TestResults
├── Metadata (TestMetadata)
│   ├── Protocol, PacketSize, Concurrency, Port, PPS
│   ├── LocalIP, TargetIPs
│   ├── StartTime, EndTime, ElapsedMs, DurationMs
│   └── ThroughputMode, PacketMode
├── Overall (OverallStats)
│   ├── TotalThroughputMbps/Gbps, AvgThroughputMbps
│   ├── Packets: TotalPacketsSent/Received/Lost, AveragePacketLoss
│   ├── Latency: AvgRTTMs, AvgJitterMs, MinRTTMs, MaxRTTMs
│   └── Connections: TotalConnections, TotalReconnects, TargetCount
├── Targets (map[string]TargetMetrics)
│   └── TargetMetrics per IP
│       ├── IP, PacketsSent/Received/Lost, PacketLossRate
│       ├── ThroughputMbps, AvgRTTMs, JitterMs
│       └── ConnectionCount, ReconnectCount, LastReconnectTime
├── Anomalies ([]string)
│   └── Detected issues (final only)
├── IsComplete (bool)
│   └── true=final, false=periodic
└── Message (string)
    └── Status description
```

## API Endpoints

```bash
# Current stats
GET /api/stats

# Final stats with anomalies
GET /api/stats/final

# Health check
GET /health
```

## Example Usage Patterns

### Pattern 1: Real-time Monitoring
```go
ticker := time.NewTicker(1 * time.Second)
for range ticker.C {
    results := tester.GetCurrentStats()
    fmt.Printf("%.2f Gbps\n", results.Overall.TotalThroughputGbps)
}
```

### Pattern 2: Per-target Analysis
```go
results := tester.GetCurrentStats()
for ip, metrics := range results.Targets {
    fmt.Printf("%s: %.2f Mbps, Loss: %.2f%%\n",
        ip, metrics.ThroughputMbps, metrics.PacketLossRate)
}
```

### Pattern 3: Anomaly Detection
```go
tester.Wait()
final := tester.GetFinalStats()
for _, anomaly := range final.Anomalies {
    log.Println("Issue:", anomaly)
}
```

### Pattern 4: HTTP Monitoring
```go
tester.StartAPIServer(8080)
// External monitor queries:
// curl http://localhost:8080/api/stats | jq .overall
```

### Pattern 5: Export to JSON
```go
results := tester.GetFinalStats()
data, _ := json.MarshalIndent(results, "", "  ")
fmt.Println(string(data))
```

## Special Values

| Value | Meaning | When |
|-------|---------|------|
| -1 (float64) | Not applicable | RTT metrics in throughput mode |
| nil (pointer) | Not set | EndTime before test ends, LastReconnectTime if no reconnects |
| empty []string | No issues | Anomalies array in GetCurrentStats or when test OK |

## Thread Safety

✅ Safe to call from multiple goroutines
✅ Safe during test execution  
✅ Safe after test completion
✅ Safe with concurrent HTTP requests

## Common Queries

```go
// Throughput
throughputGbps := results.Overall.TotalThroughputGbps

// Packet loss
loss := results.Overall.AveragePacketLoss

// Latency
rtt := results.Overall.AvgRTTMs

// Per-target throughput
targetMbps := results.Targets["192.168.1.2"].ThroughputMbps

// Anomalies
issues := len(results.Anomalies)

// Test duration
duration := results.Metadata.DurationMs

// Is complete?
isComplete := results.IsComplete
```

## HTTP Query Examples

```bash
# Get overall throughput
curl http://localhost:8080/api/stats | jq .overall.total_throughput_gbps

# Get all anomalies
curl http://localhost:8080/api/stats/final | jq .anomalies

# Get per-target loss
curl http://localhost:8080/api/stats | jq '.targets[].packet_loss_rate'

# Get test metadata
curl http://localhost:8080/api/stats | jq .metadata

# Full result with pretty print
curl http://localhost:8080/api/stats/final | jq .
```

## Lifecycle

```
1. Create tester: tester := NewNetworkTester(...)
2. Start test: tester.Start()
3. During: GetCurrentStats() multiple times
4. Optional: StartAPIServer(8080) for monitoring
5. Wait: tester.Wait()
6. End: GetFinalStats() once
7. Cleanup: StopAPIServer(), Stop()
```

## Error Handling

```go
// GetCurrentStats/GetFinalStats - no errors, always safe to call
results := tester.GetCurrentStats()

// StartAPIServer - check for binding errors
if err := tester.StartAPIServer(8080); err != nil {
    log.Printf("Failed to start API: %v", err)
}

// StopAPIServer - check for shutdown errors
if err := tester.StopAPIServer(); err != nil {
    log.Printf("Failed to stop API: %v", err)
}
```

## Performance

- GetCurrentStats(): ~1-2ms
- GetFinalStats(): ~1-2ms  
- HTTP request: <1ms
- Memory: Minimal per call

## Files Reference

- Implementation: `pkg/testing/tester.go`
- Documentation: `API_RESULTS.md`
- Examples: `api_examples.go`
- This guide: API_QUICK_REFERENCE.md

---

**Ready to use! See API_RESULTS.md for complete documentation.**

