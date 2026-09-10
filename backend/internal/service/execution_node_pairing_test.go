//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type executionNodePairingRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func newExecutionNodePairingRepo(databaseID string) *executionNodePairingRepo {
	return &executionNodePairingRepo{values: map[string]string{SettingKeyExecutionNodeClusterID: databaseID}}
}

func (r *executionNodePairingRepo) Get(_ context.Context, key string) (*Setting, error) {
	value, err := r.GetValue(context.Background(), key)
	if err != nil {
		return nil, err
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *executionNodePairingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *executionNodePairingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *executionNodePairingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *executionNodePairingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *executionNodePairingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(r.values))
	for key, value := range r.values {
		result[key] = value
	}
	return result, nil
}

func (r *executionNodePairingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func (r *executionNodePairingRepo) EnsureExecutionNodeClusterID(_ context.Context, candidate string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.values[SettingKeyExecutionNodeClusterID]) == "" {
		r.values[SettingKeyExecutionNodeClusterID] = candidate
	}
	return r.values[SettingKeyExecutionNodeClusterID], nil
}

func (r *executionNodePairingRepo) AcceptExecutionNodePairing(_ context.Context, expectedInvite string, peerSettings map[string]string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values[SettingKeyExecutionNodePairingInvite] != expectedInvite {
		return false, nil
	}
	r.values[SettingKeyExecutionNodePairingInvite] = ""
	for key, value := range peerSettings {
		r.values[key] = value
	}
	return true, nil
}

type executionNodePairingState string

func (s executionNodePairingState) EnsureSharedStateIdentity(context.Context, string) (string, error) {
	return string(s), nil
}

func newExecutionNodePairingService(nodeID, databaseID, redisID string) (*SettingService, *executionNodePairingRepo) {
	repo := newExecutionNodePairingRepo(databaseID)
	cfg := &config.Config{
		JWT:  config.JWTConfig{Secret: "shared-jwt-secret-value"},
		Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)},
	}
	cfg.Gateway.ExecutionNode.Enabled = true
	cfg.Gateway.ExecutionNode.ID = nodeID
	svc := NewSettingService(repo, cfg)
	svc.SetVersion("1.1.74")
	svc.SetExecutionNodePairingStateReader(executionNodePairingState(redisID))
	return svc, repo
}

func TestExecutionNodePairingInviteStoresHashAndIsSingleUse(t *testing.T) {
	target, repo := newExecutionNodePairingService("api2", "shared-db", "shared-redis")
	invite, err := target.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	require.Len(t, invite.Token, 64)

	stored := repo.values[SettingKeyExecutionNodePairingInvite]
	require.NotContains(t, stored, invite.Token)
	require.Contains(t, stored, pairingTokenHash(invite.Token))

	initiator, _ := newExecutionNodePairingService("api", "shared-db", "shared-redis")
	request, err := initiator.localPairingHandshakeResponse(context.Background())
	require.NoError(t, err)
	accepted, err := target.AcceptExecutionNodePairingHandshake(context.Background(), invite.Token, &ExecutionNodePairingHandshakeRequest{
		NodeID:              request.NodeID,
		Version:             request.Version,
		ProtocolVersion:     request.ProtocolVersion,
		DatabaseFingerprint: request.DatabaseFingerprint,
		RedisFingerprint:    request.RedisFingerprint,
		AuthFingerprint:     request.AuthFingerprint,
		StateFingerprint:    request.StateFingerprint,
	})
	require.NoError(t, err)
	require.Equal(t, "api2", accepted.NodeID)
	require.Empty(t, repo.values[SettingKeyExecutionNodePairingInvite])
	require.NotEmpty(t, repo.values[executionNodePairingPeerKey("api")])
	require.NotEmpty(t, repo.values[executionNodePairingPeerKey("api2")])

	_, err = target.AcceptExecutionNodePairingHandshake(context.Background(), invite.Token, &ExecutionNodePairingHandshakeRequest{
		NodeID:              request.NodeID,
		ProtocolVersion:     request.ProtocolVersion,
		DatabaseFingerprint: request.DatabaseFingerprint,
		RedisFingerprint:    request.RedisFingerprint,
		AuthFingerprint:     request.AuthFingerprint,
		StateFingerprint:    request.StateFingerprint,
	})
	require.Equal(t, "EXECUTION_NODE_PAIRING_INVITE_INVALID", infraerrors.Reason(err))
}

