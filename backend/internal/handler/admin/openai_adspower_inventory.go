package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type openAIAdsPowerInventoryRequest struct {
	DeviceID       string `json:"device_id"`
	EnvironmentKey string `json:"environment_key"`
}

type openAIAdsPowerReleaseRequest struct {
	DeviceID       string `json:"device_id"`
	EnvironmentKey string `json:"environment_key"`
	ProfileID      string `json:"profile_id"`
}

type openAIAdsPowerInventoryService interface {
	ListOpenAIAdsPowerProfileInventory(context.Context, string, string) (*service.OpenAIAdsPowerProfileInventory, error)
	ReleaseOpenAIAdsPowerProfile(context.Context, string, string, string) (bool, error)
}

type openAIAdsPowerTaskActivity struct {
	activeByID      map[string]bool
	activeBySession map[string]bool
	accountIDs      map[int64]struct{}
}

var openAIAdsPowerLocalLifecycleLocks sync.Map

func (h *OpenAIOAuthHandler) lockOpenAIAdsPowerProfileLifecycle(ctx context.Context, deviceID, environmentKey, profileID string) (func(), error) {
	key := strings.Join([]string{"xiass:openai:adspower:profile-lifecycle", deviceID, environmentKey, profileID}, ":")
	if h != nil && h.adsPowerLaunchStore != nil {
		if client := h.adsPowerLaunchStore.redisClient(); client != nil {
			token, err := newTeamChildBrowserToken()
			if err != nil {
				return nil, err
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				acquired, err := client.SetNX(ctx, key, token, 15*time.Second).Result()
				if err != nil {
					return nil, err
				}
				if acquired {
					return func() {
						releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						_, _ = releaseBatchOAuthLock.Run(releaseCtx, client, []string{key}, token).Result()
					}, nil
				}
				if time.Now().After(deadline) {
					return nil, errors.New("AdsPower profile lifecycle is busy")
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(25 * time.Millisecond):
				}
			}
		}
	}
	value, _ := openAIAdsPowerLocalLifecycleLocks.LoadOrStore(key, &sync.Mutex{})
	lock, ok := value.(*sync.Mutex)
	if !ok || lock == nil {
		return nil, errors.New("AdsPower profile lifecycle lock is unavailable")
	}
	lock.Lock()
	return lock.Unlock, nil
}

func (h *OpenAIOAuthHandler) InventoryOpenAIAdsPowerProfiles(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile inventory is unavailable")
		return
	}
	var req openAIAdsPowerInventoryRequest
	if !bindOpenAIAdsPowerInventoryJSON(c, &req) {
		return
	}
	device, ok := h.authenticateOpenAIAdsPowerHelper(c, req.DeviceID, req.EnvironmentKey)
	if !ok {
		return
	}
	svc, ok := h.adminService.(openAIAdsPowerInventoryService)
	if !ok || svc == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile inventory is unavailable")
		return
	}
	inventory, err := svc.ListOpenAIAdsPowerProfileInventory(c.Request.Context(), device.DeviceID, device.EnvironmentKey)
	if err != nil || inventory == nil || !inventory.Complete {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile inventory is incomplete")
		return
	}
	activity, complete := h.openAIAdsPowerTaskActivity(device.AdminUserID)
	if !complete {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile inventory is incomplete")
		return
	}
	protected, complete, err := h.adsPowerLaunchStore.protectedProfileIDs(c.Request.Context(), device.AdminUserID, device.DeviceID, device.EnvironmentKey, activity)
	if err != nil || !complete {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile inventory is incomplete")
		return
	}
	addOpenAIAdsPowerActiveAccountProfiles(protected, inventory.Profiles, activity.accountIDs)
	inventory.PendingProfileIDs = sortedOpenAIAdsPowerProfileIDs(protected)
	response.Success(c, inventory)
}

func (h *OpenAIOAuthHandler) ReleaseOpenAIAdsPowerProfile(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile release is unavailable")
		return
	}
	var req openAIAdsPowerReleaseRequest
	if !bindOpenAIAdsPowerInventoryJSON(c, &req) {
		return
	}
	device, ok := h.authenticateOpenAIAdsPowerHelper(c, req.DeviceID, req.EnvironmentKey)
	if !ok {
		return
	}
	profileID := strings.TrimSpace(req.ProfileID)
	if !validOpenAIAdsPowerOpaqueID(profileID, 128) {
		response.BadRequest(c, "Invalid AdsPower profile ID")
		return
	}
	unlock, err := h.lockOpenAIAdsPowerProfileLifecycle(c.Request.Context(), device.DeviceID, device.EnvironmentKey, profileID)
	if err != nil {
		response.Error(c, http.StatusConflict, "AdsPower profile lifecycle is busy")
		return
	}
	defer unlock()
	operationCtx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(operationCtx)
	svc, ok := h.adminService.(openAIAdsPowerInventoryService)
	if !ok || svc == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile release is unavailable")
		return
	}
	inventory, err := svc.ListOpenAIAdsPowerProfileInventory(c.Request.Context(), device.DeviceID, device.EnvironmentKey)
	if err != nil || inventory == nil || !inventory.Complete {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile release is unavailable")
		return
	}
	activity, complete := h.openAIAdsPowerTaskActivity(device.AdminUserID)
	if !complete {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile release is unavailable")
		return
	}
	protected, complete, err := h.adsPowerLaunchStore.protectedProfileIDs(c.Request.Context(), device.AdminUserID, device.DeviceID, device.EnvironmentKey, activity)
	if err != nil || !complete {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile release is unavailable")
		return
	}
	addOpenAIAdsPowerActiveAccountProfiles(protected, inventory.Profiles, activity.accountIDs)
	if _, exists := protected[profileID]; exists {
		response.Success(c, gin.H{"released": false})
		return
	}
	released, err := svc.ReleaseOpenAIAdsPowerProfile(c.Request.Context(), device.DeviceID, device.EnvironmentKey, profileID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower profile release is unavailable")
		return
	}
	response.Success(c, gin.H{"released": released})
}

