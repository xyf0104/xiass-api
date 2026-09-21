package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	openAIModelRotationPrefix        = "gateway:openai_model_rotation:"
	openAIModelRotationMinStorageTTL = time.Minute
	openAIModelRotationMaxFailureTTL = 24 * time.Hour
	openAIModelRotationStorageMargin = time.Minute
	openAIModelRotationMaxStorageTTL = openAIModelRotationMaxFailureTTL + openAIModelRotationStorageMargin
)

var (
	errOpenAIModelRotationUnavailable = errors.New("openai model rotation cache unavailable")
	errOpenAIModelRotationScope       = errors.New("invalid openai model rotation scope")
	errOpenAIModelRotationAccountID   = errors.New("invalid openai model rotation account id")
	errOpenAIModelRotationStartedAt   = errors.New("invalid openai model rotation request start time")
	errOpenAIModelRotationTTL         = errors.New("invalid openai model rotation ttl")
)

var getOpenAIModelRotationFailuresScript = redis.NewScript(`
local blocked_key = KEYS[1]
local versions_key = KEYS[2]
local version_expiry_key = KEYS[3]
local now_ms = tonumber(ARGV[1])

local expired_versions = redis.call('ZRANGEBYSCORE', version_expiry_key, '-inf', now_ms)
for _, account_id in ipairs(expired_versions) do
  redis.call('HDEL', versions_key, account_id)
end
redis.call('ZREMRANGEBYSCORE', version_expiry_key, '-inf', now_ms)
redis.call('ZREMRANGEBYSCORE', blocked_key, '-inf', now_ms)

return redis.call('ZRANGEBYSCORE', blocked_key, now_ms, '+inf')
`)

var observeOpenAIModelRotationScript = redis.NewScript(`
local blocked_key = KEYS[1]
local versions_key = KEYS[2]
local version_expiry_key = KEYS[3]
local account_id = ARGV[1]
local started_at_ms = tonumber(ARGV[2])
local blocked_until_ms = tonumber(ARGV[3])
local now_ms = tonumber(ARGV[4])
local storage_ttl_ms = tonumber(ARGV[5])
local storage_margin_ms = tonumber(ARGV[6])
local max_storage_ttl_ms = tonumber(ARGV[7])

local expired_versions = redis.call('ZRANGEBYSCORE', version_expiry_key, '-inf', now_ms)
for _, expired_account_id in ipairs(expired_versions) do
  redis.call('HDEL', versions_key, expired_account_id)
end
redis.call('ZREMRANGEBYSCORE', version_expiry_key, '-inf', now_ms)
redis.call('ZREMRANGEBYSCORE', blocked_key, '-inf', now_ms)

local current_started_at_ms = tonumber(redis.call('HGET', versions_key, account_id))
if current_started_at_ms and current_started_at_ms > started_at_ms then
  return 0
end

if blocked_until_ms > now_ms then
  redis.call('ZADD', blocked_key, blocked_until_ms, account_id)
else
  redis.call('ZREM', blocked_key, account_id)
end

local required_ttl_ms = storage_ttl_ms
if blocked_until_ms > now_ms then
  local blocked_ttl_ms = blocked_until_ms - now_ms + storage_margin_ms
  if blocked_ttl_ms > required_ttl_ms then
    required_ttl_ms = blocked_ttl_ms
  end
end
if required_ttl_ms > max_storage_ttl_ms then
  required_ttl_ms = max_storage_ttl_ms
end

redis.call('HSET', versions_key, account_id, started_at_ms)
redis.call('ZADD', version_expiry_key, now_ms + required_ttl_ms, account_id)

local function extend_ttl(key)
  local current_ttl_ms = redis.call('PTTL', key)
  if current_ttl_ms < required_ttl_ms then
    redis.call('PEXPIRE', key, required_ttl_ms)
  end
end

extend_ttl(blocked_key)
extend_ttl(versions_key)
extend_ttl(version_expiry_key)

return 1
`)

