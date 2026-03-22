package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/weka/gosmesh/pkg/mesh"
)

func MeshCommand(args []string) {
	config := &mesh.Config{}
	fs := flag.NewFlagSet("mesh", flag.ExitOnError)

	fs.StringVar(&config.IPs, "ips", "", "Comma-separated list of IPs for mesh deployment")
	fs.StringVar(&config.SSHHosts, "ssh-hosts", "", "Optional: SSH hosts (user@host1,user@host2,...)")
	fs.DurationVar(&config.Duration, "duration", 5*time.Minute, "Test duration")
	fs.IntVar(&config.Port, "port", 9999, "Port for gosmesh testing")
	fs.IntVar(&config.APIPort, "api-port", 8080, "Port for API server")
	fs.StringVar(&config.Protocol, "protocol", "tcp", "Protocol: udp or tcp")
	fs.BoolVar(&config.Verbose, "verbose", false, "Enable verbose logging")
	fs.IntVar(&config.TotalConnections, "total-connections", 64, "Total connections per server")
	fs.IntVar(&config.Concurrency, "concurrency", 0, "Connections per target (0=auto)")
	fs.DurationVar(&config.ReportInterval, "report-interval", 5*time.Second, "Report interval")
	fs.IntVar(&config.PacketSize, "packet-size", 0, "Packet size (0=auto)")
	fs.IntVar(&config.PPS, "pps", 0, "Packets per second (0=unlimited)")
	fs.BoolVar(&config.ThroughputMode, "throughput-mode", true, "Enable throughput mode optimizations")
	fs.IntVar(&config.BufferSize, "buffer-size", 0, "Buffer size (0=auto)")
	fs.BoolVar(&config.TCPNoDelay, "tcp-nodelay", false, "Enable TCP_NODELAY")
	fs.BoolVar(&config.UseOptimized, "optimized", true, "Use optimized connections")
	fs.BoolVar(&config.EnableIOUring, "io-uring", true, "Enable io_uring")
	fs.BoolVar(&config.EnableHugePages, "huge-pages", true, "Enable huge pages")
	fs.BoolVar(&config.EnableOffload, "hw-offload", true, "Enable hardware offload")
	fs.IntVar(&config.SendBatchSize, "send-batch-size", 64, "Send batch size")
	fs.IntVar(&config.RecvBatchSize, "recv-batch-size", 64, "Receive batch size")
	fs.IntVar(&config.NumQueues, "num-queues", 0, "Number of queues (0=auto)")
	fs.IntVar(&config.BusyPollUsecs, "busy-poll-usecs", 50, "Busy polling microseconds")
	fs.BoolVar(&config.TCPCork, "tcp-cork", false, "Enable TCP_CORK")
	fs.BoolVar(&config.TCPQuickAck, "tcp-quickack", false, "Enable TCP_QUICKACK")
	fs.IntVar(&config.MemArenaSize, "memory-arena-size", 268435456, "Memory arena size")
	fs.IntVar(&config.RingSize, "ring-size", 4096, "Ring buffer size")
	fs.IntVar(&config.NumWorkers, "num-workers", 0, "Number of workers (0=auto)")
	fs.StringVar(&config.CPUList, "cpu-list", "", "CPU affinity list")
	fs.StringVar(&config.ReportTo, "report-to", "", "API endpoint to report statistics to")
	fs.BoolVar(&config.DeploySystemDService, "dont-use-systemd", true, "Skip deployment of service to hosts, if deployed externally")
	fs.IntVar(&config.WorkerAPIPort, "worker-api-port", 0, "If set, will attempt to start API server on workers")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if config.IPs == "" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: gosmesh mesh --ips ip1,ip2,ip3 [options]\n")
		_, _ = fmt.Fprintf(os.Stderr, "\nExample:\n")
		_, _ = fmt.Fprintf(os.Stderr, "  gosmesh mesh --ips 10.0.0.1,10.0.0.2,10.0.0.3 --duration 120s\n\n")
		fs.PrintDefaults()
		os.Exit(1)
	}

	controller := mesh.NewController(config)
	controller.Run()
}
