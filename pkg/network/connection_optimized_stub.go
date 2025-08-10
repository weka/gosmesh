//go:build !linux

package network

import (
	"context"
	"net"
)

// OptimizedConnection stub for non-Linux platforms
type OptimizedConnection struct {
	*Connection
}

// NewOptimizedConnection creates a regular connection on non-Linux platforms
func NewOptimizedConnection(localIP, targetIP string, port int, protocol string, packetSize, pps, id int) *OptimizedConnection {
	// On non-Linux platforms, just create a regular connection
	baseConn := NewConnection(localIP, targetIP, port, protocol, packetSize, pps, id)
	
	return &OptimizedConnection{
		Connection: baseConn,
	}
}

// Start just uses the base connection's Start method on non-Linux platforms
func (c *OptimizedConnection) Start(ctx context.Context) error {
	return c.Connection.Start(ctx)
}

// applyOptimizedTCPOptions is a no-op on non-Linux platforms
func (c *Connection) applyOptimizedTCPOptions(tcpConn *net.TCPConn) error {
	// No optimizations available on non-Linux platforms
	return nil
}