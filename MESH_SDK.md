# GoSmesh Mesh Controller - Public SDK

The mesh package provides the MeshController for orchestrating distributed network tests across multiple nodes.

## Overview

The MeshController operates in two modes:

1. **With SSH Deployment** - Automatically builds, deploys, and starts gosmesh on remote nodes
2. **Without SSH Deployment** - Assumes servers are already running and just orchestrates the test

## Basic Usage

### Automatic Deployment

```go
import "github.com/weka/gosmesh/pkg/mesh"

config := &mesh.Config{
    IPs:       "10.0.0.1,10.0.0.2,10.0.0.3",
    UseSSH:    true,  // Enable automatic deployment
    SSHHosts:  "root@h1,root@h2,root@h3",
    Duration:  5 * time.Minute,
    Port:      9999,
    Protocol:  "tcp",
}

controller := mesh.NewController(config)
controller.Run()  // Deploy -> Start -> Wait
```

### Manual Control (Servers Already Running)

```go
config := &mesh.Config{
    IPs:      "10.0.0.1,10.0.0.2,10.0.0.3",
    UseSSH:   false,  // Assume servers already running
    Duration: 5 * time.Minute,
    Port:     9999,
    Protocol: "tcp",
}

controller := mesh.NewController(config)
if err := controller.Start(); err != nil {
    log.Fatal(err)
}
controller.Wait()
```

### Step-by-Step Control

```go
controller := mesh.NewController(config)

// Deploy to all nodes
if err := controller.Deploy(); err != nil {
    log.Fatal(err)
}

// Start the test
if err := controller.Start(); err != nil {
    log.Fatal(err)
}

// Monitor until completion
controller.Wait()

// Get final stats
stats := controller.GetStats()
fmt.Printf("Total throughput: %.2f Gbps\n", stats.TotalThroughput)
```

## API Reference

### Config

Configuration structure for MeshController:

```go
type Config struct {
    // Network configuration
    IPs       string
    Port      int
    Protocol  string  // "tcp" or "udp"
    LocalIP   string  // Optional: explicitly set local IP

    // Test configuration
    Duration          time.Duration
    ReportInterval    time.Duration
    Concurrency       int
    TotalConnections  int
    PacketSize        int
    PPS               int

    // SSH deployment (optional)
    SSHHosts string  // Comma-separated SSH hosts
    UseSSH   bool    // Enable SSH deployment

    // Performance tuning
    BufferSize      int
    TCPNoDelay      bool
    UseOptimized    bool
    EnableIOUring   bool
    EnableHugePages bool
    EnableOffload   bool
    // ... more fields ...

    // API configuration
    APIPort  int
    ReportTo string

    // Misc
    Verbose bool
}
```

### Methods

#### NewController

```go
func NewController(config *Config) *Controller
```

Creates a new MeshController instance.

#### Deploy

```go
func (mc *Controller) Deploy() error
```

Performs SSH deployment to all nodes (if SSH is enabled). Deploys the binary and creates services.

#### Start

```go
func (mc *Controller) Start() error
```

Starts the test services on all nodes. Can be called after Deploy() or if servers are already running.

#### Wait

```go
func (mc *Controller) Wait()
```

Blocks until the test completes or receives a signal. Continuously monitors and displays statistics.

#### Run

```go
func (mc *Controller) Run()
```

Complete workflow: Deploy (if SSH enabled) -> Start -> Wait. Main entry point for simple usage.

#### GetStats

```go
func (mc *Controller) GetStats() *MeshStats
```

Returns current aggregated statistics across all nodes as a structured object (not printed output).

**Returns:**
```go
type MeshStats struct {
    Timestamp       time.Time
    ActiveServers   int
    TotalServers    int
    TotalThroughput float64
    AvgThroughput   float64
    MinThroughput   float64
    MaxThroughput   float64
    AvgPacketLoss   float64
    AvgJitter       float64
    AvgRTT          float64
    TotalReconnects int64
    ServerStats     map[string]*ServerStats
    UpdatedAt       time.Time
}
```

#### Stop

```go
func (mc *Controller) Stop()
```

Terminates the controller.

## SSH Deployment Details

When `UseSSH: true` is set, the controller:

1. Connects to each node via SSH
2. Deploys the gosmesh binary
3. Creates a systemd service (gosmesh-mesh)
4. Starts the service
5. Verifies the service is running

## Integration Patterns

### Pattern 1: Monitoring During Test

```go
controller := mesh.NewController(config)
controller.Run()

// In parallel, monitor stats:
go func() {
    ticker := time.NewTicker(10 * time.Second)
    for range ticker.C {
        stats := controller.GetStats()
        fmt.Printf("Throughput: %.2f Gbps\n", stats.TotalThroughput)
    }
}()
```

### Pattern 2: Custom Deployment

```go
// Disable automatic SSH deployment
config.UseSSH = false

// Deploy manually
myDeployFunc(config.IPs, config.Port)

// Then use controller for orchestration
controller := mesh.NewController(config)
controller.Start()
controller.Wait()
```

### Pattern 3: Check Results

```go
controller := mesh.NewController(config)
controller.Run()  // Blocks until complete

// Get final statistics
stats := controller.GetStats()

// Check for issues
if stats.TotalReconnects > 0 {
    fmt.Printf("Warning: %d reconnections detected\n", stats.TotalReconnects)
}

if stats.AvgPacketLoss > 1.0 {
    fmt.Printf("Warning: %.2f%% packet loss\n", stats.AvgPacketLoss)
}
```

## SSH Deployer (Low-Level API)

For custom deployment logic, you can use the SSHDeployer directly:

```go
deployer := mesh.NewSSHDeployer(verbose)

// Execute remote command
if err := deployer.Exec("root@host1", "command", "description"); err != nil {
    log.Fatal(err)
}

// Copy file
if err := deployer.SCP("local/file", "root@host1:/remote/file", "copy binary"); err != nil {
    log.Fatal(err)
}
```

## Error Handling

All methods return errors for failure conditions:

```go
if err := controller.Deploy(); err != nil {
    log.Printf("Deployment failed: %v", err)
    // Handle cleanup
}
```

## Performance Considerations

- `GetStats()` is fast (~1ms) as it aggregates cached statistics
- The Controller uses goroutines for concurrent operations
- SSH operations timeout after 5 minutes for deployment, 2 minutes for service start
- Statistics are cached per-node and updated by receiving reports

## See Also

- `pkg/testing` - NetworkTester for single-node testing
- `pkg/network` - Low-level connection primitives

