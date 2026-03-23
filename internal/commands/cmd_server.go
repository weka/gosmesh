package commands

import (
	"flag"
	"log"
	"runtime"
	"runtime/debug"

	"github.com/weka/gosmesh/pkg/testing"
)

func ServerCommand(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	var port int
	var apiPort int
	fs.IntVar(&apiPort, "api-port", 44444, "Port for test control and results API")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	// Runtime optimizations
	runtime.GOMAXPROCS(runtime.NumCPU())
	debug.SetGCPercent(200)

	log.Printf("Starting gosmesh server on port %d", port)

	// Create a NetworkTester that will be reconfigured for each test request
	tester := testing.NewNetworkTester(
		"0.0.0.0",             // Will be updated per test
		[]string{"127.0.0.1"}, // Will be updated per test
		"tcp",                 // Will be updated per test
		2,                     // Will be updated per test
		0,                     // Will be updated per test
		0,                     // Will be updated per test
		0,                     // Will be updated per test
		9999,                  // Will be updated per test
		0,                     // Will be updated per test
		apiPort,
	)

	// Start the control server
	if err := tester.StartHTTPServer(port); err != nil {
		log.Fatalf("Failed to start control server: %v", err)
	}

	// Keep the process alive
	select {}
}