func TestExecutionNodePairingRejectsIndependentStateWithoutConsumingInvite(t *testing.T) {
	target, repo := newExecutionNodePairingService("api2", "target-db", "target-redis")
	invite, err := target.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	storedInvite := repo.values[SettingKeyExecutionNodePairingInvite]

	initiator, _ := newExecutionNodePairingService("api", "other-db", "other-redis")
	request, err := initiator.localPairingHandshakeResponse(context.Background())
	require.NoError(t, err)
	_, err = target.AcceptExecutionNodePairingHandshake(context.Background(), invite.Token, &ExecutionNodePairingHandshakeRequest{
		NodeID:              request.NodeID,
		ProtocolVersion:     request.ProtocolVersion,
		DatabaseFingerprint: request.DatabaseFingerprint,
		RedisFingerprint:    request.RedisFingerprint,
		AuthFingerprint:     request.AuthFingerprint,
		StateFingerprint:    request.StateFingerprint,
	})
	require.Equal(t, "EXECUTION_NODE_PAIRING_STATE_MISMATCH", infraerrors.Reason(err))
	require.Equal(t, storedInvite, repo.values[SettingKeyExecutionNodePairingInvite])
}

func TestPairExecutionNodeVerifiesPeerAndPersistsProductionReadyStatus(t *testing.T) {
	initiator, _ := newExecutionNodePairingService("api", "shared-db", "shared-redis")
	local, err := initiator.localPairingHandshakeResponse(context.Background())
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/internal/execution-nodes/pair", r.URL.Path)
		require.Equal(t, strings.Repeat("a", 64), r.Header.Get(executionNodePairingInviteHeader))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": ExecutionNodePairingHandshakeResponse{
				NodeID:              "api2",
				Version:             "1.1.74",
				ProtocolVersion:     executionNodePairingProtocolVersion,
				DatabaseFingerprint: local.DatabaseFingerprint,
				RedisFingerprint:    local.RedisFingerprint,
				AuthFingerprint:     local.AuthFingerprint,
				StateFingerprint:    local.StateFingerprint,
			},
		})
	}))
	defer server.Close()

	status, err := initiator.PairExecutionNode(context.Background(), server.URL, strings.Repeat("a", 64))
	require.NoError(t, err)
	require.True(t, status.Paired)
	require.True(t, status.ProductionReady)
	require.True(t, status.DatabaseShared)
	require.True(t, status.RedisShared)
	require.True(t, status.AuthCompatible)
	require.Equal(t, "api2", status.Peer.NodeID)
}

func TestPairExecutionNodeRejectsRedirectAndPublicHTTP(t *testing.T) {
	initiator, _ := newExecutionNodePairingService("api", "shared-db", "shared-redis")
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://example.com")
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()

	_, err := initiator.PairExecutionNode(context.Background(), redirect.URL, strings.Repeat("a", 64))
	require.Equal(t, "EXECUTION_NODE_PAIRING_REJECTED", infraerrors.Reason(err))
	_, err = initiator.PairExecutionNode(context.Background(), "http://example.com", strings.Repeat("a", 64))
	require.Equal(t, "EXECUTION_NODE_PAIRING_URL_INVALID", infraerrors.Reason(err))
}

type executionNodeJoinApplierRecorder struct {
	join ExecutionNodeJoinConfig
}

func (r *executionNodeJoinApplierRecorder) LaunchExecutionNodeJoin(_ context.Context, join ExecutionNodeJoinConfig) error {
	r.join = join
	return nil
}

type executionNodePairingProxyRepo struct {
	ProxyRepository
	proxies map[int64]Proxy
	nextID  int64
}

func (r *executionNodePairingProxyRepo) ListAllForFallback(_ context.Context) ([]Proxy, error) {
	result := make([]Proxy, 0, len(r.proxies))
	for _, proxy := range r.proxies {
		result = append(result, proxy)
	}
	return result, nil
}

