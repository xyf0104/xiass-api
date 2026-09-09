package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/redis/go-redis/v9"
)

// RefreshSessionRuntimeACLParameters accepts literal application cache names,
// never Redis rules, command categories or selectors. Empty fields keep defaults.
type RefreshSessionRuntimeACLParameters struct {
	DashboardPrefix *string `json:"dashboard_prefix,omitempty"`
	QueueReadyKey   string  `json:"queue_ready_key,omitempty"`
	QueueDelayedKey string  `json:"queue_delayed_key,omitempty"`
	QueueActiveKey  string  `json:"queue_active_key,omitempty"`
	InflightPrefix  string  `json:"inflight_prefix,omitempty"`
	LockPrefix      string  `json:"lock_prefix,omitempty"`
	AlertLockKey    string  `json:"alert_lock_key,omitempty"`
}

func (p RefreshSessionRuntimeACLParameters) Rules(db int) ([]string, error) {
	cfg := &config.Config{}
	cfg.Redis.DB, cfg.Dashboard.KeyPrefix = db, "sub2api:"
	if p.DashboardPrefix != nil {
		cfg.Dashboard.KeyPrefix = *p.DashboardPrefix
	}
	cfg.BatchImage.QueueReadyKey, cfg.BatchImage.QueueDelayedKey, cfg.BatchImage.QueueActiveKey = p.QueueReadyKey, p.QueueDelayedKey, p.QueueActiveKey
	cfg.BatchImage.InflightKeyPrefix, cfg.BatchImage.LockKeyPrefix = p.InflightPrefix, p.LockPrefix
	return RefreshSessionRuntimeACL(cfg, p.AlertLockKey)
}

// LegacyRefreshRuntimeAccess is an explicit maintenance capability. The caller
// must independently block AND drain login/refresh/revocation on every app node.
// It is not a zero-downtime auth protocol. No provider selection is automatic.
type LegacyRefreshRuntimeAccess struct {
	AuthEndpointsBlockedAndDrained bool
	ACL                            RefreshSessionRuntimeACLParameters
	// Independent replacement-role password hashes, never plaintext. At least
	// the replacement app credential is required, at most app/replica/backup.
	// Pin them on retry and reject reuse of ANY old password on ANY node.
	ReservedPasswordSHA256 []string
}

type refreshRuntimeFencePolicy struct {
	Version     int
	Access      LegacyRefreshRuntimeAccess
	RulesSHA256 string
}

func refreshRuntimePolicy(access LegacyRefreshRuntimeAccess) (*refreshRuntimeFencePolicy, error) {
	if !access.AuthEndpointsBlockedAndDrained {
		return nil, refreshTransitionReject("runtime access requires independently blocked and drained auth endpoints")
	}
	if len(access.ReservedPasswordSHA256) == 0 || len(access.ReservedPasswordSHA256) > 3 {
		return nil, refreshTransitionReject("runtime access requires bounded independent replacement credential hashes")
	}
	access.ReservedPasswordSHA256 = append([]string(nil), access.ReservedPasswordSHA256...)
	sort.Strings(access.ReservedPasswordSHA256)
	for i, hash := range access.ReservedPasswordSHA256 {
		if !persistentRefreshHex(hash, 32) || (i > 0 && hash == access.ReservedPasswordSHA256[i-1]) {
			return nil, refreshTransitionReject("runtime replacement credential hashes must be valid and independent")
		}
	}
	if access.ACL.DashboardPrefix != nil {
		prefix := *access.ACL.DashboardPrefix
		access.ACL.DashboardPrefix = &prefix
	}
	rules, err := access.ACL.Rules(0)
	if err != nil {
		return nil, refreshTransitionReject("unsafe runtime ACL parameters")
	}
	return &refreshRuntimeFencePolicy{Version: 1, Access: access, RulesSHA256: refreshRuntimeJSONHash(rules)}, nil
}

type refreshRuntimeUserProof struct {
	User, BeforeSHA256, RestrictedSHA256 string
}

// Canonical GETUSER state includes all permission-bearing fields. Redis may
// reorder commands/passwords/patterns; selectors are never allowed in a proof.
type refreshRuntimeUserState struct {
	Flags, Passwords, Commands, Keys, Channels []string
}

func refreshRuntimeJSONHash(v any) string {
	b, _ := json.Marshal(v)
	return refreshTransitionDigest(string(b))
}

