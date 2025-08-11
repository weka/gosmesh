package performance

import (
	"fmt"
	"net"
	"runtime"
	"syscall"
	"unsafe"
)

// Socket options for hardware offloading
const (
	// TCP offload options
	TCP_NODELAY      = 1
	TCP_CORK         = 3
	TCP_QUICKACK     = 12
	TCP_DEFER_ACCEPT = 9

	// Socket offload options
	SO_ZEROCOPY              = 60
	SO_BUSY_POLL             = 46
	SO_INCOMING_CPU          = 49
	SO_ATTACH_BPF            = 50
	SO_ATTACH_REUSEPORT_EBPF = 52

	// Ethtool commands for offload configuration
	ETHTOOL_GSET    = 0x00000001
	ETHTOOL_SSET    = 0x00000002
	ETHTOOL_GRXCSUM = 0x00000014
	ETHTOOL_SRXCSUM = 0x00000015
	ETHTOOL_GTXCSUM = 0x00000016
	ETHTOOL_STXCSUM = 0x00000017
	ETHTOOL_GSG     = 0x00000018
	ETHTOOL_SSG     = 0x00000019
	ETHTOOL_GTSO    = 0x0000001e
	ETHTOOL_STSO    = 0x0000001f
	ETHTOOL_GGSO    = 0x00000023
	ETHTOOL_SGSO    = 0x00000024
	ETHTOOL_GGRO    = 0x0000002b
	ETHTOOL_SGRO    = 0x0000002c

	// ioctl commands
	SIOCETHTOOL  = 0x8946
	SIOCGIFNAME  = 0x8910
	SIOCGIFFLAGS = 0x8913
	SIOCSIFFLAGS = 0x8914
)

// OffloadConfig manages hardware offloading settings
type OffloadConfig struct {
	EnableTSO      bool // TCP Segmentation Offload
	EnableGSO      bool // Generic Segmentation Offload
	EnableGRO      bool // Generic Receive Offload
	EnableLRO      bool // Large Receive Offload
	EnableChecksum bool // Checksum offload
	EnableZeroCopy bool // Zero-copy send
	BusyPoll       int  // Busy poll microseconds
}

// NetworkOffloader manages network hardware offloading
type NetworkOffloader struct {
	config    OffloadConfig
	ifaceName string
	fd        int
}

// NewNetworkOffloader creates a new network offloader
func NewNetworkOffloader(interfaceName string) *NetworkOffloader {
	return &NetworkOffloader{
		ifaceName: interfaceName,
		config: OffloadConfig{
			EnableTSO:      true,
			EnableGSO:      true,
			EnableGRO:      true,
			EnableLRO:      true,
			EnableChecksum: true,
			EnableZeroCopy: true,
			BusyPoll:       50, // 50 microseconds
		},
	}
}

// EnableAllOffloads enables all hardware offloading features
func (n *NetworkOffloader) EnableAllOffloads() error {
	// Get socket for ioctl
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("failed to create socket: %v", err)
	}
	defer syscall.Close(fd)
	n.fd = fd

	// Enable TSO (TCP Segmentation Offload)
	if n.config.EnableTSO {
		if err := n.setEthtoolFlag(ETHTOOL_STSO, 1); err != nil {
			fmt.Printf("Warning: Could not enable TSO: %v\n", err)
		}
	}

	// Enable GSO (Generic Segmentation Offload)
	if n.config.EnableGSO {
		if err := n.setEthtoolFlag(ETHTOOL_SGSO, 1); err != nil {
			fmt.Printf("Warning: Could not enable GSO: %v\n", err)
		}
	}

	// Enable GRO (Generic Receive Offload)
	if n.config.EnableGRO {
		if err := n.setEthtoolFlag(ETHTOOL_SGRO, 1); err != nil {
			fmt.Printf("Warning: Could not enable GRO: %v\n", err)
		}
	}

	// Enable checksum offloading
	if n.config.EnableChecksum {
		if err := n.setEthtoolFlag(ETHTOOL_SRXCSUM, 1); err != nil {
			fmt.Printf("Warning: Could not enable RX checksum: %v\n", err)
		}
		if err := n.setEthtoolFlag(ETHTOOL_STXCSUM, 1); err != nil {
			fmt.Printf("Warning: Could not enable TX checksum: %v\n", err)
		}
	}

	// Enable scatter-gather
	if err := n.setEthtoolFlag(ETHTOOL_SSG, 1); err != nil {
		fmt.Printf("Warning: Could not enable scatter-gather: %v\n", err)
	}

	return nil
}

