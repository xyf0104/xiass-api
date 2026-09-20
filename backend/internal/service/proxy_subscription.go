package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const SettingKeyProxySubscriptions = "xiass_proxy_subscriptions"
const proxySubscriptionLimit = 256

var ErrProxySubscriptionChanged = infraerrors.Conflict("PROXY_SUBSCRIPTION_CHANGED", "Subscriptions changed; preview again before applying")

// ProxySubscriptionPersistence keeps managed proxy rows and the encrypted
// subscription snapshot in the same transaction. The expected value is a CAS.
type ProxySubscriptionPersistence interface {
	Load(context.Context) (string, error)
	Update(context.Context, string, func(ProxyRepository) (string, error)) error
}

type ProxySubscriptionSource struct {
	AllowInsecureTLS bool     `json:"allow_insecure_tls,omitempty"`
	ID               string   `json:"id"`
	Name             string   `json:"name,omitempty"`
	URL              string   `json:"url,omitempty"`
	Input            string   `json:"input,omitempty"`
	UserAgent        string   `json:"user_agent,omitempty"`
	IncludeProtocols []string `json:"include_protocols,omitempty"`
	ExcludeKeywords  []string `json:"exclude_keywords,omitempty"`
}

type ProxySubscriptionNode struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name,omitempty"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	ProxyID    int64  `json:"proxy_id,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
}

type proxySubscriptionConfig struct {
	Sources         []ProxySubscriptionSource `json:"sources"`
	SelectedNodeIDs []string                  `json:"selected_node_ids"`
	Nodes           []ProxySubscriptionNode   `json:"nodes"`
	Snapshot        string                    `json:"snapshot"`
	Addresses       map[string]string         `json:"addresses"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}

type ProxySubscriptionSourceView struct {
	AllowInsecureTLS bool     `json:"allow_insecure_tls,omitempty"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	URL              string   `json:"url,omitempty"`
	SourceKind       string   `json:"source_kind"`
	Configured       bool     `json:"configured"`
	UserAgent        string   `json:"user_agent,omitempty"`
	IncludeProtocols []string `json:"include_protocols,omitempty"`
	ExcludeKeywords  []string `json:"exclude_keywords,omitempty"`
}

type ProxySubscriptionNodeView struct {
	ProxySubscriptionNode
	Selected bool `json:"selected"`
}

type ProxySubscriptionOverview struct {
	AgentAvailable bool                          `json:"agent_available"`
	Sources        []ProxySubscriptionSourceView `json:"sources"`
	Nodes          []ProxySubscriptionNodeView   `json:"nodes"`
	UpdatedAt      *time.Time                    `json:"updated_at,omitempty"`
	PreviewID      string                        `json:"preview_id,omitempty"`
	LastError      string                        `json:"last_error,omitempty"`
}

type proxySubscriptionAgentRoute struct {
	ID       string `json:"id"`
	StableID string `json:"stable_id"`
	SOCKS5   string `json:"socks5"`
}

type proxySubscriptionAgentResult struct {
	Nodes    []ProxySubscriptionNode       `json:"nodes"`
	Routes   []proxySubscriptionAgentRoute `json:"routes"`
	Snapshot string                        `json:"snapshot"`
}

type proxySubscriptionAgentSync struct {
	Sources         []ProxySubscriptionSource `json:"sources,omitempty"`
	SelectedNodeIDs []string                  `json:"selected_node_ids"`
	Snapshot        string                    `json:"snapshot"`
	ListenAddresses map[string]string         `json:"listen_addresses,omitempty"`
	Prune           bool                      `json:"prune"`
}

type ProxySubscriptionAgent interface {
	Health(context.Context) error
	Preview(context.Context, []ProxySubscriptionSource) (*proxySubscriptionAgentResult, error)
	Sync(context.Context, proxySubscriptionAgentSync) (*proxySubscriptionAgentResult, error)
}

type proxySubscriptionPreview struct {
	Config   *proxySubscriptionConfig
	Revision string
	Expires  time.Time
}

