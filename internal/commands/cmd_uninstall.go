package commands

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
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
	// Use provided SSH host or default to root@ip
	var sshHost string
	if len(u.sshHosts) > index {
		sshHost = u.sshHosts[index]
	} else {
		sshHost = fmt.Sprintf("root@%s", ip)
	}

	// Use the shared cleanup function
	ctx := context.Background()
	opts := CleanupNodeOptions{
		IP:          ip,
		SSHHost:     sshHost,
		Verbose:     true, // Always verbose for uninstall
		ServiceName: "gosmesh-mesh",
		RemoteDir:   "/opt/gosmesh",
	}

	return CleanupNode(ctx, opts)
}
