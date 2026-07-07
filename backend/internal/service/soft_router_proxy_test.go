package service

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestSoftRouterProxyServiceCreateMappingAssignsAvailablePorts(t *testing.T) {
	publicPorts, publicListeners := reserveContiguousLoopbackPorts(t, 3)
	publicBusy, publicUsed, publicFree := publicPorts[0], publicPorts[1], publicPorts[2]
	defer closeListener(t, publicListeners[0])
	closeListener(t, publicListeners[1])
	closeListener(t, publicListeners[2])

	rawPorts, rawListeners := reserveContiguousLoopbackPorts(t, 3)
	rawBusy, rawUsed, rawFree := rawPorts[0], rawPorts[1], rawPorts[2]
	defer closeListener(t, rawListeners[0])
	closeListener(t, rawListeners[1])
	closeListener(t, rawListeners[2])

	t.Setenv(softRouterRawPortRangeEnv, rangeText(rawBusy, rawFree))
	t.Setenv(softRouterPublicPortRangeEnv, rangeText(publicBusy, publicFree))

	repo := &softRouterProxyRepoStub{
		config: SoftRouterProxyConfig{
			GatewayListenHost: "127.0.0.1",
			UpstreamHost:      "127.0.0.1",
			FRPServerPort:     7010,
			RawPortStart:      rawBusy,
			RawPortEnd:        rawFree,
			PublicPortStart:   publicBusy,
			PublicPortEnd:     publicFree,
			DefaultUsername:   "user",
			DefaultPassword:   "pass",
			AgentPollSeconds:  20,
		},
		mappings: []SoftRouterProxyMapping{
			{
				ID:            7,
				AgentID:       1,
				Name:          "existing",
				OpenWrtPort:   1081,
				RawRemotePort: rawUsed,
				PublicPort:    publicUsed,
				Username:      "user",
				Password:      "pass",
				Enabled:       false,
				Status:        SoftRouterMappingStatusDisabled,
			},
		},
	}
	proxyRepo := &softRouterProxyProxyRepoStub{}
	svc := &SoftRouterProxyService{repo: repo, proxyRepo: proxyRepo}

	mapping, err := svc.CreateMapping(context.Background(), &SoftRouterProxyMapping{
		AgentID:     1,
		Name:        "new",
		OpenWrtPort: 1082,
		Enabled:     true,
		EnabledSet:  true,
	})

	require.NoError(t, err)
	require.Equal(t, rawFree, mapping.RawRemotePort)
	require.Equal(t, publicFree, mapping.PublicPort)
	require.Len(t, proxyRepo.created, 1)
	require.Equal(t, publicFree, proxyRepo.created[0].Port)
}

func TestSoftRouterProxyServiceCreateMappingRejectsOccupiedRequestedPorts(t *testing.T) {
	rawPorts, rawListeners := reserveContiguousLoopbackPorts(t, 1)
	rawBusy := rawPorts[0]
	defer closeListener(t, rawListeners[0])

	publicPorts, publicListeners := reserveContiguousLoopbackPorts(t, 1)
	publicFree := publicPorts[0]
	closeListener(t, publicListeners[0])

	t.Setenv(softRouterRawPortRangeEnv, strconv.Itoa(rawBusy))
	t.Setenv(softRouterPublicPortRangeEnv, strconv.Itoa(publicFree))

	repo := &softRouterProxyRepoStub{
		config: SoftRouterProxyConfig{
			GatewayListenHost: "127.0.0.1",
			UpstreamHost:      "127.0.0.1",
			FRPServerPort:     7010,
			RawPortStart:      rawBusy,
			RawPortEnd:        rawBusy,
			PublicPortStart:   publicFree,
			PublicPortEnd:     publicFree,
			DefaultUsername:   "user",
			DefaultPassword:   "pass",
			AgentPollSeconds:  20,
		},
	}
	svc := &SoftRouterProxyService{repo: repo, proxyRepo: &softRouterProxyProxyRepoStub{}}

	_, err := svc.CreateMapping(context.Background(), &SoftRouterProxyMapping{
		AgentID:       1,
		Name:          "occupied",
		OpenWrtPort:   1081,
		RawRemotePort: rawBusy,
		PublicPort:    publicFree,
		Username:      "user",
		Password:      "pass",
		Enabled:       true,
		EnabledSet:    true,
	})

	require.Error(t, err)
	require.Equal(t, "SOFT_ROUTER_RAW_PORT_OCCUPIED", infraerrors.Reason(err))
}

