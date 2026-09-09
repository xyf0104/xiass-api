package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	executionNodePairingProtocolVersion = 1
	executionNodePairingInviteTTL       = 10 * time.Minute
	executionNodePairingHTTPTimeout     = 6 * time.Second
	executionNodePairingMaxResponseSize = 256 * 1024
	executionNodePairingInviteHeader    = "X-XIASS-Execution-Node-Invite"
)

var executionNodePairingInviteMu sync.Mutex

// ExecutionNodePairingStateReader provides the Redis-side identity without
// coupling the settings service to a concrete Redis client.
type ExecutionNodePairingStateReader interface {
	EnsureSharedStateIdentity(ctx context.Context, candidate string) (string, error)
}

// ExecutionNodeJoinApplier hands a source-authoritative join bundle to the
// deployment controller. The controller runs outside the application process
// so replacing the target container cannot interrupt the request that started
// the join.
type ExecutionNodeJoinApplier interface {
	LaunchExecutionNodeJoin(ctx context.Context, join ExecutionNodeJoinConfig) error
}

// ExecutionNodeRuntimeInitializer applies the local node identity outside the
// application container. The host-side controller owns the .env backup,
// restart, health check, and rollback so an admin request never mutates a live
// process in-place.
type ExecutionNodeRuntimeInitializer interface {
	LaunchExecutionNodeRuntime(ctx context.Context, runtime ExecutionNodeRuntimeConfig) error
}

type ExecutionNodeRuntimeConfig struct {
	NodeID                  string `json:"node_id"`
	TunnelToken             string `json:"tunnel_token"`
	DefaultProxyID          int64  `json:"default_proxy_id"`
	LegacyUnassignedNodeID  string `json:"legacy_unassigned_node_id"`
	LegacyUnassignedProxyID int64  `json:"legacy_unassigned_proxy_id"`
}

type ExecutionNodeJoinConfig struct {
	SourceURL             string `json:"source_url"`
	TargetURL             string `json:"target_url"`
	SourceNodeID          string `json:"source_node_id"`
	TargetNodeID          string `json:"target_node_id"`
	TunnelProof           string `json:"tunnel_proof"`
	TargetProxyID         int64  `json:"target_proxy_id"`
	LegacyNodeID          string `json:"legacy_node_id"`
	LegacyProxyID         int64  `json:"legacy_proxy_id"`
	DatabaseHost          string `json:"database_host"`
	DatabasePort          int    `json:"database_port"`
	DatabaseUser          string `json:"database_user"`
	DatabasePass          string `json:"database_pass"`
	DatabaseName          string `json:"database_name"`
	DatabaseSSLMode       string `json:"database_sslmode"`
	RedisHost             string `json:"redis_host"`
	RedisPort             int    `json:"redis_port"`
	RedisUsername         string `json:"redis_username"`
	RedisPassword         string `json:"redis_password"`
	RedisDB               int    `json:"redis_db"`
	RedisEnableTLS        bool   `json:"redis_enable_tls"`
	JWTSecret             string `json:"jwt_secret"`
	JWTRefreshTokenStore  string `json:"jwt_refresh_token_store"`
	TOTPKey               string `json:"totp_key"`
	WitnessEnabled        bool   `json:"witness_enabled,omitempty"`
	WitnessURL            string `json:"witness_url,omitempty"`
	WitnessToken          string `json:"witness_token,omitempty"`
	WitnessClusterID      string `json:"witness_cluster_id,omitempty"`
	WitnessLeaseTTL       int    `json:"witness_lease_ttl_seconds,omitempty"`
	WitnessRequestTimeout int    `json:"witness_request_timeout_seconds,omitempty"`
}

// ExecutionNodePairingRepository adds the two operations that need database
// compare-and-set semantics. The ordinary SettingRepository remains unchanged
// for all existing callers and test doubles.
type ExecutionNodePairingRepository interface {
	EnsureExecutionNodeClusterID(ctx context.Context, candidate string) (string, error)
	AcceptExecutionNodePairing(ctx context.Context, expectedInvite string, peerSettings map[string]string) (bool, error)
}

type executionNodePairingInviteRecord struct {
	Hash      string `json:"hash"`
	ExpiresAt int64  `json:"expires_at"`
}

// ExecutionNodePairingPeer is intentionally metadata-only. It contains no
// database DSN, Redis password, JWT secret, API key, account credential, or
// proxy credential.
type ExecutionNodePairingPeer struct {
	NodeID              string    `json:"node_id"`
	Version             string    `json:"version,omitempty"`
	ProtocolVersion     int       `json:"protocol_version"`
	DatabaseFingerprint string    `json:"database_fingerprint"`
	RedisFingerprint    string    `json:"redis_fingerprint"`
	AuthFingerprint     string    `json:"auth_fingerprint"`
	StateFingerprint    string    `json:"state_fingerprint"`
	PairedAt            time.Time `json:"paired_at"`
	PeerURL             string    `json:"peer_url,omitempty"`
	Ready               bool      `json:"ready"`
	TunnelProofHash     string    `json:"tunnel_proof_hash,omitempty"`
	TunnelTokenHash     string    `json:"tunnel_token_hash,omitempty"`
}

type ExecutionNodePairingStatus struct {
	ProtocolVersion    int                       `json:"protocol_version"`
	LocalNodeID        string                    `json:"local_node_id"`
	Paired             bool                      `json:"paired"`
	ProductionReady    bool                      `json:"production_ready"`
	ProtocolCompatible bool                      `json:"protocol_compatible"`
	DatabaseShared     bool                      `json:"database_shared"`
	RedisShared        bool                      `json:"redis_shared"`
	AuthCompatible     bool                      `json:"auth_compatible"`
	InviteActive       bool                      `json:"invite_active"`
	InviteExpiresAt    *time.Time                `json:"invite_expires_at,omitempty"`
	StateFingerprint   string                    `json:"state_fingerprint,omitempty"`
	StateError         string                    `json:"state_error,omitempty"`
	Peer               *ExecutionNodePairingPeer `json:"peer,omitempty"`
}

