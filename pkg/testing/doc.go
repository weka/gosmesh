// Package testing provides the high-level orchestration for network performance testing.
//
// NetworkTester is the main entry point for using GoSmesh as a library. It manages
// the creation and coordination of multiple connections to target IPs, collection
// of statistics, and generation of reports.
//
// Basic workflow:
//  1. Create a NetworkTester with NewNetworkTester()
//  2. Configure optimization flags if desired
//  3. Call Start() to begin testing
//  4. Wait for completion with Wait() or Stop() early
//  5. Get results with GenerateFinalReport()
//
// The tester automatically:
//   - Starts echo servers on the local node
//   - Creates connections to all target IPs
//   - Measures throughput, latency, jitter, and packet loss
//   - Generates periodic reports at configurable intervals
//   - Detects and reports network anomalies
//
// For more detailed examples and options, see the pkg/testing/tester.go documentation.
package testing
