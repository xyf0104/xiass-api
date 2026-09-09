package repository

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type refreshTransitionEmptyScanHook struct {
	pages    int
	aclLines []string
}

func (h *refreshTransitionEmptyScanHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *refreshTransitionEmptyScanHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *refreshTransitionEmptyScanHook) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, command redis.Cmder) error {
		if list, ok := command.(*redis.StringSliceCmd); ok && command.Name() == "acl" {
			list.SetVal(h.aclLines)
			return nil
		}
		if scan, ok := command.(*redis.ScanCmd); ok {
			h.pages++
			scan.SetVal([]string{}, 1)
			return nil
		}
		return errors.New("unexpected command in empty-scan budget test")
	}
}

func TestPersistentRefreshTransitionExclusivePrincipalHasOnlyRecoveryPassword(t *testing.T) {
	passwordHash := strings.Repeat("a", 64)
	exclusive := "user exclusive on sanitize-payload #" + passwordHash + " ~* &* +@all"
	disabled := "user default off sanitize-payload resetchannels -@all"
	for _, fixture := range []struct {
		name, principal, old string
		valid                bool
	}{
		{"valid", exclusive, disabled, true},
		{"additional-password", exclusive + " #" + strings.Repeat("b", 64), disabled, false},
		{"nopass", exclusive + " nopass", disabled, false},
		{"off", strings.Replace(exclusive, " on ", " off ", 1), disabled, false},
		{"selector", exclusive + " (~* +@all)", disabled, false},
		{"wrong-password", strings.ReplaceAll(exclusive, passwordHash, strings.Repeat("b", 64)), disabled, false},
		{"old-enabled", exclusive, strings.Replace(disabled, " off ", " on ", 1), false},
		{"old-password-retained", exclusive, disabled + " #" + strings.Repeat("b", 64), false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
			defer func() { _ = client.Close() }()
			client.AddHook(&refreshTransitionEmptyScanHook{aclLines: []string{fixture.principal, fixture.old}})
			hash, err := refreshTransitionVerifyACL(context.Background(), client, "exclusive", passwordHash, "")
			if fixture.valid {
				require.NoError(t, err)
				require.Len(t, hash, 64)
			} else {
				require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
				require.Empty(t, hash)
			}
		})
	}
}

func TestPersistentRefreshTransitionEmptyMatchPageBudget(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379"})
	defer func() { _ = client.Close() }()
	hook := &refreshTransitionEmptyScanHook{}
	client.AddHook(hook)
	entries, err := refreshTransitionSnapshot(context.Background(), client)
	require.Nil(t, entries)
	require.ErrorContains(t, err, "scan page budget exhausted")
	require.Equal(t, refreshTransitionMaxScanPages, hook.pages, "MATCH returning no auth keys still consumes the hard page budget")
}

func refreshTransitionTestData() *service.RefreshTokenData {
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-24 * time.Hour)
	return &service.RefreshTokenData{UserID: 71, TokenVersion: -53, FamilyID: strings.Repeat("a", 32), BindingHash: strings.Repeat("b", 64),
		CreatedAt: created, ExpiresAt: created.Add(7 * 24 * time.Hour), FamilyExpiresAt: created.Add(7 * 24 * time.Hour)}
}

func TestPersistentRefreshTransitionDecodePreservesOriginalDeadlines(t *testing.T) {
	d := refreshTransitionTestData()
	encoded, err := json.Marshal(d)
	require.NoError(t, err)
	got, err := refreshTransitionDecode(string(encoded))
	require.NoError(t, err)
	require.Equal(t, d, got)
	var object map[string]any
	require.NoError(t, json.Unmarshal(encoded, &object))
	delete(object, "family_expires_at")
	object["expires_at"] = d.CreatedAt.Add(3 * 24 * time.Hour).Format(time.RFC3339Nano)
	encoded, err = json.Marshal(object)
	require.NoError(t, err)
	got, err = refreshTransitionDecode(string(encoded))
	require.NoError(t, err)
	require.Equal(t, d.CreatedAt.Add(3*24*time.Hour), got.FamilyExpiresAt)
	require.Equal(t, got.ExpiresAt, got.FamilyExpiresAt)
	d.FamilyExpiresAt = d.CreatedAt.Add(8 * 24 * time.Hour)
	encoded, err = json.Marshal(d)
	require.NoError(t, err)
	_, err = refreshTransitionDecode(string(encoded))
	require.ErrorContains(t, err, "family deadline")
	secret, err := NewLegacyRefreshTransitionRecoverySecret()
	require.NoError(t, err)
	require.Len(t, secret, 32)
}

