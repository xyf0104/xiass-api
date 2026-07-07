package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"
)

const softRouterSocksBufferSize = 64 * 1024

type SoftRouterSocksGateway struct {
	mu        sync.Mutex
	listeners map[int64]*softRouterSocksListener
	status    map[int64]SoftRouterListenInfo
}

func NewSoftRouterSocksGateway() *SoftRouterSocksGateway {
	return &SoftRouterSocksGateway{
		listeners: map[int64]*softRouterSocksListener{},
		status:    map[int64]SoftRouterListenInfo{},
	}
}

func (g *SoftRouterSocksGateway) Reconcile(ctx context.Context, cfg SoftRouterProxyConfig, mappings []SoftRouterProxyMapping) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	desired := map[int64]softRouterSocksListenerConfig{}
	if cfg.Enabled {
		for i := range mappings {
			m := mappings[i]
			if !m.Enabled {
				continue
			}
			desired[m.ID] = softRouterSocksListenerConfig{
				MappingID:     m.ID,
				Name:          m.Name,
				ListenHost:    cfg.GatewayListenHost,
				PublicPort:    m.PublicPort,
				UpstreamHost:  cfg.UpstreamHost,
				RawRemotePort: m.RawRemotePort,
				Username:      m.Username,
				Password:      m.Password,
			}
		}
	}

	for id, current := range g.listeners {
		next, ok := desired[id]
		if !ok || !current.sameConfig(next) {
			current.stop()
			delete(g.listeners, id)
			delete(g.status, id)
		}
	}

	for id, cfg := range desired {
		if _, ok := g.listeners[id]; ok {
			continue
		}
		listener := newSoftRouterSocksListener(cfg, func(info SoftRouterListenInfo) {
			g.mu.Lock()
			g.status[id] = info
			g.mu.Unlock()
		})
		if err := listener.start(ctx); err != nil {
			g.status[id] = SoftRouterListenInfo{Running: false, Error: err.Error()}
			return err
		}
		g.listeners[id] = listener
		g.status[id] = SoftRouterListenInfo{Running: true}
	}
	return nil
}

func (g *SoftRouterSocksGateway) Status() SoftRouterRuntimeStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := SoftRouterRuntimeStatus{
		Enabled:   len(g.listeners) > 0,
		Listeners: map[int64]SoftRouterListenInfo{},
	}
	for id, info := range g.status {
		out.Listeners[id] = info
	}
	return out
}

func (g *SoftRouterSocksGateway) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, listener := range g.listeners {
		listener.stop()
		delete(g.listeners, id)
	}
	g.status = map[int64]SoftRouterListenInfo{}
}

type softRouterSocksListenerConfig struct {
	MappingID     int64
	Name          string
	ListenHost    string
	PublicPort    int
	UpstreamHost  string
	RawRemotePort int
	Username      string
	Password      string
}

type softRouterSocksListener struct {
	cfg      softRouterSocksListenerConfig
	listener net.Listener
	done     chan struct{}
	report   func(SoftRouterListenInfo)
}

func newSoftRouterSocksListener(cfg softRouterSocksListenerConfig, report func(SoftRouterListenInfo)) *softRouterSocksListener {
	if cfg.ListenHost == "" {
		cfg.ListenHost = "0.0.0.0"
	}
	if cfg.UpstreamHost == "" {
		cfg.UpstreamHost = "127.0.0.1"
	}
	return &softRouterSocksListener{
		cfg:    cfg,
		done:   make(chan struct{}),
		report: report,
	}
}

func (l *softRouterSocksListener) sameConfig(cfg softRouterSocksListenerConfig) bool {
	return l.cfg == cfg
}

func (l *softRouterSocksListener) start(ctx context.Context) error {
	addr := net.JoinHostPort(l.cfg.ListenHost, strconv.Itoa(l.cfg.PublicPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	l.listener = ln
	go l.acceptLoop(ctx)
	return nil
}

func (l *softRouterSocksListener) stop() {
	select {
	case <-l.done:
		return
	default:
		close(l.done)
	}
	if l.listener != nil {
		_ = l.listener.Close()
	}
}

func (l *softRouterSocksListener) acceptLoop(ctx context.Context) {
	if l.report != nil {
		l.report(SoftRouterListenInfo{Running: true})
	}
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			case <-ctx.Done():
				return
			default:
			}
			if l.report != nil {
				l.report(SoftRouterListenInfo{Running: false, Error: err.Error()})
			}
			slog.Warn("soft router SOCKS accept failed", "mapping_id", l.cfg.MappingID, "error", err)
			return
		}
		go l.handle(conn)
	}
}

func (l *softRouterSocksListener) handle(client net.Conn) {
	defer func() { _ = client.Close() }()
	_ = client.SetDeadline(time.Now().Add(3 * time.Minute))
	targetHost, targetPort, err := l.acceptClient(client)
	if err != nil {
		_ = writeSocksReply(client, 1)
		return
	}
	upstream, err := dialViaUpstreamSocks(l.cfg.UpstreamHost, l.cfg.RawRemotePort, targetHost, targetPort)
	if err != nil {
		_ = writeSocksReply(client, 5)
		return
	}
	defer func() { _ = upstream.Close() }()
	if err := writeSocksReply(client, 0); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	relaySocks(client, upstream)
}

