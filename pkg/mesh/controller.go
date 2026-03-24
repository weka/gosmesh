// Package mesh provides the MeshController for orchestrating network tests across multiple nodes.
//
// The MeshController can operate in two modes:
// 1. With SSH deployment (automatic binary deployment and service start)
// 2. Without SSH deployment (assumes servers are already running)
//
// Usage:
//
//	controller := mesh.NewController(config)
//	if err := controller.Deploy(); err != nil {
//	    log.Fatal(err)
//	}
//	if err := controller.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	controller.Wait()
//	stats := controller.GetStats()
//
// Or with automatic deployment:
//
//	controller := mesh.NewController(config)
//	controller.Run()  // Does Deploy + Start + Wait
package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/weka/gosmesh/pkg/testing"
	"github.com/weka/gosmesh/pkg/version"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weka/gosmesh/pkg/workers"
)

const (
	GoSmeshServiceName = "gosmesh-mesh"
	GoSmeshRemotePath  = "/opt/gosmesh"
)

// Target represents a single worker, having both IP (for network test) and SSHHost (for SSH connection, if different)
type Target struct {
	IP      string
	SSHHost string
}

func (t *Target) String() string {
	return fmt.Sprintf("%s (%s)", t.IP, t.SSHHost)
}

type Targets []Target

func (t Targets) GetIPs() []string {
	ret := []string{}
	for _, t := range t {
		ret = append(ret, t.IP)
	}
	return ret
}

func (t Targets) GetSSHHosts() []string {
	ret := []string{}
	for _, t := range t {
		ret = append(ret, t.SSHHost)
	}
	return ret
}

func (t Targets) Len() int {
	return len(t)
}

func (t Targets) String() string {
	return strings.Join(t.GetIPs(), ",")
}

// Config contains configuration for the MeshController
type Config struct {
	// Mesh-specific configuration
	Targets Targets

	// Reference to run configuration for all test-related settings
	TestConfig *testing.TestConfig

	// SSH deployment (optional)
	DeploySystemDService bool // Enable SSH deployment

	// API configuration (mesh-specific)
	APIPort int // Port for the mesh controller's API server

	// Misc
	Verbose bool
}

func (config *Config) FillTargets(CommaSeparatedIPs string, SSHHosts string) error {
	if config == nil {
		return fmt.Errorf("config is nil")
	}

	ipList := parseIPs(CommaSeparatedIPs)

	// Parse SSH hosts
	var sshHosts []string
	SSHHosts = strings.TrimSpace(SSHHosts)
	if SSHHosts != "" {
		sshHosts = strings.Split(SSHHosts, ",")
	}
	if len(sshHosts) > 0 && len(sshHosts) != len(ipList) {
		return fmt.Errorf("number of SSH hosts (%d) must match number of CommaSeparatedIPs (%d)", len(sshHosts), len(ipList))
	}
	var tt Targets
	for i, ip := range ipList {
		h := ip
		if len(sshHosts) != 0 {
			h = sshHosts[i]
		}
		// Determine SSH host
		tt = append(tt, Target{
			IP:      ip,
			SSHHost: strings.TrimSpace(h),
		})
	}
	config.Targets = tt
	return nil
}

// GetDefaultMeshTestConfig returns a TestConfig with defaults appropriate for mesh testing
func GetDefaultMeshTestConfig() *testing.TestConfig {
	return testing.GetDefaultTestConfig()
}

// Controller orchestrates network tests across multiple nodes
type Controller struct {
	config       *Config
	localIP      string
	stats        map[string]*testing.ServerStats
	statsLock    sync.RWMutex
	httpServer   *http.Server
	httpClient   *http.Client   // HTTP client with 5 second timeout
	signalChan   chan os.Signal // Single channel for all signal handling
	deployHelper *SSHDeployer
}