func TestPersistentRefreshTransitionAcceptsActualHTTPBinding(t *testing.T) {
	d := refreshTransitionTestData()
	d.BindingHash = (&service.SessionBinding{IP: "192.0.2.1", UserAgent: "migration-regression"}).Hash()
	require.Len(t, d.BindingHash, 32, "the established HTTP producer uses a 128-bit binding")
	body, err := json.Marshal(d)
	require.NoError(t, err)
	got, err := refreshTransitionDecode(string(body))
	require.NoError(t, err)
	require.Equal(t, d, got, "adoption must preserve, not regenerate, the existing binding")
}

func TestPersistentRefreshTransitionRejectsUntrustedMetadata(t *testing.T) {
	encoded, err := json.Marshal(refreshTransitionTestData())
	require.NoError(t, err)
	for _, bad := range []string{
		"", "null", "[]", "not-json", string(encoded) + "{}", string(encoded[:len(encoded)-1]),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":71,"user_id":72`, 1),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":null`, 1),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":"71"`, 1),
		strings.Replace(string(encoded), `"user_id":71`, `"user_id":0`, 1),
		strings.Replace(string(encoded), `"token_version":-53,`, "", 1),
		strings.Replace(string(encoded), `"token_version":-53`, `"token_version":-53,"refresh_token":"must-never-appear"`, 1),
		strings.Repeat(" ", 4097),
	} {
		d, err := refreshTransitionDecode(bad)
		require.Nil(t, d)
		require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
		require.NotContains(t, err.Error(), "must-never-appear")
	}
	var store *PersistentRefreshTokenStore
	result, err := store.AdoptLegacyRefreshTokens(context.Background(), LegacyRefreshTransitionOptions{})
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
}

func TestPersistentRefreshTransitionSevenDayMicrosecondPrecision(t *testing.T) {
	for _, nanos := range []int{0, 1, 499, 500, 999} {
		t.Run(strconv.Itoa(nanos), func(t *testing.T) {
			d := refreshTransitionTestData()
			d.CreatedAt = time.Date(2026, time.January, 2, 3, 4, 5, 123456000+nanos, time.UTC)
			d.ExpiresAt = d.CreatedAt.Add(7 * 24 * time.Hour)
			d.FamilyExpiresAt = d.ExpiresAt
			for _, legacyFamily := range []bool{false, true} {
				payload := *d
				if legacyFamily {
					payload.FamilyExpiresAt = time.Time{}
				}
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				got, err := refreshTransitionDecode(string(encoded))
				require.NoError(t, err, "exactly seven days is valid before and after precision normalization")
				require.Equal(t, d.CreatedAt.Truncate(time.Microsecond), got.CreatedAt)
				require.Equal(t, d.ExpiresAt.Truncate(time.Microsecond), got.ExpiresAt)
				require.Equal(t, got.ExpiresAt, got.FamilyExpiresAt)
				require.Equal(t, 7*24*time.Hour, got.FamilyExpiresAt.Sub(got.CreatedAt))
				require.False(t, got.ExpiresAt.After(d.ExpiresAt), "never round expiry up")
				require.False(t, got.FamilyExpiresAt.After(d.FamilyExpiresAt), "never restart a legacy family deadline")
			}
			d.FamilyExpiresAt = d.CreatedAt.Add(7*24*time.Hour + time.Microsecond)
			encoded, err := json.Marshal(d)
			require.NoError(t, err)
			_, err = refreshTransitionDecode(string(encoded))
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe, "one microsecond beyond seven days is not a tolerated clock skew")
		})
	}
}