func openAIModelRotationKeys(scope string) (string, string, string, error) {
	if len(scope) != hex.EncodedLen(sha256.Size) {
		return "", "", "", errOpenAIModelRotationScope
	}
	if _, err := hex.DecodeString(scope); err != nil {
		return "", "", "", errOpenAIModelRotationScope
	}

	// The braces keep all per-scope structures in one Redis Cluster hash slot.
	base := openAIModelRotationPrefix + "{" + strings.ToLower(scope) + "}"
	return base + ":blocked", base + ":versions", base + ":version_expiry", nil
}

func openAIModelRotationStorageTTL(ttl time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		return 0, errOpenAIModelRotationTTL
	}
	if ttl >= openAIModelRotationMaxFailureTTL {
		return openAIModelRotationMaxStorageTTL, nil
	}

	storageTTL := ttl + openAIModelRotationStorageMargin
	if storageTTL < openAIModelRotationMinStorageTTL {
		return openAIModelRotationMinStorageTTL, nil
	}
	return storageTTL, nil
}

func (c *gatewayCache) GetOpenAIModelRotationFailures(ctx context.Context, scope string, now time.Time) ([]int64, error) {
	if c == nil || c.rdb == nil {
		return nil, errOpenAIModelRotationUnavailable
	}
	blockedKey, versionsKey, versionExpiryKey, err := openAIModelRotationKeys(scope)
	if err != nil {
		return nil, err
	}
	if now.IsZero() || now.UnixMilli() <= 0 {
		return nil, fmt.Errorf("%w: now", errOpenAIModelRotationStartedAt)
	}

	members, err := getOpenAIModelRotationFailuresScript.Run(
		ctx,
		c.rdb,
		[]string{blockedKey, versionsKey, versionExpiryKey},
		now.UnixMilli(),
	).StringSlice()
	if err != nil {
		return nil, err
	}

	accountIDs := make([]int64, 0, len(members))
	for _, member := range members {
		accountID, parseErr := strconv.ParseInt(member, 10, 64)
		if parseErr == nil && accountID > 0 {
			accountIDs = append(accountIDs, accountID)
		}
	}
	return accountIDs, nil
}

func (c *gatewayCache) ObserveOpenAIModelRotation(
	ctx context.Context,
	scope string,
	accountID int64,
	startedAt time.Time,
	blockedUntil time.Time,
	ttl time.Duration,
) error {
	if c == nil || c.rdb == nil {
		return errOpenAIModelRotationUnavailable
	}
	if accountID <= 0 {
		return errOpenAIModelRotationAccountID
	}
	startedAtMillis := startedAt.UnixMilli()
	if startedAt.IsZero() || startedAtMillis <= 0 {
		return errOpenAIModelRotationStartedAt
	}
	storageTTL, err := openAIModelRotationStorageTTL(ttl)
	if err != nil {
		return err
	}
	blockedKey, versionsKey, versionExpiryKey, err := openAIModelRotationKeys(scope)
	if err != nil {
		return err
	}

	blockedUntilMillis := int64(0)
	if !blockedUntil.IsZero() {
		blockedUntilMillis = blockedUntil.UnixMilli()
		if blockedUntilMillis <= 0 {
			return fmt.Errorf("%w: blocked until", errOpenAIModelRotationStartedAt)
		}
	}

	nowMillis := time.Now().UnixMilli()
	return observeOpenAIModelRotationScript.Run(
		ctx,
		c.rdb,
		[]string{blockedKey, versionsKey, versionExpiryKey},
		accountID,
		startedAtMillis,
		blockedUntilMillis,
		nowMillis,
		storageTTL.Milliseconds(),
		openAIModelRotationStorageMargin.Milliseconds(),
		openAIModelRotationMaxStorageTTL.Milliseconds(),
	).Err()
}

var _ service.OpenAIModelRotationStore = (*gatewayCache)(nil)
