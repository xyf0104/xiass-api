package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrAPIKeyFollowupNotFound also covers mismatched and ambiguous ownership.
// Callers must not disclose which other identity or group owns a request.
var ErrAPIKeyFollowupNotFound = errors.New("API key follow-up request not found")

// ResolveGrokVideoRequestGroup only reads exact owner-scoped bindings in the
// authenticated key's bound groups. It never scans Redis or guesses ownership
// from an account's current group membership.
func (s *OpenAIGatewayService) ResolveGrokVideoRequestGroup(
	ctx context.Context, requestID string, userID, apiKeyID int64, boundGroupIDs []int64,
) (int64, error) {
	if strings.TrimSpace(requestID) == "" || userID <= 0 || apiKeyID <= 0 || len(boundGroupIDs) == 0 || len(boundGroupIDs) > MaxAPIKeyGroups {
		return 0, ErrAPIKeyFollowupNotFound
	}
	for _, groupID := range boundGroupIDs {
		if groupID <= 0 {
			return 0, ErrAPIKeyFollowupNotFound
		}
	}
	seen := make(map[int64]struct{}, len(boundGroupIDs))
	var ownerGroupID int64
	for _, groupID := range boundGroupIDs {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if _, ok := seen[groupID]; ok {
			continue
		}
		seen[groupID] = struct{}{}
		accountID, err := s.ResolveGrokMediaVideoRequestAccount(ctx, &groupID, requestID, userID, apiKeyID)
		if errors.Is(err, ErrStickySessionNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if accountID <= 0 {
			continue
		}
		// Upstream request IDs are not guaranteed unique across groups. Do not
		// select a billing group by binding order when more than one owns it.
		if ownerGroupID != 0 {
			return 0, ErrAPIKeyFollowupNotFound
		}
		ownerGroupID = groupID
	}
	if ownerGroupID == 0 {
		return 0, ErrAPIKeyFollowupNotFound
	}
	return ownerGroupID, nil
}

// ResolveLiveCallGroup returns only the original group after verifying both
// owner IDs. The existing sideband handler still rechecks the full identity.
func (s *OpenAIGatewayService) ResolveLiveCallGroup(
	ctx context.Context, callID string, userID, apiKeyID int64,
) (int64, error) {
	if strings.TrimSpace(callID) == "" || userID <= 0 || apiKeyID <= 0 {
		return 0, ErrAPIKeyFollowupNotFound
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	store, err := s.liveStore()
	if err != nil {
		return 0, err
	}
	record, err := store.GetLiveCall(ctx, hashLiveCallID(callID))
	if errors.Is(err, ErrLiveCallNotFound) || errors.Is(err, ErrLiveIdentityMismatch) {
		return 0, ErrAPIKeyFollowupNotFound
	}
	if err != nil {
		return 0, err
	}
	if record == nil || record.CallID != callID || record.UserID != userID || record.APIKeyID != apiKeyID || record.GroupID <= 0 || record.Controller == LiveControllerClosed || !record.ExpiresAt.After(time.Now()) {
		return 0, ErrAPIKeyFollowupNotFound
	}
	return record.GroupID, nil
}
