package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type subscriptionTestCipher struct{ fail bool }

func (e *subscriptionTestCipher) Encrypt(value string) (string, error) {
	if e.fail {
		return "", errors.New("cipher unavailable")
	}
	return "encrypted:" + base64.StdEncoding.EncodeToString([]byte(value)), nil
}
func (e *subscriptionTestCipher) Decrypt(value string) (string, error) {
	if len(value) < 10 {
		return "", errors.New("invalid ciphertext")
	}
	data, err := base64.StdEncoding.DecodeString(value[10:])
	return string(data), err
}

type subscriptionTestProxies struct {
	ProxyRepository
	rows   map[int64]Proxy
	used   map[int64]int64
	nextID int64
}

func (r *subscriptionTestProxies) Create(_ context.Context, p *Proxy) error {
	r.nextID++
	p.ID = r.nextID
	r.rows[p.ID] = *p
	return nil
}
func (r *subscriptionTestProxies) GetByID(_ context.Context, id int64) (*Proxy, error) {
	p, ok := r.rows[id]
	if !ok {
		return nil, ErrProxyNotFound
	}
	return &p, nil
}
func (r *subscriptionTestProxies) Update(_ context.Context, p *Proxy) error {
	r.rows[p.ID] = *p
	return nil
}
func (r *subscriptionTestProxies) CountAccountsByProxyID(_ context.Context, id int64) (int64, error) {
	return r.used[id], nil
}

type subscriptionTestStore struct {
	mu      sync.Mutex
	raw     string
	proxies subscriptionTestProxies
	fail    bool
}

