package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Ownership is explicit. A name beginning with XIASS is not proof that a
// profile can be deleted, and manually created AdsPower profiles are untouched.
type managedProfile struct {
	ServerOrigin    string    `json:"server_origin"`
	EnvironmentKey  string    `json:"environment_key"`
	SerialNumber    string    `json:"serial_number"`
	FingerprintSlot int       `json:"fingerprint_slot"`
	Shared          bool      `json:"shared,omitempty"`
	PendingUntil    time.Time `json:"pending_until,omitempty"`
	ProtectedUntil  time.Time `json:"protected_until,omitempty"`
}

type profileLease struct {
	SessionID string    `json:"session_id"`
	Until     time.Time `json:"until"`
}

type profileInventoryRef struct {
	ProfileID       string `json:"profile_id"`
	AccountID       int64  `json:"account_id"`
	EnvironmentKey  string `json:"environment_key"`
	FingerprintSlot int    `json:"fingerprint_slot"`
	Blocked         bool   `json:"blocked"`
	SharedProfile   bool   `json:"shared_profile,omitempty"`
}

type profileInventory struct {
	Complete           bool                  `json:"complete"`
	Profiles           []profileInventoryRef `json:"profiles"`
	PendingProfileIDs  []string              `json:"pending_profile_ids"`
	ReleasedProfileIDs []string              `json:"released_profile_ids"`
}

var errAdsPowerProfileBusy = errors.New("AdsPower 固定环境正在使用，请等待当前任务完成后重试")

