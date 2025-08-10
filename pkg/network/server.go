package network

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

type Server struct {
	localIP    string
	port       int
	protocol   string
	packetSize int
	bufferSize int  // New: configurable buffer size for throughput mode
	
	tcpListener net.Listener
	udpConn     *net.UDPConn
	
	wg sync.WaitGroup
}

func NewServer(localIP string, port int, protocol string, packetSize int) *Server {
	// Use larger buffer for throughput mode
	bufferSize := packetSize
	if packetSize <= 0 || bufferSize < 4194304 {
		bufferSize = 4194304 // 4MB for high-speed networks
	}
	
	return &Server{
		localIP:    localIP,
		port:       port,
		protocol:   protocol,
		packetSize: packetSize,
		bufferSize: bufferSize,
	}
}

func (s *Server) Start() error {
	if s.protocol == "tcp" {
		return s.startTCPServer()
	}
	return s.startUDPServer()
}

func (s *Server) startTCPServer() error {
	addr := fmt.Sprintf("%s:%d", s.localIP, s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", addr, err)
	}
	s.tcpListener = listener
	log.Printf("TCP server listening on %s", addr)
	return nil
}

func (s *Server) startUDPServer() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", s.localIP, s.port))
	if err != nil {
		return err
	}
	
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", addr, err)
	}
	s.udpConn = conn
	log.Printf("UDP server listening on %s:%d", s.localIP, s.port)
	return nil
}

func (s *Server) Run(ctx context.Context) {
	if s.protocol == "tcp" {
		s.runTCPServer(ctx)
	} else {
		s.runUDPServer(ctx)
	}
}

func (s *Server) runTCPServer(ctx context.Context) {
	defer s.tcpListener.Close()
	defer log.Printf("TCP server shutdown complete")
	
	go func() {
		<-ctx.Done()
		log.Printf("TCP server context cancelled, closing listener")
		s.tcpListener.Close()
	}()
	
	for {
		conn, err := s.tcpListener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				log.Printf("TCP server exiting due to context cancellation")
				// Wait for all connection handlers to finish before returning
				s.wg.Wait()
				return
			default:
				log.Printf("Failed to accept connection: %v", err)
				continue
			}
		}
		
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleTCPConnection(ctx, c)
		}(conn)
	}
}

func (s *Server) handleTCPConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("Server: New connection from %s", remoteAddr)
	
	// Use larger buffer for throughput mode
	buffer := make([]byte, s.bufferSize)
	
	// Configure TCP connection for max throughput
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetWriteBuffer(16777216) // 16MB
		tcpConn.SetReadBuffer(16777216)  // 16MB
		tcpConn.SetNoDelay(false)        // Enable Nagle's for throughput
	}
	
	// In throughput mode, just consume data without echoing
	// This is more like iperf which measures unidirectional throughput
	totalBytes := int64(0)
	readCount := 0
	
	for {
		select {
		case <-ctx.Done():
			log.Printf("Server: Connection from %s closing due to context, total bytes: %d", remoteAddr, totalBytes)
			return
		default:
			// Remove deadline for maximum throughput
			conn.SetReadDeadline(time.Time{})
			n, err := conn.Read(buffer)
			if err != nil {
				log.Printf("Server: Connection from %s error: %v, total bytes: %d", remoteAddr, err, totalBytes)
				return
			}
			totalBytes += int64(n)
			readCount++
			
			if readCount == 1 {
				log.Printf("Server: First read from %s, n=%d", remoteAddr, n)
			}
			
			// Don't echo in throughput mode - just consume data
			// This allows sender to send at maximum speed
		}
	}
}

func (s *Server) runUDPServer(ctx context.Context) {
	defer s.udpConn.Close()
	defer log.Printf("UDP server shutdown complete")
	
	// Use max UDP packet size
	buffer := make([]byte, 65507)
	
	for {
		select {
		case <-ctx.Done():
			log.Printf("UDP server exiting due to context cancellation")
			return
		default:
			s.udpConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, addr, err := s.udpConn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				continue
			}
			
			// Echo back the packet
			if n > 0 {
				s.udpConn.WriteToUDP(buffer[:n], addr)
			}
		}
	}
}

func (s *Server) Stop() {
	if s.tcpListener != nil {
		s.tcpListener.Close()
	}
	if s.udpConn != nil {
		s.udpConn.Close()
	}
	s.wg.Wait()
}