func TestSoftRouterProxyServiceUpdateConfigRejectsUndeployedRange(t *testing.T) {
	rawPorts, rawListeners := reserveContiguousLoopbackPorts(t, 1)
	rawFree := rawPorts[0]
	closeListener(t, rawListeners[0])

	publicPorts, publicListeners := reserveContiguousLoopbackPorts(t, 2)
	publicFree := publicPorts[0]
	publicUndeployed := publicPorts[1]
	closeListener(t, publicListeners[0])
	closeListener(t, publicListeners[1])

	t.Setenv(softRouterRawPortRangeEnv, strconv.Itoa(rawFree))
	t.Setenv(softRouterPublicPortRangeEnv, strconv.Itoa(publicUndeployed))

	repo := &softRouterProxyRepoStub{
		config: SoftRouterProxyConfig{
			GatewayListenHost: "127.0.0.1",
			UpstreamHost:      "127.0.0.1",
			FRPServerPort:     7010,
			RawPortStart:      rawFree,
			RawPortEnd:        rawFree,
			PublicPortStart:   publicFree,
			PublicPortEnd:     publicFree,
			DefaultUsername:   "user",
			DefaultPassword:   "pass",
			AgentPollSeconds:  20,
		},
	}
	svc := &SoftRouterProxyService{repo: repo, proxyRepo: &softRouterProxyProxyRepoStub{}}

	_, err := svc.UpdateConfig(context.Background(), &repo.config)

	require.Error(t, err)
	require.Equal(t, "SOFT_ROUTER_PORT_RANGE_NOT_DEPLOYED", infraerrors.Reason(err))
}

func TestSoftRouterProxyServiceInstallFRPAllowsPublicPortsAlreadyPublished(t *testing.T) {
	rawPorts, rawListeners := reserveContiguousLoopbackPorts(t, 1)
	rawFree := rawPorts[0]
	closeListener(t, rawListeners[0])

	publicPorts, publicListeners := reserveContiguousLoopbackPorts(t, 1)
	publicPublished := publicPorts[0]
	defer closeListener(t, publicListeners[0])

	t.Setenv(softRouterRawPortRangeEnv, strconv.Itoa(rawFree))
	t.Setenv(softRouterPublicPortRangeEnv, strconv.Itoa(publicPublished))

	repo := &softRouterProxyRepoStub{
		config: SoftRouterProxyConfig{
			GatewayListenHost: "127.0.0.1",
			UpstreamHost:      "127.0.0.1",
			FRPServerPort:     7010,
			RawPortStart:      rawFree,
			RawPortEnd:        rawFree,
			PublicPortStart:   publicPublished,
			PublicPortEnd:     publicPublished,
			DefaultUsername:   "old",
			DefaultPassword:   "old",
			AgentPollSeconds:  20,
		},
	}
	installer := &softRouterFRPInstallerStub{}
	svc := &SoftRouterProxyService{
		repo:         repo,
		proxyRepo:    &softRouterProxyProxyRepoStub{},
		frpInstaller: installer,
	}

	result, err := svc.InstallFRP(context.Background(), SoftRouterFRPInstallInput{
		PublicHost:        "api.example.com",
		GatewayListenHost: "127.0.0.1",
		UpstreamHost:      "127.0.0.1",
		FRPServerHost:     "api.example.com",
		FRPServerPort:     7010,
		FRPToken:          "token",
		RawPortStart:      rawFree,
		RawPortEnd:        rawFree,
		PublicPortStart:   publicPublished,
		PublicPortEnd:     publicPublished,
		DefaultUsername:   "user",
		DefaultPassword:   "pass",
		AgentPollSeconds:  20,
	})

	require.NoError(t, err)
	require.True(t, result.RestartRequired)
	require.True(t, installer.installCalled)
	require.True(t, repo.config.Enabled)
	require.Equal(t, "api.example.com", repo.config.PublicHost)
	require.Equal(t, rawFree, installer.installedConfig.RawPortStart)
	require.Equal(t, publicPublished, installer.installedConfig.PublicPortStart)
}

