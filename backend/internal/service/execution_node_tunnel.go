package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
)

const (
	ExecutionNodeStateSourceURLEnv    = "XIASS_CLUSTER_STATE_SOURCE_URL"
	ExecutionNodeTunnelTokenEnv       = "XIASS_CLUSTER_TUNNEL_TOKEN"
	ExecutionNodeStateSourceNodeIDEnv = "XIASS_CLUSTER_STATE_SOURCE_NODE_ID"
	ExecutionNodeNodeURLsEnv          = "XIASS_CLUSTER_NODE_URLS_JSON"
	ExecutionNodeJoinProofTokenEnv    = "XIASS_CLUSTER_JOIN_PROOF_TOKEN"
	ExecutionNodeSOCKSBindHostEnv     = "XIASS_EXECUTION_NODE_SOCKS_BIND_HOST"

	executionNodePostgresTunnelAddress = "127.0.0.1:15432"
	executionNodeRedisTunnelAddress    = "127.0.0.1:16379"
	executionNodeEgressSOCKSAddress    = "127.0.0.1:19080"
	executionNodeTunnelDialTimeout     = 10 * time.Second
	executionNodeTunnelReadLimit       = 256 * 1024 * 1024
)

const (
	executionNodeTunnelTokenHeader       = "X-XIASS-Execution-Node-Tunnel"
	executionNodeTunnelNodeHeader        = "X-XIASS-Execution-Node-ID"
	executionNodeTunnelDestinationHeader = "X-XIASS-Execution-Destination"
	executionNodeTunnelOwnerHeader       = "X-XIASS-Execution-Owner-Node-ID"
)

type ExecutionNodeTunnelRuntime struct {
	listeners []net.Listener
	done      chan struct{}
	once      sync.Once
}

// executionNodeTunnelPeerResolver is installed after the application has
// loaded its shared settings repository. The tunnel listener starts before
// dependency injection, so the resolver is intentionally late-bound.
var executionNodeTunnelPeerResolver atomic.Value // func(context.Context, string) (*url.URL, error)

// SetExecutionNodeTunnelPeerResolver supplies a shared-state lookup for peer
// URLs. Static environment URLs remain the first choice; this resolver lets a
// source node learn the target URL without restarting the source application.
func SetExecutionNodeTunnelPeerResolver(resolver func(context.Context, string) (*url.URL, error)) {
	if resolver != nil {
		executionNodeTunnelPeerResolver.Store(resolver)
	}
}

func executionNodeTunnelToken(jwtSecret string) string {
	digest := sha256.Sum256([]byte("xiass-execution-node-tunnel:v1:" + strings.TrimSpace(jwtSecret)))
	return hex.EncodeToString(digest[:])
}

func configuredExecutionNodeTunnelToken() string {
	if token := strings.TrimSpace(os.Getenv(ExecutionNodeTunnelTokenEnv)); len(token) == 64 {
		return token
	}
	// Older manually configured installations may not have the explicit tunnel
	// variable yet. Keep that compatibility path deterministic, but never derive
	// a tunnel token for an ordinary single-node installation: doing so would
	// start the loopback listeners before the node runtime has been opted in.
	enabled := strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ENABLED")), "true")
	if !enabled || !validExecutionNodeID(strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))) {
		return ""
	}
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret == "" {
		return ""
	}
	return executionNodeTunnelToken(jwtSecret)
}