func (r *executionNodePairingProxyRepo) Create(_ context.Context, proxy *Proxy) error {
	if proxy.ID == 0 {
		proxy.ID = r.nextID
		r.nextID++
	}
	r.proxies[proxy.ID] = *proxy
	return nil
}

func (r *executionNodePairingProxyRepo) Update(_ context.Context, proxy *Proxy) error {
	r.proxies[proxy.ID] = *proxy
	return nil
}

func TestAuthoritativePairingPublishesTargetURLAndFixedEgressMapping(t *testing.T) {
	source, sourceRepo := newExecutionNodePairingService("primary-us", "source-db", "source-redis")
	source.cfg.Database = config.DatabaseConfig{Host: "postgres", Port: 5432, User: "xiass", Password: "db-secret", DBName: "xiass", SSLMode: "disable"}
	source.cfg.Redis = config.RedisConfig{Host: "redis", Port: 6379, Password: "redis-secret"}
	source.cfg.Totp.EncryptionKeyConfigured = true
	source.cfg.Gateway.ExecutionNode.DefaultProxyID = 84
	source.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID = "primary-us"
	source.cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID = 84
	sourceProxies := &executionNodePairingProxyRepo{
		proxies: map[int64]Proxy{84: {ID: 84, Name: "primary egress", Protocol: "socks5", Host: "127.0.0.1", Port: 19080, Username: "primary-us", Password: strings.Repeat("a", 64), Status: StatusActive}},
		nextID:  100,
	}
	source.SetProxyRepository(sourceProxies)

	target, _ := newExecutionNodePairingService("edge-jp", "target-db", "target-redis")
	recorder := &executionNodeJoinApplierRecorder{}
	target.SetExecutionNodeJoinApplier(recorder)

	invite, err := source.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	server := httptest.NewServer(setupExecutionNodePairingHTTPRouter(source))
	defer server.Close()
	targetURL := "http://127.0.0.1:18082"

	status, err := target.PairExecutionNodeWithTarget(context.Background(), server.URL, invite.Token, "edge-jp", targetURL)
	require.NoError(t, err)
	require.True(t, status.Paired)
	require.Equal(t, targetURL, recorder.join.TargetURL)
	require.Equal(t, strings.TrimRight(server.URL, "/"), recorder.join.SourceURL)
	require.Equal(t, int64(100), recorder.join.TargetProxyID)
	require.Equal(t, "primary-us", recorder.join.LegacyNodeID)
	require.Equal(t, int64(84), recorder.join.LegacyProxyID)

	var sourcePeer ExecutionNodePairingPeer
	require.NoError(t, json.Unmarshal([]byte(sourceRepo.values[executionNodePairingPeerKey("primary-us")]), &sourcePeer))
	require.Equal(t, "edge-jp", sourcePeer.NodeID)
	require.Equal(t, targetURL, sourcePeer.PeerURL)
	require.Equal(t, pairingTokenHash(recorder.join.TunnelProof), sourcePeer.TunnelTokenHash)

	var weights map[string]float64
	require.NoError(t, json.Unmarshal([]byte(sourceRepo.values[SettingKeyExecutionNodeWeights]), &weights))
	require.Equal(t, map[string]float64{"primary-us": 9, "edge-jp": 1}, weights)
	var proxyIDs map[string]int64
	require.NoError(t, json.Unmarshal([]byte(sourceRepo.values[SettingKeyExecutionNodeProxyIDs]), &proxyIDs))
	require.Equal(t, map[string]int64{"primary-us": 84, "edge-jp": 100}, proxyIDs)
	require.NotContains(t, sourceRepo.values, "execution_node_emergency_egress:edge-jp", "pairing cannot enable offline takeover")
}

func TestExecutionNodePairingDoesNotWriteTakeoverPermissions(t *testing.T) {
	source, repo := newExecutionNodePairingService("primary", "db", "redis")
	source.cfg.Gateway.ExecutionNode.DefaultProxyID = 84
	for _, value := range []string{"true", "false"} {
		for _, node := range []string{"primary", "secondary"} {
			repo.values["execution_node_emergency_egress:"+node] = value
		}
		updates, err := source.executionNodePairingRoutingSettings(context.Background(), "secondary", 100)
		require.NoError(t, err)
		for _, node := range []string{"primary", "secondary"} {
			require.NotContains(t, updates, "execution_node_emergency_egress:"+node)
			require.Equal(t, value, repo.values["execution_node_emergency_egress:"+node])
		}
	}
}