func TestSoftRouterProxyServiceDeleteMappingRemovesGeneratedProxyWithoutAccounts(t *testing.T) {
	proxyID := int64(37)
	repo := &softRouterProxyRepoStub{
		config: SoftRouterProxyConfig{
			GatewayListenHost: "127.0.0.1",
			UpstreamHost:      "127.0.0.1",
			FRPServerPort:     7010,
			RawPortStart:      12084,
			RawPortEnd:        12084,
			PublicPortStart:   1102,
			PublicPortEnd:     1102,
			DefaultUsername:   "user",
			DefaultPassword:   "pass",
			AgentPollSeconds:  20,
		},
		mappings: []SoftRouterProxyMapping{
			{
				ID:            12,
				AgentID:       1,
				Name:          "Japan",
				OpenWrtPort:   1081,
				RawRemotePort: 12084,
				PublicPort:    1102,
				Username:      "user",
				Password:      "pass",
				Enabled:       true,
				Status:        SoftRouterMappingStatusRunning,
				ProxyID:       &proxyID,
			},
		},
	}
	proxyRepo := &softRouterProxyProxyRepoStub{
		created: []Proxy{
			{
				ID:       proxyID,
				Name:     "OpenWrt - Japan",
				Protocol: "socks5",
				Host:     "api.example.com",
				Port:     1102,
				Status:   StatusActive,
			},
		},
	}
	svc := &SoftRouterProxyService{repo: repo, proxyRepo: proxyRepo}

	err := svc.DeleteMapping(context.Background(), 12)

	require.NoError(t, err)
	require.Empty(t, repo.mappings)
	require.Equal(t, []int64{proxyID}, proxyRepo.deleted)
	require.Empty(t, proxyRepo.updated)
	require.Equal(t, 1, repo.cleanupOrphanedCalls)
}

func TestSoftRouterProxyServiceReconcileKeepsClosedButEnabledNode(t *testing.T) {
	nodeID := int64(88)
	repo := &softRouterProxyRepoStub{
		config: SoftRouterProxyConfig{
			Enabled:           true,
			GatewayListenHost: "127.0.0.1",
			UpstreamHost:      "127.0.0.1",
			FRPServerPort:     7010,
			RawPortStart:      12084,
			RawPortEnd:        12084,
			PublicPortStart:   1102,
			PublicPortEnd:     1102,
			DefaultUsername:   "user",
			DefaultPassword:   "pass",
			AgentPollSeconds:  20,
		},
		nodes: []SoftRouterSocksNode{
			{
				ID:           nodeID,
				AgentID:      1,
				Name:         "Japan",
				OpenWrtPort:  1082,
				ListenStatus: "closed",
				Enabled:      true,
			},
		},
		mappings: []SoftRouterProxyMapping{
			{
				ID:            17,
				AgentID:       1,
				NodeID:        &nodeID,
				Name:          "Japan",
				OpenWrtPort:   1082,
				RawRemotePort: 12084,
				PublicPort:    1102,
				Username:      "user",
				Password:      "pass",
				Enabled:       true,
				Status:        SoftRouterMappingStatusRunning,
			},
		},
	}
	runtime := &softRouterRuntimeStub{}
	svc := &SoftRouterProxyService{repo: repo, proxyRepo: &softRouterProxyProxyRepoStub{}, runtime: runtime}

	err := svc.Reconcile(context.Background())

	require.NoError(t, err)
	require.Len(t, runtime.mappings, 1)
	require.Equal(t, int64(17), runtime.mappings[0].ID)
	require.Equal(t, 1, repo.cleanupOrphanedCalls)
}

