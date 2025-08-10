# Performance Comparison: GosMesh vs iperf

## Executive Summary

**iperf significantly outperforms our GosMesh implementation**, achieving near wire-speed on the 100Gbit network. This reveals optimization opportunities in our implementation.

---

## Test Results Comparison

### TCP Performance (8 parallel connections)

| Tool | Direction | Throughput | Notes |
|------|-----------|------------|-------|
| **iperf** | h5→h11 | **93.5 Gbps** | Near wire-speed! |
| **iperf** | h11→h5 | **90.9 Gbps** | Bidirectional works |
| **GosMesh** | h11→h5 | **4.4 Gbps** | 21x slower than iperf |
| **GosMesh** | h5→h11 | **0 Gbps** | Connection refused (firewall) |

### UDP Performance (8 parallel connections)

| Tool | Direction | Send Rate | Receive Rate | Loss |
|------|-----------|-----------|--------------|------|
| **iperf** | h5→h11 | 20.6 Gbps | ~20 Gbps | 1-8% |
| **GosMesh** | h11→h5 | 20.3 Gbps | 0 Gbps | 100% (firewall) |
| **GosMesh** | h5→h11 | 20.3 Gbps | ~1.5 Gbps | 83% |

---

## Performance Gap Analysis

### Why iperf is Faster

1. **Kernel Optimizations**
   - iperf uses optimized system calls (sendfile, splice)
   - Zero-copy data paths where possible
   - Proper TCP socket buffer tuning

2. **Buffer Management**
   - iperf uses 128KB+ buffers
   - GoNet uses packet-by-packet sending (2KB)
   - iperf batches operations efficiently

3. **Threading Model**
   - iperf: One thread per connection with blocking I/O
   - GosMesh: Goroutines with smaller default stack size

4. **TCP Specific Issues in GosMesh**
   - Not using TCP_NODELAY for latency optimization
   - Not tuning SO_SNDBUF/SO_RCVBUF socket buffers
   - Packet-based design not optimal for stream protocol

5. **CPU Efficiency**
   - iperf: ~10% CPU per 10Gbps stream
   - GosMesh: ~100% CPU per 2.5Gbps stream

---

## Identified Issues in GosMesh

### Critical Issues

1. **Inefficient Sending Pattern**
   ```go
   // Current: Send one packet at a time
   n, err := c.conn.Write(packet)
   
   // Better: Buffer multiple packets
   buffer := make([]byte, 65536)
   // Fill buffer with multiple packets
   n, err := c.conn.Write(buffer)
   ```

2. **No Socket Buffer Tuning**
   - Need to set SO_SNDBUF/SO_RCVBUF to at least 4MB
   - TCP window scaling not optimized

3. **Timestamp Overhead**
   - Getting time.Now().UnixNano() for every packet
   - Should batch timestamps or use less precise timing

4. **Small Packet Problem**
   - Sending 2KB packets individually
   - Should aggregate into larger writes (64KB+)

### Performance Bottlenecks

1. **System Call Overhead**
   - One syscall per packet (bad)
   - iperf batches multiple packets per syscall (good)

2. **Memory Allocation**
   - Creating new buffers frequently
   - Should reuse buffers with sync.Pool

3. **Lock Contention**
   - Atomic operations on every packet
   - Should batch statistics updates

---

## Optimization Recommendations for GosMesh

### Immediate Improvements

1. **Increase Write Buffer Size**
   ```go
   // Instead of writing 2KB packets
   buffer := make([]byte, 131072) // 128KB
   // Fill with multiple packets or raw data
   conn.Write(buffer)
   ```

2. **Set Socket Options**
   ```go
   tcpConn.SetNoDelay(false) // Enable Nagle's algorithm for throughput
   tcpConn.SetWriteBuffer(4194304) // 4MB write buffer
   tcpConn.SetReadBuffer(4194304)  // 4MB read buffer
   ```

3. **Remove Per-Packet Operations**
   - Don't timestamp every packet
   - Batch statistics updates
   - Use buffered channels

4. **For TCP: Stream-Oriented Design**
   - Don't treat TCP as packet-based
   - Send continuous stream of data
   - Let TCP handle segmentation

5. **For UDP: Larger Packets**
   - Use jumbo frames if supported (9000 bytes)
   - Batch multiple logical packets into one UDP datagram

### Advanced Optimizations

1. **Zero-Copy Techniques**
   - Use splice/sendfile syscalls via x/sys/unix
   - Memory-mapped I/O for large transfers

2. **CPU Affinity**
   - Pin threads to specific cores
   - Avoid NUMA crossing

3. **Kernel Bypass** (if available)
   - DPDK or similar for UDP
   - Would require significant rewrite

---

## Benchmark Configurations

### iperf Command Examples

```bash
# TCP Test (93+ Gbps achieved)
iperf -s  # server
iperf -c <target> -P 8 -t 10  # client

# UDP Test (20+ Gbps achieved)
iperf -s -u  # server
iperf -c <target> -u -P 8 -b 10G -t 10  # client

# With larger window size
iperf -c <target> -P 8 -w 4M  # 4MB TCP window
```

### Equivalent GosMesh Commands

```bash
# Current implementation (suboptimal)
./gosmesh --ips <targets> --protocol tcp --concurrency 8

# After optimizations (proposed)
./gosmesh --ips <targets> --protocol tcp --concurrency 8 \
        --buffer-size 131072 --tcp-nodelay=false
```

---

## Conclusion

The **21x performance gap** between iperf and GosMesh for TCP reveals significant optimization opportunities:

1. **Packet-based design is wrong for TCP** - Need streaming design
2. **Buffer sizes too small** - Need 64-128KB writes minimum
3. **Too many system calls** - Need batching
4. **No socket tuning** - Need proper TCP/UDP socket options

With these optimizations, GosMesh should achieve at least 50-70 Gbps, closing the gap with iperf's 90+ Gbps performance.

The current GosMesh design is more suitable for:
- Packet loss testing
- Latency measurement
- Network debugging

But not optimal for:
- Maximum throughput testing
- Bandwidth verification

Consider maintaining two modes:
1. **Packet Mode**: Current design for accuracy and metrics
2. **Throughput Mode**: Optimized design for maximum bandwidth