// setEthtoolFlag sets an ethtool flag via ioctl
func (n *NetworkOffloader) setEthtoolFlag(cmd uint32, value uint32) error {
	type ethtoolValue struct {
		cmd  uint32
		data uint32
	}

	// Prepare interface request
	type ifreq struct {
		name [16]byte
		data uintptr
	}

	req := ifreq{}
	copy(req.name[:], n.ifaceName)

	ethValue := ethtoolValue{
		cmd:  cmd,
		data: value,
	}

	req.data = uintptr(unsafe.Pointer(&ethValue))

	// Execute ioctl
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(n.fd),
		SIOCETHTOOL,
		uintptr(unsafe.Pointer(&req)),
	)

	if errno != 0 {
		return errno
	}

	return nil
}

// OptimizeTCPSocket optimizes a TCP socket for high throughput
func (n *NetworkOffloader) OptimizeTCPSocket(conn *net.TCPConn) error {
	// Get the file descriptor
	file, err := conn.File()
	if err != nil {
		return err
	}
	defer file.Close()
	fd := int(file.Fd())

	// Disable Nagle's algorithm for low latency
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, TCP_NODELAY, 1); err != nil {
		return fmt.Errorf("failed to set TCP_NODELAY: %v", err)
	}

	// Enable TCP quick acknowledgment
	if err := syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, TCP_QUICKACK, 1); err != nil {
		return fmt.Errorf("failed to set TCP_QUICKACK: %v", err)
	}

	// Enable zero-copy send if available
	if n.config.EnableZeroCopy {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, SO_ZEROCOPY, 1); err != nil {
			// Zero-copy might not be available, continue without it
			fmt.Printf("Warning: Zero-copy not available: %v\n", err)
		}
	}

	// Set busy polling for lower latency
	if n.config.BusyPoll > 0 {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, SO_BUSY_POLL, n.config.BusyPoll); err != nil {
			fmt.Printf("Warning: Busy poll not available: %v\n", err)
		}
	}

	// Set large socket buffers for high throughput
	bufSize := 16 * 1024 * 1024 // 16MB
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, bufSize)
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, bufSize)

	// Enable TCP_CORK for batching small writes
	syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, TCP_CORK, 0)

	return nil
}

// OptimizeUDPSocket optimizes a UDP socket for high throughput
func (n *NetworkOffloader) OptimizeUDPSocket(conn *net.UDPConn) error {
	// Get the file descriptor
	file, err := conn.File()
	if err != nil {
		return err
	}
	defer file.Close()
	fd := int(file.Fd())

	// Enable zero-copy send if available
	if n.config.EnableZeroCopy {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, SO_ZEROCOPY, 1); err != nil {
			fmt.Printf("Warning: Zero-copy not available for UDP: %v\n", err)
		}
	}

	// Set busy polling
	if n.config.BusyPoll > 0 {
		if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, SO_BUSY_POLL, n.config.BusyPoll); err != nil {
			fmt.Printf("Warning: Busy poll not available for UDP: %v\n", err)
		}
	}

	// Set large socket buffers
	bufSize := 16 * 1024 * 1024 // 16MB
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, bufSize)
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, bufSize)

	return nil
}

// MultiQueueNIC manages multiple NIC queues for better parallelism
type MultiQueueNIC struct {
	ifaceName string
	numQueues int
	queueMap  map[int]int // CPU to queue mapping
	rssKey    []byte
}