func (r *ExecutionNodeTunnelRuntime) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	r.once.Do(func() {
		close(r.done)
		for _, listener := range r.listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

func executionNodeSOCKSListenAddress() (string, error) {
	host := strings.TrimSpace(os.Getenv(ExecutionNodeSOCKSBindHostEnv))
	if host == "" || host == "127.0.0.1" {
		return executionNodeEgressSOCKSAddress, nil
	}
	// Docker browsers use the API's private interface. Keep the same port and
	// token-authenticated handler, and retain IPv4 loopback access for the API.
	if host != "0.0.0.0" {
		return "", fmt.Errorf("%s must be 127.0.0.1 or 0.0.0.0", ExecutionNodeSOCKSBindHostEnv)
	}
	_, port, _ := net.SplitHostPort(executionNodeEgressSOCKSAddress)
	return net.JoinHostPort(host, port), nil
}

// StartExecutionNodeTunnelRuntimeFromEnv starts the local state and egress
// listeners before configuration loading. A member therefore reaches the
// source PostgreSQL/Redis through verified HTTPS without exposing either
// service on a public TCP port.
func StartExecutionNodeTunnelRuntimeFromEnv() (*ExecutionNodeTunnelRuntime, error) {
	token := configuredExecutionNodeTunnelToken()
	if token == "" {
		return nil, nil
	}
	localNodeID := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))
	if !validExecutionNodeID(localNodeID) {
		return nil, fmt.Errorf("%s is invalid", "GATEWAY_EXECUTION_NODE_ID")
	}
	nodeURLs, err := parseExecutionNodeRuntimeURLs(os.Getenv(ExecutionNodeNodeURLsEnv))
	if err != nil {
		return nil, err
	}
	runtime := &ExecutionNodeTunnelRuntime{done: make(chan struct{})}
	closeOnError := func(cause error) (*ExecutionNodeTunnelRuntime, error) {
		_ = runtime.Close()
		return nil, cause
	}

	egressAddress, err := executionNodeSOCKSListenAddress()
	if err != nil {
		return closeOnError(err)
	}
	egressListener, err := net.Listen("tcp", egressAddress)
	if err != nil {
		return nil, fmt.Errorf("listen on execution-node egress SOCKS address: %w", err)
	}
	runtime.listeners = append(runtime.listeners, egressListener)
	go acceptExecutionNodeConnections(runtime.done, egressListener, func(conn net.Conn) {
		handleExecutionNodeSOCKSConnection(conn, localNodeID, token, nodeURLs)
	})

	sourceURL := strings.TrimSpace(os.Getenv(ExecutionNodeStateSourceURLEnv))
	if sourceURL == "" {
		return runtime, nil
	}
	parsedSource, err := normalizeExecutionNodePeerURL(sourceURL)
	if err != nil {
		return closeOnError(fmt.Errorf("invalid execution-node state source URL: %w", err))
	}
	for _, listenerConfig := range []struct {
		address string
		kind    string
	}{
		{address: executionNodePostgresTunnelAddress, kind: "postgres"},
		{address: executionNodeRedisTunnelAddress, kind: "redis"},
	} {
		listener, listenErr := net.Listen("tcp", listenerConfig.address)
		if listenErr != nil {
			return closeOnError(fmt.Errorf("listen on %s state tunnel: %w", listenerConfig.kind, listenErr))
		}
		runtime.listeners = append(runtime.listeners, listener)
		kind := listenerConfig.kind
		go acceptExecutionNodeConnections(runtime.done, listener, func(conn net.Conn) {
			relayLocalConnectionToExecutionNode(conn, parsedSource, token, kind, localNodeID, nil)
		})
	}
	return runtime, nil
}

func parseExecutionNodeRuntimeURLs(raw string) (map[string]*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]*url.URL{}, nil
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("decode %s: %w", ExecutionNodeNodeURLsEnv, err)
	}
	result := make(map[string]*url.URL, len(values))
	for nodeID, rawURL := range values {
		if !validExecutionNodeID(nodeID) {
			return nil, fmt.Errorf("%s contains an invalid node ID", ExecutionNodeNodeURLsEnv)
		}
		parsed, err := normalizeExecutionNodePeerURL(rawURL)
		if err != nil {
			return nil, fmt.Errorf("%s contains an invalid URL for node %s: %w", ExecutionNodeNodeURLsEnv, nodeID, err)
		}
		result[nodeID] = parsed
	}
	return result, nil
}

func acceptExecutionNodeConnections(done <-chan struct{}, listener net.Listener, handle func(net.Conn)) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-done:
				return
			default:
				continue
			}
		}
		go handle(conn)
	}
}

func executionNodeTunnelEndpoint(peer *url.URL, kind string) string {
	clone := *peer
	if clone.Scheme == "https" {
		clone.Scheme = "wss"
	} else {
		clone.Scheme = "ws"
	}
	clone.Path = strings.TrimRight(clone.Path, "/") + "/api/v1/internal/execution-nodes/tunnel/" + kind
	clone.RawPath = ""
	clone.RawQuery = ""
	clone.Fragment = ""
	return clone.String()
}

func relayLocalConnectionToExecutionNode(local net.Conn, peer *url.URL, token, kind, requesterNodeID string, headers http.Header) {
	defer func() { _ = local.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set(executionNodeTunnelTokenHeader, token)
	headers.Set(executionNodeTunnelNodeHeader, requesterNodeID)
	wsConn, _, err := coderws.Dial(ctx, executionNodeTunnelEndpoint(peer, kind), &coderws.DialOptions{HTTPHeader: headers})
	if err != nil {
		return
	}
	wsConn.SetReadLimit(executionNodeTunnelReadLimit)
	remote := coderws.NetConn(ctx, wsConn, coderws.MessageBinary)
	relayBidirectional(local, remote)
}

func relayBidirectional(left, right net.Conn) {
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(left, right); done <- struct{}{} }()
	go func() { _, _ = io.Copy(right, left); done <- struct{}{} }()
	<-done
}

