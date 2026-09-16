package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const openAIAdsPowerPairingTTL = 5 * time.Minute

type openAIAdsPowerHelperPairing struct {
	AdminUserID    int64     `json:"admin_user_id"`
	EnvironmentKey string    `json:"environment_key"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type openAIAdsPowerHelperDevice struct {
	AdminUserID    int64     `json:"admin_user_id"`
	DeviceID       string    `json:"device_id"`
	EnvironmentKey string    `json:"environment_key"`
	SecretHash     string    `json:"secret_hash"`
	PairedAt       time.Time `json:"paired_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

type openAIAdsPowerPairingRequest struct {
	EnvironmentKey string `json:"environment_key"`
}

type openAIAdsPowerPairingResponse struct {
	HelperURL      string    `json:"helper_url"`
	EnvironmentKey string    `json:"environment_key"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type openAIAdsPowerPairingRedeemRequest struct {
	Ticket   string `json:"ticket"`
	DeviceID string `json:"device_id"`
}

type openAIAdsPowerPairingRedeemResponse struct {
	DeviceSecret   string `json:"device_secret"`
	EnvironmentKey string `json:"environment_key"`
}

type openAIAdsPowerCommandRequest struct {
	DeviceID       string `json:"device_id"`
	EnvironmentKey string `json:"environment_key"`
}

type openAIAdsPowerCommandResponse struct {
	Ticket string `json:"ticket,omitempty"`
}

func openAIAdsPowerHashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func openAIAdsPowerDefaultDeviceKey(adminUserID int64, environmentKey string) string {
	return strconv.FormatInt(adminUserID, 10) + ":" + environmentKey
}

func openAIAdsPowerDeviceKey(deviceID, environmentKey string) string {
	return deviceID + ":" + environmentKey
}

func (s *openAIAdsPowerLaunchStore) savePairing(ctx context.Context, ticket string, pairing openAIAdsPowerHelperPairing) error {
	if s == nil || !validOpenAIAdsPowerOpaqueID(ticket, 256) || pairing.AdminUserID <= 0 || pairing.EnvironmentKey == "" {
		return errors.New("AdsPower helper pairing is invalid")
	}
	if client := s.redisClient(); client != nil {
		payload, err := json.Marshal(pairing)
		if err != nil {
			return err
		}
		return client.Set(ctx, openAIAdsPowerPairingRedisPrefix+ticket, payload, time.Until(pairing.ExpiresAt)).Err()
	}
	s.mu.Lock()
	s.pairings[ticket] = pairing
	s.mu.Unlock()
	return nil
}

func (s *openAIAdsPowerLaunchStore) consumePairing(ctx context.Context, ticket string) (openAIAdsPowerHelperPairing, bool, error) {
	if s == nil || ticket == "" {
		return openAIAdsPowerHelperPairing{}, false, nil
	}
	if client := s.redisClient(); client != nil {
		payload, err := client.GetDel(ctx, openAIAdsPowerPairingRedisPrefix+ticket).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAIAdsPowerHelperPairing{}, false, nil
		}
		if err != nil {
			return openAIAdsPowerHelperPairing{}, false, err
		}
		var pairing openAIAdsPowerHelperPairing
		if json.Unmarshal(payload, &pairing) != nil || !pairing.ExpiresAt.After(s.now()) {
			return openAIAdsPowerHelperPairing{}, false, nil
		}
		return pairing, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pairing, ok := s.pairings[ticket]
	delete(s.pairings, ticket)
	if !ok || !pairing.ExpiresAt.After(s.now()) {
		return openAIAdsPowerHelperPairing{}, false, nil
	}
	return pairing, true, nil
}

func (s *openAIAdsPowerLaunchStore) saveDevice(ctx context.Context, device openAIAdsPowerHelperDevice) error {
	if s == nil || device.AdminUserID <= 0 || device.DeviceID == "" || device.EnvironmentKey == "" || device.SecretHash == "" {
		return errors.New("AdsPower helper device is invalid")
	}
	if client := s.redisClient(); client != nil {
		payload, err := json.Marshal(device)
		if err != nil {
			return err
		}
		_, err = client.TxPipelined(ctx, func(pipe redisclient.Pipeliner) error {
			pipe.Set(ctx, openAIAdsPowerDeviceRedisPrefix+openAIAdsPowerDeviceKey(device.DeviceID, device.EnvironmentKey), payload, 0)
			pipe.Set(ctx, openAIAdsPowerDefaultRedisPrefix+openAIAdsPowerDefaultDeviceKey(device.AdminUserID, device.EnvironmentKey), device.DeviceID, 0)
			return nil
		})
		return err
	}
	s.mu.Lock()
	s.devices[openAIAdsPowerDeviceKey(device.DeviceID, device.EnvironmentKey)] = device
	s.defaults[openAIAdsPowerDefaultDeviceKey(device.AdminUserID, device.EnvironmentKey)] = device.DeviceID
	s.mu.Unlock()
	return nil
}

func (s *openAIAdsPowerLaunchStore) device(ctx context.Context, deviceID, environmentKey string) (openAIAdsPowerHelperDevice, bool, error) {
	if s == nil || deviceID == "" || environmentKey == "" {
		return openAIAdsPowerHelperDevice{}, false, nil
	}
	key := openAIAdsPowerDeviceKey(deviceID, environmentKey)
	if client := s.redisClient(); client != nil {
		payload, err := client.Get(ctx, openAIAdsPowerDeviceRedisPrefix+key).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAIAdsPowerHelperDevice{}, false, nil
		}
		if err != nil {
			return openAIAdsPowerHelperDevice{}, false, err
		}
		var device openAIAdsPowerHelperDevice
		if json.Unmarshal(payload, &device) != nil {
			return openAIAdsPowerHelperDevice{}, false, nil
		}
		return device, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[key]
	return device, ok, nil
}

func (s *openAIAdsPowerLaunchStore) defaultDevice(ctx context.Context, adminUserID int64, environmentKey string) (string, error) {
	key := openAIAdsPowerDefaultDeviceKey(adminUserID, environmentKey)
	if client := s.redisClient(); client != nil {
		value, err := client.Get(ctx, openAIAdsPowerDefaultRedisPrefix+key).Result()
		if errors.Is(err, redisclient.Nil) {
			return "", nil
		}
		return strings.TrimSpace(value), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.defaults[key], nil
}

func (s *openAIAdsPowerLaunchStore) enqueueHelperCommand(ctx context.Context, record openAIAdsPowerLaunchRecord, ticket, deliveryEnvironmentKey string) (bool, error) {
	deliveryEnvironmentKey = strings.TrimSpace(deliveryEnvironmentKey)
	if deliveryEnvironmentKey == "" {
		return false, nil
	}
	deviceID := ""
	if record.Existing != nil {
		deviceID = strings.TrimSpace(record.Existing.DeviceID)
	} else {
		var err error
		deviceID, err = s.defaultDevice(ctx, record.AdminUserID, deliveryEnvironmentKey)
		if err != nil {
			return false, err
		}
	}
	if deviceID == "" {
		return false, nil
	}
	device, ok, err := s.device(ctx, deviceID, deliveryEnvironmentKey)
	if err != nil || !ok {
		return false, err
	}
	if device.AdminUserID != record.AdminUserID {
		return false, nil
	}
	if client := s.redisClient(); client != nil {
		key := openAIAdsPowerCommandRedisPrefix + openAIAdsPowerDeviceKey(deviceID, deliveryEnvironmentKey)
		_, err := client.TxPipelined(ctx, func(pipe redisclient.Pipeliner) error {
			pipe.RPush(ctx, key, ticket)
			pipe.Expire(ctx, key, openAIAdsPowerLaunchTTL+time.Minute)
			return nil
		})
		return err == nil, err
	}
	s.mu.Lock()
	key := openAIAdsPowerDeviceKey(deviceID, deliveryEnvironmentKey)
	s.commands[key] = append(s.commands[key], ticket)
	s.mu.Unlock()
	return true, nil
}

func (s *openAIAdsPowerLaunchStore) nextHelperCommand(ctx context.Context, deviceID, environmentKey string) (string, error) {
	key := openAIAdsPowerDeviceKey(deviceID, environmentKey)
	if client := s.redisClient(); client != nil {
		value, err := client.LPop(ctx, openAIAdsPowerCommandRedisPrefix+key).Result()
		if errors.Is(err, redisclient.Nil) {
			return "", nil
		}
		return strings.TrimSpace(value), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.commands[key]
	if len(queue) == 0 {
		return "", nil
	}
	ticket := queue[0]
	s.commands[key] = queue[1:]
	return ticket, nil
}

func (s *openAIAdsPowerLaunchStore) authenticateDevice(ctx context.Context, deviceID, environmentKey, secret string) (openAIAdsPowerHelperDevice, bool, error) {
	device, ok, err := s.device(ctx, deviceID, environmentKey)
	if err != nil || !ok || device.EnvironmentKey != environmentKey {
		return openAIAdsPowerHelperDevice{}, false, err
	}
	expected, decodeErr := hex.DecodeString(device.SecretHash)
	actualSum := sha256.Sum256([]byte(secret))
	if decodeErr != nil || len(expected) != len(actualSum) || subtle.ConstantTimeCompare(expected, actualSum[:]) != 1 {
		return openAIAdsPowerHelperDevice{}, false, nil
	}
	now := s.now().UTC()
	if device.LastSeenAt.IsZero() || now.Sub(device.LastSeenAt) >= 30*time.Second {
		device.LastSeenAt = now
		if err := s.saveDevice(ctx, device); err != nil {
			return openAIAdsPowerHelperDevice{}, false, err
		}
	}
	return device, true, nil
}

func (h *OpenAIOAuthHandler) CreateOpenAIAdsPowerHelperPairing(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower helper pairing is unavailable")
		return
	}
	owner, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || owner.UserID <= 0 {
		response.Unauthorized(c, "administrator session is required")
		return
	}
	var req openAIAdsPowerPairingRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower helper pairing request")
		return
	}
	environmentKey := strings.TrimSpace(req.EnvironmentKey)
	if environmentKey == "" {
		environmentKey = openAIAdsPowerLocalEnvironment(c)
	}
	if !validOpenAIAdsPowerOpaqueID(environmentKey, 128) || environmentKey != openAIAdsPowerLocalEnvironment(c) {
		response.BadRequest(c, "AdsPower helper environment does not match this XIASS server")
		return
	}
	ticket, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "AdsPower helper pairing ticket could not be created")
		return
	}
	expiresAt := h.adsPowerLaunchStore.now().UTC().Add(openAIAdsPowerPairingTTL)
	if err := h.adsPowerLaunchStore.savePairing(c.Request.Context(), ticket, openAIAdsPowerHelperPairing{
		AdminUserID: owner.UserID, EnvironmentKey: environmentKey, ExpiresAt: expiresAt,
	}); err != nil {
		response.InternalError(c, "AdsPower helper pairing ticket could not be stored")
		return
	}
	helperURL, err := openAIAdsPowerHelperPairingURL(openAIAdsPowerRequestOrigin(c), environmentKey, ticket)
	if err != nil {
		response.InternalError(c, "AdsPower helper pairing URL is invalid")
		return
	}
	response.Success(c, openAIAdsPowerPairingResponse{HelperURL: helperURL, EnvironmentKey: environmentKey, ExpiresAt: expiresAt})
}

func (h *OpenAIOAuthHandler) RedeemOpenAIAdsPowerHelperPairing(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower helper pairing is unavailable")
		return
	}
	var req openAIAdsPowerPairingRedeemRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if c.ShouldBindJSON(&req) != nil || !validOpenAIAdsPowerOpaqueID(strings.TrimSpace(req.DeviceID), 128) {
		response.BadRequest(c, "Invalid AdsPower helper pairing")
		return
	}
	pairing, ok, err := h.adsPowerLaunchStore.consumePairing(c.Request.Context(), strings.TrimSpace(req.Ticket))
	if err != nil {
		response.InternalError(c, "AdsPower helper pairing ticket could not be read")
		return
	}
	if !ok {
		response.Error(c, http.StatusGone, "AdsPower helper pairing ticket has expired or was already used")
		return
	}
	secret, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "AdsPower helper device secret could not be created")
		return
	}
	now := h.adsPowerLaunchStore.now().UTC()
	device := openAIAdsPowerHelperDevice{
		AdminUserID: pairing.AdminUserID, DeviceID: strings.TrimSpace(req.DeviceID), EnvironmentKey: pairing.EnvironmentKey,
		SecretHash: openAIAdsPowerHashSecret(secret), PairedAt: now, LastSeenAt: now,
	}
	if err := h.adsPowerLaunchStore.saveDevice(c.Request.Context(), device); err != nil {
		response.InternalError(c, "AdsPower helper pairing could not be saved")
		return
	}
	response.Success(c, openAIAdsPowerPairingRedeemResponse{DeviceSecret: secret, EnvironmentKey: pairing.EnvironmentKey})
}

func (h *OpenAIOAuthHandler) PollOpenAIAdsPowerHelperCommand(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower helper command service is unavailable")
		return
	}
	var req openAIAdsPowerCommandRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower helper command request")
		return
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	environmentKey := strings.TrimSpace(req.EnvironmentKey)
	secret := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	device, ok, err := h.adsPowerLaunchStore.authenticateDevice(c.Request.Context(), deviceID, environmentKey, secret)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower helper device could not be verified")
		return
	}
	if !ok {
		response.Unauthorized(c, "AdsPower helper device is not paired")
		return
	}
	ticket, err := h.adsPowerLaunchStore.nextHelperCommand(c.Request.Context(), device.DeviceID, device.EnvironmentKey)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower helper command could not be read")
		return
	}
	response.Success(c, openAIAdsPowerCommandResponse{Ticket: ticket})
}

func openAIAdsPowerHelperPairingURL(serverOrigin, environmentKey, ticket string) (string, error) {
	launchURL, err := openAIAdsPowerHelperURL(serverOrigin, ticket)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(launchURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/pair"
	query := parsed.Query()
	query.Set("environment", environmentKey)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *openAIAdsPowerLaunchStore) helperDeviceStatus(ctx context.Context, adminUserID int64, environmentKey string) (*openAIAdsPowerHelperDevice, error) {
	deviceID, err := s.defaultDevice(ctx, adminUserID, environmentKey)
	if err != nil || deviceID == "" {
		return nil, err
	}
	device, ok, err := s.device(ctx, deviceID, environmentKey)
	if err != nil || !ok {
		return nil, err
	}
	if device.AdminUserID != adminUserID || device.EnvironmentKey != environmentKey {
		return nil, fmt.Errorf("AdsPower helper device does not match this administrator")
	}
	return &device, nil
}
