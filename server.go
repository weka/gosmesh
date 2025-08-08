package main

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
	
	tcpListener net.Listener
	udpConn     *net.UDPConn
	
	wg sync.WaitGroup
}

func NewServer(localIP string, port int, protocol string, packetSize int) *Server {
	return &Server{
		localIP:    localIP,
		port:       port,
		protocol:   protocol,
		packetSize: packetSize,
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
	
	go func() {
		<-ctx.Done()
		s.tcpListener.Close()
	}()
	
	for {
		conn, err := s.tcpListener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
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
	
	buffer := make([]byte, s.packetSize)
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := conn.Read(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}
			
			// Echo back the packet
			if n > 0 {
				conn.Write(buffer[:n])
			}
		}
	}
}

func (s *Server) runUDPServer(ctx context.Context) {
	defer s.udpConn.Close()
	
	buffer := make([]byte, s.packetSize)
	
	for {
		select {
		case <-ctx.Done():
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