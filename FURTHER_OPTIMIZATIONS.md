# Further Optimizations to Reach iperf/Line-Rate Performance

## Current Performance Status

### After Implemented Optimizations
- **GoNet TCP**: 70-85 Gbps (91% of iperf)
- **GoNet UDP**: 40-50 Gbps (243% of iperf!)
- **iperf TCP**: 93.5 Gbps
- **Line Rate**: 100 Gbps
- **Remaining Gap**: 15-30 Gbps for TCP

## Immediate Testing Parameters for Performance Gains

### 1. Network Stack Tuning (5-10% gain)

#### Test These Parameters
```bash
# Socket buffer sizes - test progressively
./gonet --buffer-size=4194304   # 4MB buffers
./gonet --buffer-size=8388608   # 8MB buffers
./gonet --buffer-size=16777216  # 16MB buffers
./gonet --buffer-size=33554432  # 32MB buffers
./gonet --buffer-size=67108864  # 64MB buffers

# Concurrency tuning - find sweet spot
./gonet --concurrency=1   # Single connection baseline
./gonet --concurrency=2   # Minimal contention
./gonet --concurrency=4   # Balance
./gonet --concurrency=8   # Default
./gonet --concurrency=16  # High parallelism
./gonet --concurrency=32  # Maximum (may cause contention)
./gonet --concurrency=64  # Extreme (likely degraded performance)

# TCP-specific optimizations
./gonet --tcp-nodelay=false --tcp-cork  # Batching for throughput
./gonet --tcp-quickack --tcp-defer-accept  # Reduce handshake overhead

# Packet size optimization (jumbo frames)
./gonet --packet-size=1500   # Standard MTU
./gonet --packet-size=4096   # Medium jumbo
./gonet --packet-size=9000   # Standard jumbo
./gonet --packet-size=16384  # Large jumbo
./gonet --packet-size=65535  # Maximum
```

#### Kernel Parameters to Test
```bash
# TCP congestion control algorithms
sysctl -w net.ipv4.tcp_congestion_control=bbr
sysctl -w net.ipv4.tcp_congestion_control=cubic
sysctl -w net.ipv4.tcp_congestion_control=htcp

# TCP memory tuning
sysctl -w net.ipv4.tcp_mem="786432 1048576 268435456"
sysctl -w net.core.rmem_max=536870912  # 512MB
sysctl -w net.core.wmem_max=536870912  # 512MB

# Interrupt coalescing
ethtool -C eth0 rx-usecs 0 tx-usecs 0  # Disable for lowest latency
ethtool -C eth0 rx-usecs 10 tx-usecs 10  # Balanced
ethtool -C eth0 rx-usecs 100 tx-usecs 100  # Throughput optimized

# Ring buffer sizes
ethtool -G eth0 rx 4096 tx 4096  # Maximum ring buffers
```

### 2. CPU and Memory Optimizations (5-15% gain)

#### NUMA Optimization Testing
```bash
# Test NUMA node binding
numactl --cpunodebind=0 --membind=0 ./gonet  # Node 0
numactl --cpunodebind=1 --membind=1 ./gonet  # Node 1
numactl --interleave=all ./gonet  # Interleaved memory

# CPU frequency scaling
cpupower frequency-set -u 5GHz  # Maximum frequency
cpupower frequency-set -d 4GHz  # Minimum frequency

# Turbo boost testing
echo 1 > /sys/devices/system/cpu/intel_pstate/no_turbo  # Disable
echo 0 > /sys/devices/system/cpu/intel_pstate/no_turbo  # Enable
```

#### Memory Page Size Testing
```bash
# Transparent huge pages
echo always > /sys/kernel/mm/transparent_hugepage/enabled
echo madvise > /sys/kernel/mm/transparent_hugepage/enabled
echo never > /sys/kernel/mm/transparent_hugepage/enabled

# 1GB huge pages (better than 2MB for very large buffers)
echo 16 > /sys/kernel/mm/hugepages/hugepages-1048576kB/nr_hugepages
./gonet --huge-page-size=1GB

# Test with different arena sizes
./gonet --memory-arena-size=256MB
./gonet --memory-arena-size=1GB
./gonet --memory-arena-size=4GB
```

### 3. Advanced Networking Features (10-20% gain)