// NewController creates a new MeshController
func NewController(config *Config) *Controller {

	// Determine local IP
	localIP := config.TestConfig.LocalIP
	if localIP == "" {

		localIP = detectLocalIP(config.Targets.GetIPs())
		if localIP == "" {
			localIP = getLocalIPAddress()
			if localIP == "" {
				log.Fatal("Could not detect any local IP address")
			}
		}
		if config.Verbose {
			log.Printf("Detected local IP from mesh list: %s", localIP)
		}
	}

	ret := &Controller{
		config:       config,
		localIP:      localIP,
		stats:        make(map[string]*testing.ServerStats),
		signalChan:   make(chan os.Signal, 1),
		deployHelper: NewSSHDeployer(config.Verbose),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	// Register for OS signals
	signal.Notify(ret.signalChan, os.Interrupt, syscall.SIGTERM)

	return ret
}

// Start begins the test by starting services on all nodes
// Assumes deployment has already been done (either via Deploy() or servers already running)
func (mc *Controller) Start() error {
	if mc.config.Verbose {
		log.Println("\n========== PHASE 2: STARTING TESTS VIA HTTP ==========")
	} else {
		fmt.Printf("🚀 Starting tests via HTTP... ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	numWorkers := minInt(mc.config.Targets.Len(), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.config.Targets, numWorkers,
		func(ctx context.Context, target Target, i int) error {
			return mc.startTestViaHTTP(target)
		})

	mc.handleTestStartResults(results)

	if !results.AllSucceeded() {
		if mc.config.Verbose {
			log.Printf("⚠️  Some tests failed to start")
		}
		if err := results.AsError(); err != nil {
			return fmt.Errorf("test start errors: %v", err)
		}
	}

	// Give tests time to initialize
	if mc.config.Verbose {
		log.Println("Waiting for tests to initialize...")
	}
	time.Sleep(3 * time.Second)

	return nil
}

// Wait blocks until the test duration completes or a signal is received
// Continuously monitors and displays statistics
func (mc *Controller) Wait() {
	if mc.config.Verbose {
		log.Println("Waiting for test completion...")
	} else {
		fmt.Printf("📊 Monitoring mesh performance:\n")
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	durationTimer := time.NewTimer(mc.config.TestConfig.Duration)
	defer durationTimer.Stop()

	for {
		select {
		case <-ticker.C:
			mc.displayStats()
		case sig := <-mc.signalChan:
			log.Printf("\n⚠️  Received signal: %v - stopping all tests", sig)
			mc.stopAllTests()
			mc.displayStats()
			return
		case <-durationTimer.C:
			log.Println("\n✓ Test duration completed - stopping all tests")
			mc.stopAllTests()
			return
		}
	}
}

// stopAllTests sends HTTP stop requests to all nodes
func (mc *Controller) stopAllTests() {
	if mc.config.Verbose {
		log.Println("\n========== STOPPING ALL TESTS ==========")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	numWorkers := minInt(mc.config.Targets.Len(), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.config.Targets, numWorkers,
		func(ctx context.Context, target Target, i int) error {
			return mc.stopTestViaHTTP(target)
		})

	successCount := 0
	for _, result := range results.Items {
		if result.Err == nil {
			successCount++
		}
	}

	if mc.config.Verbose {
		log.Printf("✅ Successfully stopped tests on %d/%d nodes", successCount, len(results.Items))
		log.Println("========================================")
	}
}

// Run performs the complete workflow: Deploy -> Start -> Wait
// This is the main entry point for simple usage
func (mc *Controller) Run() {
	if !mc.config.Verbose {
		log.Printf("🚀 GoSmesh Mesh Controller\n")
		log.Printf("Controller: %s | Nodes: %d | Duration: %v\n\n", mc.localIP, mc.config.Targets.Len(), mc.config.TestConfig.Duration)
	} else {
		log.Printf("Starting mesh controller on %s", mc.localIP)
		log.Printf("Deploying to nodes: %v", mc.config.Targets.String())
	}

	mc.startAPIServer()
	// Start API server
	defer mc.cleanup()
	// Deploy (if SSH is enabled)
	if mc.config.DeploySystemDService {
		deployDone := make(chan error)
		go func() {
			deployDone <- mc.Deploy()
		}()

		select {
		case err := <-deployDone:
			if err != nil {
				log.Printf("Deployment failed: %v", err)
				return
			}
		case sig := <-mc.signalChan:
			log.Printf("\n⚠️  Received signal: %v", sig)
			return
		}
	}

	time.Sleep(3 * time.Second)
	// Start services
	if err := mc.Start(); err != nil {
		log.Printf("Failed to start services: %v", err)
		mc.cleanup()
		return
	}

	// Wait for completion
	mc.Wait()
	mc.displayStats()
}

// CleanupNode performs thorough cleanup on a single node
// Stops services, kills processes, removes binaries and directories
// This uses the controller's configuration for service name and remote directory
// killEvenIfNoSystemD means kill the process even if it was deployed auxiliary (not just from systemd), to ensure cleanup is complete
func (mc *Controller) cleanupNode(ctx context.Context, target Target, killEvenIfNoSystemD bool) error {
	remoteBinary := fmt.Sprintf("%s/gosmesh", GoSmeshRemotePath)

	// Determine SSH host
	sshHost := target.SSHHost
	ip := target.IP

	// only if deployed service and no auxiliary installation was performed
	if mc.config.DeploySystemDService {
		if mc.config.Verbose {
			log.Printf("[%s] Stopping existing service...", ip)
		}

		stopCmd := fmt.Sprintf("systemctl stop %s || true", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, stopCmd, "stop service"); err != nil {
			return fmt.Errorf("[%s] stop service failed: %v", ip, err)
		}
		// Disable service if it exists
		if mc.config.Verbose {
			log.Printf("[%s] Disabling existing service...", ip)
		}
		disableCmd := fmt.Sprintf("systemctl disable %s || true", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, disableCmd, "disable service"); err != nil {
			return fmt.Errorf("[%s] disable service failed: %v", ip, err)
		}
		// Reset failed state (redirect stderr to avoid hanging)
		if mc.config.Verbose {
			log.Printf("[%s] Resetting failed state...", ip)
		}
		resetCmd := fmt.Sprintf("systemctl reset-failed %s 2>/dev/null || true", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, resetCmd, "reset failed state"); err != nil {
			return fmt.Errorf("[%s] reset failed state failed: %v", ip, err)
		}
	}

	// kill processes only if we spawned them OR if explicitly required
	if mc.config.DeploySystemDService || killEvenIfNoSystemD {
		// Kill processes running specifically from the remote directory path using their executable location
		if mc.config.Verbose {
			log.Printf("[%s] Killing processes from %s...", ip, GoSmeshRemotePath)
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
    echo "No processes found from %s"
fi
`, remoteBinary, remoteBinary, GoSmeshRemotePath)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, killCmd, "kill processes"); err != nil {
			return fmt.Errorf("[%s] kill processes failed: %v", ip, err)
		}

		// Force kill any remaining processes
		if mc.config.Verbose {
			log.Printf("[%s] Force killing any remaining processes from %s...", ip, GoSmeshRemotePath)
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
    echo "No processes from %s to force kill"
fi
`, remoteBinary, remoteBinary, GoSmeshRemotePath)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, forceKillCmd, "force kill processes"); err != nil {
			return fmt.Errorf("[%s] force kill processes failed: %v", ip, err)
		}
	}

	// Remove service file
	if mc.config.DeploySystemDService {
		if mc.config.Verbose {
			log.Printf("[%s] Removing service file...", ip)
		}
		removeServiceCmd := fmt.Sprintf("rm -f /etc/systemd/system/%s.service", GoSmeshServiceName)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, removeServiceCmd, "remove service file"); err != nil {
			return fmt.Errorf("[%s] remove service file failed: %v", ip, err)
		}

		// Remove binary and directory
		if mc.config.Verbose {
			log.Printf("[%s] Removing binary and directory...", ip)
		}
		removeCmd := fmt.Sprintf("rm -rf %s", GoSmeshRemotePath)
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, removeCmd, "remove directory"); err != nil {
			return fmt.Errorf("[%s] remove directory failed: %v", ip, err)
		}

		// Reload systemd
		if mc.config.Verbose {
			log.Printf("[%s] Reloading systemd...", ip)
		}
		reloadCmd := "systemctl daemon-reload"
		if err := mc.deployHelper.ExecWithContext(ctx, sshHost, reloadCmd, "reload systemd"); err != nil {
			return fmt.Errorf("[%s] reload systemd failed: %v", ip, err)
		}

		if mc.config.Verbose {
			log.Printf("[%s] Cleanup completed successfully", ip)
		}
	}

	return nil
}

// Deploy performs SSH deployment to all nodes (if SSH is enabled)
// This is optional - you can also assume servers are already running
func (mc *Controller) Deploy() error {
	if !mc.config.DeploySystemDService {
		if mc.config.Verbose {
			log.Println("SSH deployment disabled, assuming servers are already running")
		}
		return nil
	}

	if mc.config.Verbose {
		log.Println("\n========== PHASE 1: DEPLOYMENT ==========")
	} else {
		fmt.Printf("📦 Deploying to %d nodes... ", mc.config.Targets.Len())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	numWorkers := minInt(mc.config.Targets.Len(), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.config.Targets, numWorkers,
		func(ctx context.Context, target Target, i int) error {
			return mc.deployToNode(target, true)
		})

	mc.handleDeploymentResults(results)

	if !results.AllSucceeded() {
		if mc.config.Verbose {
			log.Printf("⚠️  Some deployments failed, but continuing with successful nodes")
		}
		if err := results.AsError(); err != nil {
			return fmt.Errorf("deployment errors: %v", err)
		}
	}

	return nil
}

// Uninstall performs cleanup and uninstallation on all nodes
// Stops services, removes binaries, and cleans up directories in parallel
func (mc *Controller) Uninstall() error {
	if mc.config.Verbose {
		log.Println("\n========== PHASE: UNINSTALL ==========")
	} else {
		fmt.Printf("🧹 Uninstalling from %d nodes... ", mc.config.Targets.Len())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	numWorkers := minInt(mc.config.Targets.Len(), 32)
	results := workers.ProcessConcurrentlyWithIndexes(ctx, mc.config.Targets, numWorkers,
		func(ctx context.Context, target Target, i int) error {
			return mc.cleanupNode(ctx, target, false)
		})

	successCount := 0
	failCount := 0

	for _, result := range results.Items {
		if result.Err != nil {
			if mc.config.Verbose {
				log.Printf("❌ Failed to uninstall from %s: %v", result.Object.IP, result.Err)
			}
			failCount++
		} else {
			if mc.config.Verbose {
				log.Printf("✅ Successfully uninstalled from %s", result.Object.IP)
			}
			successCount++
		}
	}

	if mc.config.Verbose {
		log.Println("\n========== UNINSTALL COMPLETE ==========")
		log.Printf("Successful uninstalls: %d/%d", successCount, len(results.Items))
		if failCount > 0 {
			log.Printf("Failed uninstalls: %d", failCount)
		}
	} else {
		if failCount == 0 {
			fmt.Printf("Done (✅ %d/%d)\n", successCount, len(results.Items))
		} else {
			fmt.Printf("Done (✅ %d, ❌ %d)\n", successCount, failCount)
		}
	}

	if !results.AllSucceeded() {
		if err := results.AsError(); err != nil {
			return fmt.Errorf("uninstall errors: %v", err)
		}
	}

	return nil
}

// startService starts the gosmesh service on a node
// Routes to either startServiceWithDeployment or startServiceNoDeployment based on config
func (mc *Controller) startService(target Target) error {
	if mc.config.DeploySystemDService {
		return mc.startServiceWithDeployment(target)
	}
	return mc.startServiceNoDeployment(target)
}

// startTestViaHTTP sends an HTTP request to start a test on a node
func (mc *Controller) startTestViaHTTP(target Target) error {
	ip := target.IP
	port := mc.config.TestConfig.ApiServerPort
	if port == 0 {
		port = 44444 // Default API server port (must differ from controller API port)
	}

	// Start with defaults and apply mesh-specific overrides
	testConfig := GetDefaultMeshTestConfig()
	testConfig = testing.MergeTestConfig(testConfig, mc.config.TestConfig)
	testConfig.IPs = strings.Join(mc.config.Targets.GetIPs(), ",")
	testConfig.ReportTo = fmt.Sprintf("http://%s:%d/stats", mc.localIP, mc.config.APIPort)

	url := fmt.Sprintf("http://%s:%d/api/start", ip, port)
	if mc.config.Verbose {
		log.Printf("[%s] 📡 Sending HTTP POST to %s", ip, url)
	}

	// Convert config to JSON and send
	jsonData, err := json.Marshal(testConfig)
	if err != nil {
		return fmt.Errorf("[%s] failed to marshal config: %v", ip, err)
	}

	resp, err := mc.httpClient.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("[%s] failed to send start request: %v", ip, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("[%s] start request failed with status %d: %s", ip, resp.StatusCode, string(body))
	}

	if mc.config.Verbose {
		log.Printf("[%s] ✅ Test start request sent successfully", ip)
	}

	return nil
}

// stopTestViaHTTP sends an HTTP request to stop a running test on a node
func (mc *Controller) stopTestViaHTTP(target Target) error {
	ip := target.IP
	port := mc.config.TestConfig.ApiServerPort
	if port == 0 {
		port = 44444 // Default API server port (must differ from controller API port)
	}

	url := fmt.Sprintf("http://%s:%d/api/stop", ip, port)
	if mc.config.Verbose {
		log.Printf("[%s] 📡 Sending HTTP POST to %s", ip, url)
	}

	resp, err := mc.httpClient.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("[%s] failed to send stop request: %v", ip, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("[%s] stop request failed with status %d: %s", ip, resp.StatusCode, string(body))
	}

	if mc.config.Verbose {
		log.Printf("[%s] ✅ Test stop request sent successfully", ip)
	}

	return nil
}

// startServiceWithDeployment starts and verifies a deployed service
// Checks that the service is active, process is running, and port is listening
func (mc *Controller) startServiceWithDeployment(target Target) error {
	// Determine SSH host
	sshHost := target.SSHHost
	ip := target.IP
	if mc.config.Verbose {
		log.Printf("[%s] 🚀 Starting service...", ip)
	}

	if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("systemctl start %s", GoSmeshServiceName), "start service"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Failed to start service: %v", ip, err)
		}
		return fmt.Errorf("[%s] failed to start service: %v", ip, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] ⏳ Service start command completed, verifying...", ip)
	}

	// Verify service is running
	if mc.config.Verbose {
		log.Printf("[%s] 🔍 Checking service health...", ip)
	}
	// Build the port check command based on protocol
	var ssCmd string
	if mc.config.TestConfig.Protocol == "udp" {
		ssCmd = fmt.Sprintf("ss -uln | grep ':%d ' >/dev/null 2>&1", mc.config.TestConfig.Port)
	} else {
		ssCmd = fmt.Sprintf("ss -tln | grep ':%d ' >/dev/null 2>&1", mc.config.TestConfig.Port)
	}

	verifyCmd := fmt.Sprintf(`
		echo "[VERIFY] Quick service check..."
		if systemctl is-active %s >/dev/null 2>&1 && pgrep -f 'gosmesh run' >/dev/null 2>&1 && %s; then
			echo "[SUCCESS] Service active, process running, port listening"
			exit 0
		fi
		
		echo "[ERROR] Service verification failed"
		echo "Service active: $(systemctl is-active %s 2>/dev/null || echo 'inactive')"
		echo "Process running: $(pgrep -f 'gosmesh run' >/dev/null 2>&1 && echo 'yes' || echo 'no')"
		echo "Port listening: $(%s && echo 'yes' || echo 'no')"
		
		echo "Recent logs:"
		journalctl -u %s --no-pager --since "30 seconds ago" --lines=5 || true
		exit 1
	`, GoSmeshServiceName, ssCmd, GoSmeshServiceName, ssCmd, GoSmeshServiceName)

	if err := mc.deployHelper.Exec(sshHost, verifyCmd, "verify service health"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Service verification failed", ip)
		}
		return fmt.Errorf("[%s] service verification failed: %v", ip, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] ✅ SUCCESS: Service started and verified", ip)
	}

	return nil
}

