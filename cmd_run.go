package main

import (
	"flag"
	"fmt"
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

type RunConfig struct {
	IPs              string
	Protocol         string
	TotalConnections int
	Concurrency      int
	Duration         time.Duration
	ReportInterval   time.Duration
	PacketSize       int
	Port             int
	PPS              int
	ThroughputMode   bool
	BufferSize       int
	TCPNoDelay       bool
	UseOptimized     bool
	EnableIOUring    bool
	EnableHugePages  bool
	EnableOffload    bool
	SendBatchSize    int
	RecvBatchSize    int
	NumQueues        int
	BusyPollUsecs    int
	TCPCork          bool
	TCPQuickAck      bool
	MemArenaSize     int
	RingSize         int
	NumWorkers       int
	CPUList          string
	ReportTo         string // API endpoint to report stats to
}

func RunCommand(args []string) {
	config := &RunConfig{}
	fs := flag.NewFlagSet("run", flag.ExitOnError)

	fs.StringVar(&config.IPs, "ips", "", "Comma-separated list of IPs for full mesh testing")
	fs.StringVar(&config.Protocol, "protocol", "tcp", "Protocol to use: udp or tcp")
	fs.IntVar(&config.TotalConnections, "total-connections", 64, "Total connections each server establishes (distributed across all peers)")
	fs.IntVar(&config.Concurrency, "concurrency", 0, "Number of concurrent connections per target (0=auto-calculate)")
	fs.DurationVar(&config.Duration, "duration", 5*time.Minute, "Test duration (default: 5m)")
	fs.DurationVar(&config.ReportInterval, "report-interval", 5*time.Second, "Interval for periodic reports")
	fs.IntVar(&config.PacketSize, "packet-size", 0, "Size of test packets in bytes (0=auto-detect)")
	fs.IntVar(&config.Port, "port", 9999, "Port to use for testing")
	fs.IntVar(&config.PPS, "pps", 0, "Packets per second per connection (0=unlimited)")
	fs.BoolVar(&config.ThroughputMode, "throughput-mode", true, "Enable throughput mode optimizations")
	fs.IntVar(&config.BufferSize, "buffer-size", 0, "Buffer size for throughput mode (0=auto)")
	fs.BoolVar(&config.TCPNoDelay, "tcp-nodelay", false, "Enable TCP_NODELAY")
	fs.BoolVar(&config.UseOptimized, "optimized", true, "Use optimized connections")
	fs.BoolVar(&config.EnableIOUring, "io-uring", true, "Enable io_uring on Linux")
	fs.BoolVar(&config.EnableHugePages, "huge-pages", true, "Enable huge pages")
	fs.BoolVar(&config.EnableOffload, "hw-offload", true, "Enable hardware offloading")
	fs.IntVar(&config.SendBatchSize, "send-batch-size", 64, "Send batch size")
	fs.IntVar(&config.RecvBatchSize, "recv-batch-size", 64, "Receive batch size")
	fs.IntVar(&config.NumQueues, "num-queues", 0, "Number of queues (0=auto)")
	fs.IntVar(&config.BusyPollUsecs, "busy-poll-usecs", 50, "Busy polling microseconds")
	fs.BoolVar(&config.TCPCork, "tcp-cork", false, "Enable TCP_CORK")
	fs.BoolVar(&config.TCPQuickAck, "tcp-quickack", false, "Enable TCP_QUICKACK")
	fs.IntVar(&config.MemArenaSize, "memory-arena-size", 268435456, "Memory arena size (default: 256MB)")
	fs.IntVar(&config.RingSize, "ring-size", 4096, "Ring buffer size")
	fs.IntVar(&config.NumWorkers, "num-workers", 0, "Number of workers (0=auto)")
	fs.StringVar(&config.CPUList, "cpu-list", "", "CPU affinity list")
	fs.StringVar(&config.ReportTo, "report-to", "", "API endpoint to report statistics to")

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

func runWithConfig(config *RunConfig) {
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

	// Calculate concurrency if not specified
	if config.Concurrency == 0 {
		numTargets := len(ipList) - 1  // Number of other servers to connect to
		if numTargets > 0 {
			// Distribute total connections across all targets
			config.Concurrency = config.TotalConnections / numTargets
			
			// Ensure minimum of 2 connections per target
			minConnectionsPerTarget := 2
			if config.Concurrency < minConnectionsPerTarget {
				config.Concurrency = minConnectionsPerTarget
				actualTotal := config.Concurrency * numTargets
				log.Printf("Increasing total connections from %d to %d to maintain minimum %d connections per target", 
					config.TotalConnections, actualTotal, minConnectionsPerTarget)
				config.TotalConnections = actualTotal
			}
			
			log.Printf("Configuration: %d total connections, %d targets, %d connections per target", 
				config.TotalConnections, numTargets, config.Concurrency)
		} else {
			// Single node (loopback testing)
			config.Concurrency = config.TotalConnections
			log.Printf("Single node configuration: %d connections", config.Concurrency)
		}
	}

	// Auto-detect packet size
	if config.PacketSize == 0 {
		mtu, err := GetMTU(localIP)
		if err != nil {
			mtu = 9000
		}
		if mtu >= 9000 {
			if config.Protocol == "udp" {
				config.PacketSize = 8972
			} else {
				config.PacketSize = mtu - 40
			}
		} else {
			config.PacketSize = CalculateOptimalPacketSize(mtu, config.Protocol)
		}
	}

	log.Printf("Starting gosmesh run mode")
	log.Printf("Local IP: %s", localIP)
	log.Printf("Protocol: %s", config.Protocol)
	log.Printf("Duration: %v", config.Duration)

	// Auto-enable throughput mode
	if config.PPS == 0 {
		config.ThroughputMode = true
	}

	if config.ThroughputMode && config.BufferSize == 0 {
		if config.Protocol == "tcp" {
			config.BufferSize = 4194304 // 4MB
		} else {
			config.BufferSize = 1048576 // 1MB
		}
	}

	tester := NewNetworkTester(localIP, ipList, config.Protocol, config.Concurrency,
		config.Duration, config.ReportInterval, config.PacketSize, config.Port, config.PPS)

	// Configure tester with all settings
	tester.UseOptimized = config.UseOptimized
	tester.EnableIOUring = config.EnableIOUring
	tester.EnableHugePages = config.EnableHugePages
	tester.EnableOffload = config.EnableOffload
	tester.BufferSize = config.BufferSize
	tester.SendBatchSize = config.SendBatchSize
	tester.RecvBatchSize = config.RecvBatchSize
	tester.NumQueues = config.NumQueues
	tester.BusyPollUsecs = config.BusyPollUsecs
	tester.TCPCork = config.TCPCork
	tester.TCPQuickAck = config.TCPQuickAck
	tester.TCPNoDelay = config.TCPNoDelay
	tester.MemArenaSize = config.MemArenaSize
	tester.RingSize = config.RingSize
	tester.NumWorkers = config.NumWorkers
	tester.CPUList = config.CPUList

	// Set up API reporting if configured
	if config.ReportTo != "" {
		log.Printf("Reporting stats to: %s", config.ReportTo)
		tester.EnableAPIReporting(config.ReportTo, localIP)
	}

	// Signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received interrupt signal")
		tester.Stop()
	}()

	// Start test
	if err := tester.Start(); err != nil {
		log.Fatalf("Failed to start test: %v", err)
	}

	tester.Wait()

	// Generate final report
	report := tester.GenerateFinalReport()
	fmt.Println(report)
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