// Package mesh provides the MeshController for orchestrating distributed network tests
// across multiple nodes.
//
// The MeshController can be used in two modes:
//
// 1. Automatic SSH Deployment Mode
//
// Automatically builds, deploys, and starts gosmesh on remote nodes:
//
//	config := &mesh.Config{
//	    IPs:      "10.0.0.1,10.0.0.2,10.0.0.3",
//	    DeploySystemDService:   true,
//	    SSHHosts: "root@h1,root@h2,root@h3",
//	    Duration: 5 * time.Minute,
//	}
//	controller := mesh.NewController(config)
//	controller.Run()  // Deploy, Start, Wait all in one call
//
// 2. Manual Mode (Servers Already Running)
//
// Assumes services are already deployed and running:
//
//	config := &mesh.Config{
//	    IPs:      "10.0.0.1,10.0.0.2,10.0.0.3",
//	    DeploySystemDService:   false,
//	    Duration: 5 * time.Minute,
//	}
//	controller := mesh.NewController(config)
//	controller.Start()  // Start the test
//	controller.Wait()   // Monitor until complete
//	stats := controller.GetStats()  // Get results
//
// Key Methods:
//   - Deploy() - Perform SSH deployment (optional)
//   - Start() - Start the test services
//   - Wait() - Monitor test progress
//   - Run() - Complete workflow (Deploy if SSH enabled, Start, Wait)
//   - GetStats() - Get current statistics as structured data
//
// The MeshController automatically:
// - Detects local IP from the provided IP list
// - Manages concurrent deployment/start operations
// - Aggregates statistics from all nodes
// - Handles signals and graceful shutdown
package mesh