type ExecutionNodePairingInvite struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ExecutionNodePairingHandshakeRequest struct {
	NodeID              string `json:"node_id"`
	PeerURL             string `json:"peer_url,omitempty"`
	SourceURL           string `json:"source_url,omitempty"`
	Version             string `json:"version,omitempty"`
	ProtocolVersion     int    `json:"protocol_version"`
	JoinBundleVersion   int    `json:"join_bundle_version,omitempty"`
	DatabaseFingerprint string `json:"database_fingerprint"`
	RedisFingerprint    string `json:"redis_fingerprint"`
	AuthFingerprint     string `json:"auth_fingerprint"`
	StateFingerprint    string `json:"state_fingerprint"`
}

type ExecutionNodePairingHandshakeResponse struct {
	NodeID              string `json:"node_id"`
	Version             string `json:"version,omitempty"`
	ProtocolVersion     int    `json:"protocol_version"`
	DatabaseFingerprint string `json:"database_fingerprint"`
	RedisFingerprint    string `json:"redis_fingerprint"`
	AuthFingerprint     string `json:"auth_fingerprint"`
	StateFingerprint    string `json:"state_fingerprint"`
	Authoritative       bool   `json:"authoritative,omitempty"`
	EncryptedJoinBundle string `json:"encrypted_join_bundle,omitempty"`
	TunnelProof         string `json:"tunnel_proof,omitempty"`
}

type executionNodeJoinBundle struct {
	Version               int    `json:"version"`
	SourceURL             string `json:"source_url,omitempty"`
	TargetURL             string `json:"target_url,omitempty"`
	SourceNodeID          string `json:"source_node_id"`
	TargetNodeID          string `json:"target_node_id"`
	TunnelProof           string `json:"tunnel_proof"`
	TargetProxyID         int64  `json:"target_proxy_id,omitempty"`
	LegacyNodeID          string `json:"legacy_node_id,omitempty"`
	LegacyProxyID         int64  `json:"legacy_proxy_id,omitempty"`
	DatabaseHost          string `json:"database_host"`
	DatabasePort          int    `json:"database_port"`
	DatabaseUser          string `json:"database_user"`
	DatabasePass          string `json:"database_pass"`
	DatabaseName          string `json:"database_name"`
	DatabaseSSLMode       string `json:"database_sslmode"`
	RedisHost             string `json:"redis_host"`
	RedisPort             int    `json:"redis_port"`
	RedisUsername         string `json:"redis_username"`
	RedisPassword         string `json:"redis_password"`
	RedisDB               int    `json:"redis_db"`
	RedisEnableTLS        bool   `json:"redis_enable_tls"`
	JWTSecret             string `json:"jwt_secret"`
	JWTRefreshTokenStore  string `json:"jwt_refresh_token_store,omitempty"`
	TOTPKey               string `json:"totp_key"`
	WitnessEnabled        bool   `json:"witness_enabled,omitempty"`
	WitnessURL            string `json:"witness_url,omitempty"`
	WitnessToken          string `json:"witness_token,omitempty"`
	WitnessClusterID      string `json:"witness_cluster_id,omitempty"`
	WitnessLeaseTTL       int    `json:"witness_lease_ttl_seconds,omitempty"`
	WitnessRequestTimeout int    `json:"witness_request_timeout_seconds,omitempty"`
}

const executionNodeJoinBundleVersion = 1
const executionNodePersistentJoinBundleVersion = 2

type executionNodePairingEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (s *SettingService) SetExecutionNodePairingStateReader(reader ExecutionNodePairingStateReader) {
	if s != nil {
		s.executionNodePairingState = reader
	}
}

func (s *SettingService) localExecutionNodeID() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return strings.TrimSpace(s.cfg.Gateway.ExecutionNode.ID)
}

func executionNodeFingerprint(label, value string) string {
	digest := sha256.Sum256([]byte("xiass-execution-node:" + label + ":" + value))
	return hex.EncodeToString(digest[:])
}

func executionNodeStateFingerprint(databaseFingerprint, redisFingerprint string) string {
	digest := sha256.Sum256([]byte("xiass-execution-state:v1:" + databaseFingerprint + ":" + redisFingerprint))
	return hex.EncodeToString(digest[:])
}

func executionNodeAuthFingerprint(jwtSecret, encryptionKey string) string {
	return executionNodeFingerprint("auth", strings.TrimSpace(jwtSecret)+":"+strings.TrimSpace(encryptionKey))
}

func newPairingRandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func pairingTokenHash(token string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(digest[:])
}

func executionNodeJoinKey(token, targetNodeID string) []byte {
	digest := sha256.Sum256([]byte("xiass-execution-node-join:v1:" + strings.TrimSpace(token) + ":" + strings.TrimSpace(targetNodeID)))
	return digest[:]
}

func encryptExecutionNodeJoinBundle(token, targetNodeID string, bundle executionNodeJoinBundle) (string, error) {
	plaintext, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(executionNodeJoinKey(token, targetNodeID))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(targetNodeID))
	payload := append(nonce, ciphertext...)
	return hex.EncodeToString(payload), nil
}

