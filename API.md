/*
Package gosmesh provides a high-performance network testing library for measuring
network performance metrics in a full mesh topology.

GoSmesh can be used both as a standalone CLI tool and as a Go library for programmatic
access to network testing capabilities. It supports both UDP and TCP protocols and
provides comprehensive metrics including throughput, packet loss, latency, and jitter.

Basic Usage:

To use gosmesh as a library, import the testing package:

	import "github.com/weka/gosmesh/pkg/testing"

Create a new tester:

	tester := testing.NewNetworkTester(
	    "192.168.1.1",                          // local IP to bind to
	    []string{"192.168.1.2", "192.168.1.3"}, // target IPs to test
	    "tcp",                                   // protocol: "tcp" or "udp"
	    2,                                       // concurrency: connections per target
	    5*time.Minute,                          // duration: how long to test
	    5*time.Second,                          // reportInterval: how often to report
	    64000,                                   // packetSize: bytes per packet
	    9999,                                    // port: testing port
	    0,                                       // pps: packets/sec per conn (0=throughput mode)
	)

Start the test:

	if err := tester.Start(); err != nil {
	    log.Fatal(err)
	}

The test runs asynchronously. You can:
	- Call tester.Wait() to block until completion
	- Call tester.Stop() to stop early
	- Set tester.UseOptimized = true for high-performance mode

Get the final report:

	tester.Stop()
	report := tester.GenerateFinalReport()
	fmt.Println(report)

Advanced Configuration:

After creating a tester but before calling Start(), configure performance tuning:

	tester.UseOptimized = true        // Enable optimized high-performance mode
	tester.BufferSize = 4194304       // 4MB buffers for throughput
	tester.TCPNoDelay = true          // Disable Nagle's algorithm
	tester.NumWorkers = 4             // Worker threads
	tester.SendBatchSize = 64         // Batch multiple packets
	tester.RecvBatchSize = 64

API Reporting:

Enable reporting to an HTTP endpoint:

	tester.EnableAPIReporting("http://api.example.com/stats", "192.168.1.1")
	if err := tester.Start(); err != nil {
	    log.Fatal(err)
	}

Metrics Explanation:

Depending on the mode and protocol:

Throughput Mode (pps=0):
  - Throughput: Measured in Mbps (or Gbps)
  - RTT/Jitter: Not measured (use packet mode instead)
  - Packet Loss: Not measured for TCP (use UDP or packet mode)

Packet Mode (pps>0):
  - Throughput: Measured in Mbps
  - RTT: Round-trip time in milliseconds
  - Jitter: Standard deviation of RTT
  - Packet Loss: Percentage of packets not echoed back

Ports and Protocols:

- Default port: 9999 (must be open/not blocked by firewall)
- TCP: Connection-based, reliable, better for throughput
- UDP: Connectionless, lower latency, supports loss measurement

Performance Tips:

1. For 100Gbps networks, use: concurrency=64, packetSize=9000, protocol="tcp"
2. Enable jumbo frames on network interfaces for better throughput
3. Use UseOptimized=true for maximum performance
4. Monitor CPU usage - may need more workers if CPU-bound
5. Ensure adequate buffer sizes for your network speed

See the pkg/testing package for detailed type documentation.
*/
package main

