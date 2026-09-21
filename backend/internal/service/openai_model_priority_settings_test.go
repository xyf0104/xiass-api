package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIModelPriorityRepoStub struct {
	mu      sync.RWMutex
	values  map[string]string
	getGate <-chan struct{}
}

func (r *openAIModelPriorityRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	value, err := r.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &Setting{Key: key, Value: value}, nil
}
func (r *openAIModelPriorityRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if r.getGate != nil {
		select {
		case <-r.getGate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (r *openAIModelPriorityRepoStub) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}
func (r *openAIModelPriorityRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}
func (r *openAIModelPriorityRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}
func (r *openAIModelPriorityRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}
func (r *openAIModelPriorityRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestOpenAIModelPrioritySettingsNormalizeAndResolve(t *testing.T) {
	settings, err := normalizeOpenAIModelPrioritySettings(&OpenAIModelPrioritySettings{
		Enabled:                      true,
		SmartRotationEnabled:         true,
		SmartRotationCooldownMinutes: 45,
		Rules: []OpenAIModelPriorityRule{
			{ModelPattern: "gpt-5.6-*", AccountIDs: []int64{9, 3, 9}},
			{ModelPattern: "gpt-5.6-luna", AccountIDs: []int64{8}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 9}, settings.Rules[0].AccountIDs)

	compiled := compileOpenAIModelPrioritySettings(*settings)
	require.True(t, compiled.smartRotationEnabled)
	require.Equal(t, 45, compiled.smartRotationCooldownMinutes)
	require.Contains(t, compiled.accountIDsForModel("gpt-5.6-luna"), int64(8), "exact rules must win")
	require.NotContains(t, compiled.accountIDsForModel("gpt-5.6-luna"), int64(9))
	require.Contains(t, compiled.accountIDsForModel("gpt-5.6-sol"), int64(3))
	require.Nil(t, compiled.accountIDsForModel("gpt-6-astra"))
}

func TestOpenAIModelPrioritySettingsDefaultsAndBackwardCompatibility(t *testing.T) {
	defaults := DefaultOpenAIModelPrioritySettings()
	require.False(t, defaults.SmartRotationEnabled)
	require.Equal(t, 30, defaults.SmartRotationCooldownMinutes)

	for name, raw := range map[string]string{
		"missing fields": `{"enabled":true,"rules":[{"model_pattern":"gpt-5.6-luna","account_ids":[42]}]}`,
		"explicit zero":  `{"enabled":true,"rules":[{"model_pattern":"gpt-5.6-luna","account_ids":[42]}],"smart_rotation_cooldown_minutes":0}`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := &openAIModelPriorityRepoStub{values: map[string]string{SettingKeyOpenAIModelPrioritySettings: raw}}
			service := NewSettingService(repo, &config.Config{})

			loaded, err := service.GetOpenAIModelPrioritySettings(context.Background())
			require.NoError(t, err)
			require.False(t, loaded.SmartRotationEnabled)
			require.Equal(t, 30, loaded.SmartRotationCooldownMinutes)

			cached, ok := service.openAIModelPriorityCache.Load().(*cachedOpenAIModelPrioritySettings)
			require.True(t, ok)
			require.False(t, cached.compiled.smartRotationEnabled)
			require.Equal(t, 30, cached.compiled.smartRotationCooldownMinutes)
		})
	}
}

func TestOpenAIModelPrioritySettingsValidateSmartRotationCooldown(t *testing.T) {
	for _, cooldownMinutes := range []int{-1, 1441} {
		_, err := normalizeOpenAIModelPrioritySettings(&OpenAIModelPrioritySettings{
			SmartRotationCooldownMinutes: cooldownMinutes,
		})
		require.ErrorContains(t, err, "smart_rotation_cooldown_minutes must be between 1 and 1440")
	}

	for _, cooldownMinutes := range []int{1, 1440} {
		normalized, err := normalizeOpenAIModelPrioritySettings(&OpenAIModelPrioritySettings{
			SmartRotationCooldownMinutes: cooldownMinutes,
		})
		require.NoError(t, err)
		require.Equal(t, cooldownMinutes, normalized.SmartRotationCooldownMinutes)
	}
}

func TestCloneOpenAIModelPrioritySettingsPreservesSmartRotation(t *testing.T) {
	original := OpenAIModelPrioritySettings{
		Enabled:                      true,
		Rules:                        []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna", AccountIDs: []int64{42}}},
		SmartRotationEnabled:         true,
		SmartRotationCooldownMinutes: 90,
	}

	cloned := cloneOpenAIModelPrioritySettings(original)
	require.True(t, cloned.SmartRotationEnabled)
	require.Equal(t, 90, cloned.SmartRotationCooldownMinutes)
	cloned.SmartRotationCooldownMinutes = 15
	cloned.Rules[0].AccountIDs[0] = 99
	require.Equal(t, 90, original.SmartRotationCooldownMinutes)
	require.Equal(t, int64(42), original.Rules[0].AccountIDs[0])
}

func TestOpenAIModelPrioritySettingsRejectInvalidRules(t *testing.T) {
	_, err := normalizeOpenAIModelPrioritySettings(&OpenAIModelPrioritySettings{Enabled: true, Rules: []OpenAIModelPriorityRule{{ModelPattern: "gpt-*bad", AccountIDs: []int64{1}}}})
	require.ErrorContains(t, err, "invalid model_pattern")
	_, err = normalizeOpenAIModelPrioritySettings(&OpenAIModelPrioritySettings{Enabled: true, Rules: []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna"}}})
	require.ErrorContains(t, err, "account_ids cannot be empty")
}

func TestResolveOpenAIModelPriorityAccountIDsNeverWaitsForSettingsDB(t *testing.T) {
	gate := make(chan struct{})
	repo := &openAIModelPriorityRepoStub{values: map[string]string{}, getGate: gate}
	service := NewSettingService(repo, &config.Config{})

	started := time.Now()
	ids := service.ResolveOpenAIModelPriorityAccountIDs(context.Background(), "gpt-5.6-luna")
	require.Nil(t, ids)
	require.Less(t, time.Since(started), 50*time.Millisecond)
	close(gate)
}

func TestSetOpenAIModelPrioritySettingsPublishesCompiledSnapshot(t *testing.T) {
	repo := &openAIModelPriorityRepoStub{values: map[string]string{}}
	service := NewSettingService(repo, &config.Config{})
	err := service.SetOpenAIModelPrioritySettings(context.Background(), &OpenAIModelPrioritySettings{
		Enabled:                      true,
		Rules:                        []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna", AccountIDs: []int64{42}}},
		SmartRotationEnabled:         true,
		SmartRotationCooldownMinutes: 120,
	})
	require.NoError(t, err)
	require.Contains(t, service.ResolveOpenAIModelPriorityAccountIDs(context.Background(), "gpt-5.6-luna"), int64(42))

	var persisted OpenAIModelPrioritySettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyOpenAIModelPrioritySettings]), &persisted))
	require.True(t, persisted.Enabled)
	require.True(t, persisted.SmartRotationEnabled)
	require.Equal(t, 120, persisted.SmartRotationCooldownMinutes)

	cached, ok := service.openAIModelPriorityCache.Load().(*cachedOpenAIModelPrioritySettings)
	require.True(t, ok)
	require.True(t, cached.compiled.smartRotationEnabled)
	require.Equal(t, 120, cached.compiled.smartRotationCooldownMinutes)

	loaded, err := service.GetOpenAIModelPrioritySettings(context.Background())
	require.NoError(t, err)
	require.True(t, loaded.SmartRotationEnabled)
	require.Equal(t, 120, loaded.SmartRotationCooldownMinutes)
}

func BenchmarkResolveOpenAIModelPriorityAccountIDs(b *testing.B) {
	settings := OpenAIModelPrioritySettings{Enabled: true, Rules: []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna", AccountIDs: []int64{1, 2, 3, 4, 5}}}}
	service := NewSettingService(&openAIModelPriorityRepoStub{values: map[string]string{}}, &config.Config{})
	service.openAIModelPriorityCache.Store(&cachedOpenAIModelPrioritySettings{
		settings: settings, compiled: compileOpenAIModelPrioritySettings(settings),
		expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = service.ResolveOpenAIModelPriorityAccountIDs(context.Background(), "gpt-5.6-luna")
	}
}

func TestOpenAIModelPriorityOrderRoundTripAndValidation(t *testing.T) {
	repo := &openAIModelPriorityRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	settings := &OpenAIModelPrioritySettings{Enabled: true, Rules: []OpenAIModelPriorityRule{
		{ModelPattern: "gpt-*", AccountIDs: []int64{9, 3, 7}, AccountOrder: []int64{9, 7, 3}},
		{ModelPattern: "gpt-6-astra", AccountIDs: []int64{9, 3, 7}, AccountOrder: []int64{7, 3, 9}},
	}}
	require.NoError(t, svc.SetOpenAIModelPrioritySettings(context.Background(), settings))
	// Reload from persistence, not the publishing instance's cache.
	fresh := NewSettingService(repo, &config.Config{})
	loaded, err := fresh.GetOpenAIModelPrioritySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{7, 3, 9}, loaded.Rules[1].AccountOrder)
	require.Equal(t, 0, fresh.resolveOpenAIModelPriorityPreference(context.Background(), "gpt-6-astra").tier(&Account{ID: 7}))
	require.Equal(t, 0, fresh.resolveOpenAIModelPriorityPreference(context.Background(), "gpt-5.6-sol").tier(&Account{ID: 9}))
	loaded.Rules[1].AccountOrder[0] = 999
	again, err := fresh.GetOpenAIModelPrioritySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{7, 3, 9}, again.Rules[1].AccountOrder, "callers cannot mutate the cached order")
	for _, invalid := range [][]int64{{9}, {9, 9, 3}, {9, 7, 99}, {9, 7, 3, 9}} {
		settings.Rules[0].AccountOrder = invalid
		require.Error(t, svc.SetOpenAIModelPrioritySettings(context.Background(), settings))
	}
}
