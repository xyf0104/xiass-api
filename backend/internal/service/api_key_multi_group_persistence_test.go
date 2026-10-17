//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type multiGroupAPIKeyRepoStub struct {
	APIKeyRepository
	key     *APIKey
	created *APIKey
	updated *APIKey
}

func (r *multiGroupAPIKeyRepoStub) GetByID(_ context.Context, _ int64) (*APIKey, error) {
	clone := *r.key
	clone.GroupIDs = append([]int64(nil), r.key.GroupIDs...)
	return &clone, nil
}

func (r *multiGroupAPIKeyRepoStub) Create(_ context.Context, key *APIKey) error {
	clone := *key
	clone.GroupIDs = append([]int64(nil), key.GroupIDs...)
	r.created = &clone
	key.ID = 1
	return nil
}

func (r *multiGroupAPIKeyRepoStub) Update(_ context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	clone := *key
	clone.GroupIDs = append([]int64(nil), key.GroupIDs...)
	r.updated = &clone
	r.key = &clone
	return nil
}

func (r *multiGroupAPIKeyRepoStub) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	return nil, nil
}

type multiGroupUserRepoStub struct {
	UserRepository
	user *User
}

func (r *multiGroupUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	return r.user, nil
}

type multiGroupRepoStub struct {
	GroupRepository
	groups map[int64]*Group
	calls  []int64
}

func (r *multiGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	r.calls = append(r.calls, id)
	return r.groups[id], nil
}

func TestAPIKeyMultiGroup_NormalizesBeforeApplyingLimit(t *testing.T) {
	ids := make([]int64, 0, MaxAPIKeyGroups+8)
	for i := 0; i < MaxAPIKeyGroups+8; i++ {
		ids = append(ids, 7)
	}

	normalized, err := normalizeRequestedAPIKeyGroups(ids)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, normalized)
}

func TestAPIKeyService_CreateAndUpdateValidateEverySelectedGroup(t *testing.T) {
	user := &User{ID: 7, Status: StatusActive, RestrictPublicGroups: true, AllowedGroups: []int64{1, 2, 3}}
	groups := &multiGroupRepoStub{groups: map[int64]*Group{
		1: {ID: 1, Name: "one", Status: StatusActive, IsExclusive: true},
		2: {ID: 2, Name: "two", Status: StatusActive, IsExclusive: true},
		3: {ID: 3, Name: "three", Status: StatusActive, IsExclusive: true},
	}}
	repo := &multiGroupAPIKeyRepoStub{}
	svc := NewAPIKeyService(repo, &multiGroupUserRepoStub{user: user}, groups, nil, nil, nil, &config.Config{})

	created, err := svc.Create(context.Background(), user.ID, CreateAPIKeyRequest{
		Name:     "multi",
		GroupIDs: []int64{1, 2},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, created.GroupIDs)
	require.Equal(t, []int64{1, 2}, repo.created.GroupIDs)
	require.Equal(t, []int64{1, 2}, groups.calls)

	groups.calls = nil
	primaryGroupID := int64(1)
	repo.key = &APIKey{
		ID:      1,
		UserID:  user.ID,
		Key:     "sk-update",
		Name:    "multi",
		Status:  StatusActive,
		GroupID: &primaryGroupID,
	}
	updated, err := svc.Update(context.Background(), 1, user.ID, UpdateAPIKeyRequest{
		GroupIDs: &[]int64{2, 3},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, updated.GroupIDs)
	require.Equal(t, []int64{2, 3}, repo.updated.GroupIDs)
	require.Equal(t, []int64{2, 3}, groups.calls)
}

func TestAPIKeyAuthSnapshot_PreservesNilAndEmptyGroupBindings(t *testing.T) {
	svc := &APIKeyService{}
	user := &User{ID: 7, Status: StatusActive}

	for name, sourceIDs := range map[string][]int64{
		"legacy nil":     nil,
		"explicit empty": make([]int64, 0),
	} {
		t.Run(name, func(t *testing.T) {
			source := &APIKey{ID: 1, UserID: user.ID, Key: "sk-cache", Status: StatusActive, User: user, GroupIDs: sourceIDs, Groups: make([]*Group, 0)}
			if sourceIDs != nil {
				// The persisted empty binding must win even if a stale legacy
				// relation was attached to the same in-memory object.
				stale := &Group{ID: 99, Name: "stale", Status: StatusActive}
				source.Group = stale
				source.Groups = []*Group{stale}
			}
			snapshot := svc.snapshotFromAPIKey(context.Background(), source)
			encoded, err := json.Marshal(snapshot)
			require.NoError(t, err)
			if sourceIDs != nil {
				require.Contains(t, string(encoded), `"group_ids":[]`)
				require.Contains(t, string(encoded), `"groups":[`)
			}

			var decoded APIKeyAuthSnapshot
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			roundTrip := svc.snapshotToAPIKey(source.Key, &decoded)
			require.NotNil(t, roundTrip)
			if sourceIDs == nil {
				require.Nil(t, roundTrip.GroupIDs)
			} else {
				require.NotNil(t, roundTrip.GroupIDs)
				require.Empty(t, roundTrip.GroupIDs)
				require.NotNil(t, roundTrip.Groups)
			}
		})
	}
}

func TestAPIKeyAuthSnapshot_DoesNotRestoreDeletedPrimaryOverSurvivingGroup(t *testing.T) {
	svc := &APIKeyService{}
	deletedPrimary := int64(10)
	survivingSecondary := &Group{ID: 20, Name: "surviving", Status: StatusActive}
	source := &APIKey{
		ID:       1,
		UserID:   7,
		Key:      "sk-cache-survivor",
		Status:   StatusActive,
		GroupID:  &survivingSecondary.ID,
		GroupIDs: []int64{deletedPrimary, survivingSecondary.ID},
		Group:    survivingSecondary,
		Groups:   []*Group{survivingSecondary},
		User:     &User{ID: 7, Status: StatusActive},
	}

	roundTrip := svc.snapshotToAPIKey(source.Key, svc.snapshotFromAPIKey(context.Background(), source))
	require.NotNil(t, roundTrip.GroupID)
	require.Equal(t, survivingSecondary.ID, *roundTrip.GroupID)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, survivingSecondary.ID, roundTrip.Group.ID)
	require.Equal(t, []int64{deletedPrimary, survivingSecondary.ID}, roundTrip.GroupIDs)
}