type refreshTransitionTopologyHook struct {
	redis.Hook
	states []map[string]string
	reads  int
}

func (h *refreshTransitionTopologyHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *refreshTransitionTopologyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *refreshTransitionTopologyHook) ProcessHook(_ redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch command.Name() {
		case "info":
			state := h.states[min(h.reads, len(h.states)-1)]
			h.reads++
			lines := make([]string, 0, len(state))
			for key, value := range state {
				lines = append(lines, key+":"+value)
			}
			sort.Strings(lines)
			info, ok := command.(*redis.StringCmd)
			if !ok {
				return errors.New("unexpected info command type")
			}
			info.SetVal(strings.Join(lines, "\r\n"))
			return nil
		case "config":
			cfg, ok := command.(*redis.MapStringStringCmd)
			if !ok {
				return errors.New("unexpected config command type")
			}
			cfg.SetVal(map[string]string{"aclfile": "/fixture/acl"})
			return nil
		}
		return errors.New("unexpected topology command")
	}
}

func refreshTransitionTopologyFixture(t *testing.T) (*refreshTransitionGroupRuntime, []*redis.Client, []*refreshTransitionTopologyHook) {
	t.Helper()
	replID := strings.Repeat("c", 40)
	primary := map[string]string{"run_id": strings.Repeat("a", 40), "redis_mode": "standalone", "cluster_enabled": "0", "loading": "0",
		"master_replid": replID, "master_replid2": strings.Repeat("0", 40), "role": "master", "connected_slaves": "1", "slave0": "ip=192.0.2.2,port=6379,state=online,offset=1,lag=0"}
	replica := map[string]string{"run_id": strings.Repeat("b", 40), "redis_mode": "standalone", "cluster_enabled": "0", "loading": "0",
		"master_replid": replID, "role": "slave", "master_host": "192.0.2.1", "master_port": "6379", "master_sync_in_progress": "0", "master_link_status": "up", "connected_slaves": "0"}
	g := &refreshTransitionGroupRuntime{manifest: refreshTransitionGroupManifest{PrimaryAddress: "192.0.2.1:6379", ReplicationID: replID}, nodes: []*refreshTransitionGroupNode{
		{pin: refreshTransitionNodeManifest{RunID: primary["run_id"]}},
		{pin: refreshTransitionNodeManifest{RunID: replica["run_id"], ReplicaAddress: "192.0.2.2:6379"}, aclHash: strings.Repeat("d", 64)},
	}}
	var clients []*redis.Client
	var hooks []*refreshTransitionTopologyHook
	for _, state := range []map[string]string{primary, replica} {
		client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379", MaxRetries: -1})
		hook := &refreshTransitionTopologyHook{states: []map[string]string{state}}
		client.AddHook(hook)
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		clients, hooks = append(clients, client), append(hooks, hook)
	}
	return g, clients, hooks
}

func TestPersistentRefreshTransitionTopologyWaitsForStrictConvergence(t *testing.T) {
	for _, phase := range []string{"replica-sync", "replica-loading", "primary-wait-bgsave", "primary-send-bulk"} {
		t.Run(phase, func(t *testing.T) {
			g, clients, hooks := refreshTransitionTopologyFixture(t)
			target := 1
			if strings.HasPrefix(phase, "primary-") {
				target = 0
			}
			ready := hooks[target].states[0]
			pending := maps.Clone(ready)
			if target == 1 {
				pending["master_sync_in_progress"], pending["master_link_status"] = "1", "down"
				if phase == "replica-loading" {
					pending["loading"] = "1"
				}
			} else {
				state := "wait_bgsave"
				if phase == "primary-send-bulk" {
					state = "send_bulk"
				}
				pending["slave0"] = strings.Replace(ready["slave0"], "online", state, 1)
			}
			hooks[target].states = []map[string]string{pending, ready}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, g.topology(ctx, clients, false))
			for _, hook := range hooks {
				require.Equal(t, 2, hook.reads, "the entire group must be revalidated, not just the formerly syncing node")
			}
		})
	}
}