func decryptExecutionNodeJoinBundle(token, targetNodeID, encoded string) (executionNodeJoinBundle, error) {
	payload, err := hex.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return executionNodeJoinBundle{}, errors.New("join bundle encoding is invalid")
	}
	block, err := aes.NewCipher(executionNodeJoinKey(token, targetNodeID))
	if err != nil {
		return executionNodeJoinBundle{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return executionNodeJoinBundle{}, err
	}
	if len(payload) < gcm.NonceSize() {
		return executionNodeJoinBundle{}, errors.New("join bundle is incomplete")
	}
	plaintext, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], []byte(targetNodeID))
	if err != nil {
		return executionNodeJoinBundle{}, errors.New("join bundle authentication failed")
	}
	var bundle executionNodeJoinBundle
	if err := json.Unmarshal(plaintext, &bundle); err != nil {
		return executionNodeJoinBundle{}, errors.New("join bundle payload is invalid")
	}
	if (bundle.Version != executionNodeJoinBundleVersion && bundle.Version != executionNodePersistentJoinBundleVersion) || bundle.TargetNodeID != targetNodeID || !validExecutionNodeID(bundle.SourceNodeID) || len(bundle.TunnelProof) != 64 {
		return executionNodeJoinBundle{}, errors.New("join bundle fields are invalid")
	}
	store, err := executionNodeJoinRefreshStore(bundle.JWTRefreshTokenStore)
	if err != nil || (bundle.Version == executionNodeJoinBundleVersion && store != "redis") ||
		(bundle.Version == executionNodePersistentJoinBundleVersion && store != "postgres") {
		return executionNodeJoinBundle{}, errors.New("join bundle refresh-session storage is invalid")
	}
	bundle.JWTRefreshTokenStore = store
	if !validExecutionNodeWitnessJoinBundle(bundle) {
		return executionNodeJoinBundle{}, errors.New("join bundle witness configuration is invalid")
	}
	return bundle, nil
}

func validExecutionNodeWitnessJoinBundle(bundle executionNodeJoinBundle) bool {
	if !bundle.WitnessEnabled {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(bundle.WitnessURL))
	if err != nil || parsed == nil || !parsed.IsAbs() || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	return (parsed.Scheme == "https" || (parsed.Scheme == "http" && loopback)) &&
		len(strings.TrimSpace(bundle.WitnessToken)) >= 32 && validExecutionNodeClusterID(bundle.WitnessClusterID) &&
		bundle.WitnessLeaseTTL >= 10 && bundle.WitnessLeaseTTL <= 60 &&
		bundle.WitnessRequestTimeout >= 1 && bundle.WitnessRequestTimeout <= 10 && bundle.WitnessRequestTimeout < bundle.WitnessLeaseTTL
}

func executionNodeJoinRefreshStore(value string) (string, error) {
	if value == "" {
		return "redis", nil
	}
	if value != "redis" && value != "postgres" {
		return "", errors.New("source refresh-session storage is invalid")
	}
	return value, nil
}

func (s *SettingService) authoritativeJoinAvailable() bool {
	return s != nil && s.cfg != nil &&
		strings.TrimSpace(s.cfg.Database.Host) != "" && s.cfg.Database.Port > 0 &&
		strings.TrimSpace(s.cfg.Redis.Host) != "" && s.cfg.Redis.Port > 0 &&
		strings.TrimSpace(s.cfg.JWT.Secret) != "" && strings.TrimSpace(s.cfg.Totp.EncryptionKey) != "" && s.cfg.Totp.EncryptionKeyConfigured
}

func (s *SettingService) createExecutionNodeJoinBundle(ctx context.Context, targetNodeID, targetURL, sourceURL string) (executionNodeJoinBundle, string, error) {
	if !s.authoritativeJoinAvailable() {
		return executionNodeJoinBundle{}, "", errors.New("source node does not have a complete runtime configuration")
	}
	store, err := executionNodeJoinRefreshStore(s.cfg.JWT.RefreshTokenStore)
	if err != nil {
		return executionNodeJoinBundle{}, "", err
	}
	bundleVersion := executionNodeJoinBundleVersion
	if store == "postgres" {
		bundleVersion = executionNodePersistentJoinBundleVersion
	}
	witnessCfg := s.cfg.Gateway.ExecutionNode.Witness
	witnessClusterID := strings.TrimSpace(witnessCfg.ClusterID)
	if witnessCfg.Enabled {
		if s.executionNodeFailover == nil || !s.executionNodeFailover.Ready() {
			return executionNodeJoinBundle{}, "", errors.New("source node failover witness is not ready")
		}
		if witnessClusterID == "" {
			witnessClusterID, err = s.ensureExecutionNodeDatabaseIdentity(ctx)
			if err != nil {
				return executionNodeJoinBundle{}, "", err
			}
		}
	}
	tunnelProof := configuredExecutionNodeTunnelToken()
	if tunnelProof == "" && s.cfg != nil {
		// Keep constructor-level and older manually configured deployments
		// compatible. The runtime uses the same deterministic derivation when its
		// explicit token is absent, while new web initialization persists a random
		// token in the host environment.
		tunnelProof = executionNodeTunnelToken(s.cfg.JWT.Secret)
	}
	if len(tunnelProof) != 64 {
		return executionNodeJoinBundle{}, "", errors.New("source node tunnel runtime is not initialized; initialize this node before pairing")
	}
	targetProxyID := int64(0)
	if s.proxyRepo != nil {
		proxy, _, err := s.ensureExecutionNodeBuiltinProxy(ctx, targetNodeID, tunnelProof)
		if err != nil {
			return executionNodeJoinBundle{}, "", err
		}
		targetProxyID = proxy.ID
	}
	legacyNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	if !validExecutionNodeID(legacyNodeID) {
		legacyNodeID = s.localExecutionNodeID()
	}
	legacyProxyID := s.cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID
	if legacyProxyID <= 0 {
		legacyProxyID = s.cfg.Gateway.ExecutionNode.DefaultProxyID
	}
	bundle := executionNodeJoinBundle{
		Version: bundleVersion, SourceURL: strings.TrimRight(strings.TrimSpace(sourceURL), "/"), TargetURL: strings.TrimRight(strings.TrimSpace(targetURL), "/"), SourceNodeID: s.localExecutionNodeID(), TargetNodeID: targetNodeID, TunnelProof: tunnelProof,
		TargetProxyID: targetProxyID, LegacyNodeID: legacyNodeID, LegacyProxyID: legacyProxyID,
		DatabaseHost: s.cfg.Database.Host, DatabasePort: s.cfg.Database.Port, DatabaseUser: s.cfg.Database.User, DatabasePass: s.cfg.Database.Password,
		DatabaseName: s.cfg.Database.DBName, DatabaseSSLMode: s.cfg.Database.SSLMode,
		RedisHost: s.cfg.Redis.Host, RedisPort: s.cfg.Redis.Port, RedisUsername: s.cfg.Redis.Username, RedisPassword: s.cfg.Redis.Password, RedisDB: s.cfg.Redis.DB, RedisEnableTLS: s.cfg.Redis.EnableTLS,
		JWTSecret: s.cfg.JWT.Secret, JWTRefreshTokenStore: store, TOTPKey: s.cfg.Totp.EncryptionKey,
		WitnessEnabled: witnessCfg.Enabled, WitnessURL: strings.TrimRight(strings.TrimSpace(witnessCfg.URL), "/"), WitnessToken: strings.TrimSpace(witnessCfg.Token),
		WitnessClusterID: witnessClusterID, WitnessLeaseTTL: witnessCfg.LeaseTTLSeconds, WitnessRequestTimeout: witnessCfg.RequestTimeoutSeconds,
	}
	if !validExecutionNodeWitnessJoinBundle(bundle) {
		return executionNodeJoinBundle{}, "", errors.New("source node failover witness configuration is invalid")
	}
	return bundle, tunnelProof, nil
}

