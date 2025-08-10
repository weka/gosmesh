package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type UninstallConfig struct {
	IPs      string
	SSHHosts string
}

func UninstallCommand(args []string) {
	config := &UninstallConfig{}
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)

	fs.StringVar(&config.IPs, "ips", "", "Comma-separated list of IPs to uninstall from")
	fs.StringVar(&config.SSHHosts, "ssh-hosts", "", "Comma-separated list of SSH hosts (user@host1,user@host2,...)")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if config.IPs == "" {
		fmt.Fprintf(os.Stderr, "Usage: gosmesh uninstall --ips ip1,ip2,ip3\n")
		fs.PrintDefaults()
		os.Exit(1)
	}

	uninstaller := NewUninstaller(config)
	uninstaller.Run()
}

type Uninstaller struct {
	config   *UninstallConfig
	ipList   []string
	sshHosts []string
	localIP  string
}

func NewUninstaller(config *UninstallConfig) *Uninstaller {
	ipList := parseIPs(config.IPs)
	localIP := detectLocalIP(ipList)
	
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

	return &Uninstaller{
		config:   config,
		ipList:   ipList,
		sshHosts: sshHosts,
		localIP:  localIP,
	}
}

func (u *Uninstaller) Run() {
	log.Printf("Starting uninstallation from nodes: %v", u.ipList)

	var wg sync.WaitGroup
	for i, ip := range u.ipList {
		wg.Add(1)
		go func(nodeIP string, index int) {
			defer wg.Done()
			if err := u.uninstallFromNode(nodeIP, index); err != nil {
				log.Printf("Failed to uninstall from %s: %v", nodeIP, err)
			} else {
				log.Printf("Successfully uninstalled from %s", nodeIP)
			}
		}(ip, i)
	}
	wg.Wait()

	log.Println("Uninstallation complete")
}

func (u *Uninstaller) uninstallFromNode(ip string, index int) error {
	serviceName := "gosmesh-mesh"
	remoteDir := "/opt/gosmesh"

	if ip == u.localIP {
		// Uninstall locally
		log.Printf("Uninstalling from local node %s", ip)

		// Stop service
		cmd := exec.Command("systemctl", "stop", serviceName)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to stop service: %v", err)
		}

		// Disable service
		cmd = exec.Command("systemctl", "disable", serviceName)
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to disable service: %v", err)
		}

		// Remove service file
		servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)
		if err := os.Remove(servicePath); err != nil {
			log.Printf("Warning: failed to remove service file: %v", err)
		}

		// Reload systemd
		cmd = exec.Command("systemctl", "daemon-reload")
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: failed to reload systemd: %v", err)
		}

		// Remove binary and directory
		if err := os.RemoveAll(remoteDir); err != nil {
			log.Printf("Warning: failed to remove directory: %v", err)
		}
	} else {
		// Uninstall remotely via SSH
		log.Printf("Uninstalling from remote node %s", ip)
		
		// Use provided SSH host or default to root@ip
		var sshHost string
		if len(u.sshHosts) > index {
			sshHost = u.sshHosts[index]
		} else {
			sshHost = fmt.Sprintf("root@%s", ip)
		}

		// Stop service
		cmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10", sshHost, fmt.Sprintf("systemctl stop %s 2>/dev/null", serviceName))
		cmd.Run() // Ignore errors as service might not exist

		// Disable service
		cmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10", sshHost, fmt.Sprintf("systemctl disable %s 2>/dev/null", serviceName))
		cmd.Run()

		// Remove service file
		cmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10", sshHost, fmt.Sprintf("rm -f /etc/systemd/system/%s.service", serviceName))
		cmd.Run()

		// Reload systemd
		cmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10", sshHost, "systemctl daemon-reload")
		cmd.Run()

		// Remove binary and directory
		cmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10", sshHost, fmt.Sprintf("rm -rf %s", remoteDir))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to remove remote directory: %v", err)
		}
	}

	return nil
}