func bindOpenAIAdsPowerInventoryJSON(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	if c.ShouldBindJSON(target) != nil {
		response.BadRequest(c, "Invalid AdsPower profile request")
		return false
	}
	return true
}

func (h *OpenAIOAuthHandler) authenticateOpenAIAdsPowerHelper(c *gin.Context, deviceID, environmentKey string) (openAIAdsPowerHelperDevice, bool) {
	deviceID = strings.TrimSpace(deviceID)
	environmentKey = strings.TrimSpace(environmentKey)
	if !validOpenAIAdsPowerOpaqueID(deviceID, 128) || !validOpenAIAdsPowerOpaqueID(environmentKey, 128) {
		response.BadRequest(c, "Invalid AdsPower helper identity")
		return openAIAdsPowerHelperDevice{}, false
	}
	if environmentKey != openAIAdsPowerLocalEnvironment(c) {
		response.Unauthorized(c, "AdsPower helper environment does not match this XIASS server")
		return openAIAdsPowerHelperDevice{}, false
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		response.Unauthorized(c, "AdsPower helper device is not paired")
		return openAIAdsPowerHelperDevice{}, false
	}
	device, authenticated, err := h.adsPowerLaunchStore.authenticateDevice(c.Request.Context(), deviceID, environmentKey, parts[1])
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower helper device could not be verified")
		return openAIAdsPowerHelperDevice{}, false
	}
	if !authenticated {
		response.Unauthorized(c, "AdsPower helper device is not paired")
		return openAIAdsPowerHelperDevice{}, false
	}
	return device, true
}

func (h *OpenAIOAuthHandler) openAIAdsPowerTaskActivity(adminUserID int64) (openAIAdsPowerTaskActivity, bool) {
	activity := openAIAdsPowerTaskActivity{
		activeByID: make(map[string]bool), activeBySession: make(map[string]bool), accountIDs: make(map[int64]struct{}),
	}
	if h == nil || h.batchOAuthStore == nil {
		return activity, true
	}
	h.batchOAuthStore.mu.Lock()
	defer h.batchOAuthStore.mu.Unlock()
	for _, task := range h.batchOAuthStore.tasks {
		if task == nil || task.ownerID != adminUserID {
			continue
		}
		if !task.mu.TryLock() {
			return openAIAdsPowerTaskActivity{}, false
		}
		if task.usesAdsPower() {
			active := !task.terminal()
			activity.activeByID[task.ID] = active
			if task.sessionID != "" {
				activity.activeBySession[task.sessionID] = active
			}
			if active {
				if task.TargetAccountID > 0 {
					activity.accountIDs[task.TargetAccountID] = struct{}{}
				}
				if task.AccountID > 0 {
					activity.accountIDs[task.AccountID] = struct{}{}
				}
			}
		}
		task.mu.Unlock()
	}
	return activity, true
}