func (s *SettingService) executionNodePairingRoutingSettings(ctx context.Context, targetNodeID string, targetProxyID int64) (map[string]string, error) {
	if targetProxyID <= 0 || s == nil || s.settingRepo == nil || s.cfg == nil {
		return map[string]string{}, nil
	}
	localNodeID := s.localExecutionNodeID()
	if !validExecutionNodeID(localNodeID) {
		return nil, errors.New("source node ID is invalid")
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyExecutionNodeWeights, SettingKeyExecutionNodeProxyIDs})
	if err != nil {
		return nil, fmt.Errorf("read execution-node routing settings: %w", err)
	}
	weights := map[string]float64{localNodeID: 9}
	if raw := strings.TrimSpace(values[SettingKeyExecutionNodeWeights]); raw != "" {
		weights, err = decodeExecutionNodeWeights(raw)
		if err != nil {
			return nil, err
		}
	}
	// Older single-node runtimes were initialized with weight 1. When their
	// first peer is added, finish the default migration in the same atomic
	// pairing write so the cluster never starts life as an accidental 1:1 pool.
	if len(weights) == 1 && weights[localNodeID] == 1 {
		weights[localNodeID] = 9
	}
	if _, exists := weights[localNodeID]; !exists {
		weights[localNodeID] = 9
	}
	if _, exists := weights[targetNodeID]; !exists {
		weights[targetNodeID] = 1
	}
	proxyIDs := map[string]int64{}
	if raw := strings.TrimSpace(values[SettingKeyExecutionNodeProxyIDs]); raw != "" {
		proxyIDs, err = decodeExecutionNodeProxyIDs(raw)
		if err != nil {
			return nil, err
		}
	}
	if localProxyID := s.cfg.Gateway.ExecutionNode.DefaultProxyID; localProxyID > 0 {
		proxyIDs[localNodeID] = localProxyID
	}
	if existing := proxyIDs[targetNodeID]; existing > 0 && existing != targetProxyID {
		return nil, fmt.Errorf("target node %s is already mapped to a different proxy", targetNodeID)
	}
	proxyIDs[targetNodeID] = targetProxyID
	if err := validateExecutionNodeProxyIDs(proxyIDs, weights); err != nil {
		return nil, err
	}
	weightsJSON, err := json.Marshal(weights)
	if err != nil {
		return nil, err
	}
	proxyJSON, err := json.Marshal(proxyIDs)
	if err != nil {
		return nil, err
	}
	// Pairing establishes routing, not takeover permission. Never overwrite a
	// concurrent or existing admin choice; the independent toggle owns it.
	return map[string]string{
		SettingKeyExecutionNodeWeights:  string(weightsJSON),
		SettingKeyExecutionNodeProxyIDs: string(proxyJSON),
	}, nil
}