func (l *softRouterSocksListener) acceptClient(client net.Conn) (string, int, error) {
	methods, err := readSocksMethods(client)
	if err != nil {
		return "", 0, err
	}
	if l.cfg.Username != "" || l.cfg.Password != "" {
		if !containsByte(methods, 0x02) {
			_, _ = client.Write([]byte{0x05, 0xff})
			return "", 0, errors.New("SOCKS username/password auth required")
		}
		if _, err := client.Write([]byte{0x05, 0x02}); err != nil {
			return "", 0, err
		}
		if err := l.verifyPasswordAuth(client); err != nil {
			_, _ = client.Write([]byte{0x01, 0x01})
			return "", 0, err
		}
		if _, err := client.Write([]byte{0x01, 0x00}); err != nil {
			return "", 0, err
		}
	} else {
		if !containsByte(methods, 0x00) {
			_, _ = client.Write([]byte{0x05, 0xff})
			return "", 0, errors.New("SOCKS no-auth method unavailable")
		}
		if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
			return "", 0, err
		}
	}
	return readSocksConnectRequest(client)
}

func (l *softRouterSocksListener) verifyPasswordAuth(client net.Conn) error {
	header, err := readExact(client, 2)
	if err != nil {
		return err
	}
	if header[0] != 0x01 {
		return errors.New("invalid auth version")
	}
	userBytes, err := readExact(client, int(header[1]))
	if err != nil {
		return err
	}
	plen, err := readExact(client, 1)
	if err != nil {
		return err
	}
	passBytes, err := readExact(client, int(plen[0]))
	if err != nil {
		return err
	}
	if string(userBytes) != l.cfg.Username || string(passBytes) != l.cfg.Password {
		return errors.New("invalid SOCKS credentials")
	}
	return nil
}

func readSocksMethods(r io.Reader) ([]byte, error) {
	header, err := readExact(r, 2)
	if err != nil {
		return nil, err
	}
	if header[0] != 0x05 {
		return nil, errors.New("not SOCKS5")
	}
	return readExact(r, int(header[1]))
}

func readSocksConnectRequest(r io.Reader) (string, int, error) {
	header, err := readExact(r, 4)
	if err != nil {
		return "", 0, err
	}
	if header[0] != 0x05 || header[1] != 0x01 {
		return "", 0, errors.New("only SOCKS5 CONNECT is supported")
	}
	var host string
	switch header[3] {
	case 0x01:
		ip, err := readExact(r, 4)
		if err != nil {
			return "", 0, err
		}
		host = net.IP(ip).String()
	case 0x03:
		ln, err := readExact(r, 1)
		if err != nil {
			return "", 0, err
		}
		name, err := readExact(r, int(ln[0]))
		if err != nil {
			return "", 0, err
		}
		host = string(name)
	case 0x04:
		ip, err := readExact(r, 16)
		if err != nil {
			return "", 0, err
		}
		host = net.IP(ip).String()
	default:
		return "", 0, errors.New("unsupported SOCKS address type")
	}
	portBytes, err := readExact(r, 2)
	if err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(portBytes)), nil
}

func dialViaUpstreamSocks(upstreamHost string, upstreamPort int, targetHost string, targetPort int) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(upstreamHost, strconv.Itoa(upstreamPort)), 15*time.Second)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	method, err := readExact(conn, 2)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if method[0] != 0x05 || method[1] != 0x00 {
		_ = conn.Close()
		return nil, errors.New("upstream SOCKS no-auth handshake failed")
	}
	hostBytes := []byte(targetHost)
	if len(hostBytes) > 255 {
		_ = conn.Close()
		return nil, errors.New("target host too long")
	}
	req := make([]byte, 0, 7+len(hostBytes))
	req = append(req, 0x05, 0x01, 0x00, 0x03, byte(len(hostBytes)))
	req = append(req, hostBytes...)
	req = binary.BigEndian.AppendUint16(req, uint16(targetPort))
	if _, err := conn.Write(req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reply, err := readExact(conn, 4)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if reply[1] != 0x00 {
		_ = conn.Close()
		return nil, fmt.Errorf("upstream SOCKS connect failed: %d", reply[1])
	}
	if err := discardSocksBindAddress(conn, reply[3]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func discardSocksBindAddress(r io.Reader, atyp byte) error {
	switch atyp {
	case 0x01:
		_, err := readExact(r, 4)
		if err != nil {
			return err
		}
	case 0x03:
		ln, err := readExact(r, 1)
		if err != nil {
			return err
		}
		if _, err := readExact(r, int(ln[0])); err != nil {
			return err
		}
	case 0x04:
		if _, err := readExact(r, 16); err != nil {
			return err
		}
	default:
		return errors.New("unsupported upstream bind address type")
	}
	_, err := readExact(r, 2)
	return err
}

func writeSocksReply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

func readExact(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func containsByte(values []byte, needle byte) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func relaySocks(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	relayOne := func(dst, src net.Conn) {
		defer wg.Done()
		buf := make([]byte, softRouterSocksBufferSize)
		_, _ = io.CopyBuffer(dst, src, buf)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go relayOne(a, b)
	go relayOne(b, a)
	wg.Wait()
}