func TestExecutionNodePairingRoutingPreservesExplicitZeroWeight(t *testing.T) {
	source, repo := newExecutionNodePairingService("primary-us", "source-db", "source-redis")
	source.cfg.Gateway.ExecutionNode.DefaultProxyID = 84
	source.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID = "primary-us"
	repo.values[SettingKeyExecutionNodeWeights] = `{"primary-us":0,"edge-jp":1}`
	repo.values[SettingKeyExecutionNodeProxyIDs] = `{"primary-us":84,"edge-jp":100}`

	updates, err := source.executionNodePairingRoutingSettings(context.Background(), "edge-jp", 100)
	require.NoError(t, err)

	var weights map[string]float64
	require.NoError(t, json.Unmarshal([]byte(updates[SettingKeyExecutionNodeWeights]), &weights))
	require.Equal(t, map[string]float64{"primary-us": 0, "edge-jp": 1}, weights,
		"a deliberately drained node must not be re-enabled by re-pairing")
}

func TestExecutionNodePairingRoutingUpgradesLegacySingleNodeDefault(t *testing.T) {
	source, repo := newExecutionNodePairingService("primary.example.com", "source-db", "source-redis")
	source.cfg.Gateway.ExecutionNode.DefaultProxyID = 84
	source.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID = "api"
	repo.values[SettingKeyExecutionNodeWeights] = `{"primary.example.com":1}`
	repo.values[SettingKeyExecutionNodeProxyIDs] = `{"primary.example.com":84}`

	updates, err := source.executionNodePairingRoutingSettings(context.Background(), "edge.example.com", 100)
	require.NoError(t, err)

	var weights map[string]float64
	require.NoError(t, json.Unmarshal([]byte(updates[SettingKeyExecutionNodeWeights]), &weights))
	require.Equal(t, map[string]float64{"primary.example.com": 9, "edge.example.com": 1}, weights)
}

func TestAuthoritativePairingReturnsEncryptedJoinBundleToTargetApplier(t *testing.T) {
	source, sourceRepo := newExecutionNodePairingService("api", "source-db", "source-redis")
	source.cfg.Database = config.DatabaseConfig{Host: "postgres", Port: 5432, User: "xiass", Password: "db-secret", DBName: "xiass", SSLMode: "disable"}
	source.cfg.Redis = config.RedisConfig{Host: "redis", Port: 6379, Username: "", Password: "redis-secret", DB: 2}
	source.cfg.Totp.EncryptionKeyConfigured = true
	target, _ := newExecutionNodePairingService("api2", "target-db", "target-redis")
	recorder := &executionNodeJoinApplierRecorder{}
	target.SetExecutionNodeJoinApplier(recorder)

	invite, err := source.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	server := httptest.NewServer(setupExecutionNodePairingHTTPRouter(source))
	defer server.Close()

	status, err := target.PairExecutionNodeWithTarget(context.Background(), server.URL, invite.Token, "api2", "http://127.0.0.1:18082")
	require.NoError(t, err)
	require.True(t, status.Paired)
	require.False(t, status.ProductionReady)
	require.Equal(t, "api", recorder.join.SourceNodeID)
	require.Equal(t, "api2", recorder.join.TargetNodeID)
	require.Equal(t, "postgres", recorder.join.DatabaseHost)
	require.Equal(t, "db-secret", recorder.join.DatabasePass)
	require.Equal(t, "redis-secret", recorder.join.RedisPassword)
	require.Equal(t, "redis", recorder.join.JWTRefreshTokenStore)
	require.NotEmpty(t, recorder.join.TunnelProof)

	stored, err := sourceRepo.GetValue(context.Background(), executionNodePairingPeerKey("api"))
	require.NoError(t, err)
	require.NotContains(t, stored, recorder.join.TunnelProof)
	var peer ExecutionNodePairingPeer
	require.NoError(t, json.Unmarshal([]byte(stored), &peer))
	require.False(t, peer.Ready)
	require.Equal(t, pairingTokenHash(recorder.join.TunnelProof), peer.TunnelProofHash)
}