func (s *SettingService) ensureExecutionNodeDatabaseIdentity(ctx context.Context) (string, error) {
	if s == nil || s.settingRepo == nil {
		return "", errors.New("settings repository is unavailable")
	}
	candidate := uuid.NewString()
	if repo, ok := s.settingRepo.(ExecutionNodePairingRepository); ok {
		value, err := repo.EnsureExecutionNodeClusterID(ctx, candidate)
		if err != nil {
			return "", fmt.Errorf("ensure database cluster identity: %w", err)
		}
		if strings.TrimSpace(value) == "" {
			return "", errors.New("database cluster identity is empty")
		}
		return strings.TrimSpace(value), nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyExecutionNodeClusterID)
	if err == nil && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return "", fmt.Errorf("read database cluster identity: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyExecutionNodeClusterID, candidate); err != nil {
		return "", fmt.Errorf("persist database cluster identity: %w", err)
	}
	value, err = s.settingRepo.GetValue(ctx, SettingKeyExecutionNodeClusterID)
	if err != nil {
		return "", fmt.Errorf("verify database cluster identity: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func (s *SettingService) localPairingMaterial(ctx context.Context) (databaseFingerprint, redisFingerprint, authFingerprint, stateFingerprint string, err error) {
	if s == nil {
		return "", "", "", "", errors.New("local pairing service is unavailable")
	}
	if s.cfg == nil {
		return "", "", "", "", errors.New("local auth configuration is unavailable")
	}
	if s.executionNodePairingState == nil {
		return "", "", "", "", errors.New("redis shared-state identity is unavailable")
	}
	databaseID, err := s.ensureExecutionNodeDatabaseIdentity(ctx)
	if err != nil {
		return "", "", "", "", err
	}
	databaseFingerprint = executionNodeFingerprint("postgres", databaseID)
	authFingerprint = executionNodeAuthFingerprint(s.cfg.JWT.Secret, s.cfg.Totp.EncryptionKey)
	redisID, err := s.executionNodePairingState.EnsureSharedStateIdentity(ctx, uuid.NewString())
	if err != nil {
		return databaseFingerprint, "", authFingerprint, "", fmt.Errorf("ensure Redis cluster identity: %w", err)
	}
	redisFingerprint = executionNodeFingerprint("redis", strings.TrimSpace(redisID))
	return databaseFingerprint, redisFingerprint, authFingerprint, executionNodeStateFingerprint(databaseFingerprint, redisFingerprint), nil
}

// GetExecutionNodePairingStatus is deliberately read-only from the caller's
// point of view. It lazily creates non-secret state identities, then compares
// the peer metadata saved by the handshake.
func (s *SettingService) GetExecutionNodePairingStatus(ctx context.Context) (*ExecutionNodePairingStatus, error) {
	status := &ExecutionNodePairingStatus{
		ProtocolVersion: executionNodePairingProtocolVersion,
		LocalNodeID:     s.localExecutionNodeID(),
	}
	databaseFingerprint, redisFingerprint, authFingerprint, stateFingerprint, materialErr := s.localPairingMaterial(ctx)
	if materialErr == nil {
		status.StateFingerprint = stateFingerprint
	} else {
		status.StateError = materialErr.Error()
	}

	if s == nil || s.settingRepo == nil {
		return status, errors.New("settings repository is unavailable")
	}
	if raw, err := s.settingRepo.GetValue(ctx, SettingKeyExecutionNodePairingInvite); err == nil {
		var invite executionNodePairingInviteRecord
		if json.Unmarshal([]byte(raw), &invite) == nil && invite.Hash != "" && invite.ExpiresAt > time.Now().Unix() {
			expiresAt := time.Unix(invite.ExpiresAt, 0).UTC()
			status.InviteActive = true
			status.InviteExpiresAt = &expiresAt
		}
	} else if !errors.Is(err, ErrSettingNotFound) {
		return status, fmt.Errorf("read pairing invite: %w", err)
	}

	peerKey := executionNodePairingPeerKey(status.LocalNodeID)
	if peerKey == "" {
		return status, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, peerKey)
	if errors.Is(err, ErrSettingNotFound) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("read pairing peer: %w", err)
	}
	var peer ExecutionNodePairingPeer
	if err := json.Unmarshal([]byte(raw), &peer); err != nil {
		return status, fmt.Errorf("decode pairing peer: %w", err)
	}
	status.Peer = &peer
	status.Paired = strings.TrimSpace(peer.NodeID) != "" && peer.NodeID != status.LocalNodeID
	status.ProtocolCompatible = peer.ProtocolVersion == executionNodePairingProtocolVersion
	status.DatabaseShared = materialErr == nil && peer.DatabaseFingerprint == databaseFingerprint
	status.RedisShared = materialErr == nil && peer.RedisFingerprint == redisFingerprint
	status.AuthCompatible = materialErr == nil && peer.AuthFingerprint == authFingerprint
	// Pairing records created before the source-authoritative join protocol did
	// not have a Ready bit or tunnel proof. Preserve their verified shared-state
	// semantics; only the new proof-bearing flow requires explicit finalization.
	pairingReady := peer.Ready || peer.TunnelProofHash == ""
	status.ProductionReady = status.Paired && pairingReady && status.ProtocolCompatible && status.DatabaseShared && status.RedisShared && status.AuthCompatible && peer.StateFingerprint == stateFingerprint
	return status, nil
}

func executionNodePairingPeerKey(nodeID string) string {
	if !validExecutionNodeID(nodeID) {
		return ""
	}
	return SettingKeyExecutionNodePairingPeerPrefix + nodeID
}

// GenerateExecutionNodePairingInvite stores only a SHA-256 hash. The raw token
// is returned once and is never logged or persisted.
func (s *SettingService) GenerateExecutionNodePairingInvite(ctx context.Context) (*ExecutionNodePairingInvite, error) {
	if !validExecutionNodeID(s.localExecutionNodeID()) {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_NODE_INVALID", "configure a valid local execution node ID before generating an invite")
	}
	token, err := newPairingRandomToken()
	if err != nil {
		return nil, fmt.Errorf("generate pairing invite: %w", err)
	}
	expiresAt := time.Now().Add(executionNodePairingInviteTTL).UTC()
	record, err := json.Marshal(executionNodePairingInviteRecord{Hash: pairingTokenHash(token), ExpiresAt: expiresAt.Unix()})
	if err != nil {
		return nil, fmt.Errorf("encode pairing invite: %w", err)
	}
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("settings repository is unavailable")
	}
	if err := s.settingRepo.Set(ctx, SettingKeyExecutionNodePairingInvite, string(record)); err != nil {
		return nil, fmt.Errorf("persist pairing invite: %w", err)
	}
	return &ExecutionNodePairingInvite{Token: token, ExpiresAt: expiresAt}, nil
}

func normalizeExecutionNodePeerURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 2048 {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_URL_INVALID", "peer URL is required and must be no longer than 2048 characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_URL_INVALID", "peer URL must be an HTTPS URL without credentials, query, or fragment")
	}
	if u.Scheme != "https" {
		host := strings.ToLower(u.Hostname())
		if u.Scheme != "http" || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_URL_INVALID", "peer URL must use HTTPS; HTTP is allowed only for local testing")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func executionNodePairingEndpoint(peer *url.URL) string {
	clone := *peer
	clone.Path = strings.TrimRight(clone.Path, "/") + "/api/v1/internal/execution-nodes/pair"
	clone.RawPath = ""
	return clone.String()
}

func (s *SettingService) localPairingHandshakeResponseForNode(ctx context.Context, nodeID string) (*ExecutionNodePairingHandshakeResponse, error) {
	databaseFingerprint, redisFingerprint, authFingerprint, stateFingerprint, err := s.localPairingMaterial(ctx)
	if err != nil {
		return nil, err
	}
	return &ExecutionNodePairingHandshakeResponse{
		NodeID:              strings.TrimSpace(nodeID),
		Version:             strings.TrimSpace(s.version),
		ProtocolVersion:     executionNodePairingProtocolVersion,
		DatabaseFingerprint: databaseFingerprint,
		RedisFingerprint:    redisFingerprint,
		AuthFingerprint:     authFingerprint,
		StateFingerprint:    stateFingerprint,
	}, nil
}

