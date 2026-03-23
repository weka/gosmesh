package main

import (
	"fmt"
	"os"

	"github.com/weka/gosmesh/internal/commands"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "run":
		commands.RunCommand(args)
	case "mesh":
		commands.MeshCommand(args)
	case "server":
		commands.ServerCommand(args)
	case "uninstall":
		commands.UninstallCommand(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `GoSmesh - High-Performance Network Testing Tool

Usage:
  gosmesh <command> [options]

Commands:
  run        Run network test directly (used by systemd)
  mesh       Deploy and orchestrate mesh testing across multiple nodes
  server     Start control server that listens for test commands via HTTP
  uninstall  Remove gosmesh deployment from specified nodes

Examples:
  # Run test directly on nodes
  gosmesh run --ips 10.0.0.1,10.0.0.2,10.0.0.3 --duration 5m

  # Deploy mesh test from single controller
  gosmesh mesh --ips 10.0.0.1,10.0.0.2,10.0.0.3 --duration 5m

  # Start control server
  gosmesh server --port 8080

  # Clean up deployment
  gosmesh uninstall --ips 10.0.0.1,10.0.0.2,10.0.0.3

Use "gosmesh <command> --help" for more information about a command.
`)
}