func reserveContiguousLoopbackPorts(t *testing.T, count int) ([]int, []net.Listener) {
	t.Helper()
	for start := 20000; start <= 60000-count; start++ {
		ports := make([]int, 0, count)
		listeners := make([]net.Listener, 0, count)
		ok := true
		for port := start; port < start+count; port++ {
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				ok = false
				break
			}
			ports = append(ports, port)
			listeners = append(listeners, ln)
		}
		if ok {
			return ports, listeners
		}
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}
	t.Fatalf("failed to reserve %d contiguous loopback ports", count)
	return nil, nil
}

func closeListener(t *testing.T, ln net.Listener) {
	t.Helper()
	require.NoError(t, ln.Close())
}

func rangeText(start, end int) string {
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

type softRouterProxyRepoStub struct {
	config               SoftRouterProxyConfig
	agents               []SoftRouterAgent
	nodes                []SoftRouterSocksNode
	mappings             []SoftRouterProxyMapping
	nextID               int64
	cleanupOrphanedCalls int
}

func (s *softRouterProxyRepoStub) GetConfig(context.Context) (*SoftRouterProxyConfig, error) {
	out := s.config
	return &out, nil
}

func (s *softRouterProxyRepoStub) UpdateConfig(_ context.Context, cfg *SoftRouterProxyConfig) (*SoftRouterProxyConfig, error) {
	s.config = *cfg
	out := s.config
	return &out, nil
}

func (s *softRouterProxyRepoStub) ListAgents(context.Context) ([]SoftRouterAgent, error) {
	return append([]SoftRouterAgent(nil), s.agents...), nil
}

func (s *softRouterProxyRepoStub) GetAgentByID(_ context.Context, id int64) (*SoftRouterAgent, error) {
	for i := range s.agents {
		if s.agents[i].ID == id {
			out := s.agents[i]
			return &out, nil
		}
	}
	return nil, ErrSoftRouterAgentNotFound
}

func (s *softRouterProxyRepoStub) GetAgentByToken(_ context.Context, token string) (*SoftRouterAgent, error) {
	for i := range s.agents {
		if s.agents[i].Token == token {
			out := s.agents[i]
			return &out, nil
		}
	}
	return nil, ErrSoftRouterAgentNotFound
}

func (s *softRouterProxyRepoStub) CreateAgent(_ context.Context, agent *SoftRouterAgent) error {
	if s.nextID == 0 {
		s.nextID = 1
	}
	agent.ID = s.nextID
	s.nextID++
	s.agents = append(s.agents, *agent)
	return nil
}

func (s *softRouterProxyRepoStub) UpdateAgent(_ context.Context, agent *SoftRouterAgent) error {
	for i := range s.agents {
		if s.agents[i].ID == agent.ID {
			s.agents[i] = *agent
			return nil
		}
	}
	return ErrSoftRouterAgentNotFound
}

func (s *softRouterProxyRepoStub) DeleteAgent(_ context.Context, id int64) error {
	for i := range s.agents {
		if s.agents[i].ID == id {
			s.agents = append(s.agents[:i], s.agents[i+1:]...)
			return nil
		}
	}
	return ErrSoftRouterAgentNotFound
}

func (s *softRouterProxyRepoStub) TouchAgent(context.Context, int64, string, time.Time) error {
	return nil
}

func (s *softRouterProxyRepoStub) UpsertReportedNodes(context.Context, int64, []SoftRouterSocksNodeReport, time.Time, bool) error {
	return nil
}

func (s *softRouterProxyRepoStub) CleanupOrphanedGeneratedProxies(context.Context) error {
	s.cleanupOrphanedCalls++
	return nil
}

func (s *softRouterProxyRepoStub) ListNodes(context.Context) ([]SoftRouterSocksNode, error) {
	return append([]SoftRouterSocksNode(nil), s.nodes...), nil
}

func (s *softRouterProxyRepoStub) GetNodeByID(_ context.Context, id int64) (*SoftRouterSocksNode, error) {
	for i := range s.nodes {
		if s.nodes[i].ID == id {
			out := s.nodes[i]
			return &out, nil
		}
	}
	return nil, ErrSoftRouterNodeNotFound
}

func (s *softRouterProxyRepoStub) ListMappings(context.Context) ([]SoftRouterProxyMapping, error) {
	return append([]SoftRouterProxyMapping(nil), s.mappings...), nil
}

func (s *softRouterProxyRepoStub) ListEnabledMappings(context.Context) ([]SoftRouterProxyMapping, error) {
	out := make([]SoftRouterProxyMapping, 0, len(s.mappings))
	for i := range s.mappings {
		if s.mappings[i].Enabled && s.nodeAllowsMapping(s.mappings[i]) {
			out = append(out, s.mappings[i])
		}
	}
	return out, nil
}

func (s *softRouterProxyRepoStub) nodeAllowsMapping(mapping SoftRouterProxyMapping) bool {
	if mapping.NodeID == nil {
		return true
	}
	for i := range s.nodes {
		if s.nodes[i].ID == *mapping.NodeID {
			return s.nodes[i].Enabled
		}
	}
	return false
}

func (s *softRouterProxyRepoStub) GetMappingByID(_ context.Context, id int64) (*SoftRouterProxyMapping, error) {
	for i := range s.mappings {
		if s.mappings[i].ID == id {
			out := s.mappings[i]
			return &out, nil
		}
	}
	return nil, ErrSoftRouterMappingNotFound
}

func (s *softRouterProxyRepoStub) CreateMapping(_ context.Context, mapping *SoftRouterProxyMapping) error {
	if s.nextID == 0 {
		s.nextID = 100
	}
	mapping.ID = s.nextID
	s.nextID++
	s.mappings = append(s.mappings, *mapping)
	return nil
}

func (s *softRouterProxyRepoStub) UpdateMapping(_ context.Context, mapping *SoftRouterProxyMapping) error {
	for i := range s.mappings {
		if s.mappings[i].ID == mapping.ID {
			s.mappings[i] = *mapping
			return nil
		}
	}
	return ErrSoftRouterMappingNotFound
}

func (s *softRouterProxyRepoStub) DeleteMapping(_ context.Context, id int64) error {
	for i := range s.mappings {
		if s.mappings[i].ID == id {
			s.mappings = append(s.mappings[:i], s.mappings[i+1:]...)
			return nil
		}
	}
	return ErrSoftRouterMappingNotFound
}

func (s *softRouterProxyRepoStub) UpdateMappingProxy(_ context.Context, id int64, proxyID *int64, status, lastError string) error {
	for i := range s.mappings {
		if s.mappings[i].ID == id {
			s.mappings[i].ProxyID = proxyID
			s.mappings[i].Status = status
			s.mappings[i].LastError = lastError
			return nil
		}
	}
	return ErrSoftRouterMappingNotFound
}

type softRouterProxyProxyRepoStub struct {
	created []Proxy
	updated []Proxy
	deleted []int64
	nextID  int64
}

type softRouterRuntimeStub struct {
	cfg      SoftRouterProxyConfig
	mappings []SoftRouterProxyMapping
}

func (s *softRouterRuntimeStub) Reconcile(_ context.Context, cfg SoftRouterProxyConfig, mappings []SoftRouterProxyMapping) error {
	s.cfg = cfg
	s.mappings = append([]SoftRouterProxyMapping(nil), mappings...)
	return nil
}

func (s *softRouterRuntimeStub) Status() SoftRouterRuntimeStatus {
	return SoftRouterRuntimeStatus{}
}

func (s *softRouterRuntimeStub) Stop() {}

type softRouterFRPInstallerStub struct {
	installCalled   bool
	installedConfig SoftRouterProxyConfig
}

func (s *softRouterFRPInstallerStub) Status(context.Context, SoftRouterProxyConfig) SoftRouterFRPStatus {
	return SoftRouterFRPStatus{InstallSupported: true}
}

func (s *softRouterFRPInstallerStub) Install(_ context.Context, cfg SoftRouterProxyConfig) (*SoftRouterFRPInstallResult, error) {
	s.installCalled = true
	s.installedConfig = cfg
	return &SoftRouterFRPInstallResult{
		Status: SoftRouterFRPStatus{
			Installed:           true,
			InstallSupported:    true,
			ControlPortOpen:     true,
			RawRangeDeployed:    true,
			PublicRangeDeployed: true,
		},
		Message: "installed",
	}, nil
}

func (s *softRouterProxyProxyRepoStub) Create(_ context.Context, proxy *Proxy) error {
	if s.nextID == 0 {
		s.nextID = 1
	}
	proxy.ID = s.nextID
	s.nextID++
	s.created = append(s.created, *proxy)
	return nil
}

func (s *softRouterProxyProxyRepoStub) GetByID(_ context.Context, id int64) (*Proxy, error) {
	for i := range s.created {
		if s.created[i].ID == id {
			out := s.created[i]
			return &out, nil
		}
	}
	for i := range s.updated {
		if s.updated[i].ID == id {
			out := s.updated[i]
			return &out, nil
		}
	}
	return nil, ErrProxyNotFound
}

func (s *softRouterProxyProxyRepoStub) ListByIDs(context.Context, []int64) ([]Proxy, error) {
	return nil, nil
}

func (s *softRouterProxyProxyRepoStub) Update(_ context.Context, proxy *Proxy) error {
	s.updated = append(s.updated, *proxy)
	return nil
}

func (s *softRouterProxyProxyRepoStub) Delete(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *softRouterProxyProxyRepoStub) List(context.Context, pagination.PaginationParams) ([]Proxy, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (s *softRouterProxyProxyRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]Proxy, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (s *softRouterProxyProxyRepoStub) ListWithFiltersAndAccountCount(context.Context, pagination.PaginationParams, string, string, string) ([]ProxyWithAccountCount, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (s *softRouterProxyProxyRepoStub) ListActive(context.Context) ([]Proxy, error) {
	return nil, nil
}

func (s *softRouterProxyProxyRepoStub) ListActiveWithAccountCount(context.Context) ([]ProxyWithAccountCount, error) {
	return nil, nil
}

func (s *softRouterProxyProxyRepoStub) ExistsByHostPortAuth(context.Context, string, int, string, string) (bool, error) {
	return false, nil
}

func (s *softRouterProxyProxyRepoStub) CountAccountsByProxyID(context.Context, int64) (int64, error) {
	return 0, nil
}

func (s *softRouterProxyProxyRepoStub) ListAccountSummariesByProxyID(context.Context, int64) ([]ProxyAccountSummary, error) {
	return nil, nil
}

func (s *softRouterProxyProxyRepoStub) SweepExpiredProxies(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s *softRouterProxyProxyRepoStub) ListAllForFallback(context.Context) ([]Proxy, error) {
	return nil, nil
}

func (s *softRouterProxyProxyRepoStub) CountExpired(context.Context) (int64, error) {
	return 0, nil
}

func (s *softRouterProxyProxyRepoStub) CountExpiringSoon(context.Context, time.Time) (int64, error) {
	return 0, nil
}