func refreshRuntimeReadUser(ctx context.Context, client *redis.Client, user string) (*refreshRuntimeUserState, error) {
	cmd := redis.NewMapStringInterfaceCmd(ctx, "ACL", "GETUSER", user)
	if err := client.Process(ctx, cmd); err != nil {
		return nil, refreshTransitionReject("cannot inspect runtime ACL user")
	}
	m := cmd.Val()
	selectors, ok := m["selectors"].([]any)
	if !ok || len(selectors) != 0 || len(m) != 6 {
		return nil, refreshTransitionReject("runtime fence requires known selector-free ACL state")
	}
	s := &refreshRuntimeUserState{}
	for key, target := range map[string]*[]string{"flags": &s.Flags, "passwords": &s.Passwords} {
		items, ok := m[key].([]any)
		if !ok {
			return nil, refreshTransitionReject("unrecognized runtime ACL state")
		}
		*target = []string{}
		for _, item := range items {
			value, ok := item.(string)
			if !ok {
				return nil, refreshTransitionReject("unrecognized runtime ACL value")
			}
			// sanitize-payload is the reset default, not a permission grant.
			if key != "flags" || value != "sanitize-payload" {
				*target = append(*target, value)
			}
		}
		sort.Strings(*target)
	}
	for key, target := range map[string]*[]string{"commands": &s.Commands, "keys": &s.Keys, "channels": &s.Channels} {
		value, ok := m[key].(string)
		if !ok {
			return nil, refreshTransitionReject("unrecognized runtime ACL rules")
		}
		*target = strings.Fields(value)
		sort.Strings(*target)
	}
	if !slices.Equal(s.Flags, []string{"on"}) && !slices.Equal(s.Flags, []string{"off"}) {
		return nil, refreshTransitionReject("runtime fence rejects nopass or unknown ACL flags")
	}
	if len(s.Passwords) > 16 || (s.Flags[0] == "on" && len(s.Passwords) == 0) {
		return nil, refreshTransitionReject("runtime fence requires bounded password-protected principals")
	}
	for _, hash := range s.Passwords {
		if !persistentRefreshHex(hash, 32) {
			return nil, refreshTransitionReject("invalid runtime password hash")
		}
	}
	return s, nil
}

func (s refreshRuntimeUserState) restricted(rules []string) refreshRuntimeUserState {
	s.Commands, s.Keys, s.Channels = []string{"-@all"}, []string{}, []string{}
	if s.Flags[0] == "off" {
		s.Passwords = []string{}
		return s
	}
	for _, rule := range rules {
		switch rule[0] {
		case '+':
			s.Commands = append(s.Commands, rule)
		case '~':
			s.Keys = append(s.Keys, rule)
		case '&':
			s.Channels = append(s.Channels, rule)
		}
	}
	for _, items := range [][]string{s.Commands, s.Keys, s.Channels} {
		sort.Strings(items)
	}
	return s
}

func (g *refreshTransitionGroupRuntime) prepareRuntimeProof(ctx context.Context) error {
	if g.manifest.RuntimeFence == nil {
		return nil
	}
	for i, node := range g.nodes {
		if err := refreshTransitionMixedBoundary(ctx, node.bootstrap, node.runtimeRules); err != nil {
			return err
		}
		for _, user := range node.pin.ACLUsers {
			state, err := refreshRuntimeReadUser(ctx, node.bootstrap, user)
			if err != nil {
				return err
			}
			for _, hash := range g.manifest.RuntimeFence.Access.ReservedPasswordSHA256 {
				if slices.Contains(state.Passwords, hash) {
					return refreshTransitionReject("replacement runtime credential reuses a legacy password")
				}
			}
			node.pin.RuntimeACLProof = append(node.pin.RuntimeACLProof, refreshRuntimeUserProof{
				User: user, BeforeSHA256: refreshRuntimeJSONHash(state), RestrictedSHA256: refreshRuntimeJSONHash(state.restricted(node.runtimeRules)),
			})
		}
		g.manifest.Nodes[i] = node.pin
	}
	return nil
}

// Accept only the immutable initial inventory or its exact restricted target.
// This covers an ambiguous EXEC/ACL SAVE/PG proof commit without adopting a new
// password, widening grants, or trusting an unproved "already restricted" user.
func (node *refreshTransitionGroupNode) runtimeFenceRules(ctx context.Context, restrictedOnly bool) (map[string][]string, error) {
	result := map[string][]string{}
	if len(node.pin.RuntimeACLProof) != len(node.pin.ACLUsers) {
		return nil, refreshTransitionReject("runtime ACL proof missing")
	}
	for i, proof := range node.pin.RuntimeACLProof {
		if proof.User != node.pin.ACLUsers[i] || !persistentRefreshHex(proof.BeforeSHA256, 32) || !persistentRefreshHex(proof.RestrictedSHA256, 32) {
			return nil, refreshTransitionReject("runtime ACL proof inventory mismatch")
		}
		state, err := refreshRuntimeReadUser(ctx, node.fenced, proof.User)
		if err != nil {
			return nil, err
		}
		hash := refreshRuntimeJSONHash(state)
		if (hash != proof.RestrictedSHA256 && (restrictedOnly || hash != proof.BeforeSHA256)) || refreshRuntimeJSONHash(state.restricted(node.runtimeRules)) != proof.RestrictedSHA256 {
			return nil, refreshTransitionReject("immutable runtime ACL or password proof changed")
		}
		if state.Flags[0] == "off" {
			result[proof.User] = []string{"reset", "off", "resetkeys", "resetchannels"}
			continue
		}
		// One SETUSER resets permissions/selectors, keeps ALL original hashes,
		// and stays on. EXEC serializes it with already-authenticated clients.
		rules := []string{"reset", "on", "resetkeys", "resetchannels", "-@all"}
		for _, password := range state.Passwords {
			rules = append(rules, "#"+password)
		}
		result[proof.User] = append(rules, node.runtimeRules...)
	}
	return result, nil
}

