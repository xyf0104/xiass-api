package proxyroute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	upstream "github.com/gylive/ccodex-sleep-state/bridge"
)

// Route is an XIASS-owned loopback SOCKS listener around one immutable
// upstream route. It contains no parsing or outbound protocol implementation.
type Route struct {
	ID          string
	StableID    string
	DisplayName string
	Protocol    string

	outbound *upstream.Route
	listener net.Listener
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	close    sync.Once
}

func Build(node map[string]any, index int) (*Route, error) {
	candidate, err := BuildCandidate(node, index, "", "")
	if err != nil {
		return nil, err
	}
	return NewRoute(candidate), nil
}

func NewRoute(candidate Candidate) *Route {
	return &Route{
		ID: candidate.ID, StableID: candidate.ID, DisplayName: candidate.Name, Protocol: candidate.Protocol,
		outbound: candidate.Outbound, conns: make(map[net.Conn]struct{}),
	}
}

func (r *Route) Start(listenAddr string) error {
	if r == nil || r.outbound == nil || r.outbound.Transport == nil {
		return errors.New("route is not initialized")
	}
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	if !IsLoopbackAddress(listenAddr) {
		return errors.New("route listener must use a loopback address")
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return errors.New("cannot start loopback route listener")
	}
	r.mu.Lock()
	if r.closed || r.listener != nil {
		r.mu.Unlock()
		_ = listener.Close()
		return errors.New("route listener is already closed or started")
	}
	r.listener = listener
	r.mu.Unlock()
	go r.acceptLoop()
	return nil
}

func (r *Route) Addr() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener == nil {
		return ""
	}
	return r.listener.Addr().String()
}

func (r *Route) acceptLoop() {
	for {
		client, err := r.listener.Accept()
		if err != nil {
			return
		}
		if !r.track(client) {
			_ = client.Close()
			return
		}
		go r.serve(client)
	}
}

func (r *Route) serve(client net.Conn) {
	defer r.untrackAndClose(client)
	_ = client.SetDeadline(time.Now().Add(15 * time.Second))
	target, err := readSOCKS5Connect(client)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	upstreamConn, err := r.outbound.Transport.DialContext(ctx, "tcp", target)
	cancel()
	if err != nil {
		_, _ = client.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	if !r.track(upstreamConn) {
		_ = upstreamConn.Close()
		return
	}
	defer r.untrackAndClose(upstreamConn)
	_ = client.SetDeadline(time.Time{})
	if _, err = client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	proxyBothDirections(client, upstreamConn)
}

func (r *Route) track(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.conns[conn] = struct{}{}
	return true
}

func (r *Route) untrackAndClose(conn net.Conn) {
	r.mu.Lock()
	delete(r.conns, conn)
	r.mu.Unlock()
	_ = conn.Close()
}

func proxyBothDirections(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyDirection := func(dst net.Conn, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go copyDirection(left, right)
	go copyDirection(right, left)
	<-done
}

func (r *Route) Close() {
	if r == nil {
		return
	}
	r.close.Do(func() {
		r.mu.Lock()
		r.closed = true
		listener := r.listener
		connections := make([]net.Conn, 0, len(r.conns))
		for conn := range r.conns {
			connections = append(connections, conn)
		}
		r.mu.Unlock()
		if listener != nil {
			_ = listener.Close()
		}
		for _, conn := range connections {
			_ = conn.Close()
		}
		if r.outbound != nil {
			r.outbound.Close()
		}
	})
}

func IsLoopbackAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func readSOCKS5Connect(conn net.Conn) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil || header[0] != 5 {
		return "", errors.New("invalid SOCKS5 greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", errors.New("invalid SOCKS5 methods")
	}
	noAuth := false
	for _, method := range methods {
		noAuth = noAuth || method == 0
	}
	if !noAuth {
		_, _ = conn.Write([]byte{5, 255})
		return "", errors.New("SOCKS5 authentication is not available")
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return "", err
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(conn, request); err != nil || request[0] != 5 || request[1] != 1 || request[2] != 0 {
		return "", errors.New("only SOCKS5 CONNECT is supported")
	}
	var host string
	switch request[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil || length[0] == 0 {
			return "", errors.New("invalid SOCKS5 domain")
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", err
		}
		host = string(name)
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", err
		}
		host = net.IP(address).String()
	default:
		return "", errors.New("unsupported SOCKS5 address type")
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", uint16(port[0])<<8|uint16(port[1]))), nil
}
