//go:build linux
// +build linux

package system

import (
	"fmt"
	"net"
	"sync"
	"syscall"
	"unsafe"

	"github.com/weka/gosmesh/pkg/network"
)

// io_uring syscall numbers for Linux
const (
	SYS_IO_URING_SETUP    = 425
	SYS_IO_URING_ENTER    = 426
	SYS_IO_URING_REGISTER = 427
)

// io_uring operation codes
const (
	IORING_OP_NOP     = 0
	IORING_OP_READV   = 1
	IORING_OP_WRITEV  = 2
	IORING_OP_READ    = 22
	IORING_OP_WRITE   = 23
	IORING_OP_SEND    = 26
	IORING_OP_RECV    = 27
	IORING_OP_SENDMSG = 9
	IORING_OP_RECVMSG = 10
	IORING_OP_ACCEPT  = 13
	IORING_OP_CONNECT = 16
)

// io_uring setup flags
const (
	IORING_SETUP_IOPOLL        = 1 << 0
	IORING_SETUP_SQPOLL        = 1 << 1
	IORING_SETUP_SQ_AFF        = 1 << 2
	IORING_SETUP_CQSIZE        = 1 << 3
	IORING_SETUP_SINGLE_ISSUER = 1 << 12
	IORING_SETUP_DEFER_TASKRUN = 1 << 13
)

// io_uring enter flags
const (
	IORING_ENTER_GETEVENTS = 1 << 0
	IORING_ENTER_SQ_WAKEUP = 1 << 1
)

// io_uring params structure
type ioUringParams struct {
	sqEntries    uint32
	cqEntries    uint32
	flags        uint32
	sqThreadCPU  uint32
	sqThreadIdle uint32
	features     uint32
	resv         [4]uint32
	sqOff        ioSqringOffsets
	cqOff        ioCqringOffsets
}

type ioSqringOffsets struct {
	head        uint32
	tail        uint32
	ringMask    uint32
	ringEntries uint32
	flags       uint32
	dropped     uint32
	array       uint32
	resv        [3]uint32
}

type ioCqringOffsets struct {
	head        uint32
	tail        uint32
	ringMask    uint32
	ringEntries uint32
	overflow    uint32
	cqes        uint32
	flags       uint32
	resv        [3]uint32
}

// Submission Queue Entry
type ioUringSQE struct {
	opcode      uint8
	flags       uint8
	ioprio      uint16
	fd          int32
	off         uint64
	addr        uint64
	len         uint32
	unionFlags  uint32
	userData    uint64
	bufIndex    uint16
	personality uint16
	spliceFdIn  int32
	addr3       uint64
	resv        uint64
}

// Completion Queue Entry
type ioUringCQE struct {
	userData uint64
	res      int32
	flags    uint32
}

// IOUringConnection wraps a connection with io_uring support
type IOUringConnection struct {
	conn       net.Conn
	fd         int
	ring       *IOUring
	bufferPool *sync.Pool
	stats      *network.ConnectionStats
}

// IOUring represents an io_uring instance
type IOUring struct {
	fd         int
	sqRing     []byte
	cqRing     []byte
	sqes       []ioUringSQE
	params     ioUringParams
	sqHead     *uint32
	sqTail     *uint32
	sqMask     uint32
	sqArray    []uint32
	cqHead     *uint32
	cqTail     *uint32
	cqMask     uint32
	cqes       []ioUringCQE
	pendingOps map[uint64]chan IOResult
	opCounter  uint64
	mu         sync.Mutex
}

// IOResult represents the result of an async I/O operation
type IOResult struct {
	Bytes int32
	Error error
}

// NewIOUring creates a new io_uring instance
func NewIOUring(entries uint32) (*IOUring, error) {
	params := ioUringParams{
		sqEntries: entries,
		flags:     IORING_SETUP_SINGLE_ISSUER | IORING_SETUP_DEFER_TASKRUN,
	}

	fd, _, errno := syscall.Syscall(SYS_IO_URING_SETUP, uintptr(entries), uintptr(unsafe.Pointer(&params)), 0)
	if errno != 0 {
		return nil, fmt.Errorf("io_uring_setup failed: %v", errno)
	}

	ring := &IOUring{
		fd:         int(fd),
		params:     params,
		pendingOps: make(map[uint64]chan IOResult),
	}

	if err := ring.mapRings(); err != nil {
		syscall.Close(ring.fd)
		return nil, err
	}

	return ring, nil
}

