package mesh

import (
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/weka/gosmesh/pkg/workers"
)

// parseIPs parses comma-separated IP list
func parseIPs(ips string) []string {
	if ips == "" {
		return []string{}
	}

	parts := strings.Split(ips, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// detectLocalIP finds the local IP that matches one in the mesh list
func detectLocalIP(ipList []string) string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	// Create a map for quick lookup
	ipMap := make(map[string]bool)
	for _, ip := range ipList {
		ipMap[ip] = true
	}

	// Check each interface for matching IP
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
				ipStr := ip.String()
				if ipMap[ipStr] {
					return ipStr
				}
			}
		}
	}

	return ""
}

// getLocalIPAddress gets any local IPv4 address
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

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// handleDeploymentResults processes deployment results
func (mc *Controller) handleDeploymentResults(results *workers.Results[target]) {
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

// handleServiceStartResults processes service start results
func (mc *Controller) handleServiceStartResults(results *workers.Results[target]) {
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