func (s *openAIAdsPowerLaunchStore) protectedProfileIDs(ctx context.Context, adminUserID int64, deviceID, environmentKey string, activity openAIAdsPowerTaskActivity) (map[string]struct{}, bool, error) {
	deviceID, environmentKey = strings.TrimSpace(deviceID), strings.TrimSpace(environmentKey)
	if s == nil || adminUserID <= 0 || deviceID == "" || environmentKey == "" {
		return nil, false, errors.New("AdsPower helper scope is invalid")
	}
	protected := make(map[string]struct{})
	if client := s.redisClient(); client != nil {
		patterns := []string{
			openAIAdsPowerLaunchRedisPrefix + "*",
			openAIAdsPowerBindingRedisPrefix + "*",
			openAIAdsPowerCallbackRedisPrefix + "*",
			openAIAdsPowerPendingRedisPrefix + "*",
		}
		seen := 0
		for _, pattern := range patterns {
			var cursor uint64
			pages := 0
			for {
				pages++
				if pages > 4096 {
					return nil, false, errors.New("AdsPower helper inventory scan is incomplete")
				}
				keys, next, err := client.Scan(ctx, cursor, pattern, 256).Result()
				if err != nil {
					return nil, false, err
				}
				seen += len(keys)
				if seen > service.OpenAIAdsPowerInventoryMaxBindings {
					return nil, false, errors.New("AdsPower helper inventory is too large")
				}
				for _, key := range keys {
					payload, err := client.Get(ctx, key).Bytes()
					if errors.Is(err, redisclient.Nil) {
						continue
					}
					if err != nil {
						return nil, false, err
					}
					if strings.HasPrefix(key, openAIAdsPowerPendingRedisPrefix) {
						var pending openAIAdsPowerPendingBinding
						if json.Unmarshal(payload, &pending) != nil || pending.AdminUserID <= 0 {
							return nil, false, errors.New("invalid AdsPower pending binding")
						}
						if pending.AdminUserID != adminUserID {
							continue
						}
						sessionID := strings.TrimPrefix(key, openAIAdsPowerPendingRedisPrefix)
						if active, known := activity.activeBySession[sessionID]; known && !active {
							continue
						}
						addOpenAIAdsPowerProtectedBinding(protected, pending.Binding, deviceID, environmentKey)
						continue
					}
					var record openAIAdsPowerLaunchRecord
					if json.Unmarshal(payload, &record) != nil {
						return nil, false, errors.New("invalid AdsPower launch record")
					}
					if record.AdminUserID != adminUserID {
						continue
					}
					if record.TaskID != "" {
						active, known := activity.activeByID[record.TaskID]
						if !known {
							// Batch tasks are process-local while launch records are
							// shared in Redis. A restart or another execution node can
							// therefore make a valid task ID locally unknown. Preserve
							// its existing profile when present; create-task profiles
							// are protected by the separately scanned pending binding.
							if record.Existing != nil {
								addOpenAIAdsPowerProtectedBinding(protected, *record.Existing, deviceID, environmentKey)
							}
							continue
						}
						if !active {
							continue
						}
					}
					if record.Existing != nil {
						addOpenAIAdsPowerProtectedBinding(protected, *record.Existing, deviceID, environmentKey)
					}
				}
				cursor = next
				if cursor == 0 {
					break
				}
			}
		}
		return protected, true, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	for _, record := range s.launches {
		protect, _ := openAIAdsPowerRecordNeedsProtection(record, adminUserID, activity)
		if !protect {
			continue
		}
		if record.Existing != nil {
			addOpenAIAdsPowerProtectedBinding(protected, *record.Existing, deviceID, environmentKey)
		}
	}
	for _, record := range s.bindings {
		protect, _ := openAIAdsPowerRecordNeedsProtection(record, adminUserID, activity)
		if !protect {
			continue
		}
		if record.Existing != nil {
			addOpenAIAdsPowerProtectedBinding(protected, *record.Existing, deviceID, environmentKey)
		}
	}
	for _, record := range s.callbacks {
		protect, _ := openAIAdsPowerRecordNeedsProtection(record, adminUserID, activity)
		if !protect {
			continue
		}
		if record.Existing != nil {
			addOpenAIAdsPowerProtectedBinding(protected, *record.Existing, deviceID, environmentKey)
		}
	}
	for sessionID, pending := range s.pending {
		if pending.AdminUserID != adminUserID {
			continue
		}
		if active, known := activity.activeBySession[sessionID]; known && !active {
			continue
		}
		addOpenAIAdsPowerProtectedBinding(protected, pending.Binding, deviceID, environmentKey)
	}
	return protected, true, nil
}

func openAIAdsPowerRecordNeedsProtection(record openAIAdsPowerLaunchRecord, adminUserID int64, activity openAIAdsPowerTaskActivity) (bool, bool) {
	if record.AdminUserID != adminUserID {
		return false, true
	}
	if record.TaskID == "" {
		return true, true
	}
	active, known := activity.activeByID[record.TaskID]
	if !known {
		// The in-memory path follows the same restart/cross-node rule as
		// Redis: unknown is not malformed, and only a known existing profile
		// can be protected directly from the launch record.
		return record.Existing != nil, true
	}
	return active, known
}

func addOpenAIAdsPowerActiveAccountProfiles(out map[string]struct{}, profiles []service.OpenAIAdsPowerInventoryProfile, accountIDs map[int64]struct{}) {
	for _, profile := range profiles {
		if _, active := accountIDs[profile.AccountID]; active && profile.ProfileID != "" {
			out[profile.ProfileID] = struct{}{}
		}
	}
}

func addOpenAIAdsPowerProtectedBinding(out map[string]struct{}, binding service.OpenAIAdsPowerBinding, deviceID, environmentKey string) {
	binding.Normalize()
	if binding.DeviceID == deviceID && binding.EnvironmentKey == environmentKey && binding.ProfileID != "" {
		out[binding.ProfileID] = struct{}{}
	}
}

func sortedOpenAIAdsPowerProfileIDs(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}