func (r *subscriptionTestStore) Load(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.raw, nil
}
func (r *subscriptionTestStore) Update(_ context.Context, expected string, fn func(ProxyRepository) (string, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.raw != expected {
		return ErrProxySubscriptionChanged
	}
	next := r.proxies
	next.rows = maps.Clone(r.proxies.rows)
	raw, err := fn(&next)
	if err != nil {
		return err
	}
	if r.fail {
		return errors.New("commit failed")
	}
	r.raw, r.proxies = raw, next
	return nil
}

type subscriptionTestAgent struct {
	nodes        []ProxySubscriptionNode
	previewCount int
	syncs        []proxySubscriptionAgentSync
	previewErr   error
	syncErr      error
}

func (a *subscriptionTestAgent) Health(context.Context) error { return nil }
func (a *subscriptionTestAgent) Preview(_ context.Context, sources []ProxySubscriptionSource) (*proxySubscriptionAgentResult, error) {
	a.previewCount++
	if a.previewErr != nil {
		return nil, a.previewErr
	}
	if len(sources) == 0 {
		return &proxySubscriptionAgentResult{}, nil
	}
	return &proxySubscriptionAgentResult{Nodes: a.nodes, Snapshot: "private node password: fixture-secret"}, nil
}
func (a *subscriptionTestAgent) Sync(_ context.Context, req proxySubscriptionAgentSync) (*proxySubscriptionAgentResult, error) {
	a.syncs = append(a.syncs, req)
	if a.syncErr != nil {
		return nil, a.syncErr
	}
	r := &proxySubscriptionAgentResult{Nodes: a.nodes}
	for i, id := range req.SelectedNodeIDs {
		addr := req.ListenAddresses[id]
		if addr == "" {
			addr = "127.0.0.1:31001"
			if i > 0 {
				addr = "127.0.0.1:31002"
			}
		}
		r.Routes = append(r.Routes, proxySubscriptionAgentRoute{ID: id, StableID: id, SOCKS5: addr})
	}
	return r, nil
}

func subscriptionFixture() (*ProxySubscriptionService, *subscriptionTestStore, *subscriptionTestAgent, []ProxySubscriptionSource) {
	store := &subscriptionTestStore{proxies: subscriptionTestProxies{rows: map[int64]Proxy{}, used: map[int64]int64{}}}
	svc := NewProxySubscriptionService(store, &subscriptionTestCipher{}, nil)
	agent := &subscriptionTestAgent{nodes: []ProxySubscriptionNode{
		{ID: "route-a", SourceID: "source-a", SourceName: "First", Name: "Japan", Protocol: "anytls"},
		{ID: "route-b", SourceID: "source-b", SourceName: "Second", Name: "Germany", Protocol: "vless"},
	}}
	svc.agent = agent
	sources := []ProxySubscriptionSource{{ID: "source-a", Name: "First", URL: "https://sub.example/secret-path?token=url-secret"}, {ID: "source-b", Name: "Second", Input: "socks5://user:input-secret@proxy.example:1080"}}
	return svc, store, agent, sources
}

func TestProxySubscriptionPreviewApplyUsesSnapshotAndEncryptsSecrets(t *testing.T) {
	svc, store, agent, sources := subscriptionFixture()
	ctx := context.Background()
	preview, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	require.Empty(t, store.raw)
	require.Empty(t, store.proxies.rows)
	require.Empty(t, agent.syncs)
	encoded, err := json.Marshal(preview)
	require.NoError(t, err)
	for _, secret := range []string{"url-secret", "secret-path", "input-secret", "fixture-secret"} {
		require.NotContains(t, string(encoded), secret)
	}
	out, err := svc.Apply(ctx, sources, []string{"route-b", "route-a", "route-a"}, preview.PreviewID)
	require.NoError(t, err)
	require.Len(t, store.proxies.rows, 2)
	require.Equal(t, 1, agent.previewCount)
	require.False(t, agent.syncs[0].Prune)
	require.True(t, agent.syncs[1].Prune)
	require.Equal(t, "private node password: fixture-secret", agent.syncs[0].Snapshot)
	for _, secret := range []string{"url-secret", "input-secret", "fixture-secret"} {
		require.NotContains(t, store.raw, secret)
	}
	require.True(t, out.Nodes[0].Selected)
	require.Positive(t, out.Nodes[0].ProxyID)
	require.NoError(t, svc.reconcile(ctx))
	require.Equal(t, 1, agent.previewCount)
	require.Equal(t, "127.0.0.1:31001", agent.syncs[2].ListenAddresses["route-a"])
}

func TestProxySubscriptionRestartUsesSavedSnapshotAndKeepsProxyIDs(t *testing.T) {
	svc, store, agent, sources := subscriptionFixture()
	ctx := context.Background()
	preview, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	_, err = svc.Apply(ctx, sources, []string{"route-a", "route-b"}, preview.PreviewID)
	require.NoError(t, err)
	before := maps.Clone(store.proxies.rows)
	other := NewProxySubscriptionService(store, &subscriptionTestCipher{}, nil)
	second := &subscriptionTestAgent{previewErr: errors.New("subscription provider offline"), nodes: agent.nodes}
	other.agent = second
	require.NoError(t, other.reconcile(ctx))
	require.Zero(t, second.previewCount)
	require.Equal(t, before, store.proxies.rows)
	require.Equal(t, agent.syncs[1].ListenAddresses, second.syncs[0].ListenAddresses)
}

func TestProxySubscriptionFailureRollsBackProxyRowsAndKeepsLiveRoutes(t *testing.T) {
	svc, store, agent, sources := subscriptionFixture()
	ctx := context.Background()
	p, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	_, err = svc.Apply(ctx, sources, []string{"route-a"}, p.PreviewID)
	require.NoError(t, err)
	raw, before := store.raw, maps.Clone(store.proxies.rows)
	p, err = svc.Preview(ctx, sources)
	require.NoError(t, err)
	store.fail = true
	_, err = svc.Apply(ctx, sources, []string{"route-a", "route-b"}, p.PreviewID)
	require.Error(t, err)
	require.Equal(t, raw, store.raw)
	require.Equal(t, before, store.proxies.rows)
	last := agent.syncs[len(agent.syncs)-1]
	require.True(t, last.Prune)
	require.Equal(t, []string{"route-a"}, last.SelectedNodeIDs)
}

func TestProxySubscriptionRefreshFailureAndMissingNodeRetainLastGood(t *testing.T) {
	svc, store, agent, sources := subscriptionFixture()
	ctx := context.Background()
	p, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	_, err = svc.Apply(ctx, sources, []string{"route-a"}, p.PreviewID)
	require.NoError(t, err)
	raw := store.raw
	count := len(agent.syncs)
	agent.previewErr = errors.New("offline")
	_, err = svc.Refresh(ctx)
	require.Error(t, err)
	require.Equal(t, raw, store.raw)
	require.Len(t, agent.syncs, count)
	agent.previewErr = nil
	agent.nodes = agent.nodes[1:]
	_, err = svc.Refresh(ctx)
	require.Error(t, err)
	require.Equal(t, raw, store.raw)
	require.Len(t, agent.syncs, count)
}

func TestProxySubscriptionRejectsEmptyUnknownStaleAndMutatedPreview(t *testing.T) {
	svc, store, _, sources := subscriptionFixture()
	ctx := context.Background()
	_, err := svc.Apply(ctx, sources, nil, "")
	require.Error(t, err)
	p, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	_, err = svc.Apply(ctx, sources, []string{"other"}, p.PreviewID)
	require.Error(t, err)
	changed := append([]ProxySubscriptionSource(nil), sources...)
	changed[0].URL = "https://changed.example/sub"
	_, err = svc.Apply(ctx, changed, []string{"route-a"}, p.PreviewID)
	require.ErrorIs(t, err, ErrProxySubscriptionChanged)
	_, err = svc.Apply(ctx, sources, []string{"route-a"}, p.PreviewID)
	require.NoError(t, err)
	_, err = svc.Apply(ctx, sources, []string{"route-a"}, p.PreviewID)
	require.Error(t, err)
	require.Len(t, store.proxies.rows, 1)
}

func TestProxySubscriptionRemovalProtectsBindingsAndPreservesAdminDisable(t *testing.T) {
	svc, store, _, sources := subscriptionFixture()
	ctx := context.Background()
	p, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	out, err := svc.Apply(ctx, sources, []string{"route-a"}, p.PreviewID)
	require.NoError(t, err)
	id := out.Nodes[0].ProxyID
	require.Positive(t, id)
	proxy := store.proxies.rows[id]
	proxy.Status = "inactive"
	expires := time.Now().Add(time.Hour)
	proxy.ExpiresAt = &expires
	store.proxies.rows[id] = proxy
	_, err = svc.Refresh(ctx)
	require.NoError(t, err)
	require.Equal(t, "inactive", store.proxies.rows[id].Status)
	require.Equal(t, &expires, store.proxies.rows[id].ExpiresAt)
	store.proxies.used[id] = 1
	p, err = svc.Preview(ctx, []ProxySubscriptionSource{})
	require.NoError(t, err)
	_, err = svc.Apply(ctx, []ProxySubscriptionSource{}, nil, p.PreviewID)
	require.Error(t, err)
	store.proxies.used[id] = 0
	_, err = svc.Apply(ctx, []ProxySubscriptionSource{}, nil, p.PreviewID)
	require.Error(t, err, "even an unbound imported proxy must be removed explicitly")
	require.Equal(t, "inactive", store.proxies.rows[id].Status)
	delete(store.proxies.rows, id)
	_, err = svc.Refresh(ctx)
	require.Error(t, err, "refresh must not recreate an explicitly deleted proxy")
	require.Empty(t, store.proxies.rows)
	_, err = svc.Apply(ctx, []ProxySubscriptionSource{}, nil, p.PreviewID)
	require.NoError(t, err)
	c, _, err := svc.load(ctx)
	require.NoError(t, err)
	require.Empty(t, c.Sources)
	require.Empty(t, c.SelectedNodeIDs)
}

func TestProxySubscriptionReconcilePurgesExpiredPreviewSecrets(t *testing.T) {
	svc, _, _, sources := subscriptionFixture()
	preview, err := svc.Preview(context.Background(), sources)
	require.NoError(t, err)
	entry := svc.previews[preview.PreviewID]
	entry.Expires = time.Now().Add(-time.Second)
	svc.previews[preview.PreviewID] = entry
	require.NoError(t, svc.reconcile(context.Background()))
	require.Empty(t, svc.previews)
}

func TestProxySubscriptionMultipleSourcesValidation(t *testing.T) {
	_, _, _, sources := subscriptionFixture()
	_, err := normalizeProxySubscriptionSources(sources, nil)
	require.NoError(t, err)
	retained := []ProxySubscriptionSource{{ID: "source-a", Name: "First"}, {ID: "source-b", Name: "Second"}}
	got, err := normalizeProxySubscriptionSources(retained, sources)
	require.NoError(t, err)
	require.Equal(t, sources, got)
	_, err = normalizeProxySubscriptionSources(append(sources, ProxySubscriptionSource{ID: "different", URL: sources[0].URL}), nil)
	require.Error(t, err)
	for _, raw := range []string{"file:///etc/passwd", "http://external.example/sub", "https://user:pass@host/sub"} {
		require.Error(t, validateProxySubscriptionURL(raw))
	}
	require.NoError(t, validateProxySubscriptionURL("https://external.example/sub"))
	require.NoError(t, validateProxySubscriptionURL("http://127.0.0.1:9000/sub"))
}

func TestProxySubscriptionURLFragmentNormalization(t *testing.T) {
	const endpoint = "https://subscription.example/s/private%2Fpath?target=clash&token=synthetic%2Bsecret&flag=&flag=2"
	for _, fragment := range []string{"#socks", "#socks%E4%B8%93%E7%94%A8", "#", "#https://label.example"} {
		t.Run(fragment, func(t *testing.T) {
			raw := endpoint + fragment
			require.NoError(t, validateProxySubscriptionURL(raw))
			got, err := normalizeProxySubscriptionSources([]ProxySubscriptionSource{{ID: "source", URL: raw}}, nil)
			require.NoError(t, err)
			require.Equal(t, endpoint, got[0].URL)
		})
	}
	_, err := normalizeProxySubscriptionSources([]ProxySubscriptionSource{
		{ID: "one", URL: endpoint + "#first"},
		{ID: "two", URL: endpoint + "#second"},
	}, nil)
	require.ErrorContains(t, err, "DUPLICATE_SUBSCRIPTION")
	for _, raw := range []string{"http://external.example/sub#label", "file:///etc/passwd#label", "https://user:password@host/sub#label"} {
		require.Error(t, validateProxySubscriptionURL(raw))
	}
	got, err := normalizeProxySubscriptionSources([]ProxySubscriptionSource{{ID: "local", URL: "http://[::1]:9000/sub#label"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "http://[::1]:9000/sub", got[0].URL)
	got, err = normalizeProxySubscriptionSources([]ProxySubscriptionSource{{ID: "node", Input: "socks5://127.0.0.1:1080#node-label"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "socks5://127.0.0.1:1080#node-label", got[0].Input)
}

func TestProxySubscriptionTLSConsentRoundTripAndPreviewBinding(t *testing.T) {
	svc, _, _, sources := subscriptionFixture()
	sources[0].AllowInsecureTLS = true
	ctx := context.Background()
	preview, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	require.True(t, preview.Sources[0].AllowInsecureTLS)
	require.False(t, preview.Sources[1].AllowInsecureTLS)
	changed := append([]ProxySubscriptionSource(nil), sources...)
	changed[0].AllowInsecureTLS = false
	_, err = svc.Apply(ctx, changed, []string{"route-a"}, preview.PreviewID)
	require.Error(t, err)
	preview, err = svc.Preview(ctx, sources)
	require.NoError(t, err)
	out, err := svc.Apply(ctx, sources, []string{"route-a"}, preview.PreviewID)
	require.NoError(t, err)
	require.True(t, out.Sources[0].AllowInsecureTLS)
	saved, _, err := svc.load(ctx)
	require.NoError(t, err)
	require.True(t, saved.Sources[0].AllowInsecureTLS)
	require.False(t, saved.Sources[1].AllowInsecureTLS)
}