func TestPersistentRefreshTransitionTopologyDeadlineAndInitialAdmission(t *testing.T) {
	for _, initial := range []bool{true, false} {
		t.Run(strconv.FormatBool(initial), func(t *testing.T) {
			g, clients, hooks := refreshTransitionTopologyFixture(t)
			hooks[1].states[0]["master_sync_in_progress"] = "1"
			ctx, cancel := context.WithTimeout(context.Background(), 140*time.Millisecond)
			defer cancel()
			err := g.topology(ctx, clients, initial)
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
			if initial {
				require.Equal(t, 1, hooks[1].reads, "preflight must still reject an unsynchronized replica immediately")
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.GreaterOrEqual(t, hooks[1].reads, 2)
				require.Contains(t, err.Error(), "phase=post-fence")
				require.Contains(t, err.Error(), "sync_in_progress=1")
				require.Contains(t, err.Error(), "run_id_match=true")
			}
			require.NotContains(t, err.Error(), "192.0.2.1")
		})
	}
}

func TestPersistentRefreshTransitionTopologyRejectsStableContradictions(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"role", "master"}, {"run_id", strings.Repeat("f", 40)}, {"master_replid", strings.Repeat("f", 40)},
		{"master_host", "private-upstream.invalid"}, {"master_port", "6380"}, {"connected_slaves", "1"},
		{"master_link_status", "unknown-private-value"}, {"master_sync_in_progress", "2"}, {"loading", "2"}, {"cluster_enabled", "1"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			g, clients, hooks := refreshTransitionTopologyFixture(t)
			hooks[1].states[0]["master_sync_in_progress"] = "1"
			hooks[1].states[0][tc.field] = tc.value
			err := g.topology(context.Background(), clients, false)
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
			require.Equal(t, 1, hooks[0].reads)
			require.Equal(t, 1, hooks[1].reads, "a contradictory syncing node must not enter the wait loop")
			for _, secret := range []string{"private-upstream.invalid", "unknown-private-value", "192.0.2.1"} {
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestPersistentRefreshTransitionTopologyRechecksOtherNodesDuringSync(t *testing.T) {
	g, clients, hooks := refreshTransitionTopologyFixture(t)
	hooks[1].states[0]["master_sync_in_progress"] = "1"
	promoted := maps.Clone(hooks[0].states[0])
	promoted["run_id"] = strings.Repeat("f", 40)
	hooks[0].states = append(hooks[0].states, promoted)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.ErrorContains(t, g.topology(ctx, clients, false), "identity changed")
	require.Equal(t, 2, hooks[0].reads)
	require.Equal(t, 1, hooks[1].reads)

	g, clients, hooks = refreshTransitionTopologyFixture(t)
	hooks[1].states[0]["master_sync_in_progress"] = "1"
	wrong := maps.Clone(hooks[1].states[0])
	wrong["master_host"] = "wrong.invalid"
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:6379", MaxRetries: -1})
	defer func() { _ = client.Close() }()
	last := &refreshTransitionTopologyHook{states: []map[string]string{wrong}}
	client.AddHook(last)
	clients = append(clients, client)
	g.nodes = append(g.nodes, &refreshTransitionGroupNode{pin: refreshTransitionNodeManifest{RunID: wrong["run_id"], ReplicaAddress: "192.0.2.3:6379"}})
	require.ErrorContains(t, g.topology(ctx, clients, false), "upstream_match=false")
	require.Equal(t, 1, hooks[0].reads, "a later contradictory node must reject within the same group pass")
	require.Equal(t, 1, last.reads)
}
