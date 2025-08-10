package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type MeshConfig struct {
	IPs       string
	Duration  time.Duration
	Port      int
	APIPort   int
	Protocol  string
	SSHHosts  string  // SSH hosts for deployment (host1,host2,host3)
}

type ServerStats struct {
	IP         string    `json:"ip"`
	Throughput float64   `json:"throughput_gbps"`
	PacketLoss float64   `json:"packet_loss_percent"`
	Jitter     float64   `json:"jitter_ms"`
	RTT        float64   `json:"rtt_ms"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type MeshController struct {
	config     *MeshConfig
	localIP    string
	ipList     []string
	sshHosts   []string  // SSH hosts for each IP
	stats      map[string]*ServerStats
	statsLock  sync.RWMutex
	httpServer *http.Server
	stopChan   chan struct{}
}

func MeshCommand(args []string) {
	config := &MeshConfig{}
	fs := flag.NewFlagSet("mesh", flag.ExitOnError)

	fs.StringVar(&config.IPs, "ips", "", "Comma-separated list of IPs for mesh deployment")
	fs.StringVar(&config.SSHHosts, "ssh-hosts", "", "Optional: SSH hosts (user@host1,user@host2,...) - if not provided, uses root@IP")
	fs.DurationVar(&config.Duration, "duration", 5*time.Minute, "Test duration (default: 5m)")
	fs.IntVar(&config.Port, "port", 9999, "Port for gonet testing")
	fs.IntVar(&config.APIPort, "api-port", 8080, "Port for API server")
	fs.StringVar(&config.Protocol, "protocol", "tcp", "Protocol to use: udp or tcp")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if config.IPs == "" {
		fmt.Fprintf(os.Stderr, "Usage: gonet mesh --ips ip1,ip2,ip3 [options]\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  gonet mesh --ips 10.200.5.55,10.200.6.240,10.200.6.28,10.200.6.25 --duration 120s\n\n")
		fs.PrintDefaults()
		os.Exit(1)
	}

	controller := NewMeshController(config)
	controller.Run()
}

func NewMeshController(config *MeshConfig) *MeshController {
	ipList := parseIPs(config.IPs)
	
	// Parse SSH hosts
	var sshHosts []string
	if config.SSHHosts != "" {
		sshHosts = strings.Split(config.SSHHosts, ",")
		for i := range sshHosts {
			sshHosts[i] = strings.TrimSpace(sshHosts[i])
		}
		
		if len(sshHosts) != len(ipList) {
			log.Fatalf("Number of SSH hosts (%d) must match number of IPs (%d)", len(sshHosts), len(ipList))
		}
	}
	
	localIP := detectLocalIP(ipList)
	
	// If local IP is not in the mesh list, use any local IP for API server
	if localIP == "" {
		localIP = getLocalIPAddress()
		if localIP == "" {
			log.Fatal("Could not detect any local IP address")
		}
	}
	

	return &MeshController{
		config:   config,
		localIP:  localIP,
		ipList:   ipList,
		sshHosts: sshHosts,
		stats:    make(map[string]*ServerStats),
		stopChan: make(chan struct{}),
	}
}

func getLocalIPAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
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
			if ip != nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return ""
}



func (mc *MeshController) Run() {
	log.Printf("Starting mesh controller on %s", mc.localIP)
	log.Printf("Deploying to nodes: %v", mc.ipList)
	
	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	// Start API server
	mc.startAPIServer()
	
	// Deploy to all nodes (including self)
	log.Println("\n========== DEPLOYMENT PHASE ==========")
	var wg sync.WaitGroup
	deployDone := make(chan bool)
	
	go func() {
		for i, ip := range mc.ipList {
			wg.Add(1)
			go func(nodeIP string, index int) {
				defer wg.Done()
				if err := mc.deployToNode(nodeIP, index); err != nil {
					log.Printf("❌ Failed to deploy to %s: %v", nodeIP, err)
				} else {
					log.Printf("✅ Successfully deployed to %s", nodeIP)
				}
			}(ip, i)
		}
		wg.Wait()
		close(deployDone)
	}()
	
	// Wait for deployment or signal
	select {
	case <-deployDone:
		log.Println("\n========== DEPLOYMENT COMPLETE ==========")
		log.Println("All nodes deployed and services started.")
		log.Println("Now monitoring mesh performance...")
	case sig := <-sigChan:
		log.Printf("\n⚠️  Received signal: %v", sig)
		log.Println("Deployment interrupted. Cleaning up...")
		mc.cleanup()
		return
	}
	
	// Start periodic stats display
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	// Create duration timer ONCE outside the loop
	durationTimer := time.NewTimer(mc.config.Duration)
	defer durationTimer.Stop()
	
	// Wait for initial stats
	time.Sleep(10 * time.Second)
	
	for {
		select {
		case <-ticker.C:
			mc.displayStats()
		case <-mc.stopChan:
			mc.cleanup()
			return
		case sig := <-sigChan:
			log.Printf("\n⚠️  Received signal: %v", sig)
			log.Println("Shutting down mesh controller...")
			mc.cleanup()
			return
		case <-durationTimer.C:
			log.Println("Test duration completed")
			mc.cleanup()
			return
		}
	}
}

// Execute a remote command via SSH
func (mc *MeshController) execSSH(sshHost, command, description string) error {
	log.Printf("[SSH] Executing %s on %s", description, sshHost)
	
	// Add timeout and batch mode to fail fast if no auth
	cmd := exec.Command("ssh", 
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",  // This will fail fast if no passwordless auth
		sshHost, command)
	
	log.Printf("[SSH] Command: ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o BatchMode=yes %s '%s'", sshHost, command)
	
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("[SSH] Command failed with exit code %d", exitErr.ExitCode())
		}
		log.Printf("[SSH] Error: %v", err)
		if len(output) > 0 {
			log.Printf("[SSH] Output: %s", string(output))
		}
		return fmt.Errorf("%s failed: %v (output: %s)", description, err, string(output))
	}
	
	log.Printf("[SSH] %s completed successfully", description)
	return nil
}

// Execute SCP
func (mc *MeshController) execSCP(source, dest, description string) error {
	log.Printf("[SCP] Executing %s: %s -> %s", description, source, dest)
	
	cmd := exec.Command("scp", 
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		source, dest)
	
	log.Printf("[SCP] Command: scp -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o BatchMode=yes %s %s", source, dest)
	
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("[SCP] Command failed with exit code %d", exitErr.ExitCode())
		}
		log.Printf("[SCP] Error: %v", err)
		if len(output) > 0 {
			log.Printf("[SCP] Output: %s", string(output))
		}
		return fmt.Errorf("%s failed: %v (output: %s)", description, err, string(output))
	}
	
	log.Printf("[SCP] %s completed successfully", description)
	return nil
}

func (mc *MeshController) deployToNode(ip string, index int) error {
	log.Printf("[%s] Starting deployment", ip)
	
	serviceName := "gonet-mesh"
	remoteDir := "/opt/gonet"
	remoteBinary := filepath.Join(remoteDir, "gonet")
	
	// Get the path of the currently executing binary
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("[%s] failed to get current binary path: %v", ip, err)
	}
	
	// Determine SSH host - always use SSH (even for local)
	var sshHost string
	if len(mc.sshHosts) > index {
		sshHost = mc.sshHosts[index]
	} else {
		if strings.Contains(ip, "@") {
			sshHost = ip
		} else {
			sshHost = fmt.Sprintf("root@%s", ip)
		}
	}
	
	log.Printf("[%s] Deployment via SSH to %s", ip, sshHost)
	
	// Step 1: Clean up old processes
	log.Printf("[%s] Cleaning up old processes...", ip)
	cleanupCmd := fmt.Sprintf("pkill -f 'gonet run' 2>/dev/null || true; systemctl stop %s 2>/dev/null || true; systemctl reset-failed %s 2>/dev/null || true; rm -f %s", 
		serviceName, serviceName, remoteBinary)
	if err := mc.execSSH(sshHost, cleanupCmd, "cleanup"); err != nil {
		log.Printf("[%s] Warning: cleanup had issues: %v", ip, err)
		// Continue anyway - cleanup errors are not fatal
	}
	
	// Step 2: Create remote directory
	log.Printf("[%s] Creating remote directory %s...", ip, remoteDir)
	if err := mc.execSSH(sshHost, fmt.Sprintf("mkdir -p %s", remoteDir), "create directory"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 3: Copy binary (use the currently executing binary)
	log.Printf("[%s] Copying binary to remote...", ip)
	if err := mc.execSCP(currentBinary, fmt.Sprintf("%s:%s", sshHost, remoteBinary), "copy binary"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 4: Make executable
	log.Printf("[%s] Making binary executable...", ip)
	if err := mc.execSSH(sshHost, fmt.Sprintf("chmod +x %s", remoteBinary), "make executable"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 5: Create systemd service file
	log.Printf("[%s] Creating systemd service...", ip)
	serviceContent := mc.generateSystemdUnit(remoteBinary)
	serviceTempPath := fmt.Sprintf("/tmp/gonet-mesh-%s.service", strings.ReplaceAll(ip, ".", "_"))
	if err := os.WriteFile(serviceTempPath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("[%s] failed to write service file: %v", ip, err)
	}
	defer os.Remove(serviceTempPath)
	
	// Copy service file to remote
	if err := mc.execSCP(serviceTempPath, fmt.Sprintf("%s:/etc/systemd/system/%s.service", sshHost, serviceName), "copy service file"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 6: Reload systemd
	log.Printf("[%s] Reloading systemd...", ip)
	if err := mc.execSSH(sshHost, "systemctl daemon-reload", "reload systemd"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 7: Start service
	log.Printf("[%s] Starting gonet service...", ip)
	if err := mc.execSSH(sshHost, fmt.Sprintf("systemctl start %s", serviceName), "start service"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	log.Printf("[%s] ✓ Deployment completed successfully", ip)
	return nil
}


func (mc *MeshController) generateSystemdUnit(binaryPath string) string {
	apiEndpoint := fmt.Sprintf("http://%s:%d/stats", mc.localIP, mc.config.APIPort)
	
	return fmt.Sprintf(`[Unit]
Description=GoNet Mesh Testing Service
After=network.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s run --ips %s --duration %s --port %d --protocol %s --report-to %s
StandardOutput=journal
StandardError=journal
Restart=no

[Install]
WantedBy=multi-user.target
`, binaryPath, mc.config.IPs, mc.config.Duration, mc.config.Port, mc.config.Protocol, apiEndpoint)
}

func (mc *MeshController) startAPIServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", mc.handleStats)
	
	mc.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", mc.config.APIPort),
		Handler: mux,
	}
	
	go func() {
		log.Printf("API server listening on :%d", mc.config.APIPort)
		if err := mc.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
		}
	}()
}

func (mc *MeshController) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var stats ServerStats
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	
	if err := json.Unmarshal(body, &stats); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	stats.UpdatedAt = time.Now()
	
	mc.statsLock.Lock()
	mc.stats[stats.IP] = &stats
	mc.statsLock.Unlock()
	
	w.WriteHeader(http.StatusOK)
}


func (mc *MeshController) displayStats() {
	mc.statsLock.RLock()
	defer mc.statsLock.RUnlock()
	
	if len(mc.stats) == 0 {
		log.Println("No statistics received yet...")
		return
	}
	
	var totalThroughput float64
	var minThroughput, maxThroughput float64
	var minServer, maxServer string
	
	first := true
	for ip, stats := range mc.stats {
		// Skip stale stats (older than 30 seconds)
		if time.Since(stats.UpdatedAt) > 30*time.Second {
			continue
		}
		
		totalThroughput += stats.Throughput
		
		if first || stats.Throughput < minThroughput {
			minThroughput = stats.Throughput
			minServer = ip
			first = false
		}
		
		if stats.Throughput > maxThroughput {
			maxThroughput = stats.Throughput
			maxServer = ip
		}
	}
	
	activeServers := 0
	for _, stats := range mc.stats {
		if time.Since(stats.UpdatedAt) <= 30*time.Second {
			activeServers++
		}
	}
	
	avgThroughput := totalThroughput / float64(activeServers)
	
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("\n=== Mesh Statistics [%s] ===\n", timestamp)
	fmt.Printf("Active Servers: %d/%d\n", activeServers, len(mc.ipList))
	fmt.Printf("Total Throughput: %.2f Gbps\n", totalThroughput)
	fmt.Printf("Avg Throughput per Server: %.2f Gbps\n", avgThroughput)
	fmt.Printf("Fastest Server: %s (%.2f Gbps)\n", maxServer, maxThroughput)
	fmt.Printf("Slowest Server: %s (%.2f Gbps)\n", minServer, minThroughput)
	fmt.Printf("=======================\n")
}

func (mc *MeshController) cleanup() {
	log.Println("Cleaning up mesh deployment...")
	
	// Stop all services via SSH
	serviceName := "gonet-mesh"
	
	var wg sync.WaitGroup
	for i, ip := range mc.ipList {
		wg.Add(1)
		go func(nodeIP string, index int) {
			defer wg.Done()
			
			// Always use SSH (even for local)
			var sshHost string
			if len(mc.sshHosts) > index {
				sshHost = mc.sshHosts[index]
			} else {
				if strings.Contains(nodeIP, "@") {
					sshHost = nodeIP
				} else {
					sshHost = fmt.Sprintf("root@%s", nodeIP)
				}
			}
			
			cmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", sshHost, fmt.Sprintf("systemctl stop %s", serviceName))
			cmd.Run()
		}(ip, i)
	}
	wg.Wait()
	
	// Shutdown API server
	if mc.httpServer != nil {
		mc.httpServer.Close()
	}
}