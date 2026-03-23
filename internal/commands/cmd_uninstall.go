package commands

import (
	"flag"
	"fmt"
	"github.com/weka/gosmesh/pkg/mesh"
	"log"
	"os"
)

func UninstallCommand(args []string) {
	config := &mesh.Config{
		DeploySystemDService: true,
		TestConfig:           mesh.GetDefaultMeshTestConfig(),
	}
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	var ips string
	var sshHosts string

	fs.StringVar(&ips, "ips", "", "Comma-separated list of IPs to uninstall from")
	fs.StringVar(&sshHosts, "ssh-hosts", "", "Comma-separated list of SSH hosts (user@host1,user@host2,...)")
	fs.BoolVar(&config.Verbose, "verbose", false, "Verbose output")

	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}

	if ips == "" {
		fmt.Fprintf(os.Stderr, "Usage: gosmesh uninstall --ips ip1,ip2,ip3\n")
		fs.PrintDefaults()
		os.Exit(1)
	}

	if err := config.FillTargets(ips, sshHosts); err != nil {
		log.Fatal(err)
	}

	// parse IPs and SSH hosts, and fill them in into configuration
	controller := mesh.NewController(config)
	if err := controller.Uninstall(); err != nil {
		log.Fatal(err)
	}

}