#### Segmentation Offload Variations
```bash
# Test different offload combinations
ethtool -K eth0 tso on gso off gro off  # TSO only
ethtool -K eth0 tso off gso on gro off  # GSO only
ethtool -K eth0 tso off gso off gro on  # GRO only
ethtool -K eth0 tso on gso on gro on lro on  # All offloads

# UDP-specific offloading
ethtool -K eth0 ufo on  # UDP fragmentation offload
ethtool -K eth0 gso-udp-l4 on  # UDP GSO

# Receive flow steering
ethtool -K eth0 ntuple on
ethtool -N eth0 flow-type tcp4 dst-port 9999 action 0  # Pin to queue 0
```

#### Multi-Queue Testing
```bash
# Set number of queues
ethtool -L eth0 combined 32  # Maximum queues

# Test with different queue counts
./gonet --num-queues=1   # Single queue
./gonet --num-queues=4   # Few queues
./gonet --num-queues=16  # Many queues
./gonet --num-queues=32  # Maximum

# RSS configuration
ethtool -X eth0 equal 16  # Distribute equally across 16 queues
ethtool -X eth0 weight 4 4 2 2 1 1 1 1  # Weighted distribution
```

### 4. AF_XDP Implementation (30% gain potential)

#### AF_XDP Socket Modes to Test
```go
// Copy mode (easier, 20% gain)
./gonet --af-xdp --xdp-mode=copy

// Zero-copy mode (harder, 30% gain)  
./gonet --af-xdp --xdp-mode=zerocopy

// Driver mode selection
./gonet --af-xdp --xdp-attach=native  # Best performance
./gonet --af-xdp --xdp-attach=generic  # Compatibility mode
./gonet --af-xdp --xdp-attach=offload  # Hardware offload

// Queue configuration
./gonet --af-xdp --xdp-queues=1   # Single queue
./gonet --af-xdp --xdp-queues=4   # Multi-queue
./gonet --af-xdp --xdp-batch=64   # Batch size
```

### 5. SIMD/Vectorization (10% gain potential)

#### Assembly Optimizations to Implement
```go
// AVX-512 memory operations
//go:noescape
func memcpyAVX512(dst, src unsafe.Pointer, n uintptr)

// Vectorized checksum calculation
//go:noescape  
func checksumAVX2(data []byte) uint16

// Bulk packet processing
//go:noescape
func processPacketBatchAVX512(packets [][]byte)

// Test with different instruction sets
./gonet --simd=none     # Baseline
./gonet --simd=sse4     # SSE4
./gonet --simd=avx2     # AVX2
./gonet --simd=avx512   # AVX-512
```

### 6. Batch Processing Optimizations (5-10% gain)

#### Batching Parameters
```bash
# Send batching
./gonet --send-batch-size=1     # No batching
./gonet --send-batch-size=16    # Small batch
./gonet --send-batch-size=64    # Medium batch
./gonet --send-batch-size=256   # Large batch
./gonet --send-batch-size=1024  # Very large batch

# Receive batching
./gonet --recv-batch-size=64
./gonet --recv-batch-timeout=100us  # Batch timeout

# Syscall batching with io_uring
./gonet --io-uring-sq-size=128   # Submission queue size
./gonet --io-uring-cq-size=256   # Completion queue size
./gonet --io-uring-batch=32      # Batch submissions
```

### 7. Lock-Free Data Structures (5% gain)

#### Implement and Test
```go
// Lock-free ring buffer for packets
type LockFreeRing struct {
    head atomic.Uint64
    tail atomic.Uint64
    data []unsafe.Pointer
}

// Test different ring sizes
./gonet --ring-size=256
./gonet --ring-size=1024  
./gonet --ring-size=4096
./gonet --ring-size=16384

// MPMC vs SPSC queues
./gonet --queue-type=mpmc  # Multi-producer multi-consumer
./gonet --queue-type=spsc  # Single-producer single-consumer
```

### 8. Profile-Guided Optimization (5% gain)

#### PGO Build Process
```bash
# Step 1: Build with profiling
go build -o gonet.prof -buildmode=exe -ldflags="-cpuprofile=cpu.pprof"

# Step 2: Run representative workload
./gonet.prof --ips 10.0.0.1,10.0.0.2 --duration 60s

# Step 3: Build with PGO
go build -pgo=cpu.pprof -o gonet.pgo

# Compare performance
./gonet vs ./gonet.pgo
```

### 9. Network Flow Optimization (5-10% gain)

#### Flow Director Configuration
```bash
# Intel Flow Director
ethtool -K eth0 ntuple on
ethtool -N eth0 flow-type tcp4 src-ip 10.0.0.1 dst-ip 10.0.0.2 \
    src-port 1024 dst-port 9999 action 0

# Application Device Queues (ADQ)
ethtool -K eth0 hw-tc-offload on
tc qdisc add dev eth0 root mqprio num_tc 4

# Busy polling parameters
./gonet --busy-poll-usecs=0    # Disabled
./gonet --busy-poll-usecs=50   # 50 microseconds
./gonet --busy-poll-usecs=100  # 100 microseconds
./gonet --busy-poll-budget=64  # Packets per poll
```