// NewMultiQueueNIC creates a multi-queue NIC manager
func NewMultiQueueNIC(interfaceName string) (*MultiQueueNIC, error) {
	nic := &MultiQueueNIC{
		ifaceName: interfaceName,
		queueMap:  make(map[int]int),
	}

	// Detect number of queues
	if err := nic.detectQueues(); err != nil {
		return nil, err
	}

	// Configure RSS (Receive Side Scaling)
	if err := nic.configureRSS(); err != nil {
		return nil, err
	}

	return nic, nil
}

// detectQueues detects the number of hardware queues
func (m *MultiQueueNIC) detectQueues() error {
	// Read from sysfs
	// This is a simplified implementation
	m.numQueues = runtime.NumCPU() // Assume one queue per CPU

	// Map CPUs to queues
	for i := 0; i < m.numQueues; i++ {
		m.queueMap[i] = i
	}

	return nil
}

// configureRSS configures Receive Side Scaling
func (m *MultiQueueNIC) configureRSS() error {
	// Generate RSS key (40 bytes for Toeplitz hash)
	m.rssKey = make([]byte, 40)
	for i := range m.rssKey {
		m.rssKey[i] = byte(i)
	}

	// Configure indirection table
	// This would normally use ethtool or device-specific APIs

	return nil
}

// GetQueueForCPU returns the optimal queue for a CPU
func (m *MultiQueueNIC) GetQueueForCPU(cpu int) int {
	if queue, ok := m.queueMap[cpu]; ok {
		return queue
	}
	return cpu % m.numQueues
}

// ZeroCopySender implements zero-copy sending
type ZeroCopySender struct {
	fd         int
	msgControl []byte
	iovec      []syscall.Iovec
}

// NewZeroCopySender creates a zero-copy sender
func NewZeroCopySender(conn net.Conn) (*ZeroCopySender, error) {
	var fd int

	switch c := conn.(type) {
	case *net.TCPConn:
		file, err := c.File()
		if err != nil {
			return nil, err
		}
		fd = int(file.Fd())
	case *net.UDPConn:
		file, err := c.File()
		if err != nil {
			return nil, err
		}
		fd = int(file.Fd())
	default:
		return nil, fmt.Errorf("unsupported connection type")
	}

	// Enable zero-copy
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, SO_ZEROCOPY, 1); err != nil {
		return nil, fmt.Errorf("failed to enable zero-copy: %v", err)
	}

	return &ZeroCopySender{
		fd:         fd,
		msgControl: make([]byte, 1024),
		iovec:      make([]syscall.Iovec, 16),
	}, nil
}

// SendZeroCopy sends data with zero-copy
// The actual implementation is platform-specific in offload_linux.go and offload_darwin.go
func (z *ZeroCopySender) SendZeroCopy(buffers [][]byte) (int, error) {
	return z.SendBatchLinux(buffers)
}

// BatchSender implements batched sending for efficiency
type BatchSender struct {
	conn       net.Conn
	batchSize  int
	buffers    [][]byte
	totalBytes int
}

// NewBatchSender creates a batch sender
func NewBatchSender(conn net.Conn, batchSize int) *BatchSender {
	return &BatchSender{
		conn:      conn,
		batchSize: batchSize,
		buffers:   make([][]byte, 0, batchSize),
	}
}

// Add adds data to the batch
func (b *BatchSender) Add(data []byte) error {
	b.buffers = append(b.buffers, data)
	b.totalBytes += len(data)

	if len(b.buffers) >= b.batchSize {
		return b.Flush()
	}

	return nil
}

// Flush sends all batched data
func (b *BatchSender) Flush() error {
	if len(b.buffers) == 0 {
		return nil
	}

	// Combine buffers for single syscall
	combined := make([]byte, 0, b.totalBytes)
	for _, buf := range b.buffers {
		combined = append(combined, buf...)
	}

	_, err := b.conn.Write(combined)

	// Reset batch
	b.buffers = b.buffers[:0]
	b.totalBytes = 0

	return err
}