func (node *refreshTransitionGroupNode) verifyACL(ctx context.Context, username, passwordHash, expectedHash string) (string, error) {
	if node.runtimeRules == nil {
		return refreshTransitionVerifyACL(ctx, node.fenced, username, passwordHash, expectedHash)
	}
	if _, err := node.runtimeFenceRules(ctx, true); err != nil {
		return "", err
	}
	// Old users have exact fixed grants; the only unrestricted user is the new
	// operator capability. Every old writer (including the bootstrap admin) is
	// accounted for by the exact inventory and per-user restricted proof.
	if err := refreshTransitionNodeInventory(ctx, node, node.fenced, username, passwordHash); err != nil {
		return "", err
	}
	state, err := refreshRuntimeReadUser(ctx, node.fenced, username)
	if err != nil {
		return "", err
	}
	if !slices.Equal(state.Flags, []string{"on"}) || !slices.Equal(state.Passwords, []string{passwordHash}) || !slices.Equal(state.Commands, []string{"+@all"}) || !slices.Equal(state.Keys, []string{"~*"}) || !slices.Equal(state.Channels, []string{"&*"}) {
		return "", refreshTransitionReject("exclusive runtime fence credential changed")
	}
	lines, err := node.fenced.ACLList(ctx).Result()
	if err != nil {
		return "", refreshTransitionReject("cannot verify runtime ACL digest")
	}
	sort.Strings(lines)
	hash := refreshTransitionDigest(strings.Join(lines, "\n"))
	if expectedHash != "" && hash != expectedHash {
		return "", refreshTransitionReject("persisted fence changed")
	}
	return hash, nil
}

// Only '*' and escaped literal bytes occur in the fixed builder's key patterns.
// Unlike path.Match, '*' also covers '/' in Redis key names. Unknown syntax is
// rejected, not approximated. This is admission only; Redis enforces the ACL.
func refreshRuntimeKeyMatcher(rules []string) (*regexp.Regexp, error) {
	patterns := []string{}
	for _, rule := range rules {
		if !strings.HasPrefix(rule, "~") {
			continue
		}
		var p strings.Builder
		for i := 1; i < len(rule); i++ {
			switch rule[i] {
			case '*':
				_, _ = p.WriteString(".*")
			case '\\':
				i++
				if i == len(rule) {
					return nil, refreshTransitionReject("invalid runtime key literal")
				}
				_, _ = p.WriteString(regexp.QuoteMeta(rule[i : i+1]))
			case '?', '[', ']':
				return nil, refreshTransitionReject("unsupported runtime key pattern")
			default:
				_, _ = p.WriteString(regexp.QuoteMeta(rule[i : i+1]))
			}
		}
		patterns = append(patterns, p.String())
	}
	return regexp.Compile("(?s)\\A(?:" + strings.Join(patterns, "|") + ")\\z")
}

// VerifyLegacyRefreshRuntimeFence is read-only. Runtime restoration may add new
// roles only after the PG authority and every old user's restricted proof agree.
// It never "repairs" a changed legacy ACL by restoring any previous permission.
func VerifyLegacyRefreshRuntimeFence(ctx context.Context, db *sql.DB, client *redis.Client, id, runID string, access LegacyRefreshRuntimeAccess) error {
	policy, err := refreshRuntimePolicy(access)
	if err != nil {
		return err
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT group_manifest FROM refresh_token_legacy_transition t CROSS JOIN refresh_token_authority a
		WHERE t.transition_id=$1 AND t.state='completed' AND a.backend='postgres'`, id).Scan(&raw); err != nil {
		return refreshTransitionReject("completed PostgreSQL runtime fence witness missing")
	}
	var manifest refreshTransitionGroupManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.RuntimeFence == nil || refreshRuntimeJSONHash(manifest.RuntimeFence) != refreshRuntimeJSONHash(policy) {
		return refreshTransitionReject("runtime restoration policy does not match immutable witness")
	}
	for _, pin := range manifest.Nodes {
		if pin.RunID != runID {
			continue
		}
		rules, err := access.ACL.Rules(pin.DB)
		if err != nil {
			return err
		}
		node := &refreshTransitionGroupNode{pin: pin, fenced: client, runtimeRules: rules}
		_, err = node.runtimeFenceRules(ctx, true)
		return err
	}
	return refreshTransitionReject(fmt.Sprintf("runtime node is not in fence witness (%s)", runID))
}