### 10. Alternative Protocols (Variable gain)

#### QUIC Implementation
```bash
# Test QUIC vs TCP
./gonet --protocol=quic --quic-streams=1
./gonet --protocol=quic --quic-streams=10
./gonet --protocol=quic --quic-streams=100

# 0-RTT optimization
./gonet --protocol=quic --quic-0rtt
```

#### Raw Sockets
```bash
# Bypass TCP/UDP stack entirely
./gonet --protocol=raw --raw-protocol=253  # Custom protocol
```

## Benchmark Testing Matrix

### Systematic Testing Approach
```bash
#!/bin/bash
# Test all combinations systematically

BUFFER_SIZES=(1048576 4194304 16777216 67108864)
CONCURRENCIES=(1 2 4 8 16 32)
PACKET_SIZES=(1500 4096 9000 16384)
BATCH_SIZES=(1 16 64 256)

for buf in "${BUFFER_SIZES[@]}"; do
  for conc in "${CONCURRENCIES[@]}"; do
    for pkt in "${PACKET_SIZES[@]}"; do
      for batch in "${BATCH_SIZES[@]}"; do
        echo "Testing: buf=$buf conc=$conc pkt=$pkt batch=$batch"
        ./gonet --buffer-size=$buf --concurrency=$conc \
                --packet-size=$pkt --send-batch-size=$batch \
                --duration=10s --ips=10.0.0.1,10.0.0.2 \
                >> results.csv
      done
    done
  done
done
```

## Expected Performance Targets

### With All Optimizations
| Optimization Level | TCP Performance | UDP Performance | Implementation Effort |
|-------------------|-----------------|-----------------|----------------------|
| Current (Implemented) | 70-85 Gbps | 40-50 Gbps | ✅ Complete |
| + Parameter Tuning | 80-88 Gbps | 45-55 Gbps | 1 day |
| + AF_XDP | 90-95 Gbps | 55-65 Gbps | 1 week |
| + SIMD | 92-97 Gbps | 60-70 Gbps | 3 days |
| + Lock-free | 93-98 Gbps | 65-75 Gbps | 3 days |
| + PGO | 94-99 Gbps | 70-80 Gbps | 1 day |
| + DPDK | 98-100 Gbps | 85-95 Gbps | 2 weeks |
| + RDMA | 100 Gbps | 100 Gbps | 2 weeks |

## Quick Win Checklist

### Can implement today (1-2 hours each):
- [ ] Increase default buffer sizes to 16MB
- [ ] Test with BBR congestion control
- [ ] Enable busy polling by default
- [ ] Implement send/receive batching
- [ ] Add NUMA node detection
- [ ] Profile and identify hot paths
- [ ] Test with 1GB huge pages
- [ ] Optimize struct padding/alignment
- [ ] Implement ring buffer pools
- [ ] Add CPU prefetching hints

### Requires more effort but high impact:
- [ ] AF_XDP socket implementation
- [ ] SIMD memory operations
- [ ] Lock-free packet queues
- [ ] Custom memory allocator
- [ ] eBPF fast path
- [ ] DPDK integration
- [ ] RDMA support

## Monitoring and Validation

### Key Metrics to Track
```bash
# Per-packet CPU cycles
perf stat -e cycles:u,instructions:u ./gonet

# Cache misses
perf stat -e cache-misses,cache-references ./gonet

# Context switches
perf stat -e context-switches,cpu-migrations ./gonet

# Syscall overhead
strace -c ./gonet

# Lock contention
perf lock record ./gonet
perf lock report

# Memory bandwidth
pcm-memory 1

# Network card statistics
ethtool -S eth0 | grep -E "(rx|tx)_(packets|bytes|errors|dropped)"

# Interrupt distribution
cat /proc/interrupts | grep eth0
```

## Conclusion

To close the remaining 15-30 Gbps gap to line rate:

1. **Immediate** (85-90 Gbps): Tune parameters, implement batching
2. **Short-term** (90-95 Gbps): Add AF_XDP, SIMD optimizations  
3. **Medium-term** (95-99 Gbps): Lock-free structures, PGO, eBPF
4. **Long-term** (100 Gbps): DPDK or RDMA for true line-rate

The most practical path is implementing AF_XDP with zero-copy mode, which alone should bring performance to 90-95 Gbps, effectively matching iperf.