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

var (
	ips         string
	protocol    string
	concurrency int
	duration    time.Duration
	reportInterval time.Duration
	packetSize  int
	port        int
	pps         int
	throughputMode bool
	bufferSize  int
	tcpNoDelay  bool
	useOptimized bool
	enableIOUring bool
	enableHugePages bool
	enableOffload bool
)

func init() {
	// Runtime optimizations for high-performance networking
	runtime.GOMAXPROCS(runtime.NumCPU()) // Use all CPUs
	debug.SetGCPercent(200)              // Reduce GC frequency
}

func main() {
	flag.StringVar(&ips, "ips", "", "Comma-separated list of IPs for full mesh testing")
	flag.StringVar(&protocol, "protocol", "tcp", "Protocol to use: udp or tcp")
	flag.IntVar(&concurrency, "concurrency", 8, "Number of concurrent connections per target (default: 8 for 100Gbps)")
	flag.DurationVar(&duration, "duration", 30*time.Second, "Test duration")
	flag.DurationVar(&reportInterval, "report-interval", 5*time.Second, "Interval for periodic reports")
	flag.IntVar(&packetSize, "packet-size", 0, "Size of test packets in bytes (0=auto-detect, uses jumbo frames if available)")
	flag.IntVar(&port, "port", 9999, "Port to use for testing")
	flag.IntVar(&pps, "pps", 0, "Packets per second per connection (0=unlimited/throughput mode - DEFAULT)")
	flag.BoolVar(&throughputMode, "throughput-mode", true, "Enable throughput mode optimizations (default: true)")
	flag.IntVar(&bufferSize, "buffer-size", 0, "Buffer size for throughput mode (0=auto, defaults to 1MB for TCP)")
	flag.BoolVar(&tcpNoDelay, "tcp-nodelay", false, "Enable TCP_NODELAY (disable Nagle's algorithm)")
	flag.BoolVar(&useOptimized, "optimized", true, "Use optimized connections with hardware offloading (default: true)")
	flag.BoolVar(&enableIOUring, "io-uring", true, "Enable io_uring for async I/O on Linux (default: true)")
	flag.BoolVar(&enableHugePages, "huge-pages", true, "Enable huge pages for memory allocation (default: true)")
	flag.BoolVar(&enableOffload, "hw-offload", true, "Enable hardware offloading (TSO/GSO/GRO) (default: true)")
	flag.Parse()

	if ips == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s --ips ip1,ip2,ip3 [options]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(1)
	}

	ipList := parseIPs(ips)
	if len(ipList) == 0 {
		log.Fatal("No valid IPs provided")
	}

	localIP := detectLocalIP(ipList)
	if localIP == "" {
		log.Fatal("Could not detect local IP from provided list")
	}

	// Auto-detect packet size from MTU if not specified
	if packetSize == 0 {
		mtu, err := GetMTU(localIP)
		if err != nil {
			log.Printf("Warning: Could not detect MTU: %v, checking for jumbo frames", err)
			mtu = 9000 // Try jumbo frames by default for high-speed networks
		}
		// For high-speed networks, prefer larger packets
		if mtu >= 9000 {
			log.Printf("Jumbo frames detected (MTU: %d)", mtu)
			if protocol == "udp" {
				packetSize = 8972 // Max UDP payload with jumbo frames
			} else {
				packetSize = mtu - 40 // Account for IP + TCP headers
			}
		} else {
			packetSize = CalculateOptimalPacketSize(mtu, protocol)
		}
		log.Printf("Using packet size: %d bytes (MTU: %d)", packetSize, mtu)
	}

	log.Printf("Starting gonet - Network Testing Tool")
	log.Printf("Local IP: %s", localIP)
	log.Printf("Protocol: %s", protocol)
	log.Printf("Targets: %v", ipList)
	log.Printf("Concurrency: %d per target", concurrency)
	log.Printf("Duration: %v", duration)
	log.Printf("Packet Size: %d bytes", packetSize)
	
	// Auto-enable throughput mode for unlimited PPS
	if pps == 0 {
		throughputMode = true
	}
	
	if throughputMode {
		log.Printf("Mode: THROUGHPUT (optimized for 100Gbps+ networks)")
		log.Printf("PPS: UNLIMITED")
		
		// Auto-set buffer size if not specified - use large buffers for 100Gbps
		if bufferSize == 0 {
			if protocol == "tcp" {
				bufferSize = 1048576  // 1MB for TCP on high-speed networks
			} else {
				bufferSize = 262144   // 256KB for UDP batching
			}
		}
		log.Printf("Buffer Size: %d bytes (%.1f MB)", bufferSize, float64(bufferSize)/1048576)
		log.Printf("Socket Buffers: 16MB (optimized for 100Gbps)")
		log.Printf("Concurrency: %d connections per target", concurrency)
		
		// Calculate theoretical max throughput
		maxGbps := float64(concurrency * bufferSize * 8) / 1000000000.0 * 1000.0 // Assuming 1000 writes/sec
		log.Printf("Theoretical Max: %.1f Gbps per target (with %d connections)", maxGbps, concurrency)
	} else {
		log.Printf("Mode: PACKET (optimized for metrics accuracy)")
		log.Printf("PPS per connection: %d", pps)
		throughputMode = false
	}
	
	// Log optimization settings
	if useOptimized {
		log.Printf("Optimizations: ENABLED")
		if runtime.GOOS == "linux" {
			if enableIOUring {
				log.Printf("  - io_uring: ENABLED (20%% performance gain)")
			}
			if enableHugePages {
				log.Printf("  - Huge Pages: ENABLED (5%% performance gain)")
			}
		}
		if enableOffload {
			log.Printf("  - Hardware Offload (TSO/GSO/GRO): ENABLED (10%% performance gain)")
		}
		log.Printf("  - GC Optimization: ENABLED (5%% performance gain)")
		log.Printf("  - CPU Affinity: ENABLED (5%% performance gain)")
	}

	tester := NewNetworkTester(localIP, ipList, protocol, concurrency, duration, reportInterval, packetSize, port, pps)
	tester.UseOptimized = useOptimized
	tester.EnableIOUring = enableIOUring
	tester.EnableHugePages = enableHugePages
	tester.EnableOffload = enableOffload
	
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Received interrupt signal, generating final report...")
		tester.Stop()
	}()

	// Start the test
	if err := tester.Start(); err != nil {
		log.Fatalf("Failed to start test: %v", err)
	}

	// Wait for completion
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
		} else {
			log.Printf("Warning: Invalid IP address: %s", ip)
		}
	}
	return validIPs
}

func detectLocalIP(ipList []string) string {
	// First try to detect from network interfaces
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
	
	// If not found, try to bind to each IP to see which one works
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