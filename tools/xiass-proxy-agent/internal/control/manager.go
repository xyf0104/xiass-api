package control

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"sync"

	"github.com/xyf0104/xiass-proxy-agent/internal/proxyroute"
)

type Manager struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	routes map[string]*activeRoute
}

type activeRoute struct {
	route      *proxyroute.Route
	sourceID   string
	sourceName string
	name       string
	protocol   string
}

type RouteInfo struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id,omitempty"`
	SourceName string `json:"source_name,omitempty"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	SOCKS5     string `json:"socks5"`
	StableID   string `json:"stable_id"`
}

type SubscriptionNodeInfo struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name,omitempty"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
}

type SubscriptionResult struct {
	Nodes    []SubscriptionNodeInfo `json:"nodes"`
	Routes   []RouteInfo            `json:"routes,omitempty"`
	Snapshot string                 `json:"snapshot,omitempty"`
}

func NewManager() *Manager {
	return &Manager{routes: make(map[string]*activeRoute)}
}

func (m *Manager) Create(input string, index int) (RouteInfo, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	nodes, err := proxyroute.Parse([]byte(input))
	if err != nil {
		return RouteInfo{}, err
	}
	if index < 0 || index >= len(nodes) {
		return RouteInfo{}, errors.New("route index is out of range")
	}
	route, err := proxyroute.Build(nodes[index], index)
	if err != nil {
		return RouteInfo{}, err
	}
	if err := route.Start(""); err != nil {
		route.Close()
		return RouteInfo{}, err
	}

	m.mu.Lock()
	if _, exists := m.routes[route.ID]; exists {
		m.mu.Unlock()
		route.Close()
		return RouteInfo{}, errors.New("route already exists")
	}
	entry := &activeRoute{route: route, name: route.DisplayName, protocol: route.Protocol}
	m.routes[route.ID] = entry
	m.mu.Unlock()
	return info(entry), nil
}

func (m *Manager) List() []RouteInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return listRouteInfo(m.routes)
}

// Preview delegates loading and validation to upstream, exports the canonical
// nodes into an authenticated opaque snapshot, and closes every adapter. It
// never opens a listener or dials through a node.
func (m *Manager) Preview(ctx context.Context, sources []proxyroute.Source) (SubscriptionResult, error) {
	candidates, err := proxyroute.LoadSources(ctx, sources)
	if err != nil {
		return SubscriptionResult{}, err
	}
	defer proxyroute.CloseCandidates(candidates)
	result := SubscriptionResult{Nodes: nodeInfo(candidates)}
	result.Snapshot, err = encodeSnapshot(candidates)
	if err != nil {
		return SubscriptionResult{}, err
	}
	return result, nil
}

// Sync serializes the complete reconciliation transaction. All snapshot nodes
// are rebuilt first, and all required listeners are started before the active
// map is replaced. Any error closes only new resources and leaves old routes
// and listeners intact.
func (m *Manager) Sync(ctx context.Context, opaque string, selectedIDs []string, listenAddresses map[string]string, prune bool) (SubscriptionResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return SubscriptionResult{}, err
	}

	var candidates []proxyroute.Candidate
	var err error
	if opaque != "" {
		candidates, err = decodeAndBuildSnapshot(opaque)
		if err != nil {
			return SubscriptionResult{}, err
		}
	} else if len(selectedIDs) != 0 {
		return SubscriptionResult{}, errors.New("snapshot is required when selecting nodes")
	}
	defer proxyroute.CloseCandidates(candidates)

	candidateByID := make(map[string]*proxyroute.Candidate, len(candidates))
	for index := range candidates {
		candidate := &candidates[index]
		if _, duplicate := candidateByID[candidate.ID]; duplicate {
			return SubscriptionResult{}, errors.New("snapshot contains duplicate node ids")
		}
		candidateByID[candidate.ID] = candidate
	}
	selected, err := deduplicateSelected(selectedIDs, candidateByID)
	if err != nil {
		return SubscriptionResult{}, err
	}
	if err := validateListenAddresses(listenAddresses, candidateByID, selected); err != nil {
		return SubscriptionResult{}, err
	}

	m.mu.RLock()
	existing := make(map[string]*activeRoute, len(m.routes))
	for id, route := range m.routes {
		existing[id] = route
	}
	m.mu.RUnlock()

	ensured := make(map[string]*activeRoute, len(selected))
	created := make([]*proxyroute.Route, 0, len(selected))
	for _, id := range selected {
		candidate := candidateByID[id]
		desired := listenAddresses[id]
		if current := existing[id]; current != nil && (desired == "" || desired == current.route.Addr()) {
			candidate.Outbound.Close()
			candidate.Outbound = nil
			ensured[id] = &activeRoute{
				route: current.route, sourceID: candidate.SourceID, sourceName: candidate.SourceName,
				name: candidate.Name, protocol: candidate.Protocol,
			}
			continue
		}
		route := proxyroute.NewRoute(*candidate)
		candidate.Outbound = nil
		if err := route.Start(desired); err != nil {
			route.Close()
			closeLoopbackRoutes(created)
			return SubscriptionResult{}, err
		}
		created = append(created, route)
		ensured[id] = &activeRoute{
			route: route, sourceID: candidate.SourceID, sourceName: candidate.SourceName,
			name: candidate.Name, protocol: candidate.Protocol,
		}
	}
	if err := ctx.Err(); err != nil {
		closeLoopbackRoutes(created)
		return SubscriptionResult{}, err
	}

	next := make(map[string]*activeRoute, len(existing)+len(ensured))
	if !prune {
		for id, route := range existing {
			next[id] = route
		}
	}
	for id, route := range ensured {
		next[id] = route
	}

	m.mu.Lock()
	old := m.routes
	m.routes = next
	m.mu.Unlock()
	for id, route := range old {
		if replacement := next[id]; replacement == nil || replacement.route != route.route {
			route.route.Close()
		}
	}

	return SubscriptionResult{
		Nodes: nodeInfo(candidates), Routes: listRouteInfo(next), Snapshot: opaque,
	}, nil
}

func deduplicateSelected(ids []string, candidates map[string]*proxyroute.Candidate) ([]string, error) {
	result := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		if candidates[id] == nil {
			return nil, errors.New("selected subscription node does not exist in snapshot")
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func validateListenAddresses(addresses map[string]string, candidates map[string]*proxyroute.Candidate, selected []string) error {
	selectedSet := make(map[string]struct{}, len(selected))
	for _, id := range selected {
		selectedSet[id] = struct{}{}
	}
	for id, address := range addresses {
		if candidates[id] == nil {
			return errors.New("listen address references an unknown snapshot node")
		}
		if _, ok := selectedSet[id]; !ok {
			return errors.New("listen address references an unselected snapshot node")
		}
		host, portText, err := net.SplitHostPort(address)
		port, portErr := strconv.Atoi(portText)
		ip := net.ParseIP(host)
		if err != nil || portErr != nil || ip == nil || !ip.IsLoopback() || port < 1 || port > 65535 {
			return errors.New("listen addresses must use a literal loopback IP and fixed port")
		}
	}
	return nil
}

func nodeInfo(candidates []proxyroute.Candidate) []SubscriptionNodeInfo {
	result := make([]SubscriptionNodeInfo, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, SubscriptionNodeInfo{
			ID: candidate.ID, SourceID: candidate.SourceID, SourceName: candidate.SourceName,
			Name: candidate.Name, Protocol: candidate.Protocol,
		})
	}
	return result
}

func listRouteInfo(routes map[string]*activeRoute) []RouteInfo {
	result := make([]RouteInfo, 0, len(routes))
	for _, route := range routes {
		result = append(result, info(route))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func closeLoopbackRoutes(routes []*proxyroute.Route) {
	for _, route := range routes {
		route.Close()
	}
}

func (m *Manager) Delete(id string) bool {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	route, ok := m.routes[id]
	if ok {
		delete(m.routes, id)
	}
	m.mu.Unlock()
	if ok {
		route.route.Close()
	}
	return ok
}

func (m *Manager) Close() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	routes := m.routes
	m.routes = make(map[string]*activeRoute)
	m.mu.Unlock()
	for _, route := range routes {
		route.route.Close()
	}
}

func info(entry *activeRoute) RouteInfo {
	return RouteInfo{
		ID: entry.route.ID, StableID: entry.route.StableID, SourceID: entry.sourceID, SourceName: entry.sourceName,
		Name: entry.name, Protocol: entry.protocol, SOCKS5: entry.route.Addr(),
	}
}