func handleExecutionNodeSOCKSConnection(conn net.Conn, localNodeID, token string, nodeURLs map[string]*url.URL) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(conn)
	ownerNodeID, destination, err := readExecutionNodeSOCKSRequest(reader, conn, token)
	if err != nil {
		return
	}
	var remote net.Conn
	if ownerNodeID == localNodeID {
		remote, err = net.DialTimeout("tcp", destination, executionNodeTunnelDialTimeout)
	} else if peer := executionNodeTunnelPeerURL(ownerNodeID, nodeURLs); peer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), executionNodeTunnelDialTimeout)
		defer cancel()
		headers := make(http.Header)
		headers.Set(executionNodeTunnelTokenHeader, token)
		headers.Set(executionNodeTunnelNodeHeader, localNodeID)
		headers.Set(executionNodeTunnelOwnerHeader, ownerNodeID)
		headers.Set(executionNodeTunnelDestinationHeader, base64.RawURLEncoding.EncodeToString([]byte(destination)))
		wsConn, _, dialErr := coderws.Dial(ctx, executionNodeTunnelEndpoint(peer, "egress"), &coderws.DialOptions{HTTPHeader: headers})
		if dialErr == nil {
			wsConn.SetReadLimit(executionNodeTunnelReadLimit)
			remote = coderws.NetConn(context.Background(), wsConn, coderws.MessageBinary)
		}
		err = dialErr
	} else {
		err = fmt.Errorf("execution node %s has no endpoint", ownerNodeID)
	}
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		_ = remote.Close()
		return
	}
	relayBidirectional(conn, remote)
}

func executionNodeTunnelPeerURL(nodeID string, nodeURLs map[string]*url.URL) *url.URL {
	if peer := nodeURLs[nodeID]; peer != nil {
		return peer
	}
	if resolver, ok := executionNodeTunnelPeerResolver.Load().(func(context.Context, string) (*url.URL, error)); ok {
		peer, err := resolver(context.Background(), nodeID)
		if err == nil {
			return peer
		}
	}
	return nil
}