// mapRings maps the submission and completion rings into memory
func (r *IOUring) mapRings() error {
	// Calculate sizes
	sqRingSize := uintptr(r.params.sqOff.array) + uintptr(r.params.sqEntries*4)
	cqRingSize := uintptr(r.params.cqOff.cqes) + uintptr(r.params.cqEntries)*unsafe.Sizeof(ioUringCQE{})

	// Map submission ring
	sqRing, err := syscall.Mmap(r.fd, 0, int(sqRingSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|MAP_POPULATE)
	if err != nil {
		return fmt.Errorf("mmap sq ring failed: %v", err)
	}
	r.sqRing = sqRing

	// Map completion ring (might be same as sq ring)
	if r.params.features&(1<<0) != 0 { // IORING_FEAT_SINGLE_MMAP
		r.cqRing = sqRing
	} else {
		cqRing, err := syscall.Mmap(r.fd, 0x8000000, int(cqRingSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|MAP_POPULATE)
		if err != nil {
			syscall.Munmap(r.sqRing)
			return fmt.Errorf("mmap cq ring failed: %v", err)
		}
		r.cqRing = cqRing
	}

	// Map submission queue entries
	sqeSize := uintptr(r.params.sqEntries) * unsafe.Sizeof(ioUringSQE{})
	sqeBytes, err := syscall.Mmap(r.fd, 0x10000000, int(sqeSize), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED|MAP_POPULATE)
	if err != nil {
		syscall.Munmap(r.sqRing)
		if &r.cqRing[0] != &r.sqRing[0] {
			syscall.Munmap(r.cqRing)
		}
		return fmt.Errorf("mmap sqes failed: %v", err)
	}

	// Set up pointers
	r.sqHead = (*uint32)(unsafe.Pointer(&r.sqRing[r.params.sqOff.head]))
	r.sqTail = (*uint32)(unsafe.Pointer(&r.sqRing[r.params.sqOff.tail]))
	r.sqMask = *(*uint32)(unsafe.Pointer(&r.sqRing[r.params.sqOff.ringMask]))

	arrayPtr := unsafe.Add(unsafe.Pointer(&r.sqRing[0]), r.params.sqOff.array)
	r.sqArray = unsafe.Slice((*uint32)(arrayPtr), r.params.sqEntries)

	r.cqHead = (*uint32)(unsafe.Pointer(&r.cqRing[r.params.cqOff.head]))
	r.cqTail = (*uint32)(unsafe.Pointer(&r.cqRing[r.params.cqOff.tail]))
	r.cqMask = *(*uint32)(unsafe.Pointer(&r.cqRing[r.params.cqOff.ringMask]))

	cqePtr := unsafe.Add(unsafe.Pointer(&r.cqRing[0]), r.params.cqOff.cqes)
	r.cqes = unsafe.Slice((*ioUringCQE)(cqePtr), r.params.cqEntries)

	sqePtr := unsafe.Pointer(&sqeBytes[0])
	r.sqes = unsafe.Slice((*ioUringSQE)(sqePtr), r.params.sqEntries)

	return nil
}

// SubmitSend submits an async send operation
func (r *IOUring) SubmitSend(fd int, buf []byte) (chan IOResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tail := *r.sqTail
	head := *r.sqHead

	if tail-head >= r.params.sqEntries {
		return nil, fmt.Errorf("submission queue full")
	}

	idx := tail & r.sqMask
	sqe := &r.sqes[idx]

	r.opCounter++
	userData := r.opCounter
	result := make(chan IOResult, 1)
	r.pendingOps[userData] = result

	sqe.opcode = IORING_OP_SEND
	sqe.fd = int32(fd)
	sqe.addr = uint64(uintptr(unsafe.Pointer(&buf[0])))
	sqe.len = uint32(len(buf))
	sqe.userData = userData
	sqe.flags = 0

	r.sqArray[idx] = uint32(idx)

	// Memory barrier
	_ = *r.sqTail
	*r.sqTail = tail + 1

	return result, nil
}

// SubmitRecv submits an async receive operation
func (r *IOUring) SubmitRecv(fd int, buf []byte) (chan IOResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tail := *r.sqTail
	head := *r.sqHead

	if tail-head >= r.params.sqEntries {
		return nil, fmt.Errorf("submission queue full")
	}

	idx := tail & r.sqMask
	sqe := &r.sqes[idx]

	r.opCounter++
	userData := r.opCounter
	result := make(chan IOResult, 1)
	r.pendingOps[userData] = result

	sqe.opcode = IORING_OP_RECV
	sqe.fd = int32(fd)
	sqe.addr = uint64(uintptr(unsafe.Pointer(&buf[0])))
	sqe.len = uint32(len(buf))
	sqe.userData = userData
	sqe.flags = 0

	r.sqArray[idx] = uint32(idx)

	// Memory barrier
	_ = *r.sqTail
	*r.sqTail = tail + 1

	return result, nil
}

// Submit submits all pending operations
func (r *IOUring) Submit() (int, error) {
	submitted, _, errno := syscall.Syscall6(
		SYS_IO_URING_ENTER,
		uintptr(r.fd),
		uintptr(*r.sqTail-*r.sqHead),
		0,
		0,
		0,
		0,
	)
	if errno != 0 {
		return 0, fmt.Errorf("io_uring_enter failed: %v", errno)
	}
	return int(submitted), nil
}

// Wait waits for completions
func (r *IOUring) Wait(minComplete uint32) error {
	_, _, errno := syscall.Syscall6(
		SYS_IO_URING_ENTER,
		uintptr(r.fd),
		0,
		uintptr(minComplete),
		IORING_ENTER_GETEVENTS,
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("io_uring_enter wait failed: %v", errno)
	}

	r.processCompletions()
	return nil
}

// processCompletions processes completed operations
func (r *IOUring) processCompletions() {
	head := *r.cqHead
	tail := *r.cqTail

	for head != tail {
		idx := head & r.cqMask
		cqe := &r.cqes[idx]

		r.mu.Lock()
		if ch, ok := r.pendingOps[cqe.userData]; ok {
			result := IOResult{Bytes: cqe.res}
			if cqe.res < 0 {
				result.Error = syscall.Errno(-cqe.res)
			}
			ch <- result
			close(ch)
			delete(r.pendingOps, cqe.userData)
		}
		r.mu.Unlock()

		head++
	}

	// Memory barrier
	_ = *r.cqHead
	*r.cqHead = head
}

// Close closes the io_uring instance
func (r *IOUring) Close() error {
	if r.sqes != nil {
		sqeSize := uintptr(r.params.sqEntries) * unsafe.Sizeof(ioUringSQE{})
		syscall.Munmap(unsafe.Slice((*byte)(unsafe.Pointer(&r.sqes[0])), sqeSize))
	}
	if r.sqRing != nil {
		syscall.Munmap(r.sqRing)
	}
	// Check if cqRing is different from sqRing by checking features
	if r.cqRing != nil && r.params.features&(1<<0) == 0 {
		syscall.Munmap(r.cqRing)
	}
	return syscall.Close(r.fd)
}

// NewIOUringConnection creates a new connection with io_uring support
func NewIOUringConnection(conn net.Conn) (*IOUringConnection, error) {
	// Extract file descriptor
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

	ring, err := NewIOUring(256)
	if err != nil {
		return nil, err
	}

	return &IOUringConnection{
		conn: conn,
		fd:   fd,
		ring: ring,
		bufferPool: &sync.Pool{
			New: func() interface{} {
				return make([]byte, 65536)
			},
		},
		stats: &network.ConnectionStats{},
	}, nil
}

// SendAsync sends data asynchronously using io_uring
func (c *IOUringConnection) SendAsync(data []byte) (chan IOResult, error) {
	result, err := c.ring.SubmitSend(c.fd, data)
	if err != nil {
		return nil, err
	}

	_, err = c.ring.Submit()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// RecvAsync receives data asynchronously using io_uring
func (c *IOUringConnection) RecvAsync(buf []byte) (chan IOResult, error) {
	result, err := c.ring.SubmitRecv(c.fd, buf)
	if err != nil {
		return nil, err
	}

	_, err = c.ring.Submit()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// BatchSend sends multiple buffers in a batch
func (c *IOUringConnection) BatchSend(buffers [][]byte) ([]chan IOResult, error) {
	results := make([]chan IOResult, len(buffers))

	for i, buf := range buffers {
		result, err := c.ring.SubmitSend(c.fd, buf)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}

	_, err := c.ring.Submit()
	if err != nil {
		return nil, err
	}

	return results, nil
}

// Close closes the io_uring connection
func (c *IOUringConnection) Close() error {
	if c.ring != nil {
		c.ring.Close()
	}
	return c.conn.Close()
}
