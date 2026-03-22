// Package network provides low-level network testing primitives.
//
// This package contains the core components for network performance testing:
//   - Server: An echo server that receives and reflects test packets
//   - Connection: Represents a single test connection to a target
//   - ConnectionStats: Performance metrics for a connection
//
// Most users should use the pkg/testing package which provides higher-level
// orchestration via NetworkTester. Use this package directly only for
// advanced use cases where you need fine-grained control.
//
// Example of using Connection directly:
//
//	conn := network.NewConnection("192.168.1.1", "192.168.1.2", 9999, "tcp", 64000, 0, 0)
//	if err := conn.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	time.Sleep(30 * time.Second)
//	stats := conn.GetStats()
//	fmt.Printf("Throughput: %.2f Mbps\n", stats.ThroughputMbps)
package network
