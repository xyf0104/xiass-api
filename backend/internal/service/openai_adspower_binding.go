package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const OpenAIAdsPowerBindingExtraKey = "xiass_openai_adspower_binding"

// OpenAIAdsPowerBinding contains only non-secret browser-profile metadata.
// The AdsPower API key and proxy credentials remain on the administrator's
// local device and must never be persisted in XIASS account data.
type OpenAIAdsPowerBinding struct {
	Version               int        `json:"version"`
	DeviceID              string     `json:"device_id"`
	ProfileID             string     `json:"profile_id"`
	ProfileNo             string     `json:"profile_no,omitempty"`
	ProfileName           string     `json:"profile_name,omitempty"`
	EnvironmentKey        string     `json:"environment_key"`
	ProxyType             string     `json:"proxy_type,omitempty"`
	ProxyHost             string     `json:"proxy_host,omitempty"`
	ProxyPort             string     `json:"proxy_port,omitempty"`
	ProxyExitIP           string     `json:"proxy_exit_ip,omitempty"`
	WebRTCDisabled        bool       `json:"webrtc_disabled"`
	FingerprintRandomized bool       `json:"fingerprint_randomized"`
	FingerprintSlot       int        `json:"fingerprint_slot,omitempty"`
	SharedProfile         bool       `json:"shared_profile,omitempty"`
	BoundAt               *time.Time `json:"bound_at,omitempty"`
	LastVerifiedAt        *time.Time `json:"last_verified_at,omitempty"`
	LastLaunchedAt        *time.Time `json:"last_launched_at,omitempty"`
}

func OpenAIAdsPowerBindingFromAccount(account *Account) *OpenAIAdsPowerBinding {
	if account == nil || account.Extra == nil {
		return nil
	}
	return ParseOpenAIAdsPowerBinding(account.Extra[OpenAIAdsPowerBindingExtraKey])
}

func ParseOpenAIAdsPowerBinding(raw any) *OpenAIAdsPowerBinding {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var binding OpenAIAdsPowerBinding
	if json.Unmarshal(encoded, &binding) != nil {
		return nil
	}
	binding.Normalize()
	if binding.ProfileID == "" || binding.DeviceID == "" || binding.EnvironmentKey == "" ||
		!binding.WebRTCDisabled || !binding.FingerprintRandomized {
		return nil
	}
	return &binding
}

func (b *OpenAIAdsPowerBinding) Normalize() {
	if b == nil {
		return
	}
	b.Version = 1
	b.DeviceID = strings.TrimSpace(b.DeviceID)
	b.ProfileID = strings.TrimSpace(b.ProfileID)
	b.ProfileNo = strings.TrimSpace(b.ProfileNo)
	b.ProfileName = strings.TrimSpace(b.ProfileName)
	b.EnvironmentKey = strings.TrimSpace(b.EnvironmentKey)
	b.ProxyType = strings.ToLower(strings.TrimSpace(b.ProxyType))
	b.ProxyHost = strings.ToLower(strings.TrimSpace(b.ProxyHost))
	b.ProxyPort = strings.TrimSpace(b.ProxyPort)
	b.ProxyExitIP = strings.TrimSpace(b.ProxyExitIP)
	if b.FingerprintSlot < 0 || b.FingerprintSlot > 52 {
		b.FingerprintSlot = 0
	}
	b.BoundAt = normalizeAdsPowerTime(b.BoundAt)
	b.LastVerifiedAt = normalizeAdsPowerTime(b.LastVerifiedAt)
	b.LastLaunchedAt = normalizeAdsPowerTime(b.LastLaunchedAt)
}

func normalizeAdsPowerTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

// UpdateOpenAIAdsPowerBinding is intentionally outside AdminService so
// existing focused test doubles do not gain an unrelated method. Handlers use
// a narrow optional interface and fail closed when the production service is
// not wired.
func (s *adminServiceImpl) UpdateOpenAIAdsPowerBinding(ctx context.Context, id int64, binding *OpenAIAdsPowerBinding) error {
	if s == nil || s.accountRepo == nil {
		return errors.New("account repository is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	return s.persistOpenAIAdsPowerBinding(ctx, account, binding)
}

// UpdateOpenAIAdsPowerBindingFromLaunch applies a one-time helper report only
// while the account still has the profile identity captured by its launch ticket.
func (s *adminServiceImpl) UpdateOpenAIAdsPowerBindingFromLaunch(ctx context.Context, id int64, expected *OpenAIAdsPowerBinding, binding *OpenAIAdsPowerBinding) error {
	if s == nil || s.accountRepo == nil {
		return errors.New("account repository is unavailable")
	}
	if binding == nil {
		return errors.New("AdsPower launch must report a verified profile binding")
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if account == nil || !sameOpenAIAdsPowerBindingIdentity(OpenAIAdsPowerBindingFromAccount(account), expected) {
		return errors.New("AdsPower profile binding changed after the launch ticket was created")
	}
	if expected != nil && !sameOpenAIAdsPowerBindingIdentity(binding, expected) {
		return errors.New("AdsPower launch cannot change the account profile identity")
	}
	return s.persistOpenAIAdsPowerBinding(ctx, account, binding)
}

func sameOpenAIAdsPowerBindingIdentity(left, right *OpenAIAdsPowerBinding) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	l, r := *left, *right
	l.Normalize()
	r.Normalize()
	return l.DeviceID == r.DeviceID && l.EnvironmentKey == r.EnvironmentKey && l.ProfileID == r.ProfileID
}

func (s *adminServiceImpl) persistOpenAIAdsPowerBinding(ctx context.Context, account *Account, binding *OpenAIAdsPowerBinding) error {
	if account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
		return errors.New("only primary OpenAI OAuth accounts can bind an AdsPower profile")
	}
	if binding == nil {
		return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{OpenAIAdsPowerBindingExtraKey: nil})
	}
	binding.Normalize()
	if binding.ProfileID == "" || binding.DeviceID == "" || binding.EnvironmentKey == "" ||
		!binding.WebRTCDisabled || !binding.FingerprintRandomized {
		return errors.New("AdsPower binding is incomplete or unsafe")
	}
	return s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{OpenAIAdsPowerBindingExtraKey: binding})
}
