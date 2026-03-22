package mesh

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// SSHDeployer handles SSH-based deployment to remote nodes
type SSHDeployer struct {
	verbose bool
}

// NewSSHDeployer creates a new SSH deployer
func NewSSHDeployer(verbose bool) *SSHDeployer {
	return &SSHDeployer{verbose: verbose}
}

// Exec executes a remote command via SSH
func (d *SSHDeployer) Exec(sshHost, command, description string) error {
	if d.verbose {
		log.Printf("[SSH] Executing %s on %s", description, sshHost)
		log.Printf("[SSH] Command: ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o BatchMode=yes -o ServerAliveInterval=5 -o ServerAliveCountMax=1 %s '%s'", sshHost, command)
	}

	cmd := exec.Command("ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=1",
		sshHost, command)

	output, err := cmd.CombinedOutput()

	if err != nil {
		if d.verbose {
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

	if d.verbose {
		if len(output) > 0 {
			log.Printf("[SSH] Output: %s", strings.TrimSpace(string(output)))
		}
		log.Printf("[SSH] %s completed successfully", description)
	}
	return nil
}

// ExecWithContext executes a remote command via SSH with context support
func (d *SSHDeployer) ExecWithContext(ctx context.Context, sshHost, command, description string) error {
	if d.verbose {
		log.Printf("[SSH] Executing %s on %s with context", description, sshHost)
	}

	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=1",
		sshHost, command)

	if d.verbose {
		log.Printf("[SSH] Command: ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o BatchMode=yes -o ServerAliveInterval=5 -o ServerAliveCountMax=1 %s '%s'", sshHost, command)
	}

	output, err := cmd.CombinedOutput()

	if err != nil {
		if d.verbose {
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

	if d.verbose {
		if len(output) > 0 {
			log.Printf("[SSH] Output: %s", strings.TrimSpace(string(output)))
		}
		log.Printf("[SSH] %s completed successfully", description)
	}
	return nil
}

// SCP copies a file via SCP
func (d *SSHDeployer) SCP(source, dest, description string) error {
	if d.verbose {
		log.Printf("[SCP] Executing %s: %s -> %s", description, source, dest)
		log.Printf("[SCP] Command: scp -o StrictHostKeyChecking=no -o ConnectTimeout=10 -o BatchMode=yes %s %s", source, dest)
	}

	cmd := exec.Command("scp",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		source, dest)

	output, err := cmd.CombinedOutput()

	if err != nil {
		if d.verbose {
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

	if d.verbose {
		log.Printf("[SCP] %s completed successfully", description)
	}

	return nil
}
