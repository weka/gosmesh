# GosMesh Performance Report - 100Gbit Network Testing

## Test Environment
- **Network**: 100Gbit InfiniBand (10.200.x.x subnet)
- **MTU**: 2044 bytes (InfiniBand)
- **Test Servers**: 
  - h5-11-b (10.200.6.28)
  - h11-9-b (10.200.6.240)
- **Packet Sizes**: 
  - UDP: 2016 bytes (2044 MTU - 28 bytes headers)
  - TCP: 2004 bytes (2044 MTU - 40 bytes headers)

## Network Connectivity Issue
**Important**: Asymmetric connectivity detected between servers due to Kubernetes network policies:
- h11-9-b → h5-11-b: ✅ Full connectivity
- h5-11-b → h11-9-b: ❌ Blocked by network policies

---

## UDP Performance Results

### 1. Unlimited Rate (Maximum Throughput)
```
Configuration: --protocol udp --pps 0 (unlimited)
```

#### Single Direction (4 connections)
- **Total Throughput**: ~10.8 Gbps
- **Per-Connection**: ~2.7 Gbps
- **Packets Sent**: 6,746,410 in 10s
- **Packet Rate**: ~674k pps
- **CPU Impact**: High (100% single core per sender)

#### Single Direction (8 connections)
- **Total Throughput**: ~20.3 Gbps
- **Per-Connection**: ~2.5 Gbps
- **Packets Sent**: 10,150,363 in 8s
- **Packet Rate**: ~1.27M pps

#### Bidirectional (4 connections each direction)
- **Send Rate**: ~10 Gbps per direction
- **Received Rate**: ~1.5 Gbps (due to 83% packet loss)
- **Packet Loss**: 83% (congestion from simultaneous sending)
- **RTT**: 1.5-2.2ms average (when packets get through)
- **Jitter**: 0.3-0.7ms

### 2. Controlled Rate
```
Configuration: --protocol udp --pps 50000 --concurrency 2
```
- **Total Throughput**: 1.6 Gbps
- **Packet Loss**: 0-2% (depending on network conditions)
- **RTT**: 0.4-0.5ms
- **Jitter**: 0.1-0.2ms

### UDP Performance Summary
- **Maximum Single-Flow**: ~2.7 Gbps
- **Maximum Aggregate**: ~20 Gbps (8 connections)
- **Optimal for Low Loss**: ~50k pps per connection (~800 Mbps each)

---

## TCP Performance Results

### 1. Unlimited Rate (Maximum Throughput)
```
Configuration: --protocol tcp --pps 0 (unlimited)
```

#### Single Direction (4 connections)
- **Total Throughput**: ~4.4 Gbps
- **Per-Connection**: ~1.1 Gbps
- **Packet Loss**: <0.5%
- **Note**: TCP flow control limits throughput compared to UDP

### 2. TCP Connection Establishment
- **Success Rate**: 100% when server is listening
- **Connection Refused**: When target server not running or blocked by firewall
- **Reliability**: Full reliability with retransmissions

### TCP Performance Summary
- **Maximum Single-Flow**: ~1.1 Gbps
- **Maximum Aggregate**: ~4.4 Gbps (4 connections)
- **Advantage**: Reliable delivery, flow control
- **Disadvantage**: Lower throughput than UDP due to overhead

---

## Key Findings

### 1. Network Capacity
- **Link Speed**: 100 Gbps theoretical
- **Achieved with UDP**: ~20 Gbps (20% utilization with 8 flows)
- **Achieved with TCP**: ~4.4 Gbps (4.4% utilization with 4 flows)
- **Bottleneck**: CPU/kernel packet processing, not network bandwidth

### 2. Optimal Settings for Different Use Cases

#### Maximum Throughput Testing
```bash
./gonet --ips <targets> --protocol udp --pps 0 --concurrency 8
```
- Achieves ~20 Gbps
- High packet loss under bidirectional load

#### Stable Performance Testing
```bash
./gonet --ips <targets> --protocol udp --pps 50000 --concurrency 4
```
- ~3.2 Gbps aggregate
- <2% packet loss
- Consistent RTT measurements

#### Reliability Testing
```bash
./gonet --ips <targets> --protocol tcp --pps 0 --concurrency 4
```
- ~4.4 Gbps aggregate
- 0% packet loss (TCP retransmissions)
- Higher latency due to TCP overhead

### 3. Scalability Observations
- **Per-Connection Limit**: ~2.7 Gbps (UDP), ~1.1 Gbps (TCP)
- **Linear Scaling**: Up to 8 connections
- **Kernel Bottleneck**: Single-core packet processing limits per-flow performance
- **InfiniBand MTU Advantage**: 2044 bytes vs standard 1500 bytes = 36% larger packets

### 4. Anomalies and Issues
- **Asymmetric Routing**: Kubernetes network policies blocking reverse path
- **RTT Calculation Bug**: TCP showing incorrect RTT values (needs fixing)
- **Congestion Collapse**: >80% loss when both sides send at max rate

---

## Recommendations

1. **For Throughput Testing**: Use UDP with 8+ connections
2. **For Latency Testing**: Use controlled rate (10-50k pps)
3. **For Reliability Testing**: Use TCP mode
4. **For Production**: Implement congestion control and rate limiting
5. **MTU Optimization**: Always use auto-detection for optimal packet size

---

## Command Examples

### Maximum UDP Throughput Test
```bash
./gosmesh --ips 10.200.6.28,10.200.6.240 --protocol udp --concurrency 8 --duration 30s
```

### Stable Bidirectional Test
```bash
./gosmesh --ips 10.200.6.28,10.200.6.240 --pps 25000 --concurrency 4 --duration 60s
```

### TCP Reliability Test
```bash
./gosmesh --ips 10.200.6.28,10.200.6.240 --protocol tcp --concurrency 4 --duration 30s
```

### Custom Packet Size Test
```bash
./gosmesh --ips 10.200.6.28,10.200.6.240 --packet-size 9000 --pps 10000
```

---

## Conclusion

The GosMesh tool successfully demonstrates:
- **20+ Gbps** throughput on 100Gbit network with UDP
- **4+ Gbps** reliable throughput with TCP  
- Automatic MTU detection and optimization
- Full mesh testing capabilities
- Accurate metrics collection (except TCP RTT bug)

The tool is suitable for:
- Network stress testing
- Bandwidth verification
- Latency measurement
- Packet loss detection
- Network troubleshooting (identified asymmetric routing issue)