// startServiceNoDeployment checks if a service is running by verifying port connectivity
// Assumes service is already deployed and just checks if the port is open
func (mc *Controller) startServiceNoDeployment(target Target) error {
	// Determine SSH host
	sshHost := target.SSHHost
	ip := target.IP

	if mc.config.Verbose {
		log.Printf("[%s] 🔍 Checking if service is running on port %d...", ip, mc.config.TestConfig.Port)
	}

	// Build the port check command based on protocol
	var portCheckCmd string
	if mc.config.TestConfig.Protocol == "udp" {
		portCheckCmd = fmt.Sprintf("ss -uln | grep -q ':%d ' && echo 'Port listening' || (echo 'Port not listening'; exit 1)", mc.config.TestConfig.Port)
	} else {
		portCheckCmd = fmt.Sprintf("ss -tln | grep -q ':%d ' && echo 'Port listening' || (echo 'Port not listening'; exit 1)", mc.config.TestConfig.Port)
	}

	if err := mc.deployHelper.Exec(sshHost, portCheckCmd, "check port connectivity"); err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] ❌ Port %d is not listening", ip, mc.config.TestConfig.Port)
		}
		return fmt.Errorf("[%s] port %d not listening: %v", ip, mc.config.TestConfig.Port, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] ✅ SUCCESS: Service is running (port %d is listening)", ip, mc.config.TestConfig.Port)
	}

	return nil
}

