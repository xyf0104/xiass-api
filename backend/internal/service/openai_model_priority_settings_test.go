package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIModelPriorityRepoStub struct {
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
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (r *openAIModelPriorityRepoStub) Set(_ context.Context, key, value string) error {
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
		Enabled: true,
		Rules: []OpenAIModelPriorityRule{
			{ModelPattern: "gpt-5.6-*", AccountIDs: []int64{9, 3, 9}},
			{ModelPattern: "gpt-5.6-luna", AccountIDs: []int64{8}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 9}, settings.Rules[0].AccountIDs)

	compiled := compileOpenAIModelPrioritySettings(*settings)
	require.Contains(t, compiled.accountIDsForModel("gpt-5.6-luna"), int64(8), "exact rules must win")
	require.NotContains(t, compiled.accountIDsForModel("gpt-5.6-luna"), int64(9))
	require.Contains(t, compiled.accountIDsForModel("gpt-5.6-sol"), int64(3))
	require.Nil(t, compiled.accountIDsForModel("gpt-6-astra"))
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
		Enabled: true,
		Rules:   []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna", AccountIDs: []int64{42}}},
	})
	require.NoError(t, err)
	require.Contains(t, service.ResolveOpenAIModelPriorityAccountIDs(context.Background(), "gpt-5.6-luna"), int64(42))

	var persisted OpenAIModelPrioritySettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyOpenAIModelPrioritySettings]), &persisted))
	require.True(t, persisted.Enabled)
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
