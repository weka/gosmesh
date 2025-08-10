package main

import (
	"fmt"
	"os"
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
		RunCommand(args)
	case "mesh":
		MeshCommand(args)
	case "uninstall":
		UninstallCommand(args)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `GoNet - High-Performance Network Testing Tool

Usage:
  gonet <command> [options]

Commands:
  run        Run network test directly (used by systemd)
  mesh       Deploy and orchestrate mesh testing across multiple nodes
  uninstall  Remove gonet deployment from specified nodes

Examples:
  # Run test directly on nodes
  gonet run --ips 10.0.0.1,10.0.0.2,10.0.0.3 --duration 5m

  # Deploy mesh test from single controller
  gonet mesh --ips 10.0.0.1,10.0.0.2,10.0.0.3 --duration 5m

  # Clean up deployment
  gonet uninstall --ips 10.0.0.1,10.0.0.2,10.0.0.3

Use "gonet <command> --help" for more information about a command.
`)
}