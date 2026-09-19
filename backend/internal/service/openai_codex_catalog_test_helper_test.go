package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type countingCodexModelsAccountRepo struct {
	AccountRepository
	accounts        []Account
	err             error
	availabilityErr error
	groupID         *int64
	platforms       []string
	includeGrouped  bool
	calls           atomic.Int32
}

func (r *countingCodexModelsAccountRepo) ListSchedulableByGroupID(_ context.Context, _ int64) ([]Account, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	return append([]Account(nil), r.accounts...), nil
}

func (r *countingCodexModelsAccountRepo) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	if groupID != nil {
		value := *groupID
		r.groupID = &value
	}
	r.platforms = append([]string(nil), platforms...)
	r.includeGrouped = includeGrouped
	if r.availabilityErr != nil {
		return nil, r.availabilityErr
	}
	return append([]Account(nil), r.accounts...), nil
}

type codexModelsVisibilityAccountRepo struct {
	AccountRepository
	byGroup map[int64][]Account
}

func (r codexModelsVisibilityAccountRepo) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	return append([]Account(nil), r.byGroup[groupID]...), nil
}

func (r codexModelsVisibilityAccountRepo) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, _ []string, _ bool) ([]Account, error) {
	if groupID == nil {
		return nil, nil
	}
	return append([]Account(nil), r.byGroup[*groupID]...), nil
}

type upstreamModelMetadataRepoStub struct {
	AccountRepository
	accountID int64
	updates   map[string]any
	err       error
}

type splitCodexModelsAccountRepo struct {
	AccountRepository
	schedulable map[int64][]Account
	catalog     map[int64][]Account
	all         map[int64][]Account
}

func (r splitCodexModelsAccountRepo) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	return append([]Account(nil), r.schedulable[groupID]...), nil
}

func (r splitCodexModelsAccountRepo) ListByGroup(_ context.Context, groupID int64) ([]Account, error) {
	accounts := r.all[groupID]
	if accounts == nil {
		accounts = r.catalog[groupID]
	}
	return append([]Account(nil), accounts...), nil
}

func (r splitCodexModelsAccountRepo) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, _ []string, _ bool) ([]Account, error) {
	if groupID == nil {
		return nil, nil
	}
	return append([]Account(nil), r.catalog[*groupID]...), nil
}

func newCodexCatalogMappedAccount(
	id int64,
	target string,
	displayName string,
	levels []string,
	modalities []string,
	contextWindow int64,
	schedulable bool,
	extraMapping map[string]any,
) Account {
	reasoning := true
	mapping := map[string]any{"my-coder": target}
	for key, value := range extraMapping {
		mapping[key] = value
	}
	account := Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: schedulable,
		Credentials: map[string]any{
			"base_url":      fmt.Sprintf("https://provider-%d.example/v1", id),
			"model_mapping": mapping,
		},
	}
	models := map[string]UpstreamModelMetadata{
		target: {
			ID:                       target,
			DisplayName:              displayName,
			Description:              displayName + " upstream",
			Reasoning:                &reasoning,
			SupportedReasoningLevels: levels,
			InputModalities:          modalities,
			ContextWindow:            contextWindow,
		},
	}
	for _, value := range extraMapping {
		exclusive, _ := value.(string)
		if exclusive == "" || exclusive == target {
			continue
		}
		models[exclusive] = UpstreamModelMetadata{
			ID:                       exclusive,
			DisplayName:              "Exclusive Model",
			Description:              "Only mapped on the unschedulable account",
			Reasoning:                &reasoning,
			SupportedReasoningLevels: []string{"high"},
			InputModalities:          []string{"text", "image"},
			ContextWindow:            1_000_000,
		}
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: models})
	return account
}

func (r *upstreamModelMetadataRepoStub) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.accountID = id
	r.updates = updates
	return r.err
}

func decodeCodexManifestModels(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var envelope struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Models
}

func effortsFromManifestModel(t *testing.T, model map[string]any) []string {
	t.Helper()
	levels, ok := model["supported_reasoning_levels"].([]any)
	require.True(t, ok)
	efforts := make([]string, 0, len(levels))
	for _, rawLevel := range levels {
		level, ok := rawLevel.(map[string]any)
		require.True(t, ok)
		effort, ok := level["effort"].(string)
		require.True(t, ok)
		efforts = append(efforts, effort)
	}
	return efforts
}
