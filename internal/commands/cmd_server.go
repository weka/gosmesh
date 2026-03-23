package commands

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

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
	if err := tester.StartHTTPServer(); err != nil {
		log.Fatalf("Failed to start control server: %v", err)
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigChan
	log.Printf("\n⚠️  Received signal: %v - shutting down...", sig)

	// Stop any running test
	if tester != nil {
		log.Printf("Stopping NetworkTester...")
		tester.Stop()
		log.Printf("Stopping API server")
		tester.StopHTTPServer()
	}

	log.Printf("Goodbye!")
}
