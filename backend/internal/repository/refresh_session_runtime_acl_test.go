package repository

import (
	"slices"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRefreshSessionRuntimeACLWithoutRetiredNodeGrants(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {Redis: config.RedisConfig{DB: 2}}} {
		rules, err := RefreshSessionRuntimeACL(cfg, "")
		require.NoError(t, err)
		for _, rule := range rules {
			require.NotContains(t, rule, "execution_node")
			require.NotContains(t, rule, "cluster_identity")
		}
		for _, rule := range []string{
			"+role", "+scan", "+eval", "+subscribe",
			"~billing:balance:*", "~oauth:token:*", "~concurrency:account:*", "~leader:lock:*",
			"~batch_image:queue:ready", "~sub2api:dashboard:stats:v1",
			"~xiass:team-child:mailbox:*", "~xiass:team-child:browser:session:*",
			"&auth:cache:invalidate", "&subscription:cache:invalidate",
		} {
			require.Contains(t, rules, rule)
		}
		require.Equal(t, cfg != nil && cfg.Redis.DB > 0, slices.Contains(rules, "+select"))
		for _, rule := range []string{"~*", "+@all", "+acl", "+config", "+flushdb", "~refresh_token:*", "~user_refresh_tokens:*", "~token_family:*"} {
			require.NotContains(t, rules, rule)
		}
	}
}

func TestRefreshSessionRuntimeACLRetainsLiteralCustomKeys(t *testing.T) {
	cfg := &config.Config{}
	cfg.Dashboard.KeyPrefix = "cache[1]*"
	cfg.BatchImage.QueueReadyKey = "jobs:ready"
	cfg.BatchImage.LockKeyPrefix = "jobs:lock?"
	rules, err := RefreshSessionRuntimeACL(cfg, "ops:custom-lock")
	require.NoError(t, err)
	for _, rule := range []string{`~cache\[1\]\*:dashboard:stats:v1`, "~jobs:ready", `~jobs:lock\?*`, "~ops:custom-lock"} {
		require.Contains(t, rules, rule)
	}
	for _, protected := range []string{"refresh_token:", "user_refresh_tokens:", "token_family:"} {
		cfg.BatchImage.LockKeyPrefix = protected
		_, err := RefreshSessionRuntimeACL(cfg, "")
		require.ErrorContains(t, err, "protected refresh-session namespace")
	}
	cfg.BatchImage.LockKeyPrefix = "jobs:unsafe key"
	_, err = RefreshSessionRuntimeACL(cfg, "")
	require.ErrorContains(t, err, "ASCII whitespace")
}
