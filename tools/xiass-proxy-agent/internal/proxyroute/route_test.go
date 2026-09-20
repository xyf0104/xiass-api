package proxyroute

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildSupportsDeclaredProtocols(t *testing.T) {
	base := map[string]any{"server": "example.invalid", "port": 443}
	nodes := []map[string]any{
		{"type": "anytls", "password": "fixture-secret"},
		{"type": "ss", "cipher": "aes-128-gcm", "password": "fixture-secret"},
		{"type": "ssr", "cipher": "rc4-md5", "password": "fixture-secret", "obfs": "plain", "protocol": "origin"},
		{"type": "vmess", "uuid": "00000000-0000-4000-8000-000000000001", "alterId": 0, "cipher": "auto"},
		{"type": "vless", "uuid": "00000000-0000-4000-8000-000000000001", "tls": true},
		{"type": "trojan", "password": "fixture-secret", "sni": "example.invalid"},
		{"type": "hysteria", "up": "10 Mbps", "down": "50 Mbps", "auth-str": "fixture-secret", "sni": "example.invalid"},
		{"type": "hysteria2", "password": "fixture-secret", "sni": "example.invalid"},
		{"type": "tuic", "uuid": "00000000-0000-4000-8000-000000000001", "password": "fixture-secret", "sni": "example.invalid"},
		{"type": "http"},
		{"type": "socks5"},
	}
	for index, node := range nodes {
		for key, value := range base {
			node[key] = value
		}
		route, err := Build(node, index)
		if err != nil {
			t.Fatalf("protocol %q: %v", node["type"], err)
		}
		route.Close()
	}
}

func TestCloseReleasesListenerAndAcceptedConnections(t *testing.T) {
	route, err := Build(map[string]any{
		"type": "http", "server": "127.0.0.1", "port": 18051,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := route.Start(""); err != nil {
		t.Fatal(err)
	}
	address := route.Addr()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte{5}); err != nil {
		t.Fatal(err)
	}
	route.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("accepted connection remained open after Close")
	}
	if listener, err := net.Listen("tcp", address); err != nil {
		t.Fatalf("listener address was not released: %v", err)
	} else {
		_ = listener.Close()
	}
}

func TestHTTPAdapterSOCKSEndToEnd(t *testing.T) {
	target := startEchoServer(t)
	proxy := startHTTPConnectProxy(t)
	proxyHost, proxyPortText, _ := net.SplitHostPort(proxy.Addr().String())
	proxyPort, _ := strconv.Atoi(proxyPortText)
	route, err := Build(map[string]any{
		"type": "http", "server": proxyHost, "port": proxyPort,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer route.Close()
	if err := route.Start(""); err != nil {
		t.Fatal(err)
	}

	client, err := net.DialTimeout("tcp", route.Addr(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(client, response); err != nil || response[0] != 5 || response[1] != 0 {
		t.Fatalf("SOCKS greeting response=%v err=%v", response, err)
	}
	targetHost, targetPortText, _ := net.SplitHostPort(target.Addr().String())
	targetPort, _ := strconv.Atoi(targetPortText)
	ip := net.ParseIP(targetHost).To4()
	request := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], 0, 0}
	binary.BigEndian.PutUint16(request[len(request)-2:], uint16(targetPort))
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS connect response=%v err=%v", reply, err)
	}
	payload := []byte("xiass-proxy-agent-e2e")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	echoed := make([]byte, len(payload))
	if _, err := io.ReadFull(client, echoed); err != nil || string(echoed) != string(payload) {
		t.Fatalf("echo=%q err=%v", echoed, err)
	}
}

func startEchoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener
}

func startHTTPConnectProxy(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go serveHTTPConnect(client)
		}
	}()
	return listener
}

func serveHTTPConnect(client net.Conn) {
	defer client.Close()
	reader := bufio.NewReader(client)
	request, err := http.ReadRequest(reader)
	if err != nil || request.Method != http.MethodConnect || strings.TrimSpace(request.Host) == "" {
		return
	}
	upstream, err := net.DialTimeout("tcp", request.Host, time.Second)
	if err != nil {
		_, _ = fmt.Fprint(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer upstream.Close()
	if _, err := fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}