func TestAuthoritativePairingPreservesPostgresRefreshAuthority(t *testing.T) {
	source, repo := newExecutionNodePairingService("api", "source-db", "source-redis")
	source.cfg.Database = config.DatabaseConfig{Host: "postgres", Port: 5432}
	source.cfg.Redis = config.RedisConfig{Host: "redis", Port: 6379}
	source.cfg.Totp.EncryptionKeyConfigured = true
	source.cfg.JWT.RefreshTokenStore = "postgres"
	target, _ := newExecutionNodePairingService("api2", "target-db", "target-redis")
	recorder := &executionNodeJoinApplierRecorder{}
	target.SetExecutionNodeJoinApplier(recorder)
	invite, err := source.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	server := httptest.NewServer(setupExecutionNodePairingHTTPRouter(source))
	defer server.Close()
	status, err := target.PairExecutionNodeWithTarget(context.Background(), server.URL, invite.Token, "api2", "http://127.0.0.1:18082")
	require.NoError(t, err)
	require.True(t, status.Paired)
	require.False(t, status.ProductionReady, "the host must still validate the actual shared runtime")
	require.Equal(t, "postgres", recorder.join.JWTRefreshTokenStore)
	stored := repo.values[executionNodePairingPeerKey("api")]
	require.NotContains(t, stored, source.cfg.JWT.Secret)
	require.NotContains(t, stored, source.cfg.Totp.EncryptionKey)
}

func TestAuthoritativePairingRejectsOldTargetBeforeConsumingInvite(t *testing.T) {
	source, repo := newExecutionNodePairingService("api", "source-db", "source-redis")
	source.cfg.Database = config.DatabaseConfig{Host: "postgres", Port: 5432}
	source.cfg.Redis = config.RedisConfig{Host: "redis", Port: 6379}
	source.cfg.Totp.EncryptionKeyConfigured = true
	source.cfg.JWT.RefreshTokenStore = "postgres"
	proxies := &executionNodePairingProxyRepo{proxies: map[int64]Proxy{}, nextID: 10}
	source.SetProxyRepository(proxies)
	invite, err := source.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	before := repo.values[SettingKeyExecutionNodePairingInvite]
	_, err = source.AcceptExecutionNodePairingHandshake(context.Background(), invite.Token, &ExecutionNodePairingHandshakeRequest{
		NodeID: "api2", ProtocolVersion: executionNodePairingProtocolVersion, PeerURL: "http://127.0.0.1:18082",
	})
	require.Equal(t, "EXECUTION_NODE_PAIRING_VERSION_MISMATCH", infraerrors.Reason(err))
	require.Equal(t, before, repo.values[SettingKeyExecutionNodePairingInvite])
	require.NotContains(t, repo.values, executionNodePairingPeerKey("api"))
	require.Empty(t, proxies.proxies)
}

func TestAuthoritativePairingRejectsSessionMigrationBeforeConsumingInvite(t *testing.T) {
	source, repo := newExecutionNodePairingService("api", "source-db", "source-redis")
	source.cfg.Database = config.DatabaseConfig{Host: "postgres", Port: 5432}
	source.cfg.Redis = config.RedisConfig{Host: "redis", Port: 6379}
	source.cfg.Totp.EncryptionKeyConfigured = true
	source.cfg.JWT.RefreshTokenMigrationReadiness = true
	proxies := &executionNodePairingProxyRepo{proxies: map[int64]Proxy{}, nextID: 10}
	source.SetProxyRepository(proxies)
	invite, err := source.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	before := repo.values[SettingKeyExecutionNodePairingInvite]
	_, err = source.AcceptExecutionNodePairingHandshake(context.Background(), invite.Token, &ExecutionNodePairingHandshakeRequest{
		NodeID: "api2", ProtocolVersion: executionNodePairingProtocolVersion, PeerURL: "http://127.0.0.1:18082",
	})
	require.Equal(t, "EXECUTION_NODE_PAIRING_BUNDLE_UNAVAILABLE", infraerrors.Reason(err))
	require.ErrorContains(t, err, "finish the refresh-session migration")
	require.Equal(t, before, repo.values[SettingKeyExecutionNodePairingInvite])
	require.NotContains(t, repo.values, executionNodePairingPeerKey("api"))
	require.Empty(t, proxies.proxies)
}

