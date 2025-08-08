package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
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
)

func main() {
	flag.StringVar(&ips, "ips", "", "Comma-separated list of IPs for full mesh testing")
	flag.StringVar(&protocol, "protocol", "udp", "Protocol to use: udp or tcp")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of concurrent connections per target")
	flag.DurationVar(&duration, "duration", 60*time.Second, "Test duration")
	flag.DurationVar(&reportInterval, "report-interval", 10*time.Second, "Interval for periodic reports")
	flag.IntVar(&packetSize, "packet-size", 1024, "Size of test packets in bytes")
	flag.IntVar(&port, "port", 9999, "Port to use for testing")
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

	log.Printf("Starting gonet - Network Testing Tool")
	log.Printf("Local IP: %s", localIP)
	log.Printf("Protocol: %s", protocol)
	log.Printf("Targets: %v", ipList)
	log.Printf("Concurrency: %d per target", concurrency)
	log.Printf("Duration: %v", duration)
	log.Printf("Packet Size: %d bytes", packetSize)

	tester := NewNetworkTester(localIP, ipList, protocol, concurrency, duration, reportInterval, packetSize, port)
	
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