type ProxySubscriptionService struct {
	store       ProxySubscriptionPersistence
	encryptor   SecretEncryptor
	agent       ProxySubscriptionAgent
	cfg         *config.Config
	mu          sync.Mutex
	lifecycleMu sync.Mutex
	previews    map[string]proxySubscriptionPreview
	lastError   string
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewProxySubscriptionService(store ProxySubscriptionPersistence, encryptor SecretEncryptor, cfg *config.Config) *ProxySubscriptionService {
	return &ProxySubscriptionService{store: store, encryptor: encryptor, cfg: cfg, agent: newHTTPProxySubscriptionAgent(), previews: make(map[string]proxySubscriptionPreview)}
}

func (s *ProxySubscriptionService) load(ctx context.Context) (*proxySubscriptionConfig, string, error) {
	raw, err := s.store.Load(ctx)
	if err != nil {
		return nil, "", err
	}
	c := &proxySubscriptionConfig{Sources: []ProxySubscriptionSource{}, Nodes: []ProxySubscriptionNode{}, SelectedNodeIDs: []string{}, Addresses: map[string]string{}}
	if raw == "" {
		return c, raw, nil
	}
	if s.encryptor == nil {
		return nil, "", errors.New("subscription encryption is unavailable")
	}
	plain, err := s.encryptor.Decrypt(raw)
	if err != nil {
		return nil, "", errors.New("stored subscriptions cannot be decrypted")
	}
	if err := json.Unmarshal([]byte(plain), c); err != nil {
		return nil, "", errors.New("stored subscriptions are invalid")
	}
	return c, raw, nil
}

func (s *ProxySubscriptionService) Overview(ctx context.Context) (*ProxySubscriptionOverview, error) {
	c, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := configOverview(c)
	healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out.AgentAvailable = s.agent.Health(healthCtx) == nil
	s.mu.Lock()
	out.LastError = s.lastError
	s.mu.Unlock()
	return out, nil
}

func (s *ProxySubscriptionService) Preview(ctx context.Context, sources []ProxySubscriptionSource) (*ProxySubscriptionOverview, error) {
	current, raw, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeProxySubscriptionSources(sources, current.Sources)
	if err != nil {
		return nil, err
	}
	result, err := s.agent.Preview(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if err := validateSubscriptionAgentResult(result); err != nil {
		return nil, err
	}
	next := &proxySubscriptionConfig{Sources: normalized, Nodes: mergeAgentNodes(result.Nodes, current.Nodes), SelectedNodeIDs: current.SelectedNodeIDs, Snapshot: result.Snapshot, Addresses: current.Addresses, UpdatedAt: current.UpdatedAt}
	id := uuid.NewString()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, preview := range s.previews {
		if time.Now().After(preview.Expires) {
			delete(s.previews, key)
		}
	}
	if len(s.previews) >= 16 {
		return nil, infraerrors.Conflict("PROXY_PREVIEWS_BUSY", "Too many pending previews; retry after they expire")
	}
	s.previews[id] = proxySubscriptionPreview{Config: next, Revision: raw, Expires: time.Now().Add(10 * time.Minute)}
	out := configOverview(next)
	out.AgentAvailable, out.PreviewID = true, id
	return out, nil
}

func (s *ProxySubscriptionService) Apply(ctx context.Context, sources []ProxySubscriptionSource, selected []string, previewID string) (*ProxySubscriptionOverview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, exists := s.previews[previewID]
	if !exists || time.Now().After(p.Expires) {
		delete(s.previews, previewID)
		return nil, infraerrors.BadRequest("PROXY_PREVIEW_EXPIRED", "Preview subscriptions before applying")
	}
	current, raw, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if raw != p.Revision {
		return nil, ErrProxySubscriptionChanged
	}
	normalized, err := normalizeProxySubscriptionSources(sources, current.Sources)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(normalized, p.Config.Sources) {
		return nil, ErrProxySubscriptionChanged
	}
	ids, err := normalizeSelectedNodeIDs(selected)
	if err != nil {
		return nil, err
	}
	next := *p.Config
	next.SelectedNodeIDs = ids
	next.Nodes = append([]ProxySubscriptionNode(nil), p.Config.Nodes...)
	if err := validateSelectedSubscriptionNodes(&next); err != nil {
		return nil, err
	}
	out, err := s.applyLocked(ctx, current, raw, &next)
	if err == nil {
		delete(s.previews, previewID)
	}
	return out, err
}

func (s *ProxySubscriptionService) applyLocked(ctx context.Context, current *proxySubscriptionConfig, raw string, next *proxySubscriptionConfig) (*ProxySubscriptionOverview, error) {
	if s.encryptor == nil {
		return nil, errors.New("subscription encryption is unavailable")
	}
	// Stage listeners without removing existing ones. Only prune after the
	// database transaction commits, so persistence failures keep working exits.
	staged, err := s.agent.Sync(ctx, syncSubscriptionRequest(next, false))
	if err != nil {
		return nil, err
	}
	addresses, err := subscriptionRouteAddresses(staged, next.SelectedNodeIDs, current.Addresses)
	if err != nil {
		s.restoreSavedListeners(ctx)
		return nil, err
	}
	next.Addresses = addresses
	err = s.store.Update(ctx, raw, func(proxies ProxyRepository) (string, error) {
		if err := persistSubscriptionProxies(ctx, proxies, current, next); err != nil {
			return "", err
		}
		next.UpdatedAt = time.Now().UTC()
		data, err := json.Marshal(next)
		if err != nil {
			return "", err
		}
		return s.encryptor.Encrypt(string(data))
	})
	if err != nil {
		s.restoreSavedListeners(ctx)
		return nil, err
	}
	_, pruneErr := s.agent.Sync(ctx, syncSubscriptionRequest(next, true))
	s.lastError = ""
	if pruneErr != nil {
		s.lastError = "Node cleanup is pending; active configuration was saved"
	}
	out := configOverview(next)
	out.AgentAvailable, out.LastError = true, s.lastError
	return out, nil
}

func (s *ProxySubscriptionService) restoreSavedListeners(ctx context.Context) {
	// Reload before rollback: another instance may have committed meanwhile.
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if saved, _, err := s.load(rollbackCtx); err == nil {
		_, _ = s.agent.Sync(rollbackCtx, syncSubscriptionRequest(saved, true))
	}
}

func (s *ProxySubscriptionService) Refresh(ctx context.Context) (*ProxySubscriptionOverview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, raw, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if len(current.Sources) == 0 {
		return configOverview(current), nil
	}
	result, err := s.agent.Preview(ctx, current.Sources)
	if err != nil {
		s.lastError = "Subscription refresh failed; saved nodes retained"
		return nil, err
	}
	if err = validateSubscriptionAgentResult(result); err != nil {
		return nil, err
	}
	next := *current
	next.Snapshot = result.Snapshot
	next.Nodes = mergeAgentNodes(result.Nodes, current.Nodes)
	if err := validateSelectedSubscriptionNodes(&next); err != nil {
		s.lastError = "A selected node disappeared from its subscription; saved nodes retained"
		return nil, err
	}
	return s.applyLocked(ctx, current, raw, &next)
}

func syncSubscriptionRequest(c *proxySubscriptionConfig, prune bool) proxySubscriptionAgentSync {
	addresses := make(map[string]string, len(c.SelectedNodeIDs))
	for _, id := range c.SelectedNodeIDs {
		if address := c.Addresses[id]; address != "" {
			addresses[id] = address
		}
	}
	return proxySubscriptionAgentSync{Snapshot: c.Snapshot, SelectedNodeIDs: c.SelectedNodeIDs, ListenAddresses: addresses, Prune: prune}
}

func validateSubscriptionAgentResult(result *proxySubscriptionAgentResult) error {
	if result == nil || len(result.Nodes) > proxySubscriptionLimit || len(result.Snapshot) > 16<<20 || len(result.Nodes) > 0 && result.Snapshot == "" {
		return errors.New("invalid subscription preview")
	}
	seen := map[string]bool{}
	for _, node := range result.Nodes {
		if node.ID == "" || seen[node.ID] {
			return errors.New("invalid subscription node identity")
		}
		seen[node.ID] = true
	}
	return nil
}

func validateSelectedSubscriptionNodes(c *proxySubscriptionConfig) error {
	available := map[string]bool{}
	for _, node := range c.Nodes {
		if !node.Missing {
			available[node.ID] = true
		}
	}
	for _, id := range c.SelectedNodeIDs {
		if !available[id] {
			return infraerrors.BadRequest("PROXY_NODE_MISSING", "A selected subscription node is unavailable; preview and select again")
		}
	}
	return nil
}

func subscriptionRouteAddresses(result *proxySubscriptionAgentResult, selected []string, previous map[string]string) (map[string]string, error) {
	if result == nil {
		return nil, errors.New("proxy agent returned no listeners")
	}
	addresses := make(map[string]string, len(previous)+len(selected))
	for id, addr := range previous {
		addresses[id] = addr
	}
	seen := map[string]bool{}
	for _, route := range result.Routes {
		id := route.StableID
		if id == "" {
			id = route.ID
		}
		host, port, err := net.SplitHostPort(route.SOCKS5)
		number, pe := strconv.Atoi(port)
		if err != nil || pe != nil || number < 1 || number > 65535 || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return nil, errors.New("invalid subscription listener")
		}
		if old := previous[id]; old != "" && old != route.SOCKS5 {
			return nil, errors.New("subscription listener changed unexpectedly")
		}
		addresses[id], seen[id] = route.SOCKS5, true
	}
	for _, id := range selected {
		if !seen[id] {
			return nil, errors.New("selected subscription listener is missing")
		}
	}
	return addresses, nil
}

func persistSubscriptionProxies(ctx context.Context, repo ProxyRepository, current, next *proxySubscriptionConfig) error {
	selected := map[string]bool{}
	for _, id := range next.SelectedNodeIDs {
		selected[id] = true
	}
	oldSelected := map[string]bool{}
	for _, id := range current.SelectedNodeIDs {
		oldSelected[id] = true
	}
	for i := range next.Nodes {
		node := &next.Nodes[i]
		var existing *Proxy
		if node.ProxyID > 0 {
			var err error
			existing, err = repo.GetByID(ctx, node.ProxyID)
			if err != nil && !errors.Is(err, ErrProxyNotFound) {
				return err
			}
		}
		if !selected[node.ID] {
			if existing != nil && oldSelected[node.ID] {
				// Never retire a live proxy through a check-then-disable operation:
				// another administrator may be binding it in a separate transaction.
				return infraerrors.Conflict("PROXY_SUBSCRIPTION_NODE_IMPORTED", "Remove the imported proxy in IP management before deselecting its subscription node")
			}
			if existing == nil {
				node.ProxyID = 0
			}
			continue
		}
		host, portText, _ := net.SplitHostPort(next.Addresses[node.ID])
		port, _ := strconv.Atoi(portText)
		if existing == nil {
			if node.ProxyID > 0 && oldSelected[node.ID] {
				return infraerrors.Conflict("PROXY_SUBSCRIPTION_NODE_DELETED", "A selected proxy was deleted in IP management; preview and deselect it before refreshing")
			}
			existing = &Proxy{Name: subscriptionProxyName(node.SourceName, node.Name), Protocol: "socks5h", Host: host, Port: port, Status: StatusActive, FallbackMode: FallbackModeNone, ExpiryWarnDays: 7}
			if err := repo.Create(ctx, existing); err != nil {
				return err
			}
			node.ProxyID = existing.ID
		} else {
			// Ordinary refresh preserves administrator status/expiry/fallback.
			if existing.Host != host || existing.Port != port || existing.Protocol != "socks5h" {
				return infraerrors.Conflict("PROXY_MANAGED_NODE_EDITED", "A managed subscription proxy was edited; restore its listener before refreshing")
			}
			if !oldSelected[node.ID] {
				existing.Status = StatusActive
			}
			existing.Name = subscriptionProxyName(node.SourceName, node.Name)
			if err := repo.Update(ctx, existing); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeProxySubscriptionSources(input, current []ProxySubscriptionSource) ([]ProxySubscriptionSource, error) {
	if len(input) > 16 {
		return nil, infraerrors.BadRequest("PROXY_SOURCE_LIMIT", "At most 16 subscription sources are supported")
	}
	old := map[string]ProxySubscriptionSource{}
	for _, source := range current {
		old[source.ID] = source
	}
	result := make([]ProxySubscriptionSource, 0, len(input))
	seenIDs, seenInputs := map[string]bool{}, map[string]bool{}
	for i, source := range input {
		source.ID = strings.TrimSpace(source.ID)
		if source.ID == "" {
			source.ID = "sub-" + uuid.NewString()
		}
		if len(source.ID) > 96 || strings.ContainsAny(source.ID, "\r\n\t /\\") || seenIDs[source.ID] {
			return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION", "Invalid or duplicate source ID")
		}
		seenIDs[source.ID] = true
		source.Name = strings.TrimSpace(source.Name)
		if source.Name == "" {
			source.Name = fmt.Sprintf("Subscription %d", i+1)
		}
		if len([]rune(source.Name)) > 80 || strings.ContainsAny(source.Name, "\r\n") {
			return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION", "Invalid subscription name")
		}
		source.URL, source.Input = strings.TrimSpace(source.URL), strings.TrimSpace(source.Input)
		if source.URL == "" && source.Input == "" {
			source.URL, source.Input = old[source.ID].URL, old[source.ID].Input
		}
		if (source.URL == "") == (source.Input == "") || len(source.Input) > 2<<20 || len(source.URL) > 8192 {
			return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION", "Provide one subscription URL or file content")
		}
		if source.URL != "" {
			if err := validateProxySubscriptionURL(source.URL); err != nil {
				return nil, err
			}
			// Fragments are client-side labels, not part of the subscription request.
			parsed, _ := url.Parse(source.URL)
			parsed.Fragment, parsed.RawFragment = "", ""
			source.URL = parsed.String()
		}
		sum := sha256.Sum256([]byte(source.URL + "\x00" + source.Input))
		identity := hex.EncodeToString(sum[:])
		if seenInputs[identity] {
			return nil, infraerrors.BadRequest("DUPLICATE_SUBSCRIPTION", "The same subscription source was added twice")
		}
		seenInputs[identity] = true
		source.UserAgent = strings.TrimSpace(source.UserAgent)
		if len(source.UserAgent) > 256 || strings.ContainsAny(source.UserAgent, "\r\n") || len(source.IncludeProtocols) > 16 || len(source.ExcludeKeywords) > 64 {
			return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION", "Invalid subscription filters or user agent")
		}
		for _, keyword := range source.ExcludeKeywords {
			if strings.TrimSpace(keyword) == "" || len(keyword) > 128 {
				return nil, infraerrors.BadRequest("INVALID_SUBSCRIPTION", "Invalid exclusion keyword")
			}
		}
		// JSON [] and null carry the same filter semantics.
		if len(source.IncludeProtocols) == 0 {
			source.IncludeProtocols = nil
		}
		if len(source.ExcludeKeywords) == 0 {
			source.ExcludeKeywords = nil
		}
		result = append(result, source)
	}
	return result, nil
}

func validateProxySubscriptionURL(raw string) error {
	u, err := url.Parse(raw)
	if err == nil && u.Hostname() != "" && u.User == nil && u.Opaque == "" {
		if u.Scheme == "https" {
			return nil
		}
		if ip := net.ParseIP(u.Hostname()); u.Scheme == "http" && ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return infraerrors.BadRequest("INVALID_SUBSCRIPTION_URL", "Use an HTTPS subscription URL (literal loopback HTTP is allowed for local sources)")
}

func normalizeSelectedNodeIDs(input []string) ([]string, error) {
	if len(input) > proxySubscriptionLimit {
		return nil, infraerrors.BadRequest("PROXY_NODE_LIMIT", "At most 256 nodes can be selected")
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(input))
	for _, id := range input {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result, nil
}

func mergeAgentNodes(nodes, current []ProxySubscriptionNode) []ProxySubscriptionNode {
	old := map[string]ProxySubscriptionNode{}
	for _, node := range current {
		old[node.ID] = node
	}
	result := make([]ProxySubscriptionNode, 0, len(nodes)+len(current))
	for _, node := range nodes {
		node.ProxyID = old[node.ID].ProxyID
		node.Missing = false
		delete(old, node.ID)
		result = append(result, node)
	}
	for _, node := range old {
		if node.ProxyID > 0 {
			node.Missing = true
			result = append(result, node)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceID != result[j].SourceID {
			return result[i].SourceID < result[j].SourceID
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func configOverview(c *proxySubscriptionConfig) *ProxySubscriptionOverview {
	out := &ProxySubscriptionOverview{Sources: []ProxySubscriptionSourceView{}, Nodes: []ProxySubscriptionNodeView{}}
	for _, source := range c.Sources {
		kind := "url"
		if source.Input != "" {
			kind = "input"
		}
		out.Sources = append(out.Sources, ProxySubscriptionSourceView{ID: source.ID, Name: source.Name, URL: maskSubscriptionURL(source.URL), SourceKind: kind, Configured: true, UserAgent: source.UserAgent, IncludeProtocols: source.IncludeProtocols, ExcludeKeywords: source.ExcludeKeywords, AllowInsecureTLS: source.AllowInsecureTLS})
	}
	selected := map[string]bool{}
	for _, id := range c.SelectedNodeIDs {
		selected[id] = true
	}
	for _, node := range c.Nodes {
		out.Nodes = append(out.Nodes, ProxySubscriptionNodeView{ProxySubscriptionNode: node, Selected: selected[node.ID]})
	}
	if !c.UpdatedAt.IsZero() {
		at := c.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}

func maskSubscriptionURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/..."
}

func subscriptionProxyName(source, node string) string {
	name := "[Subscription] " + strings.TrimSpace(source) + " / " + strings.TrimSpace(node)
	runes := []rune(name)
	if len(runes) > 100 {
		name = string(runes[:100])
	}
	return name
}