func (s *helperServer) rememberCreatedProfile(origin string, launch *launchPayload, id string, slot int) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	next := cloneConfig(s.cfg)
	deadline := launch.CallbackExpiresAt
	if !deadline.After(time.Now()) || deadline.After(time.Now().Add(25*time.Minute)) {
		deadline = time.Now().Add(20 * time.Minute)
	}
	next.ManagedProfiles[id] = managedProfile{ServerOrigin: origin, EnvironmentKey: canonicalEnvironmentKey(launch.EnvironmentKey), FingerprintSlot: slot, PendingUntil: deadline.Add(2 * time.Minute)}
	identity := launch.LoginEmail
	if strings.TrimSpace(identity) == "" {
		identity = launch.AccountName
	}
	key := pendingProfileKey(origin, launch.EnvironmentKey, identity)
	if key != "" {
		next.PendingProfiles[key] = pendingProfileBinding{ProfileID: id, EnvironmentKey: canonicalEnvironmentKey(launch.EnvironmentKey), FingerprintSlot: slot}
	}
	if err := saveConfig(next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

func (s *helperServer) checkProfileIdle(ctx context.Context, ads *adsPowerClient, id string) error {
	cfg, _ := s.runtimeSnapshot()
	if lease := cfg.ProfileLeases[id]; lease.Until.After(time.Now()) {
		return errAdsPowerProfileBusy
	}
	active, err := ads.profileActive(ctx, id)
	if err != nil {
		return fmt.Errorf("无法确认浏览器是否空闲: %w", err)
	}
	if active {
		return errAdsPowerProfileBusy
	}
	return nil
}

func (s *helperServer) reserveManagedProfile(origin string, launch *launchPayload, profile *adsPowerProfile, slot int) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	next := cloneConfig(s.cfg)
	if lease := next.ProfileLeases[profile.UserID]; lease.Until.After(time.Now()) {
		return errAdsPowerProfileBusy
	}
	managed := next.ManagedProfiles[profile.UserID]
	if managed.ServerOrigin != "" && managed.ServerOrigin != origin {
		return errors.New("AdsPower 环境属于另一个服务器，不能混用")
	}
	managed.ServerOrigin = origin
	managed.EnvironmentKey = canonicalEnvironmentKey(launch.EnvironmentKey)
	managed.SerialNumber = profile.SerialNumber
	managed.FingerprintSlot = slot
	if launch.Existing != nil && launch.Existing.SharedProfile {
		managed.Shared = true
	}
	deadline := launch.CallbackExpiresAt
	if !deadline.After(time.Now()) || deadline.After(time.Now().Add(25*time.Minute)) {
		deadline = time.Now().Add(20 * time.Minute)
	}
	// Retain a just-created profile until the callback/account-creation window
	// closes, even if a server restart temporarily loses pending task metadata.
	managed.PendingUntil = deadline.Add(2 * time.Minute)
	next.ManagedProfiles[profile.UserID] = managed
	next.ProfileLeases[profile.UserID] = profileLease{SessionID: launch.SessionID, Until: deadline.Add(time.Minute)}
	if err := saveConfig(next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

func (s *helperServer) releaseProfileLease(id string) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	next := cloneConfig(s.cfg)
	delete(next.ProfileLeases, id)
	if err := saveConfig(next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

// Called under poolMu. A delayed callback must not close a newer account's
// browser, even if the old task deadline and lease have already expired.
func (s *helperServer) stopOwnedProfile(ctx context.Context, id, sessionID string) error {
	cfg, ads := s.runtimeSnapshot()
	lease, exists := cfg.ProfileLeases[id]
	if !exists || lease.SessionID != sessionID {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = ads.stopProfile(ctx, id)
		if lastErr == nil {
			return s.releaseProfileLease(id)
		}
		if err := sleepWithContext(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return err
		}
	}
	return lastErr
}

func (c *adsPowerClient) allProfiles(ctx context.Context) ([]adsPowerProfile, error) {
	profiles := make([]adsPowerProfile, 0)
	seen := make(map[string]bool)
	for page := 1; page <= 100; page++ {
		batch, err := c.profilePage(ctx, page, "")
		if err != nil {
			return nil, err
		}
		for _, profile := range batch {
			if !validOpaqueID(profile.UserID) || seen[profile.UserID] {
				return nil, errors.New("AdsPower profile listing is incomplete or repeated")
			}
			seen[profile.UserID] = true
			profiles = append(profiles, profile)
		}
		if len(batch) < 100 {
			return profiles, nil
		}
	}
	return nil, errors.New("AdsPower profile listing exceeded bounded pagination")
}

func (c *adsPowerClient) deleteManagedProfile(ctx context.Context, id string) error {
	if !validOpaqueID(id) {
		return errors.New("invalid managed profile ID")
	}
	return c.do(ctx, "POST", "/api/v1/user/delete", map[string]any{"user_ids": []string{id}}, nil)
}

func (s *helperServer) fetchProfileInventory(ctx context.Context, origin string) (*profileInventory, error) {
	cfg, _ := s.runtimeSnapshot()
	server, ok := cfg.Servers[origin]
	if !ok || server.DeviceSecret == "" {
		return nil, errors.New("profile inventory requires a paired server")
	}
	var response apiEnvelope[profileInventory]
	err := s.serverRequestWithBearer(ctx, origin, "/api/v1/tools/adspower/helpers/profiles/inventory", server.DeviceSecret,
		map[string]string{"device_id": cfg.DeviceID, "environment_key": server.EnvironmentKey}, &response)
	if err != nil || !response.Data.Complete {
		return nil, errors.New("server profile inventory unavailable or incomplete; keeping all profiles")
	}
	for _, ref := range response.Data.Profiles {
		if !validOpaqueID(ref.ProfileID) || canonicalEnvironmentKey(ref.EnvironmentKey) != canonicalEnvironmentKey(server.EnvironmentKey) {
			return nil, errors.New("server profile inventory scope mismatch; keeping all profiles")
		}
	}
	return &response.Data, nil
}

func (s *helperServer) authorizeProfileRelease(ctx context.Context, origin, id string) error {
	cfg, _ := s.runtimeSnapshot()
	server := cfg.Servers[origin]
	var response apiEnvelope[struct {
		Released bool `json:"released"`
	}]
	err := s.serverRequestWithBearer(ctx, origin, "/api/v1/tools/adspower/helpers/profiles/release", server.DeviceSecret,
		map[string]string{"device_id": cfg.DeviceID, "environment_key": server.EnvironmentKey, "profile_id": id}, &response)
	if err != nil || !response.Data.Released {
		return errors.New("server did not authorize profile release")
	}
	return nil
}

// Caller holds poolMu, excluding local launches through profile deletion.
func (s *helperServer) reconcileProfiles(ctx context.Context, origin string, ads *adsPowerClient) (int, error) {
	inventory, err := s.fetchProfileInventory(ctx, origin)
	if err != nil {
		return 0, err
	}
	profiles, err := ads.allProfiles(ctx)
	if err != nil {
		return 0, err
	}
	present := make(map[string]adsPowerProfile, len(profiles))
	for _, profile := range profiles {
		present[profile.UserID] = profile
	}
	keep := make(map[string]bool)
	for _, id := range inventory.PendingProfileIDs {
		keep[id] = true
	}
	refCounts := make(map[string]map[int64]bool)
	for _, ref := range inventory.Profiles {
		if refCounts[ref.ProfileID] == nil {
			refCounts[ref.ProfileID] = make(map[int64]bool)
		}
		refCounts[ref.ProfileID][ref.AccountID] = true
	}
	s.runtimeMu.Lock()
	next := cloneConfig(s.cfg)
	server := next.Servers[origin]
	adopt := func(id string, slot int) {
		profile, exists := present[id]
		if !exists {
			return
		}
		managed := next.ManagedProfiles[id]
		if managed.ServerOrigin != "" && managed.ServerOrigin != origin {
			keep[id] = true
			return
		}
		managed.ServerOrigin = origin
		managed.EnvironmentKey = canonicalEnvironmentKey(server.EnvironmentKey)
		managed.SerialNumber = profile.SerialNumber
		if slot > 0 {
			managed.FingerprintSlot = slot
		}
		managed.PendingUntil = time.Time{}
		if len(refCounts[id]) > 1 {
			managed.Shared = true
		}
		next.ManagedProfiles[id] = managed
	}
	for _, ref := range inventory.Profiles {
		adopt(ref.ProfileID, ref.FingerprintSlot)
		if ref.SharedProfile {
			managed, exists := next.ManagedProfiles[ref.ProfileID]
			if exists && managed.ServerOrigin == origin {
				managed.Shared = true
				next.ManagedProfiles[ref.ProfileID] = managed
			}
		}
		if !ref.Blocked {
			keep[ref.ProfileID] = true
		}
	}
	for _, id := range inventory.ReleasedProfileIDs {
		adopt(id, 0)
	}
	for id, managed := range next.ManagedProfiles {
		if managed.ServerOrigin != origin {
			continue
		}
		managed.ProtectedUntil = time.Time{}
		for _, pendingID := range inventory.PendingProfileIDs {
			if pendingID == id {
				managed.ProtectedUntil = time.Now().Add(2 * time.Minute)
				break
			}
		}
		next.ManagedProfiles[id] = managed
	}
	for id, managed := range next.ManagedProfiles {
		if managed.ServerOrigin != origin || keep[id] || next.ProfileLeases[id].Until.After(time.Now()) {
			continue
		}
		if _, exists := present[id]; !exists {
			// Remove only local stale retry hints. A bound account's absent
			// profile still requires server-side release, never silent rebinding.
			for key, pending := range next.PendingProfiles {
				if pending.ProfileID == id {
					delete(next.PendingProfiles, key)
				}
			}
			delete(next.ProfileLeases, id)
			if len(refCounts[id]) == 0 {
				delete(next.ManagedProfiles, id)
			}
		}
	}
	for _, server := range next.Servers {
		keep[server.TemplateProfileID] = true
	}
	if err := saveConfig(next); err != nil {
		s.runtimeMu.Unlock()
		return 0, err
	}
	s.cfg = next
	s.runtimeMu.Unlock()
	removed := 0
	for _, profile := range profiles {
		id := profile.UserID
		managed, owned := next.ManagedProfiles[id]
		if !owned || managed.ServerOrigin != origin || keep[id] || managed.PendingUntil.After(time.Now()) {
			continue
		}
		if err := s.checkProfileIdle(ctx, ads, id); err != nil {
			continue
		}
		if err := s.authorizeProfileRelease(ctx, origin, id); err != nil {
			continue
		}
		// Recheck after the server-side decision; manual AdsPower windows may
		// have been opened while the network request was in flight.
		if err := s.checkProfileIdle(ctx, ads, id); err != nil {
			continue
		}
		if err := ads.deleteManagedProfile(ctx, id); err != nil {
			return removed, err
		}
		if err := s.forgetManagedProfile(id); err != nil {
			return removed, err
		}
		removed++
		log.Printf("AdsPower released unoccupied managed profile %s", id)
	}
	return removed, nil
}

func (s *helperServer) forgetManagedProfile(id string) error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	next := cloneConfig(s.cfg)
	delete(next.ManagedProfiles, id)
	delete(next.ProfileLeases, id)
	for key, binding := range next.PendingProfiles {
		if binding.ProfileID == id {
			delete(next.PendingProfiles, key)
		}
	}
	if err := saveConfig(next); err != nil {
		return err
	}
	s.cfg = next
	return nil
}

func orderedPoolCandidates(cfg *config, origin, environment string, profiles []adsPowerProfile, now time.Time) []adsPowerProfile {
	result := make([]adsPowerProfile, 0)
	for _, profile := range profiles {
		managed, owned := cfg.ManagedProfiles[profile.UserID]
		if !owned || managed.ServerOrigin != origin || canonicalEnvironmentKey(managed.EnvironmentKey) != canonicalEnvironmentKey(environment) || cfg.ProfileLeases[profile.UserID].Until.After(now) || managed.PendingUntil.After(now) || managed.ProtectedUntil.After(now) {
			continue
		}
		template := false
		for _, server := range cfg.Servers {
			template = template || server.TemplateProfileID == profile.UserID
		}
		if !template {
			result = append(result, profile)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, _ := strconv.ParseUint(result[i].SerialNumber, 10, 64)
		b, _ := strconv.ParseUint(result[j].SerialNumber, 10, 64)
		if a != b {
			return a < b
		}
		return result[i].UserID < result[j].UserID
	})
	cursor := cfg.ReuseCursors[origin]
	for index, profile := range result {
		if profile.UserID == cursor {
			return append(result[index+1:], result[:index+1]...)
		}
	}
	return result
}

func (s *helperServer) reusePoolProfile(ctx context.Context, origin string, launch *launchPayload, server serverConfig, ads *adsPowerClient) (*adsPowerProfile, *adsPowerProfile, string, int, error) {
	profiles, err := ads.allProfiles(ctx)
	if err != nil {
		return nil, nil, "", 0, err
	}
	cfg, _ := s.runtimeSnapshot()
	var lastErr error
	for _, candidate := range orderedPoolCandidates(cfg, origin, launch.EnvironmentKey, profiles, time.Now()) {
		if err := s.checkProfileIdle(ctx, ads, candidate.UserID); err != nil {
			continue
		}
		managed := cfg.ManagedProfiles[candidate.UserID]
		copy := *launch
		copy.Existing = &existingBinding{DeviceID: cfg.DeviceID, ProfileID: candidate.UserID, EnvironmentKey: launch.EnvironmentKey, FingerprintSlot: managed.FingerprintSlot}
		profile, template, exit, err := s.prepareProfile(ctx, &copy, server, cfg.DeviceID, managed.FingerprintSlot, nil, ads)
		if err != nil {
			lastErr = err
			continue
		}
		s.runtimeMu.Lock()
		next := cloneConfig(s.cfg)
		managed.Shared = true
		next.ManagedProfiles[candidate.UserID] = managed
		next.ReuseCursors[origin] = candidate.UserID
		err = saveConfig(next)
		if err == nil {
			s.cfg = next
		}
		s.runtimeMu.Unlock()
		if err != nil {
			return nil, nil, "", 0, err
		}
		return profile, template, exit, managed.FingerprintSlot, err
	}
	if lastErr != nil {
		return nil, nil, "", 0, lastErr
	}
	return nil, nil, "", 0, errAdsPowerProfileBusy
}

func (s *helperServer) runProfileMaintenance(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg, ads := s.runtimeSnapshot()
			for origin, server := range cfg.Servers {
				if strings.TrimSpace(server.DeviceSecret) == "" {
					continue
				}
				if !s.poolMu.TryLock() {
					continue
				}
				work, cancel := context.WithTimeout(ctx, 45*time.Second)
				_, err := s.reconcileProfiles(work, origin, ads)
				cancel()
				s.poolMu.Unlock()
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("AdsPower profile maintenance skipped for %s: %v", server.EnvironmentKey, err)
				}
			}
		}
	}
}
