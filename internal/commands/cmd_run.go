package commands

import (
	"flag"
	"fmt"
	"github.com/weka/gosmesh/pkg/testing"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

func RunCommand(args []string) {
	config := testing.GetDefaultTestConfig()
	fs := flag.NewFlagSet("run", flag.ExitOnError)

	fs.StringVar(&config.IPs, "ips", config.IPs, "Comma-separated list of IPs for full mesh testing")
	fs.StringVar(&config.Protocol, "protocol", config.Protocol, "Protocol to use: udp or tcp")
	fs.IntVar(&config.TotalConnections, "total-connections", config.TotalConnections, "Total connections each server establishes (distributed across all peers)")
	fs.IntVar(&config.Concurrency, "concurrency", config.Concurrency, "Number of concurrent connections per target (0=auto-calculate)")
	fs.DurationVar(&config.Duration, "duration", config.Duration, "Test duration (default: 5m)")
	fs.DurationVar(&config.ReportInterval, "report-interval", config.ReportInterval, "Interval for periodic reports")
	fs.IntVar(&config.PacketSize, "packet-size", config.PacketSize, "Size of test packets in bytes (0=auto-detect)")
	fs.IntVar(&config.Port, "port", config.Port, "Port to use for testing")
	fs.IntVar(&config.ApiServerPort, "api-server-port", config.ApiServerPort, "If set, will start API server on this port to get stats")
	fs.IntVar(&config.PPS, "pps", config.PPS, "Packets per second per connection (0=unlimited)")
	fs.BoolVar(&config.ThroughputMode, "throughput-mode", config.ThroughputMode, "Enable throughput mode optimizations")
	fs.IntVar(&config.BufferSize, "buffer-size", config.BufferSize, "Buffer size for throughput mode (0=auto)")
	fs.BoolVar(&config.TCPNoDelay, "tcp-nodelay", config.TCPNoDelay, "Enable TCP_NODELAY")
	fs.BoolVar(&config.UseOptimized, "optimized", config.UseOptimized, "Use optimized connections")
	fs.BoolVar(&config.EnableIOUring, "io-uring", config.EnableIOUring, "Enable io_uring on Linux")
	fs.BoolVar(&config.EnableHugePages, "huge-pages", config.EnableHugePages, "Enable huge pages")
	fs.BoolVar(&config.EnableOffload, "hw-offload", config.EnableOffload, "Enable hardware offloading")
	fs.IntVar(&config.SendBatchSize, "send-batch-size", config.SendBatchSize, "Send batch size")
	fs.IntVar(&config.RecvBatchSize, "recv-batch-size", config.RecvBatchSize, "Receive batch size")
	fs.IntVar(&config.NumQueues, "num-queues", config.NumQueues, "Number of queues (0=auto)")
	fs.IntVar(&config.BusyPollUsecs, "busy-poll-usecs", config.BusyPollUsecs, "Busy polling microseconds")
	fs.BoolVar(&config.TCPCork, "tcp-cork", config.TCPCork, "Enable TCP_CORK")
	fs.BoolVar(&config.TCPQuickAck, "tcp-quickack", config.TCPQuickAck, "Enable TCP_QUICKACK")
	fs.IntVar(&config.MemArenaSize, "memory-arena-size", config.MemArenaSize, "Memory arena size (default: 256MB)")
	fs.IntVar(&config.RingSize, "ring-size", config.RingSize, "Ring buffer size")
	fs.IntVar(&config.NumWorkers, "num-workers", config.NumWorkers, "Number of workers (0=auto)")
	fs.StringVar(&config.CPUList, "cpu-list", config.CPUList, "CPU affinity list")
	fs.StringVar(&config.ReportTo, "report-to", config.ReportTo, "API endpoint to report statistics to")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if config.IPs == "" {
		fmt.Fprintf(os.Stderr, "Usage: gosmesh run --ips ip1,ip2,ip3 [options]\n")
		fs.PrintDefaults()
		os.Exit(1)
	}

	runWithConfig(config)
}

func runWithConfig(config *testing.TestConfig) {
	// Runtime optimizations
	runtime.GOMAXPROCS(runtime.NumCPU())
	debug.SetGCPercent(200)

	ipList := parseIPs(config.IPs)
	if len(ipList) == 0 {
		log.Fatal("No valid IPs provided")
	}

	localIP := detectLocalIP(ipList)
	if localIP == "" {
		log.Fatal("Could not detect local IP from provided list")
	}

	log.Printf("Starting gosmesh run mode")
	log.Printf("Local IP: %s", localIP)
	log.Printf("Protocol: %s", config.Protocol)
	log.Printf("Duration: %v", config.Duration)

	// Create tester (will auto-calculate settings via StartWithConfig)
	tester := testing.NewNetworkTester("", []string{}, "tcp", 0, 0, 0, 0, 0, 0, config.ApiServerPort)

	// Signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start test with config - this will handle all auto-calculations
	if err := tester.StartWithConfig(config); err != nil {
		log.Fatalf("Failed to start test: %v", err)
	}

	// Wait for test completion, monitoring for signals
	select {
	case <-tester.GetTestDone():
		// Test completed normally
		log.Printf("Test completed successfully")
	case sig := <-sigChan:
		// User interrupted - force stop
		log.Printf("\n⚠️  Received signal: %v - forcing exit...", sig)
		tester.StopAndFinalize()
		// Wait briefly for cleanup
		select {
		case <-tester.GetTestDone():
		case <-time.After(2 * time.Second):
			log.Printf("Timeout waiting for test cleanup")
		}
	}

}

func parseIPs(ipStr string) []string {
	parts := strings.Split(ipStr, ",")
	var validIPs []string
	for _, ip := range parts {
		ip = strings.TrimSpace(ip)
		if net.ParseIP(ip) != nil {
			validIPs = append(validIPs, ip)
		}
	}
	return validIPs
}

func detectLocalIP(ipList []string) string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil {
				for _, targetIP := range ipList {
					if ip.String() == targetIP {
						return targetIP
					}
				}
			}
		}
	}

	// Try binding to each IP
	for _, ip := range ipList {
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:0", ip))
		if err != nil {
			continue
		}
		conn, err := net.ListenUDP("udp", addr)
		if err == nil {
			conn.Close()
			return ip
		}
	}

	return ""
}
