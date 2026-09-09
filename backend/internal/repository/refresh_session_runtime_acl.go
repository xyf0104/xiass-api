package repository

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// RefreshSessionRuntimeACL returns grants only. The caller must apply them to a
// reset, fresh principal after adoption; this does not revoke existing grants.
func RefreshSessionRuntimeACL(cfg *config.Config, alertLockKey string) ([]string, error) {
	// ROLE is read-only readiness introspection, not replication control. SCAN
	// preserves Team mailbox recovery: it exposes all key names, not their values.
	// Key ACLs still deny GET/SET and Lua access to the legacy auth namespaces.
	rules := strings.Fields(`
		+auth +hello +ping +role +time +scan
		+get +getdel +mget +set +setnx +del +exists +incr +decr +expire +pexpire +ttl +pttl
		+hget +hgetall +hset +hincrby +hincrbyfloat
		+sadd +srem +smembers +sismember +scard +spop
		+zadd +zrem +zcard +zscore +zrange +zrangebyscore +zrevrange +zremrangebyscore
		+lpush +rpop +multi +exec +watch +unwatch +eval +evalsha +script|load
		+publish +subscribe
		~rate_limit:* ~verify_code:* ~notify_verify:* ~password_reset:* ~password_reset_sent:* ~notify_code_user_rate:*
		~totp:setup:* ~totp:login:* ~totp:attempts:* ~totp:stepup:* ~passkey:session:* ~oauth:public:openai:*
		~apikey:auth:* ~apikey:ratelimit:* ~apikey:rate:* ~apikey:usage:*
		~billing:balance:* ~billing:sub:* ~billing:user_platform_quota:* ~billing:upq:dirty
		~redeem:ratelimit:* ~redeem:lock:* ~oauth:token:* ~oauth:refresh_lock:*
		~sticky_session:* ~live:call:* ~grok_video_pending:* ~grok_video_billed:* ~reasoning_content:* ~cyber_session_block:*
		~concurrency:account:* ~concurrency:user:* ~concurrency:api_key:* ~concurrency:group:* ~concurrency:user-group-account:*
		~concurrency:live:account:* ~concurrency:live:user:* ~concurrency:live:api_key:* ~concurrency:openai_ws_ingress:api_key:*
		~concurrency:wait:* ~wait:account:* ~rpm:* ~session_limit:account:* ~window_cost:account:*
		~umq:* ~leader:lock:* ~sched:buckets ~sched:outbox:watermark ~sched:acc:* ~sched:meta:*
		~sched:active:* ~sched:ready:* ~sched:ver:* ~sched:epoch:* ~sched:retired:* ~sched:lock:*
		~sched:group:lifecycle-lock:* ~sched:*:*:*:v*
		~fingerprint:* ~masked_session:* ~proxy:latency:* ~temp_unsched:account:*
		~timeout_count:account:* ~internal500_count:account:* ~openai_403_count:account:*
		~image_task:* ~batch_image:download:active:*
		~content_moderation:flagged_hashes ~content_moderation:notification_dedupe:*
		~websearch:quota:* ~websearch:proxy_unavailable:* ~sub2api:prompt_audit:payload:*
		~update:latest ~error_passthrough_rules ~tls_fingerprint_profiles
		~ops:aggregation:hourly:leader ~ops:aggregation:daily:leader ~ops:cleanup:leader
		~ops:metrics:collector:leader ~ops:scheduled_reports:leader ~ops:scheduled_reports:last_run:*
		~xiass:execution_node:heartbeat:* ~xiass:execution_node:cluster_identity
		~xiass:team-child:browser:session:* ~xiass:team-child:browser:ticket:* ~xiass:team-child:browser:controller
		~xiass:team-child:mailbox:*
		&auth:cache:invalidate &subscription:cache:invalidate &error_passthrough_rules_updated
		&tls_fingerprint_profiles_updated &sub2api:prompt_guard:config:invalidate
	`)

	dashboardPrefix := "sub2api:"
	var batch config.BatchImageConfig
	if cfg != nil {
		dashboardPrefix = strings.TrimSpace(cfg.Dashboard.KeyPrefix)
		batch = cfg.BatchImage
		if cfg.Redis.DB > 0 {
			rules = append(rules, "+select")
		}
	}
	if dashboardPrefix != "" && !strings.HasSuffix(dashboardPrefix, ":") {
		dashboardPrefix += ":"
	}

	// These defaults and normalization mirror the repository caches and ops
	// evaluator. Only the final '*' below is syntax; configured parts are literal.
	escape := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`)
	for _, key := range []struct {
		field, value, fallback string
		prefix                 bool
	}{
		{"dashboard_cache.key_prefix", dashboardPrefix + "dashboard:stats:v1", "sub2api:dashboard:stats:v1", false},
		{"batch_image.queue_ready_key", batch.QueueReadyKey, "batch_image:queue:ready", false},
		{"batch_image.queue_delayed_key", batch.QueueDelayedKey, "batch_image:queue:delayed", false},
		{"batch_image.queue_active_key", batch.QueueActiveKey, "batch_image:queue:active", false},
		{"batch_image.inflight_key_prefix", batch.InflightKeyPrefix, "batch_image:queue:inflight:", true},
		{"batch_image.lock_key_prefix", batch.LockKeyPrefix, "batch_image:queue:lock:", true},
		{"ops alert lock key", strings.TrimSpace(alertLockKey), "ops:alert:evaluator:leader", false},
	} {
		for _, literal := range []string{key.fallback, key.value} {
			if literal == "" {
				continue
			}
			// Redis ACLStringHasSpaces rejects these bytes even in escaped patterns.
			if strings.ContainsAny(literal, "\x00 \t\n\v\f\r") {
				return nil, fmt.Errorf("%s contains NUL or ASCII whitespace unsupported by Redis ACL patterns", key.field)
			}
			for _, protected := range []string{"refresh_token:", "user_refresh_tokens:", "token_family:"} {
				if strings.HasPrefix(literal, protected) || (key.prefix && strings.HasPrefix(protected, literal)) {
					return nil, fmt.Errorf("%s overlaps a protected refresh-session namespace", key.field)
				}
			}
			rule := "~" + escape.Replace(literal)
			if key.prefix {
				rule += "*"
			}
			rules = append(rules, rule)
		}
	}

	seen := make(map[string]bool, len(rules))
	unique := rules[:0]
	for _, rule := range rules {
		if !seen[rule] {
			seen[rule] = true
			unique = append(unique, rule)
		}
	}
	return unique, nil
}