func (s *SettingService) localPairingHandshakeResponse(ctx context.Context) (*ExecutionNodePairingHandshakeResponse, error) {
	return s.localPairingHandshakeResponseForNode(ctx, s.localExecutionNodeID())
}

// PairExecutionNode contacts the invited instance. When the peer is an
// authoritative source, the response contains an encrypted, one-time join
// bundle; the target hands it to the host updater and does not mutate its
// running container in-process.
func (s *SettingService) PairExecutionNode(ctx context.Context, peerURL, token string) (*ExecutionNodePairingStatus, error) {
	return s.PairExecutionNodeWithTarget(ctx, peerURL, token, s.localExecutionNodeID(), "")
}

// PairExecutionNodeWithTarget pairs a fresh installation before it has local
// multi-node environment variables. The target node ID and public URL are
// supplied by the administrator's browser and are persisted by the host join
// controller only after the source-authoritative bundle is verified.
func (s *SettingService) PairExecutionNodeWithTarget(ctx context.Context, peerURL, token, targetNodeID, targetURL string) (*ExecutionNodePairingStatus, error) {
	peer, err := normalizeExecutionNodePeerURL(peerURL)
	if err != nil {
		return nil, err
	}
	targetNodeID = strings.TrimSpace(targetNodeID)
	if !validExecutionNodeID(targetNodeID) {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_NODE_INVALID", "configure a valid target execution node ID before pairing")
	}
	token = strings.TrimSpace(token)
	if len(token) != 64 {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_TOKEN_INVALID", "pairing invite token is invalid")
	}
	if strings.TrimSpace(targetURL) != "" {
		normalizedTargetURL, targetURLErr := normalizeExecutionNodePeerURL(targetURL)
		if targetURLErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_TARGET_URL_INVALID", "target URL must be a valid HTTPS URL")
		}
		targetURL = strings.TrimRight(normalizedTargetURL.String(), "/")
	}
	if s.executionNodeJoinInspector != nil {
		empty, inspectErr := s.executionNodeJoinInspector.IsExecutionNodeJoinTargetEmpty(ctx)
		if inspectErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_TARGET_CHECK_FAILED", "cannot verify whether the target database is empty")
		}
		if !empty {
			return nil, infraerrors.Conflict("EXECUTION_NODE_PAIRING_TARGET_NOT_EMPTY", "the target XIASS installation already contains data; export or migrate it explicitly before joining the source state")
		}
	}
	handshake, err := s.localPairingHandshakeResponseForNode(ctx, targetNodeID)
	if err != nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_STATE_UNAVAILABLE", "both PostgreSQL and Redis shared-state identities must be available before pairing")
	}
	body, err := json.Marshal(ExecutionNodePairingHandshakeRequest{
		NodeID:              handshake.NodeID,
		PeerURL:             strings.TrimRight(strings.TrimSpace(targetURL), "/"),
		SourceURL:           strings.TrimRight(peer.String(), "/"),
		Version:             handshake.Version,
		ProtocolVersion:     handshake.ProtocolVersion,
		JoinBundleVersion:   executionNodePersistentJoinBundleVersion,
		DatabaseFingerprint: handshake.DatabaseFingerprint,
		RedisFingerprint:    handshake.RedisFingerprint,
		AuthFingerprint:     handshake.AuthFingerprint,
		StateFingerprint:    handshake.StateFingerprint,
	})
	if err != nil {
		return nil, fmt.Errorf("encode pairing request: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, executionNodePairingHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, executionNodePairingEndpoint(peer), bytes.NewReader(body))
	if err != nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_URL_INVALID", "peer URL cannot be used")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(executionNodePairingInviteHeader, token)
	client := &http.Client{Timeout: executionNodePairingHTTPTimeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_UNREACHABLE", "cannot reach the peer pairing endpoint")
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, executionNodePairingMaxResponseSize)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read pairing response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var envelope executionNodePairingEnvelope
		if json.Unmarshal(responseBody, &envelope) == nil && strings.TrimSpace(envelope.Message) != "" {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_REJECTED", envelope.Message)
		}
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_REJECTED", "peer rejected the pairing request")
	}
	var envelope executionNodePairingEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil || envelope.Code != 0 {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_REJECTED", "peer returned an invalid pairing response")
	}
	var remote ExecutionNodePairingHandshakeResponse
	if err := json.Unmarshal(envelope.Data, &remote); err != nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_REJECTED", "peer returned incomplete pairing metadata")
	}
	if remote.NodeID == handshake.NodeID || remote.ProtocolVersion != executionNodePairingProtocolVersion {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_STATE_MISMATCH", "peer is reachable, but PostgreSQL or Redis shared state does not match")
	}
	if remote.Authoritative {
		if strings.TrimSpace(remote.EncryptedJoinBundle) == "" {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_BUNDLE_INVALID", "the authoritative peer did not return a complete join bundle")
		}
		bundle, decryptErr := decryptExecutionNodeJoinBundle(token, handshake.NodeID, remote.EncryptedJoinBundle)
		if decryptErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_BUNDLE_INVALID", decryptErr.Error())
		}
		if bundle.SourceNodeID != remote.NodeID {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_BUNDLE_INVALID", "the authoritative join bundle does not match the peer")
		}
		if s.executionNodeJoinApplier == nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_APPLIER_UNAVAILABLE", "the target deployment does not have a host join controller")
		}
		if err := s.executionNodeJoinApplier.LaunchExecutionNodeJoin(ctx, ExecutionNodeJoinConfig{
			SourceURL: strings.TrimRight(peer.String(), "/"), TargetURL: bundle.TargetURL, SourceNodeID: bundle.SourceNodeID, TargetNodeID: bundle.TargetNodeID, TunnelProof: bundle.TunnelProof,
			TargetProxyID: bundle.TargetProxyID, LegacyNodeID: bundle.LegacyNodeID, LegacyProxyID: bundle.LegacyProxyID,
			DatabaseHost: bundle.DatabaseHost, DatabasePort: bundle.DatabasePort, DatabaseUser: bundle.DatabaseUser, DatabasePass: bundle.DatabasePass, DatabaseName: bundle.DatabaseName, DatabaseSSLMode: bundle.DatabaseSSLMode,
			RedisHost: bundle.RedisHost, RedisPort: bundle.RedisPort, RedisUsername: bundle.RedisUsername, RedisPassword: bundle.RedisPassword, RedisDB: bundle.RedisDB, RedisEnableTLS: bundle.RedisEnableTLS,
			JWTSecret: bundle.JWTSecret, JWTRefreshTokenStore: bundle.JWTRefreshTokenStore, TOTPKey: bundle.TOTPKey,
			WitnessEnabled: bundle.WitnessEnabled, WitnessURL: bundle.WitnessURL, WitnessToken: bundle.WitnessToken,
			WitnessClusterID: bundle.WitnessClusterID, WitnessLeaseTTL: bundle.WitnessLeaseTTL, WitnessRequestTimeout: bundle.WitnessRequestTimeout,
		}); err != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_APPLY_FAILED", "the target host rejected the source-authoritative join: "+err.Error())
		}
		return &ExecutionNodePairingStatus{
			ProtocolVersion: executionNodePairingProtocolVersion, LocalNodeID: handshake.NodeID, Paired: true,
			ProtocolCompatible: true, StateError: "join is applying; the target will restart and verify the shared state",
			Peer: &ExecutionNodePairingPeer{NodeID: remote.NodeID, Version: remote.Version, ProtocolVersion: remote.ProtocolVersion, DatabaseFingerprint: remote.DatabaseFingerprint, RedisFingerprint: remote.RedisFingerprint, AuthFingerprint: remote.AuthFingerprint, StateFingerprint: remote.StateFingerprint, PairedAt: time.Now().UTC(), PeerURL: strings.TrimRight(peer.String(), "/"), Ready: false},
		}, nil
	}
	if remote.DatabaseFingerprint != handshake.DatabaseFingerprint || remote.RedisFingerprint != handshake.RedisFingerprint || remote.AuthFingerprint != handshake.AuthFingerprint || remote.StateFingerprint != handshake.StateFingerprint {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_STATE_MISMATCH", "peer is reachable, but PostgreSQL or Redis shared state does not match")
	}
	pairedAt := time.Now().UTC()
	peerRecord, err := json.Marshal(ExecutionNodePairingPeer{
		NodeID:              remote.NodeID,
		Version:             remote.Version,
		ProtocolVersion:     remote.ProtocolVersion,
		DatabaseFingerprint: remote.DatabaseFingerprint,
		RedisFingerprint:    remote.RedisFingerprint,
		AuthFingerprint:     remote.AuthFingerprint,
		StateFingerprint:    remote.StateFingerprint,
		PairedAt:            pairedAt,
		PeerURL:             strings.TrimRight(peer.String(), "/"),
		Ready:               true,
	})
	if err != nil {
		return nil, fmt.Errorf("encode pairing peer: %w", err)
	}
	if err := s.settingRepo.Set(ctx, executionNodePairingPeerKey(handshake.NodeID), string(peerRecord)); err != nil {
		return nil, fmt.Errorf("persist pairing peer: %w", err)
	}
	return s.GetExecutionNodePairingStatus(ctx)
}

