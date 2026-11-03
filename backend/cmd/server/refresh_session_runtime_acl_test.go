package main

import (
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestRefreshSessionRuntimeACLDefaults(t *testing.T) {
	rules, err := refreshSessionRuntimeACL(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range strings.Fields(`
		~sub2api:dashboard:stats:v1 ~batch_image:queue:ready ~batch_image:queue:delayed
		~batch_image:queue:active ~batch_image:queue:inflight:* ~batch_image:queue:lock:*
		~ops:alert:evaluator:leader ~rate_limit:* ~apikey:usage:* ~billing:upq:dirty
		~xiass:team-child:mailbox:*
		&auth:cache:invalidate &subscription:cache:invalidate &error_passthrough_rules_updated
		&tls_fingerprint_profiles_updated &sub2api:prompt_guard:config:invalidate
	`) {
		if !slices.Contains(rules, rule) {
			t.Errorf("missing default grant %q", rule)
		}
	}
	for _, key := range strings.Fields(`
		rate_limit:refresh-token:127.0.0.1 rate_limit:panel:accounts:user:1
		concurrency:account:active_index concurrency:user:active_index concurrency:user-group-account:group:1:users
		concurrency:live:api_key:1 concurrency:openai_ws_ingress:api_key:1
		umq:{1}:lock umq:{1}:last umq:lock:index rpm:1:123 rpm:u:1:123 rpm:ug:1:2:123
		sched:1:openai:shared:v2 sched:acc:last_used:1 sched:group:lifecycle-lock:1
		xiass:team-child:mailbox:active:1 xiass:team-child:mailbox:known:1 xiass:team-child:mailbox:address-sequence
	`) {
		if !refreshRuntimeACLAllowsKey(t, rules, key) {
			t.Errorf("missing runtime key %q", key)
		}
	}
	seen := map[string]bool{}
	for _, rule := range rules {
		if seen[rule] {
			t.Errorf("duplicate grant %q", rule)
		}
		seen[rule] = true
	}
	if slices.Contains(rules, "+select") {
		t.Error("default DB does not need SELECT")
	}
	refreshRuntimeACLAssertProtected(t, rules)
}

func TestRefreshSessionRuntimeACLRejectsRetiredNodeKeys(t *testing.T) {
	for _, cfg := range []*config.Config{nil, {Redis: config.RedisConfig{DB: 2}}} {
		rules, err := refreshSessionRuntimeACL(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range rules {
			if strings.Contains(rule, "execution_node") {
				t.Errorf("retired node grant %q", rule)
			}
		}
		for _, key := range []string{"xiass:execution_node:cluster_identity", "xiass:execution_node:heartbeat:api", "xiass:execution_node:heartbeat:api2"} {
			if refreshRuntimeACLAllowsKey(t, rules, key) {
				t.Errorf("retired node key %q is accessible", key)
			}
		}
	}
}

func TestRefreshSessionRuntimeACLCommandsAndChannels(t *testing.T) {
	cfg := &config.Config{}
	cfg.Redis.DB = 2
	rules, err := refreshSessionRuntimeACL(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	var commands, channels []string
	for _, rule := range rules {
		switch rule[0] {
		case '+':
			commands = append(commands, rule)
		case '&':
			channels = append(channels, rule)
		case '~':
		default:
			t.Errorf("not a grant: %q", rule)
		}
	}
	wantCommands := strings.Fields(`
		+auth +hello +ping +role +time +scan +select
		+get +getdel +mget +set +setnx +del +exists +incr +decr +expire +pexpire +ttl +pttl
		+hget +hgetall +hset +hincrby +hincrbyfloat +sadd +srem +smembers +sismember +scard +spop
		+zadd +zrem +zcard +zscore +zrange +zrangebyscore +zrevrange +zremrangebyscore
		+lpush +rpop +multi +exec +watch +unwatch +eval +evalsha +script|load +publish +subscribe
	`)
	slices.Sort(commands)
	slices.Sort(wantCommands)
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Errorf("commands = %v, want %v", commands, wantCommands)
	}
	wantChannels := strings.Fields(`
		&auth:cache:invalidate &subscription:cache:invalidate &error_passthrough_rules_updated
		&tls_fingerprint_profiles_updated &sub2api:prompt_guard:config:invalidate
	`)
	slices.Sort(channels)
	slices.Sort(wantChannels)
	if !reflect.DeepEqual(channels, wantChannels) {
		t.Errorf("channels = %v, want %v", channels, wantChannels)
	}
	for _, banned := range strings.Fields(`
		+@all +@read +@write +@admin +@connection +acl +acl|setuser +acl|save
		+config +config|get +config|set +client +client|kill +monitor +info +keys +randomkey +dbsize
		+flushdb +flushall +swapdb +dump +restore +migrate +sort +rename +copy
		+psync +sync +replconf +replicaof +slaveof +failover +sentinel +cluster
		+save +bgsave +bgrewriteaof +shutdown +debug +module +function +fcall
		+script +script|flush +script|kill +psubscribe ~* &*
	`) {
		if slices.Contains(rules, banned) {
			t.Errorf("unsafe grant %q", banned)
		}
	}
	refreshRuntimeACLAssertProtected(t, rules)
}

func TestRefreshSessionRuntimeACLCustomLiterals(t *testing.T) {
	cfg := &config.Config{}
	cfg.Dashboard.KeyPrefix = `  dash:*?[x]\  `
	cfg.BatchImage.QueueReadyKey = `ready:*?[x]\`
	cfg.BatchImage.QueueDelayedKey = `delayed:*?[x]\`
	cfg.BatchImage.QueueActiveKey = `active:*?[x]\`
	cfg.BatchImage.InflightKeyPrefix = `inflight:*?[x]\`
	cfg.BatchImage.LockKeyPrefix = `lock:*?[x]\`
	before := *cfg
	rules, err := refreshSessionRuntimeACL(cfg, `  alert:*?[x]\  `)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{`~dash:\*\?\[x\]\\:dashboard:stats:v1`, `dash:*?[x]\:dashboard:stats:v1`},
		{`~ready:\*\?\[x\]\\`, `ready:*?[x]\`},
		{`~delayed:\*\?\[x\]\\`, `delayed:*?[x]\`},
		{`~active:\*\?\[x\]\\`, `active:*?[x]\`},
		{`~inflight:\*\?\[x\]\\*`, `inflight:*?[x]\job`},
		{`~lock:\*\?\[x\]\\*`, `lock:*?[x]\job`},
		{`~alert:\*\?\[x\]\\`, `alert:*?[x]\`},
	} {
		if !slices.Contains(rules, pair[0]) || !refreshRuntimeACLAllowsKey(t, rules, pair[1]) {
			t.Errorf("missing literal grant %q for %q", pair[0], pair[1])
		}
	}
	for _, key := range []string{"ready:anything", "inflight:arbitrary", "alert:anything", "dash:other:dashboard:stats:v1"} {
		if refreshRuntimeACLAllowsKey(t, rules, key) {
			t.Errorf("literal metacharacters widened access to %q", key)
		}
	}
	if !reflect.DeepEqual(*cfg, before) {
		t.Error("builder mutated config")
	}
	refreshRuntimeACLAssertProtected(t, rules)
	again, err := refreshSessionRuntimeACL(cfg, `  alert:*?[x]\  `)
	if err != nil || !reflect.DeepEqual(rules, again) {
		t.Fatal("builder is not deterministic", err)
	}
	rules[0] = "+@all"
	if again[0] == "+@all" {
		t.Error("builder results share mutable storage")
	}
}

func TestRefreshSessionRuntimeACLSourceNormalization(t *testing.T) {
	for _, prefix := range []string{"", "   ", "dash", " dash: "} {
		cfg := &config.Config{}
		cfg.Dashboard.KeyPrefix = prefix
		rules, err := refreshSessionRuntimeACL(cfg, "   ")
		if err != nil {
			t.Fatal(err)
		}
		want := "~dashboard:stats:v1"
		if strings.TrimSpace(prefix) != "" {
			want = "~dash:dashboard:stats:v1"
		}
		for _, rule := range []string{want, "~sub2api:dashboard:stats:v1", "~batch_image:queue:ready", "~ops:alert:evaluator:leader"} {
			if !slices.Contains(rules, rule) {
				t.Errorf("prefix %q: missing %q", prefix, rule)
			}
		}
	}
}

func TestRefreshSessionRuntimeACLRejectsUnsupportedLiteralBytes(t *testing.T) {
	setters := []struct {
		name string
		set  func(*config.Config, string) string
	}{
		{"dashboard", func(c *config.Config, v string) string { c.Dashboard.KeyPrefix = v; return "" }},
		{"ready", func(c *config.Config, v string) string { c.BatchImage.QueueReadyKey = v; return "" }},
		{"delayed", func(c *config.Config, v string) string { c.BatchImage.QueueDelayedKey = v; return "" }},
		{"active", func(c *config.Config, v string) string { c.BatchImage.QueueActiveKey = v; return "" }},
		{"inflight", func(c *config.Config, v string) string { c.BatchImage.InflightKeyPrefix = v; return "" }},
		{"lock", func(c *config.Config, v string) string { c.BatchImage.LockKeyPrefix = v; return "" }},
		{"alert", func(_ *config.Config, v string) string { return v }},
	}
	for _, setter := range setters {
		for _, b := range []byte{0, ' ', '\t', '\n', '\r', '\v', '\f'} {
			t.Run(fmt.Sprintf("%s/%02x", setter.name, b), func(t *testing.T) {
				values := []string{"literal" + string(b) + "key"}
				if setter.name != "dashboard" && setter.name != "alert" {
					values = append(values, string(b)+"key", "key"+string(b))
				}
				for _, value := range values {
					cfg := &config.Config{}
					alert := setter.set(cfg, value)
					before := *cfg
					rules, err := refreshSessionRuntimeACL(cfg, alert)
					if err == nil || rules != nil {
						t.Fatalf("unsupported literal %q produced rules %v, error %v", value, rules, err)
					}
					if !strings.Contains(err.Error(), "NUL or ASCII whitespace") {
						t.Errorf("unexpected rejection: %v", err)
					}
					if !reflect.DeepEqual(*cfg, before) {
						t.Error("rejection mutated config")
					}
				}
			})
		}
	}
}

func TestRefreshSessionRuntimeACLRejectsOverlap(t *testing.T) {
	setters := []struct {
		name   string
		prefix bool
		set    func(*config.Config, string) string
	}{
		{"dashboard", false, func(c *config.Config, v string) string { c.Dashboard.KeyPrefix = v; return "" }},
		{"ready", false, func(c *config.Config, v string) string { c.BatchImage.QueueReadyKey = v; return "" }},
		{"delayed", false, func(c *config.Config, v string) string { c.BatchImage.QueueDelayedKey = v; return "" }},
		{"active", false, func(c *config.Config, v string) string { c.BatchImage.QueueActiveKey = v; return "" }},
		{"inflight", true, func(c *config.Config, v string) string { c.BatchImage.InflightKeyPrefix = v; return "" }},
		{"lock", true, func(c *config.Config, v string) string { c.BatchImage.LockKeyPrefix = v; return "" }},
		{"alert", false, func(_ *config.Config, v string) string { return v }},
	}
	for _, setter := range setters {
		for _, protected := range []string{"refresh_token:", "user_refresh_tokens:", "token_family:"} {
			values := []string{protected, protected + "literal*?[x]"}
			if setter.prefix {
				values = append(values, protected[:1], strings.TrimSuffix(protected, ":"))
			}
			for _, value := range values {
				t.Run(setter.name+"/"+value, func(t *testing.T) {
					cfg := &config.Config{}
					alert := setter.set(cfg, value)
					rules, err := refreshSessionRuntimeACL(cfg, alert)
					if err == nil || rules != nil {
						t.Fatalf("overlap produced rules %v, error %v", rules, err)
					}
				})
			}
		}
	}
}

func TestRefreshSessionRuntimeACLProtectedLookalikesAreLiteral(t *testing.T) {
	cfg := &config.Config{}
	cfg.BatchImage.QueueReadyKey = "refresh_token"
	cfg.BatchImage.InflightKeyPrefix = "refresh_token?"
	cfg.BatchImage.LockKeyPrefix = "*"
	rules, err := refreshSessionRuntimeACL(cfg, "token_family")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{"~refresh_token", `~refresh_token\?*`, `~\**`, "~token_family"} {
		if !slices.Contains(rules, rule) {
			t.Errorf("missing safe literal %q", rule)
		}
	}
	refreshRuntimeACLAssertProtected(t, rules)
}

func refreshRuntimeACLAssertProtected(t *testing.T, rules []string) {
	t.Helper()
	for _, prefix := range []string{"refresh_token:", "user_refresh_tokens:", "token_family:"} {
		for _, suffix := range []string{"", "1", "0123456789abcdef", "*?[x]", "nested:key"} {
			if refreshRuntimeACLAllowsKey(t, rules, prefix+suffix) {
				t.Errorf("protected key %q is accessible", prefix+suffix)
			}
		}
	}
}

func refreshRuntimeACLAllowsKey(t *testing.T, rules []string, key string) bool {
	t.Helper()
	for _, rule := range rules {
		if !strings.HasPrefix(rule, "~") {
			continue
		}
		// These colon-delimited fixtures use the shared Redis/Go glob subset.
		match, err := path.Match(strings.TrimPrefix(rule, "~"), key)
		if err != nil {
			t.Fatalf("invalid key pattern %q: %v", rule, err)
		}
		if match {
			return true
		}
	}
	return false
}