func readExecutionNodeSOCKSRequest(reader *bufio.Reader, conn net.Conn, expectedToken string) (string, string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 0x05 || header[1] == 0 {
		return "", "", errors.New("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return "", "", err
	}
	supportsPassword := false
	for _, method := range methods {
		supportsPassword = supportsPassword || method == 0x02
	}
	if !supportsPassword {
		_, _ = conn.Write([]byte{0x05, 0xff})
		return "", "", errors.New("SOCKS authentication is required")
	}
	if _, err := conn.Write([]byte{0x05, 0x02}); err != nil {
		return "", "", err
	}
	authHeader := make([]byte, 2)
	if _, err := io.ReadFull(reader, authHeader); err != nil || authHeader[0] != 0x01 || authHeader[1] == 0 {
		return "", "", errors.New("invalid SOCKS authentication")
	}
	username := make([]byte, int(authHeader[1]))
	if _, err := io.ReadFull(reader, username); err != nil {
		return "", "", err
	}
	passwordLength, err := reader.ReadByte()
	if err != nil || passwordLength == 0 {
		return "", "", errors.New("invalid SOCKS password")
	}
	password := make([]byte, int(passwordLength))
	if _, err := io.ReadFull(reader, password); err != nil {
		return "", "", err
	}
	ownerNodeID := strings.TrimSpace(string(username))
	validToken := len(password) == len(expectedToken) && subtle.ConstantTimeCompare(password, []byte(expectedToken)) == 1
	if !validExecutionNodeID(ownerNodeID) || !validToken {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return "", "", errors.New("invalid SOCKS credentials")
	}
	if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
		return "", "", err
	}

	requestHeader := make([]byte, 4)
	if _, err := io.ReadFull(reader, requestHeader); err != nil || requestHeader[0] != 0x05 || requestHeader[1] != 0x01 {
		return "", "", errors.New("invalid SOCKS connect request")
	}
	var host string
	switch requestHeader[3] {
	case 0x01:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", err
		}
		host = net.IP(address).String()
	case 0x03:
		length, err := reader.ReadByte()
		if err != nil || length == 0 {
			return "", "", errors.New("invalid SOCKS domain")
		}
		address := make([]byte, int(length))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", err
		}
		host = string(address)
	case 0x04:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", "", err
		}
		host = net.IP(address).String()
	default:
		return "", "", errors.New("unsupported SOCKS address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return "", "", err
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	if port <= 0 || strings.ContainsAny(host, "\r\n\x00") {
		return "", "", errors.New("invalid SOCKS destination")
	}
	return ownerNodeID, net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// ExecutionNodeTunnelTarget returns the source-side private target for an
// authenticated tunnel. It is intentionally limited to the configured
// PostgreSQL and Redis endpoints; arbitrary TCP forwarding belongs to the
// account egress endpoint and is separately constrained by the owner header.
func (s *SettingService) ExecutionNodeTunnelTarget(kind string) (string, int, error) {
	if s == nil || s.cfg == nil {
		return "", 0, errors.New("execution-node source configuration is unavailable")
	}
	switch kind {
	case "postgres":
		if strings.TrimSpace(s.cfg.Database.Host) == "" || s.cfg.Database.Port <= 0 {
			return "", 0, errors.New("source PostgreSQL endpoint is unavailable")
		}
		return s.cfg.Database.Host, s.cfg.Database.Port, nil
	case "redis":
		if strings.TrimSpace(s.cfg.Redis.Host) == "" || s.cfg.Redis.Port <= 0 {
			return "", 0, errors.New("source Redis endpoint is unavailable")
		}
		return s.cfg.Redis.Host, s.cfg.Redis.Port, nil
	default:
		return "", 0, errors.New("unsupported execution-node tunnel kind")
	}
}

func (s *SettingService) AuthorizeExecutionNodeTunnel(ctx context.Context, requesterNodeID, proof, kind, ownerNodeID string) error {
	if !validExecutionNodeID(requesterNodeID) || len(strings.TrimSpace(proof)) != 64 {
		return errors.New("invalid execution-node tunnel credentials")
	}
	if kind != "postgres" && kind != "redis" && kind != "egress" {
		return errors.New("unsupported execution-node tunnel kind")
	}
	if kind == "egress" && strings.TrimSpace(ownerNodeID) != s.localExecutionNodeID() {
		return errors.New("execution-node egress owner does not belong to this source")
	}
	key := executionNodePairingPeerKey(s.localExecutionNodeID())
	raw, err := s.settingRepo.GetValue(ctx, key)
	if err != nil {
		return errors.New("execution-node peer is not paired")
	}
	var peer ExecutionNodePairingPeer
	if json.Unmarshal([]byte(raw), &peer) != nil || peer.NodeID != requesterNodeID {
		return errors.New("execution-node tunnel proof is invalid")
	}
	tokenHash := peer.TunnelTokenHash
	if tokenHash == "" {
		// Pairings created before the separate tunnel-token field used the proof
		// as both values. Keep those records compatible until they are re-paired.
		tokenHash = peer.TunnelProofHash
	}
	if tokenHash == "" || len(tokenHash) != len(pairingTokenHash(proof)) || subtle.ConstantTimeCompare([]byte(tokenHash), []byte(pairingTokenHash(proof))) != 1 {
		return errors.New("execution-node tunnel proof is invalid")
	}
	return nil
}

// MarkExecutionNodePairingReady is called by the target updater only after the
// new container reaches health. Both peer records are updated in the shared
// source database so either panel reports the same readiness state.
func (s *SettingService) MarkExecutionNodePairingReady(ctx context.Context, targetNodeID, proof string) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("settings repository is unavailable")
	}
	key := executionNodePairingPeerKey(s.localExecutionNodeID())
	raw, err := s.settingRepo.GetValue(ctx, key)
	if err != nil {
		return errors.New("execution-node pairing is not pending")
	}
	var peer ExecutionNodePairingPeer
	if json.Unmarshal([]byte(raw), &peer) != nil || peer.NodeID != strings.TrimSpace(targetNodeID) || peer.TunnelProofHash != pairingTokenHash(proof) {
		return errors.New("execution-node pairing proof is invalid")
	}
	peer.Ready = true
	readySource, err := json.Marshal(peer)
	if err != nil {
		return err
	}
	peerKey := executionNodePairingPeerKey(peer.NodeID)
	peerRaw, peerErr := s.settingRepo.GetValue(ctx, peerKey)
	if peerErr == nil {
		var requesterPeer ExecutionNodePairingPeer
		if json.Unmarshal([]byte(peerRaw), &requesterPeer) == nil {
			requesterPeer.Ready = true
			if encoded, encodeErr := json.Marshal(requesterPeer); encodeErr == nil {
				peerRaw = string(encoded)
			}
		}
	}
	updates := map[string]string{key: string(readySource)}
	if peerErr == nil {
		updates[peerKey] = peerRaw
	}
	return s.settingRepo.SetMultiple(ctx, updates)
}