func (s *SettingService) AcceptExecutionNodePairingHandshake(ctx context.Context, token string, request *ExecutionNodePairingHandshakeRequest) (*ExecutionNodePairingHandshakeResponse, error) {
	if request == nil || !validExecutionNodeID(strings.TrimSpace(request.NodeID)) || request.ProtocolVersion != executionNodePairingProtocolVersion {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_REQUEST_INVALID", "pairing request is invalid or uses an incompatible protocol")
	}
	if request.NodeID == s.localExecutionNodeID() {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_SELF_REQUEST", "an execution node cannot pair with itself")
	}
	token = strings.TrimSpace(token)
	if len(token) != 64 {
		return nil, infraerrors.Unauthorized("EXECUTION_NODE_PAIRING_INVITE_INVALID", "pairing invite is invalid or expired")
	}
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("settings repository is unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyExecutionNodePairingInvite)
	if err != nil {
		return nil, infraerrors.Unauthorized("EXECUTION_NODE_PAIRING_INVITE_INVALID", "pairing invite is invalid or expired")
	}
	var invite executionNodePairingInviteRecord
	if json.Unmarshal([]byte(raw), &invite) != nil || invite.Hash == "" || invite.ExpiresAt <= time.Now().Unix() || invite.Hash != pairingTokenHash(token) {
		return nil, infraerrors.Unauthorized("EXECUTION_NODE_PAIRING_INVITE_INVALID", "pairing invite is invalid or expired")
	}
	handshake, err := s.localPairingHandshakeResponse(ctx)
	if err != nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_STATE_UNAVAILABLE", "the invited instance shared-state identity is unavailable")
	}
	authoritative := s.authoritativeJoinAvailable()
	if authoritative {
		store, storeErr := executionNodeJoinRefreshStore(s.cfg.JWT.RefreshTokenStore)
		if storeErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_BUNDLE_UNAVAILABLE", storeErr.Error())
		}
		if store == "postgres" && request.JoinBundleVersion < executionNodePersistentJoinBundleVersion {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_VERSION_MISMATCH", "upgrade the target before joining a source with PostgreSQL refresh sessions")
		}
	}
	if authoritative && strings.TrimSpace(request.PeerURL) == "" {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_TARGET_URL_REQUIRED", "the target must provide its public HTTPS URL for fixed-egress routing")
	}
	if authoritative {
		targetURL, targetURLErr := normalizeExecutionNodePeerURL(request.PeerURL)
		if targetURLErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_TARGET_URL_INVALID", "the target public URL must be a valid HTTPS URL")
		}
		request.PeerURL = strings.TrimRight(targetURL.String(), "/")
	}
	if authoritative && strings.TrimSpace(request.Version) != "" && strings.TrimSpace(handshake.Version) != "" && request.Version != handshake.Version {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_VERSION_MISMATCH", "the source and target must run the same XIASS version before joining")
	}
	if !authoritative && (request.DatabaseFingerprint != handshake.DatabaseFingerprint || request.RedisFingerprint != handshake.RedisFingerprint || request.AuthFingerprint != handshake.AuthFingerprint || request.StateFingerprint != handshake.StateFingerprint) {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_STATE_MISMATCH", "the two instances do not use the same PostgreSQL, Redis, and authentication state")
	}
	var encryptedBundle, tunnelProof string
	var targetProxyID int64
	if authoritative {
		bundle, bundleProof, bundleErr := s.createExecutionNodeJoinBundle(ctx, request.NodeID, request.PeerURL, request.SourceURL)
		if bundleErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_BUNDLE_UNAVAILABLE", bundleErr.Error())
		}
		targetProxyID = bundle.TargetProxyID
		encryptedBundle, bundleErr = encryptExecutionNodeJoinBundle(token, request.NodeID, bundle)
		if bundleErr != nil {
			return nil, fmt.Errorf("encrypt execution-node join bundle: %w", bundleErr)
		}
		tunnelProof = bundleProof
	}

	peerRecord, err := json.Marshal(ExecutionNodePairingPeer{
		NodeID:          request.NodeID,
		Version:         request.Version,
		ProtocolVersion: request.ProtocolVersion,
		DatabaseFingerprint: func() string {
			if authoritative {
				return handshake.DatabaseFingerprint
			}
			return request.DatabaseFingerprint
		}(),
		RedisFingerprint: func() string {
			if authoritative {
				return handshake.RedisFingerprint
			}
			return request.RedisFingerprint
		}(),
		AuthFingerprint: func() string {
			if authoritative {
				return handshake.AuthFingerprint
			}
			return request.AuthFingerprint
		}(),
		StateFingerprint: func() string {
			if authoritative {
				return handshake.StateFingerprint
			}
			return request.StateFingerprint
		}(),
		PairedAt: time.Now().UTC(),
		PeerURL:  strings.TrimRight(strings.TrimSpace(request.PeerURL), "/"),
		Ready:    !authoritative,
		TunnelProofHash: func() string {
			if tunnelProof == "" {
				return ""
			}
			return pairingTokenHash(tunnelProof)
		}(),
		TunnelTokenHash: func() string {
			if tunnelProof == "" {
				return ""
			}
			return pairingTokenHash(tunnelProof)
		}(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode accepted pairing peer: %w", err)
	}
	requesterRecord, err := json.Marshal(ExecutionNodePairingPeer{
		NodeID:              handshake.NodeID,
		Version:             handshake.Version,
		ProtocolVersion:     handshake.ProtocolVersion,
		DatabaseFingerprint: handshake.DatabaseFingerprint,
		RedisFingerprint:    handshake.RedisFingerprint,
		AuthFingerprint:     handshake.AuthFingerprint,
		StateFingerprint:    handshake.StateFingerprint,
		PairedAt:            time.Now().UTC(),
		PeerURL:             strings.TrimRight(strings.TrimSpace(request.SourceURL), "/"),
		Ready:               !authoritative,
		TunnelProofHash: func() string {
			if tunnelProof == "" {
				return ""
			}
			return pairingTokenHash(tunnelProof)
		}(),
		TunnelTokenHash: func() string {
			if tunnelProof == "" {
				return ""
			}
			return pairingTokenHash(tunnelProof)
		}(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode requester pairing peer: %w", err)
	}
	peerSettings := map[string]string{
		executionNodePairingPeerKey(s.localExecutionNodeID()): string(peerRecord),
		executionNodePairingPeerKey(request.NodeID):           string(requesterRecord),
	}
	if authoritative {
		routingSettings, routingErr := s.executionNodePairingRoutingSettings(ctx, request.NodeID, targetProxyID)
		if routingErr != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PAIRING_ROUTING_INVALID", routingErr.Error())
		}
		for key, value := range routingSettings {
			peerSettings[key] = value
		}
	}

	// Serialize the fallback path for test doubles. The production repository
	// consumes the invite and publishes both peer records in one transaction.
	executionNodePairingInviteMu.Lock()
	defer executionNodePairingInviteMu.Unlock()
	consumed := false
	if repo, ok := s.settingRepo.(ExecutionNodePairingRepository); ok {
		consumed, err = repo.AcceptExecutionNodePairing(ctx, raw, peerSettings)
	} else {
		current, readErr := s.settingRepo.GetValue(ctx, SettingKeyExecutionNodePairingInvite)
		if readErr == nil && current == raw {
			err = s.settingRepo.Set(ctx, SettingKeyExecutionNodePairingInvite, "")
			consumed = err == nil
			if consumed {
				err = s.settingRepo.SetMultiple(ctx, peerSettings)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("consume pairing invite: %w", err)
	}
	if !consumed {
		return nil, infraerrors.Unauthorized("EXECUTION_NODE_PAIRING_INVITE_REPLAYED", "pairing invite has already been used")
	}
	handshake.Authoritative = authoritative
	handshake.EncryptedJoinBundle = encryptedBundle
	return handshake, nil
}

func (s *SettingService) UnpairExecutionNode(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("settings repository is unavailable")
	}
	key := executionNodePairingPeerKey(s.localExecutionNodeID())
	if key == "" {
		return infraerrors.BadRequest("EXECUTION_NODE_PAIRING_NODE_INVALID", "local execution node ID is invalid")
	}
	raw, readErr := s.settingRepo.GetValue(ctx, key)
	if err := s.settingRepo.Delete(ctx, key); err != nil && !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("remove pairing peer: %w", err)
	}
	if readErr == nil {
		var peer ExecutionNodePairingPeer
		if json.Unmarshal([]byte(raw), &peer) == nil {
			if peerKey := executionNodePairingPeerKey(peer.NodeID); peerKey != "" {
				if err := s.settingRepo.Delete(ctx, peerKey); err != nil && !errors.Is(err, ErrSettingNotFound) {
					return fmt.Errorf("remove remote pairing peer: %w", err)
				}
			}
		}
	}
	return nil
}
