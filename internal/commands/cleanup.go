package commands

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// CleanupNodeOptions holds configuration for node cleanup
type CleanupNodeOptions struct {
	IP          string
	SSHHost     string
	Verbose     bool
	ServiceName string
	RemoteDir   string
}

// CleanupNode performs thorough cleanup on a single node
func CleanupNode(ctx context.Context, opts CleanupNodeOptions) error {
	serviceName := opts.ServiceName
	if serviceName == "" {
		serviceName = "gosmesh-mesh"
	}

	remoteDir := opts.RemoteDir
	if remoteDir == "" {
		remoteDir = "/opt/gosmesh"
	}
	remoteBinary := fmt.Sprintf("%s/gosmesh", remoteDir)

	// Stop service if it exists
	if opts.Verbose {
		log.Printf("[%s] Stopping existing service...", opts.IP)
	}
	stopCmd := fmt.Sprintf("systemctl stop %s || true", serviceName)
	if err := execSSHWithContext(ctx, opts.SSHHost, stopCmd, "stop service", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] stop service failed: %v", opts.IP, err)
	}

	// Disable service if it exists
	if opts.Verbose {
		log.Printf("[%s] Disabling existing service...", opts.IP)
	}
	disableCmd := fmt.Sprintf("systemctl disable %s || true", serviceName)
	if err := execSSHWithContext(ctx, opts.SSHHost, disableCmd, "disable service", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] disable service failed: %v", opts.IP, err)
	}

	// Reset failed state (redirect stderr to avoid hanging)
	if opts.Verbose {
		log.Printf("[%s] Resetting failed state...", opts.IP)
	}
	resetCmd := fmt.Sprintf("systemctl reset-failed %s 2>/dev/null || true", serviceName)
	if err := execSSHWithContext(ctx, opts.SSHHost, resetCmd, "reset failed state", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] reset failed state failed: %v", opts.IP, err)
	}

	// Kill processes running specifically from the remote directory path using their executable location
	if opts.Verbose {
		log.Printf("[%s] Killing processes from %s...", opts.IP, remoteDir)
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
`, remoteBinary, remoteBinary, remoteDir)
	if err := execSSHWithContext(ctx, opts.SSHHost, killCmd, "kill processes", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] kill processes failed: %v", opts.IP, err)
	}

	// Force kill any remaining processes
	if opts.Verbose {
		log.Printf("[%s] Force killing any remaining processes from %s...", opts.IP, remoteDir)
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
`, remoteBinary, remoteBinary, remoteDir)
	if err := execSSHWithContext(ctx, opts.SSHHost, forceKillCmd, "force kill processes", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] force kill processes failed: %v", opts.IP, err)
	}

	// Remove service file
	if opts.Verbose {
		log.Printf("[%s] Removing service file...", opts.IP)
	}
	removeServiceCmd := fmt.Sprintf("rm -f /etc/systemd/system/%s.service", serviceName)
	if err := execSSHWithContext(ctx, opts.SSHHost, removeServiceCmd, "remove service file", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] remove service file failed: %v", opts.IP, err)
	}

	// Remove binary and directory
	if opts.Verbose {
		log.Printf("[%s] Removing binary and directory...", opts.IP)
	}
	removeCmd := fmt.Sprintf("rm -rf %s", remoteDir)
	if err := execSSHWithContext(ctx, opts.SSHHost, removeCmd, "remove directory", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] remove directory failed: %v", opts.IP, err)
	}

	// Reload systemd
	if opts.Verbose {
		log.Printf("[%s] Reloading systemd...", opts.IP)
	}
	reloadCmd := "systemctl daemon-reload"
	if err := execSSHWithContext(ctx, opts.SSHHost, reloadCmd, "reload systemd", opts.Verbose); err != nil {
		return fmt.Errorf("[%s] reload systemd failed: %v", opts.IP, err)
	}

	if opts.Verbose {
		log.Printf("[%s] Cleanup completed successfully", opts.IP)
	}

	return nil
}

// execSSHWithContext executes a remote command via SSH with context support
func execSSHWithContext(ctx context.Context, sshHost, command, description string, verbose bool) error {
	if verbose {
		log.Printf("[SSH] Executing %s on %s with context", description, sshHost)
	}

	// Use CommandContext for proper context handling
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=1",
		sshHost, command)

	if verbose {
		log.Printf("[SSH] Command: ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o BatchMode=yes -o ServerAliveInterval=5 -o ServerAliveCountMax=1 %s '%s'", sshHost, command)
	}

	output, err := cmd.CombinedOutput()

	if err != nil {
		if verbose {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode := exitErr.ExitCode()
				log.Printf("[SSH] Command failed with exit code %d", exitCode)
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

	if verbose {
		if len(output) > 0 {
			log.Printf("[SSH] Output: %s", strings.TrimSpace(string(output)))
		}
		log.Printf("[SSH] %s completed successfully", description)
	}
	return nil
}
