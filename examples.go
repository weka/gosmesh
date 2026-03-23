package main

import (
	"fmt"
	"log"
	"time"

	"github.com/weka/gosmesh/pkg/testing"
)

// ExampleBasicTest demonstrates the simplest way to use GoSmesh as a library.
func ExampleBasicTest() {
	// Create a new network tester
	tester := testing.NewNetworkTester(
		"192.168.1.1",                          // local IP to bind to
		[]string{"192.168.1.2", "192.168.1.3"}, // target IPs
		"tcp",                                  // protocol: "tcp" or "udp"
		2,                                      // connections per target
		5*time.Minute,                          // test duration
		5*time.Second,                          // report interval
		64000,                                  // packet size in bytes
		9999,                                   // port
		0,                                      // pps: 0 = throughput mode
		12345,                                  // apiServer on port
	)

	// Start the test (non-blocking)
	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	// Wait for the test to complete
	tester.Wait()

	// Get the final report
	report := tester.GenerateFinalReport()
	fmt.Println(report)
}

// ExampleHighPerformance demonstrates how to configure GoSmesh for maximum throughput.
func ExampleHighPerformance() {
	tester := testing.NewNetworkTester(
		"192.168.1.1",
		[]string{"192.168.1.2"},
		"tcp",
		4, // higher concurrency
		2*time.Minute,
		10*time.Second,
		9000, // jumbo frame size
		9999,
		0,
		0, // do not start API server
	)

	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	tester.Wait()

	report := tester.GenerateFinalReport()
	fmt.Println(report)
}

// ExamplePacketMode demonstrates packet mode for measuring latency and packet loss.
func ExamplePacketMode() {
	tester := testing.NewNetworkTester(
		"192.168.1.1",
		[]string{"192.168.1.2"},
		"udp", // Use UDP for packet loss measurement
		2,
		1*time.Minute,
		5*time.Second,
		1024, // smaller packets for packet mode
		9999,
		100,   // 100 packets per second (packet mode)
		12345, // apiServer on port

	)

	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	tester.Wait()

	report := tester.GenerateFinalReport()
	fmt.Println(report)
}

// ExampleWithAPIReporting demonstrates how to send stats to an API endpoint.
func ExampleWithAPIReporting() {
	tester := testing.NewNetworkTester(
		"192.168.1.1",
		[]string{"192.168.1.2", "192.168.1.3"},
		"tcp",
		2,
		5*time.Minute,
		5*time.Second,
		64000,
		9999,
		0,
		12345, // apiServer on port

	)

	// Enable API reporting
	tester.EnableAPIReporting("http://monitoring.example.com/api/stats", "192.168.1.1")

	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	tester.Wait()

	report := tester.GenerateFinalReport()
	fmt.Println(report)
}

// ExampleEarlyStop demonstrates how to stop a test early.
func ExampleEarlyStop() {
	tester := testing.NewNetworkTester(
		"192.168.1.1",
		[]string{"192.168.1.2"},
		"tcp",
		2,
		30*time.Minute, // long duration
		5*time.Second,
		64000,
		9999,
		0,
		12345, // apiServer on port

	)

	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	// Let it run for 30 seconds, then stop early
	time.Sleep(30 * time.Second)
	tester.StopAndFinalize()

	// Wait for graceful shutdown
	tester.Wait()

	report := tester.GenerateFinalReport()
	fmt.Println(report)
}

// ExampleCustomPort demonstrates using a custom port.
func ExampleCustomPort() {
	tester := testing.NewNetworkTester(
		"192.168.1.1",
		[]string{"192.168.1.2"},
		"tcp",
		2,
		5*time.Minute,
		5*time.Second,
		64000,
		8888, // custom port instead of default 9999
		0,
		12345, // apiServer on port
	)

	if err := tester.Start(); err != nil {
		log.Fatal(err)
	}

	tester.Wait()
	fmt.Println(tester.GenerateFinalReport())
}
