package commands

import (
	"flag"
	"fmt"
	"github.com/weka/gosmesh/pkg/mesh"
	"github.com/weka/gosmesh/pkg/testing"
	"log"
	"os"
)

func MeshCommand(args []string) {
	// Start with default configuration
	defaultConfig := testing.GetDefaultTestConfig()

	config := &mesh.Config{
		TestConfig: defaultConfig,
	}
	fs := flag.NewFlagSet("mesh", flag.ExitOnError)

	IPs, SSHHosts := "", ""

	// Mesh-specific configuration
	fs.StringVar(&IPs, "ips", "", "Comma-separated list of IPs for mesh deployment")
	fs.StringVar(&SSHHosts, "ssh-hosts", "", "Optional: SSH hosts (user@host1,user@host2,...)")
	fs.IntVar(&config.APIPort, "api-port", 8080, "Port for API server")
	fs.BoolVar(&config.Verbose, "verbose", false, "Enable verbose logging")
	fs.BoolVar(&config.DeploySystemDService, "dont-use-systemd", true, "Skip deployment of service to hosts, if deployed externally")

	// TestConfig fields - all have defaults from GetDefaultTestConfig()
	fs.DurationVar(&config.TestConfig.Duration, "duration", defaultConfig.Duration, "Test duration")
	fs.IntVar(&config.TestConfig.Port, "port", defaultConfig.Port, "Port for gosmesh testing")
	fs.StringVar(&config.TestConfig.Protocol, "protocol", defaultConfig.Protocol, "Protocol: udp or tcp")
	fs.IntVar(&config.TestConfig.TotalConnections, "total-connections", defaultConfig.TotalConnections, "Total connections per server")
	fs.IntVar(&config.TestConfig.Concurrency, "concurrency", defaultConfig.Concurrency, "Connections per target (0=auto)")
	fs.DurationVar(&config.TestConfig.ReportInterval, "report-interval", defaultConfig.ReportInterval, "Report interval")
	fs.IntVar(&config.TestConfig.PacketSize, "packet-size", defaultConfig.PacketSize, "Packet size (0=auto)")
	fs.IntVar(&config.TestConfig.PPS, "pps", defaultConfig.PPS, "Packets per second (0=unlimited)")
	fs.BoolVar(&config.TestConfig.ThroughputMode, "throughput-mode", defaultConfig.ThroughputMode, "Enable throughput mode optimizations")
	fs.IntVar(&config.TestConfig.BufferSize, "buffer-size", defaultConfig.BufferSize, "Buffer size (0=auto)")
	fs.BoolVar(&config.TestConfig.TCPNoDelay, "tcp-nodelay", defaultConfig.TCPNoDelay, "Enable TCP_NODELAY")
	fs.BoolVar(&config.TestConfig.UseOptimized, "optimized", defaultConfig.UseOptimized, "Use optimized connections")
	fs.BoolVar(&config.TestConfig.EnableIOUring, "io-uring", defaultConfig.EnableIOUring, "Enable io_uring")
	fs.BoolVar(&config.TestConfig.EnableHugePages, "huge-pages", defaultConfig.EnableHugePages, "Enable huge pages")
	fs.BoolVar(&config.TestConfig.EnableOffload, "hw-offload", defaultConfig.EnableOffload, "Enable hardware offload")
	fs.IntVar(&config.TestConfig.SendBatchSize, "send-batch-size", defaultConfig.SendBatchSize, "Send batch size")
	fs.IntVar(&config.TestConfig.RecvBatchSize, "recv-batch-size", defaultConfig.RecvBatchSize, "Receive batch size")
	fs.IntVar(&config.TestConfig.NumQueues, "num-queues", defaultConfig.NumQueues, "Number of queues (0=auto)")
	fs.IntVar(&config.TestConfig.BusyPollUsecs, "busy-poll-usecs", defaultConfig.BusyPollUsecs, "Busy polling microseconds")
	fs.BoolVar(&config.TestConfig.TCPCork, "tcp-cork", defaultConfig.TCPCork, "Enable TCP_CORK")
	fs.BoolVar(&config.TestConfig.TCPQuickAck, "tcp-quickack", defaultConfig.TCPQuickAck, "Enable TCP_QUICKACK")
	fs.IntVar(&config.TestConfig.MemArenaSize, "memory-arena-size", defaultConfig.MemArenaSize, "Memory arena size")
	fs.IntVar(&config.TestConfig.RingSize, "ring-size", defaultConfig.RingSize, "Ring buffer size")
	fs.IntVar(&config.TestConfig.NumWorkers, "num-workers", defaultConfig.NumWorkers, "Number of workers (0=auto)")
	fs.StringVar(&config.TestConfig.CPUList, "cpu-list", defaultConfig.CPUList, "CPU affinity list")
	fs.StringVar(&config.TestConfig.ReportTo, "report-to", defaultConfig.ReportTo, "API endpoint to report statistics to")
	fs.IntVar(&config.TestConfig.ApiServerPort, "worker-api-port", defaultConfig.ApiServerPort, "If set, will attempt to start API server on workers")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if IPs == "" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: gosmesh mesh --ips ip1,ip2,ip3 [options]\n")
		_, _ = fmt.Fprintf(os.Stderr, "\nExample:\n")
		_, _ = fmt.Fprintf(os.Stderr, "  gosmesh mesh --ips 10.0.0.1,10.0.0.2,10.0.0.3 --duration 120s\n\n")
		fs.PrintDefaults()
		os.Exit(1)
	}

	// parse IPs and SSH hosts, and fill them in into configuration
	if err := config.FillTargets(IPs, SSHHosts); err != nil {
		log.Fatal(err)
	}

	controller := mesh.NewController(config)
	controller.Run()
}
