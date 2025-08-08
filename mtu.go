package main

import (
	"fmt"
	"net"
)

// GetMTU returns the MTU for the interface that has the given IP
func GetMTU(ipStr string) (int, error) {
	targetIP := net.ParseIP(ipStr)
	if targetIP == nil {
		return 0, fmt.Errorf("invalid IP: %s", ipStr)
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}

	for _, iface := range interfaces {
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

			if ip != nil && ip.Equal(targetIP) {
				// Found the interface with our IP
				if iface.MTU > 0 {
					return iface.MTU, nil
				}
				// Default to 1500 if MTU is not set
				return 1500, nil
			}
		}
	}

	// Default MTU if interface not found
	return 1500, nil
}

// CalculateOptimalPacketSize returns the optimal packet size based on MTU
// Subtracts IP header (20 bytes) and UDP/TCP header (8/20 bytes)
func CalculateOptimalPacketSize(mtu int, protocol string) int {
	ipHeaderSize := 20
	
	var protocolHeaderSize int
	if protocol == "udp" {
		protocolHeaderSize = 8
	} else {
		protocolHeaderSize = 20 // TCP
	}
	
	// Calculate maximum payload size
	maxPayload := mtu - ipHeaderSize - protocolHeaderSize
	
	// Ensure we have at least space for our packet header (16 bytes for sequence + timestamp)
	if maxPayload < 16 {
		return 1024 // fallback to safe default
	}
	
	return maxPayload
}