func (mc *Controller) generateSystemdUnit(binaryPath string) string {
	apiPort := mc.config.TestConfig.ApiServerPort
	if apiPort == 0 {
		apiPort = 44444
	}

	// Start gosmesh in server mode - it will listen for HTTP commands to start tests
	// The NetworkTester will start an HTTP API server and wait for /api/start requests
	cmd := fmt.Sprintf("%s server --api-port %d", binaryPath, apiPort)

	return fmt.Sprintf(`[Unit]
Description=GoSmesh Mesh Testing Service
After=network.target

[Service]
Type=simple
ExecStart=%s
StandardOutput=journal
StandardError=journal
Restart=no
KillMode=mixed
TimeoutStartSec=10
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`, cmd)
}

// deployToNode deploys gosmesh binary and service to a node
func (mc *Controller) deployToNode(target Target, start bool) error {
	// This is a stub - would contain actual deployment logic from cmd_mesh.go
	// For now, just log that we would deploy
	if mc.config.Verbose {
		log.Printf("[%s] Deploying gosmesh...", target.IP)
	}
	return mc.deployToNodeWithStart(target, start)
}

func (mc *Controller) checkAgentHealth(target Target) (bool, error) {
	ip := target.IP
	apiPort := mc.config.TestConfig.ApiServerPort
	if apiPort == 0 {
		apiPort = 44444
	}

	healthURL := fmt.Sprintf("http://%s:%d/health", ip, apiPort)
	if mc.config.Verbose {
		log.Printf("[%s] Checking if agent is already running at %s", ip, healthURL)
	}

	resp, err := mc.httpClient.Get(healthURL)
	if err != nil {
		if mc.config.Verbose {
			log.Printf("[%s] Agent not responding at %s (will deploy) - %v", ip, healthURL, err)
		}
		return false, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusOK {
		var healthResp map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&healthResp); err == nil {
			agentVersion, ok := healthResp["versionHash"].(string)
			controllerVersionHash := version.GetVersionHash()

			if ok && agentVersion == controllerVersionHash {
				if mc.config.Verbose {
					log.Printf("[%s] ✓ Agent already running with matching version hash %s - skipping deployment", ip, agentVersion)
				}
				return true, nil
			}

			if mc.config.Verbose {
				if ok {
					log.Printf("[%s] Agent <> controller mismatch (agent: %s, controller: %s) - will redeploy", ip, agentVersion, controllerVersionHash)
				} else {
					log.Printf("[%s] Agent version hash not reported - will redeploy", ip)
				}
			}
		}
	}

	return false, nil
}