func TestExecutionNodeJoinConfigOmitsRetiredWitnessFields(t *testing.T) {
	legacy := []byte(`{"source_node_id":"api","target_node_id":"api2","witness_enabled":true,"witness_url":"invalid","witness_token":"retired"}`)
	var join ExecutionNodeJoinConfig
	require.NoError(t, json.Unmarshal(legacy, &join))
	require.Equal(t, "api", join.SourceNodeID)
	raw, err := json.Marshal(join)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "witness_")
	var bundle executionNodeJoinBundle
	require.NoError(t, json.Unmarshal(legacy, &bundle))
	require.Equal(t, "api2", bundle.TargetNodeID)
	raw, err = json.Marshal(bundle)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "witness_")
}

func TestExecutionNodeJoinBundleRefreshAuthorityCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		store   string
		want    string
	}{
		{"old source", 1, "", "redis"},
		{"explicit Redis", 1, "redis", "redis"},
		{"persistent source", 2, "postgres", "postgres"},
		{"v1 cannot select persistent", 1, "postgres", ""},
		{"v2 requires explicit persistent", 2, "", ""},
		{"v2 cannot downgrade", 2, "redis", ""},
		{"unknown storage", 2, "unsafe", ""},
		{"unknown version", 3, "postgres", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := executionNodeJoinBundle{Version: tc.version, SourceNodeID: "api", TargetNodeID: "api2",
				TunnelProof: strings.Repeat("a", 64), JWTRefreshTokenStore: tc.store}
			wire, err := encryptExecutionNodeJoinBundle("invite", "api2", bundle)
			require.NoError(t, err)
			got, err := decryptExecutionNodeJoinBundle("invite", "api2", wire)
			if tc.want == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.JWTRefreshTokenStore)
		})
	}
}

func setupExecutionNodePairingHTTPRouter(svc *SettingService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/internal/execution-nodes/pair" {
			http.NotFound(w, r)
			return
		}
		token := r.Header.Get(executionNodePairingInviteHeader)
		var request ExecutionNodePairingHandshakeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result, err := svc.AcceptExecutionNodePairingHandshake(r.Context(), token, &request)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": result})
	})
}

type executionNodeJoinTargetInspectorStub struct {
	empty bool
}

func (s executionNodeJoinTargetInspectorStub) IsExecutionNodeJoinTargetEmpty(context.Context) (bool, error) {
	return s.empty, nil
}

func TestAuthoritativePairingRejectsNonEmptyTargetBeforeConsumingInvite(t *testing.T) {
	source, sourceRepo := newExecutionNodePairingService("api", "source-db", "source-redis")
	source.cfg.Database = config.DatabaseConfig{Host: "postgres", Port: 5432, User: "xiass", Password: "db-secret", DBName: "xiass", SSLMode: "disable"}
	source.cfg.Redis = config.RedisConfig{Host: "redis", Port: 6379, Password: "redis-secret"}
	source.cfg.Totp.EncryptionKeyConfigured = true
	target, _ := newExecutionNodePairingService("api2", "target-db", "target-redis")
	target.SetExecutionNodeJoinInspector(executionNodeJoinTargetInspectorStub{empty: false})

	invite, err := source.GenerateExecutionNodePairingInvite(context.Background())
	require.NoError(t, err)
	_, err = target.PairExecutionNode(context.Background(), "https://source.example.com", invite.Token)
	require.Equal(t, "EXECUTION_NODE_PAIRING_TARGET_NOT_EMPTY", infraerrors.Reason(err))
	stored, readErr := sourceRepo.GetValue(context.Background(), SettingKeyExecutionNodePairingInvite)
	require.NoError(t, readErr)
	require.NotEmpty(t, stored)
}
