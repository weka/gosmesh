package commands

import (
	"context"
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

	"github.com/weka/gosmesh/pkg/workers"
)

type MeshConfig struct {
	IPs       string
	Duration  time.Duration
	Port      int
	APIPort   int
	Protocol  string
	SSHHosts  string  // SSH hosts for deployment (host1,host2,host3)
	Verbose   bool    // Enable verbose logging
}

type ServerStats struct {
	IP         string    `json:"ip"`
	Throughput float64   `json:"throughput_gbps"`
	PacketLoss float64   `json:"packet_loss_percent"`
	Jitter     float64   `json:"jitter_ms"`
	RTT        float64   `json:"rtt_ms"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type DeployTarget struct {
	IP       string
	Index    int
	SSHHost  string
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
	fs.IntVar(&config.Port, "port", 9999, "Port for gosmesh testing")
	fs.IntVar(&config.APIPort, "api-port", 8080, "Port for API server")
	fs.StringVar(&config.Protocol, "protocol", "tcp", "Protocol to use: udp or tcp")
	fs.BoolVar(&config.Verbose, "verbose", false, "Enable verbose logging")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if config.IPs == "" {
		fmt.Fprintf(os.Stderr, "Usage: gosmesh mesh --ips ip1,ip2,ip3 [options]\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  gosmesh mesh --ips 10.200.5.55,10.200.6.240,10.200.6.28,10.200.6.25 --duration 120s\n\n")
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
	if config.Verbose {
		log.Printf("Detected local IP from mesh list: %s", localIP)
	}
	
	// If local IP is not in the mesh list, use any local IP for API server
	if localIP == "" {
		localIP = getLocalIPAddress()
		if config.Verbose {
			log.Printf("Fallback to any local IP for API server: %s", localIP)
		}
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
	if !mc.config.Verbose {
		fmt.Printf("🚀 GoNet Mesh Controller\n")
		fmt.Printf("Controller: %s | Nodes: %d | Duration: %v\n\n", mc.localIP, len(mc.ipList), mc.config.Duration)
	} else {
		log.Printf("Starting mesh controller on %s", mc.localIP)
		log.Printf("Deploying to nodes: %v", mc.ipList)
	}
	
	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	// Start API server
	mc.startAPIServer()
	
	// Phase 1: Deploy binaries and create services (but don't start)
	if mc.config.Verbose {
		log.Println("\n========== PHASE 1: DEPLOYMENT ==========")
	} else {
		fmt.Printf("📦 Deploying to %d nodes... ", len(mc.ipList))
	}
	deployDone := make(chan *workers.Results[DeployTarget])
	
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		
		// Prepare deployment targets
		targets := mc.prepareDeployTargets()
		
		// Use parallel deployment with workers package
		numWorkers := min(len(targets), 10) // Max 10 concurrent deployments
		results := workers.ProcessConcurrentlyWithIndexes(ctx, targets, numWorkers, 
			func(ctx context.Context, target DeployTarget, i int) error {
				return mc.deployToNodeWithStart(target.IP, target.Index, false) // Don't start service yet
			})
		
		deployDone <- results
	}()
	
	// Wait for deployment or signal
	var deployResults *workers.Results[DeployTarget]
	select {
	case deployResults = <-deployDone:
		mc.handleDeploymentResults(deployResults)
	case sig := <-sigChan:
		if mc.config.Verbose {
			log.Printf("\n⚠️  Received signal: %v", sig)
			log.Println("Deployment interrupted. Cleaning up...")
		} else {
			fmt.Printf("\n⚠️  Interrupted. Cleaning up...\n")
		}
		mc.cleanup()
		return
	}
	
	// Check if deployment succeeded
	if !deployResults.AllSucceeded() {
		if mc.config.Verbose {
			log.Printf("⚠️  Some deployments failed, but continuing with successful nodes")
			if err := deployResults.AsError(); err != nil {
				log.Printf("Deployment errors:\n%v", err)
			}
		}
	}
	
	// Phase 2: Start all services simultaneously
	if mc.config.Verbose {
		log.Println("\n========== PHASE 2: STARTING SERVICES ==========")
	} else {
		fmt.Printf("🚀 Starting services... ")
	}
	startDone := make(chan *workers.Results[DeployTarget])
	
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		
		// Only start services on successfully deployed nodes
		var successfulTargets []DeployTarget
		for _, result := range deployResults.Items {
			if result.Err == nil {
				successfulTargets = append(successfulTargets, result.Object)
			}
		}
		
		if mc.config.Verbose {
			log.Printf("Starting services on %d successfully deployed nodes...", len(successfulTargets))
		}
		
		// Start all services concurrently
		numWorkers := min(len(successfulTargets), 20) // Higher concurrency for starting
		results := workers.ProcessConcurrentlyWithIndexes(ctx, successfulTargets, numWorkers, 
			func(ctx context.Context, target DeployTarget, i int) error {
				return mc.startService(target.IP, target.Index)
			})
		
		startDone <- results
	}()
	
	// Wait for service starts or signal
	var startResults *workers.Results[DeployTarget]
	select {
	case startResults = <-startDone:
		mc.handleServiceStartResults(startResults)
	case sig := <-sigChan:
		if mc.config.Verbose {
			log.Printf("\n⚠️  Received signal: %v", sig)
			log.Println("Service start interrupted. Cleaning up...")
		} else {
			fmt.Printf("\n⚠️  Interrupted. Cleaning up...\n")
		}
		mc.cleanup()
		return
	}
	
	// Log final status
	if !startResults.AllSucceeded() {
		if mc.config.Verbose {
			log.Printf("⚠️  Some services failed to start, but monitoring active ones")
			if err := startResults.AsError(); err != nil {
				log.Printf("Service start errors:\n%v", err)
			}
		}
	}
	
	// Give services time to fully initialize and establish connections
	if mc.config.Verbose {
		log.Println("Waiting for services to fully initialize and establish connections...")
	} else {
		fmt.Printf("🔄 Initializing connections... ")
	}
	time.Sleep(3 * time.Second)
	if !mc.config.Verbose {
		fmt.Printf("Done\n\n📊 Monitoring mesh performance:\n")
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

// prepareDeployTargets creates DeployTarget structs for all nodes
func (mc *MeshController) prepareDeployTargets() []DeployTarget {
	targets := make([]DeployTarget, len(mc.ipList))
	
	for i, ip := range mc.ipList {
		// Determine SSH host - always use SSH (even for local)
		var sshHost string
		if len(mc.sshHosts) > i {
			sshHost = mc.sshHosts[i]
		} else {
			if strings.Contains(ip, "@") {
				sshHost = ip
			} else {
				sshHost = fmt.Sprintf("root@%s", ip)
			}
		}
		
		targets[i] = DeployTarget{
			IP:      ip,
			Index:   i,
			SSHHost: sshHost,
		}
	}
	
	return targets
}

// handleDeploymentResults processes deployment results and logs outcomes
func (mc *MeshController) handleDeploymentResults(results *workers.Results[DeployTarget]) {
	successCount := 0
	failCount := 0
	
	for _, result := range results.Items {
		if result.Err != nil {
			if mc.config.Verbose {
				log.Printf("❌ Failed to deploy to %s: %v", result.Object.IP, result.Err)
			}
			failCount++
		} else {
			if mc.config.Verbose {
				log.Printf("✅ Successfully deployed to %s", result.Object.IP)
			}
			successCount++
		}
	}
	
	if mc.config.Verbose {
		log.Println("\n========== DEPLOYMENT COMPLETE ==========")
		log.Printf("Successful deployments: %d/%d", successCount, len(results.Items))
		if failCount > 0 {
			log.Printf("Failed deployments: %d", failCount)
		}
	} else {
		if failCount == 0 {
			fmt.Printf("Done (✅ %d/%d)\n", successCount, len(results.Items))
		} else {
			fmt.Printf("Done (✅ %d, ❌ %d)\n", successCount, failCount)
		}
	}
}

// handleServiceStartResults processes service start results and logs outcomes
func (mc *MeshController) handleServiceStartResults(results *workers.Results[DeployTarget]) {
	successCount := 0
	failCount := 0
	
	for _, result := range results.Items {
		if result.Err != nil {
			if mc.config.Verbose {
				log.Printf("[RESULT] ❌ %s: FAILED - %v", result.Object.IP, result.Err)
			}
			failCount++
		} else {
			if mc.config.Verbose {
				log.Printf("[RESULT] ✅ %s: SUCCESS", result.Object.IP)
			}
			successCount++
		}
	}
	
	if mc.config.Verbose {
		log.Println("\n========== SERVICE START SUMMARY ==========")
		log.Printf("✅ Successful starts: %d/%d", successCount, len(results.Items))
		if failCount > 0 {
			log.Printf("❌ Failed starts: %d/%d", failCount, len(results.Items))
		}
		log.Println("==========================================")
		
		if successCount > 0 {
			log.Println("Now monitoring mesh performance...")
		} else {
			log.Println("⚠️  No services started successfully - check diagnostics above")
		}
	} else {
		if failCount == 0 {
			fmt.Printf("Done (✅ %d/%d)\n", successCount, len(results.Items))
		} else {
			fmt.Printf("Done (✅ %d, ❌ %d)\n", successCount, failCount)
			if successCount == 0 {
				fmt.Printf("⚠️  No services started - use --verbose for details\n")
			}
		}
	}
}

// startService starts the gosmesh service on a specific node and verifies it's running
func (mc *MeshController) startService(ip string, index int) error {
	serviceName := "gosmesh-mesh"
	
	// Determine SSH host
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
	
	if mc.config.Verbose {
		log.Printf("[%s] 🚀 Starting service...", ip)
	}
	if err := mc.execSSH(sshHost, fmt.Sprintf("systemctl start %s", serviceName), "start service"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Failed to start service: %v", ip, err)
		}
		return fmt.Errorf("[%s] failed to start service: %v", ip, err)
	}
	if mc.config.Verbose {
		log.Printf("[%s] ⏳ Service start command completed, verifying...", ip)
	}
	
	// Verify service is actually running
	if mc.config.Verbose {
		log.Printf("[%s] 🔍 Checking service health...", ip)
	}
	verifyCmd := fmt.Sprintf(`
		echo "[VERIFY] Quick service check..."
		# Check if service is active and listening in one go
		if systemctl is-active %s >/dev/null 2>&1 && pgrep -f 'gosmesh run' >/dev/null 2>&1 && ss -tln | grep ':%d ' >/dev/null 2>&1; then
			echo "[SUCCESS] Service active, process running, port listening"
			exit 0
		fi
		
		# If verification failed, show why
		echo "[ERROR] Service verification failed"
		echo "Service active: $(systemctl is-active %s 2>/dev/null || echo 'inactive')"
		echo "Process running: $(pgrep -f 'gosmesh run' >/dev/null 2>&1 && echo 'yes' || echo 'no')"
		echo "Port listening: $(ss -tln | grep ':%d ' >/dev/null 2>&1 && echo 'yes' || echo 'no')"
		
		echo "Recent logs:"
		journalctl -u %s --no-pager --since "30 seconds ago" --lines=5 || true
		exit 1
	`, serviceName, mc.config.Port, serviceName, mc.config.Port, serviceName)
	
	if err := mc.execSSH(sshHost, verifyCmd, "verify service health"); err != nil {
		// Simple diagnostic - don't spam logs
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Service verification failed - check service manually", ip)
		}
		return fmt.Errorf("[%s] service verification failed: %v", ip, err)
	}
	
	if mc.config.Verbose {
		log.Printf("[%s] ✅ SUCCESS: Service started and verified", ip)
	}
	return nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Execute a remote command via SSH
func (mc *MeshController) execSSH(sshHost, command, description string) error {
	if mc.config.Verbose {
		log.Printf("[SSH] Executing %s on %s", description, sshHost)
	}
	
	// Add timeout and batch mode to fail fast if no auth
	cmd := exec.Command("ssh", 
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",  // This will fail fast if no passwordless auth
		sshHost, command)
	
	if mc.config.Verbose {
		log.Printf("[SSH] Command: ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o BatchMode=yes %s '%s'", sshHost, command)
	}
	
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		if mc.config.Verbose {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode := exitErr.ExitCode()
				log.Printf("[SSH] Command failed with exit code %d", exitCode)
				
				// Interpret common SSH exit codes
				switch exitCode {
				case 255:
					if len(output) == 0 {
						log.Printf("[SSH] Exit 255 with no output - likely SSH connection failure (auth, network, or host unreachable)")
					}
				case 1:
					log.Printf("[SSH] Exit 1 - command execution error")
				case 130:
					log.Printf("[SSH] Exit 130 - interrupted by signal")
				}
			}
			log.Printf("[SSH] Error: %v", err)
			if len(output) > 0 {
				log.Printf("[SSH] Output: %s", string(output))
			} else {
				log.Printf("[SSH] No output received")
			}
		}
		return fmt.Errorf("%s failed: %v (output: %s)", description, err, string(output))
	}
	
	if mc.config.Verbose {
		if len(output) > 0 {
			log.Printf("[SSH] Output: %s", strings.TrimSpace(string(output)))
		}
		log.Printf("[SSH] %s completed successfully", description)
	}
	return nil
}

// Execute SCP
func (mc *MeshController) execSCP(source, dest, description string) error {
	if mc.config.Verbose {
		log.Printf("[SCP] Executing %s: %s -> %s", description, source, dest)
	}
	
	cmd := exec.Command("scp", 
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		source, dest)
	
	if mc.config.Verbose {
		log.Printf("[SCP] Command: scp -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o BatchMode=yes %s %s", source, dest)
	}
	
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		if mc.config.Verbose {
			if exitErr, ok := err.(*exec.ExitError); ok {
				log.Printf("[SCP] Command failed with exit code %d", exitErr.ExitCode())
			}
			log.Printf("[SCP] Error: %v", err)
			if len(output) > 0 {
				log.Printf("[SCP] Output: %s", string(output))
			}
		}
		return fmt.Errorf("%s failed: %v (output: %s)", description, err, string(output))
	}
	
	if mc.config.Verbose {
		log.Printf("[SCP] %s completed successfully", description)
	}
	return nil
}

func (mc *MeshController) deployToNode(ip string, index int) error {
	return mc.deployToNodeWithStart(ip, index, true)
}

func (mc *MeshController) deployToNodeWithStart(ip string, index int, startService bool) error {
	if mc.config.Verbose {
		log.Printf("[%s] Starting deployment", ip)
	}
	
	serviceName := "gosmesh-mesh"
	remoteDir := "/opt/gosmesh"
	remoteBinary := filepath.Join(remoteDir, "gosmesh")
	
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
	
	if mc.config.Verbose {
		log.Printf("[%s] Deployment via SSH to %s", ip, sshHost)
	}
	
	// Step 0: Test SSH connectivity first
	if mc.config.Verbose {
		log.Printf("[%s] Testing SSH connectivity...", ip)
	}
	if err := mc.execSSH(sshHost, "echo 'SSH connection OK'", "connectivity test"); err != nil {
		return fmt.Errorf("[%s] SSH connectivity failed: %v", ip, err)
	}
	if mc.config.Verbose {
		log.Printf("[%s] SSH connectivity confirmed", ip)
		
		// Step 1: Thorough cleanup of old processes and services (individual commands)
		log.Printf("[%s] Performing thorough cleanup...", ip)
		
		// Stop service if it exists
		log.Printf("[%s] Stopping existing service...", ip)
	}
	stopCmd := fmt.Sprintf("systemctl stop %s || true", serviceName)
	if err := mc.execSSH(sshHost, stopCmd, "stop service"); err != nil {
		return fmt.Errorf("[%s] stop service failed: %v", ip, err)
	}
	
	// Disable service if it exists
	if mc.config.Verbose {
		log.Printf("[%s] Disabling existing service...", ip)
	}
	disableCmd := fmt.Sprintf("systemctl disable %s || true", serviceName)
	if err := mc.execSSH(sshHost, disableCmd, "disable service"); err != nil {
		return fmt.Errorf("[%s] disable service failed: %v", ip, err)
	}
	
	// Reset failed state (redirect stderr to avoid hanging)
	if mc.config.Verbose {
		log.Printf("[%s] Resetting failed state...", ip)
	}
	resetCmd := fmt.Sprintf("systemctl reset-failed %s 2>/dev/null || true", serviceName)
	if err := mc.execSSH(sshHost, resetCmd, "reset failed state"); err != nil {
		return fmt.Errorf("[%s] reset failed state failed: %v", ip, err)
	}
	
	// Kill processes running specifically from /opt/gosmesh/ path using their executable location
	if mc.config.Verbose {
		log.Printf("[%s] Finding processes running from /opt/gosmesh...", ip)
	}
	findCmd := fmt.Sprintf(`
for pid in $(pgrep gosmesh 2>/dev/null || true); do
    if [ -n "$pid" ]; then
        exe_path=$(readlink /proc/$pid/exe 2>/dev/null || echo "")
        if [ "$exe_path" = "%s" ]; then
            echo "Found process $pid running from %s"
        fi
    fi
done
`, remoteBinary, remoteBinary)
	if err := mc.execSSH(sshHost, findCmd, "find /opt/gosmesh processes"); err != nil {
		return fmt.Errorf("[%s] find processes failed: %v", ip, err)
	}
	
	if mc.config.Verbose {
		log.Printf("[%s] Killing processes from /opt/gosmesh...", ip)
	}
	killCmd := fmt.Sprintf(`
killed=false
for pid in $(pgrep gosmesh 2>/dev/null || true); do
    if [ -n "$pid" ]; then
        exe_path=$(readlink /proc/$pid/exe 2>/dev/null || echo "")
        if [ "$exe_path" = "%s" ]; then
            echo "Killing process $pid from %s"
            kill $pid && echo "Killed $pid" || echo "Failed to kill $pid"
            killed=true
        fi
    fi
done
if [ "$killed" = "false" ]; then
    echo "No processes from /opt/gosmesh to kill"
fi
`, remoteBinary, remoteBinary)
	if err := mc.execSSH(sshHost, killCmd, "kill /opt/gosmesh processes"); err != nil {
		return fmt.Errorf("[%s] kill processes failed: %v", ip, err)
	}
	
	if mc.config.Verbose {
		log.Printf("[%s] Force killing any remaining processes from /opt/gosmesh...", ip)
	}
	forceKillCmd := fmt.Sprintf(`
killed=false
for pid in $(pgrep gosmesh 2>/dev/null || true); do
    if [ -n "$pid" ]; then
        exe_path=$(readlink /proc/$pid/exe 2>/dev/null || echo "")
        if [ "$exe_path" = "%s" ]; then
            echo "Force killing process $pid from %s"
            kill -9 $pid && echo "Force killed $pid" || echo "Failed to force kill $pid"
            killed=true
        fi
    fi
done
if [ "$killed" = "false" ]; then
    echo "No processes from /opt/gosmesh to force kill"
fi
`, remoteBinary, remoteBinary)
	if err := mc.execSSH(sshHost, forceKillCmd, "force kill /opt/gosmesh processes"); err != nil {
		return fmt.Errorf("[%s] force kill processes failed: %v", ip, err)
	}
	
	// Remove files
	if mc.config.Verbose {
		log.Printf("[%s] Removing old files...", ip)
	}
	removeCmd := fmt.Sprintf("rm -f %s /etc/systemd/system/%s.service", remoteBinary, serviceName)
	if err := mc.execSSH(sshHost, removeCmd, "remove files"); err != nil {
		return fmt.Errorf("[%s] remove files failed: %v", ip, err)
	}
	
	// Reload systemd
	if mc.config.Verbose {
		log.Printf("[%s] Reloading systemd...", ip)
	}
	reloadCmd := "systemctl daemon-reload"
	if err := mc.execSSH(sshHost, reloadCmd, "reload systemd"); err != nil {
		return fmt.Errorf("[%s] reload systemd failed: %v", ip, err)
	}
	
	if mc.config.Verbose {
		log.Printf("[%s] Cleanup completed successfully", ip)
	}
	
	// Step 2: Create remote directory
	if mc.config.Verbose {
		log.Printf("[%s] Creating remote directory %s...", ip, remoteDir)
	}
	if err := mc.execSSH(sshHost, fmt.Sprintf("mkdir -p %s", remoteDir), "create directory"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 3: Copy binary (use the currently executing binary)
	if mc.config.Verbose {
		log.Printf("[%s] Copying binary to remote...", ip)
	}
	if err := mc.execSCP(currentBinary, fmt.Sprintf("%s:%s", sshHost, remoteBinary), "copy binary"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 4: Make executable
	if mc.config.Verbose {
		log.Printf("[%s] Making binary executable...", ip)
	}
	if err := mc.execSSH(sshHost, fmt.Sprintf("chmod +x %s", remoteBinary), "make executable"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 5: Create systemd service file
	if mc.config.Verbose {
		log.Printf("[%s] Creating systemd service...", ip)
	}
	serviceContent := mc.generateSystemdUnit(remoteBinary)
	serviceTempPath := fmt.Sprintf("/tmp/gosmesh-mesh-%s.service", strings.ReplaceAll(ip, ".", "_"))
	if err := os.WriteFile(serviceTempPath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("[%s] failed to write service file: %v", ip, err)
	}
	defer os.Remove(serviceTempPath)
	
	// Copy service file to remote
	if err := mc.execSCP(serviceTempPath, fmt.Sprintf("%s:/etc/systemd/system/%s.service", sshHost, serviceName), "copy service file"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 6: Reload systemd
	if mc.config.Verbose {
		log.Printf("[%s] Reloading systemd...", ip)
	}
	if err := mc.execSSH(sshHost, "systemctl daemon-reload", "reload systemd"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}
	
	// Step 7: Start service (optional)
	if startService {
		if mc.config.Verbose {
			log.Printf("[%s] Starting gosmesh service...", ip)
		}
		if err := mc.execSSH(sshHost, fmt.Sprintf("systemctl start %s", serviceName), "start service"); err != nil {
			return fmt.Errorf("[%s] %v", ip, err)
		}
		if mc.config.Verbose {
			log.Printf("[%s] ✓ Deployment and service start completed successfully", ip)
		}
	} else {
		if mc.config.Verbose {
			log.Printf("[%s] ✓ Deployment completed successfully (service not started)", ip)
		}
	}
	return nil
}


func (mc *MeshController) generateSystemdUnit(binaryPath string) string {
	apiEndpoint := fmt.Sprintf("http://%s:%d/stats", mc.localIP, mc.config.APIPort)
	if mc.config.Verbose {
		log.Printf("Generated API endpoint for reporting: %s", apiEndpoint)
	}
	
	return fmt.Sprintf(`[Unit]
Description=GoNet Mesh Testing Service
After=network.target

[Service]
Type=simple
ExecStart=%s run --ips %s --duration %s --port %d --protocol %s --report-to %s
StandardOutput=journal
StandardError=journal
Restart=no
KillMode=mixed
TimeoutStartSec=10
TimeoutStopSec=30

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
		if mc.config.Verbose {
			log.Printf("API server listening on :%d", mc.config.APIPort)
		}
		if err := mc.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			if mc.config.Verbose {
				log.Printf("API server error: %v", err)
			}
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
	
	// Stop all services via SSH using workers package
	serviceName := "gosmesh-mesh"
	
	// Prepare cleanup targets
	targets := mc.prepareDeployTargets()
	
	// Use workers package for concurrent cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	numWorkers := min(len(targets), 10) // Max 10 concurrent cleanups
	results := workers.ProcessConcurrently(ctx, targets, numWorkers,
		func(ctx context.Context, target DeployTarget) error {
			cmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=5", 
				target.SSHHost, fmt.Sprintf("systemctl stop %s", serviceName))
			return cmd.Run()
		})
	
	// Log cleanup results
	successCount := 0
	for _, result := range results.Items {
		if result.Err != nil {
			log.Printf("⚠️  Failed to cleanup %s: %v", result.Object.IP, result.Err)
		} else {
			successCount++
		}
	}
	log.Printf("Cleanup completed: %d/%d nodes", successCount, len(targets))
	
	// Shutdown API server
	if mc.httpServer != nil {
		mc.httpServer.Close()
	}
}