func (mc *Controller) deployToNodeWithStart(target Target, startService bool) error {
	sshHost := target.SSHHost
	ip := target.IP
	if mc.config.Verbose {
		log.Printf("[%s] (%s) Starting deployment", ip, sshHost)
	}

	remoteBinary := filepath.Join(GoSmeshRemotePath, "gosmesh")

	// Step 0: Check if agent is already running with correct version via HTTP, in that case just do nothing
	agentHealthy, err := mc.checkAgentHealth(target)
	if err != nil {
		return err
	}

	if agentHealthy {
		if mc.config.Verbose {
			log.Printf("[%s] Agent is healthy", ip)
		}
		return nil
	}

	// Agent is not running or version mismatch - proceed with deployment

	// Get the path of the currently executing binary
	currentBinary, err := os.Executable()
	if os.Getenv("GOSMESH_EXECUTABLE_PATH") != "" {
		currentBinary = os.Getenv("GOSMESH_EXECUTABLE_PATH")
	}

	if err != nil {
		return fmt.Errorf("[%s] failed to get current binary path: %v", ip, err)
	}

	if mc.config.Verbose {
		log.Printf("[%s] Deployment via SSH to %s", ip, sshHost)
	}

	// Step 1: Test SSH connectivity first
	if mc.config.Verbose {
		log.Printf("[%s] Testing SSH connectivity...", sshHost)
	}
	if err := mc.deployHelper.Exec(sshHost, "echo 'SSH connection OK'", "connectivity test"); err != nil {
		return fmt.Errorf("[%s] SSH connectivity failed: %v", sshHost, err)
	}
	if mc.config.Verbose {
		log.Printf("[%s] SSH connectivity confirmed", ip)
	}

	// Step 2: Thorough cleanup of old processes and services
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := mc.cleanupNode(cleanupCtx, target, false); err != nil {
		return err
	}

	// Step 3: Create remote directory
	if mc.config.Verbose {
		log.Printf("[%s] Creating remote directory %s...", ip, GoSmeshRemotePath)
	}
	if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("mkdir -p %s", GoSmeshRemotePath), "create directory"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 4: Copy binary (use the currently executing binary)
	if mc.config.Verbose {
		log.Printf("[%s] Copying binary to remote...", ip)
	}
	if err := mc.deployHelper.SCP(currentBinary, fmt.Sprintf("%s:%s", sshHost, remoteBinary), "copy binary"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 5: Make executable
	if mc.config.Verbose {
		log.Printf("[%s] Making binary executable...", ip)
	}
	if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("chmod +x %s", remoteBinary), "make executable"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 6: Create systemd service file
	if mc.config.Verbose {
		log.Printf("[%s] Creating systemd service...", ip)
	}
	serviceContent := mc.generateSystemdUnit(remoteBinary)
	serviceTempPath := fmt.Sprintf("/tmp/gosmesh-mesh-%s.service", strings.ReplaceAll(ip, ".", "_"))
	if err := os.WriteFile(serviceTempPath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("[%s] failed to write service file: %v", ip, err)
	}
	defer func() {
		_ = os.Remove(serviceTempPath)
	}()

	// Copy service file to remote
	if err := mc.deployHelper.SCP(serviceTempPath, fmt.Sprintf("%s:/etc/systemd/system/%s.service", sshHost, GoSmeshServiceName), "copy service file"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 7: Reload systemd
	if mc.config.Verbose {
		log.Printf("[%s] Reloading systemd...", ip)
	}
	if err := mc.deployHelper.Exec(sshHost, "systemctl daemon-reload", "reload systemd"); err != nil {
		return fmt.Errorf("[%s] %v", ip, err)
	}

	// Step 8: Start service (optional)
	if startService {
		if mc.config.Verbose {
			log.Printf("[%s] Starting gosmesh service...", ip)
		}
		if err := mc.deployHelper.Exec(sshHost, fmt.Sprintf("systemctl start %s", GoSmeshServiceName), "start service"); err != nil {
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

// startAPIServer starts the HTTP API server for stats reporting
func (mc *Controller) startAPIServer() {
	if mc.config.Verbose {
		log.Printf("Starting API server on port %d", mc.config.APIPort)
	}

	mux := http.NewServeMux()

	// Stats endpoint
	mux.HandleFunc("/stats", mc.handleWorkerStats)

	// Get stats (for external monitoring)
	mux.HandleFunc("/getStats", mc.handleGetStats)
	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, version.GetVersionHash())
	})

	mc.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", mc.config.APIPort),
		Handler: mux,
	}

	go func() {
		if err := mc.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
			return
		}
		if mc.config.Verbose {
			log.Printf("API server listening on :%d", mc.config.APIPort)
		}
	}()
}

// cleanup performs cleanup operations
func (mc *Controller) cleanup() {
	if mc.httpServer != nil {
		_ = mc.httpServer.Close()
	}

	if mc.config.Verbose {
		log.Println("Cleanup complete")
	}
}
