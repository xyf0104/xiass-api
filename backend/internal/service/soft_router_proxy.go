package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SoftRouterAgentStatusOnline  = "online"
	SoftRouterAgentStatusOffline = "offline"

	SoftRouterMappingStatusPending  = "pending"
	SoftRouterMappingStatusRunning  = "running"
	SoftRouterMappingStatusDisabled = "disabled"
	SoftRouterMappingStatusError    = "error"

	softRouterPublicPortRangeEnv = "SOFT_ROUTER_PROXY_PUBLIC_PORT_RANGE"
	softRouterRawPortRangeEnv    = "SOFT_ROUTER_PROXY_RAW_PORT_RANGE"
	softRouterPortCheckTimeout   = 200 * time.Millisecond
)

var (
	ErrSoftRouterAgentNotFound   = infraerrors.NotFound("SOFT_ROUTER_AGENT_NOT_FOUND", "soft router agent not found")
	ErrSoftRouterNodeNotFound    = infraerrors.NotFound("SOFT_ROUTER_NODE_NOT_FOUND", "soft router SOCKS node not found")
	ErrSoftRouterMappingNotFound = infraerrors.NotFound("SOFT_ROUTER_MAPPING_NOT_FOUND", "soft router proxy mapping not found")
)

type SoftRouterProxyConfig struct {
	Enabled           bool      `json:"enabled"`
	PublicHost        string    `json:"public_host"`
	GatewayListenHost string    `json:"gateway_listen_host"`
	UpstreamHost      string    `json:"upstream_host"`
	FRPServerHost     string    `json:"frp_server_host"`
	FRPServerPort     int       `json:"frp_server_port"`
	FRPToken          string    `json:"frp_token"`
	RawPortStart      int       `json:"raw_port_start"`
	RawPortEnd        int       `json:"raw_port_end"`
	PublicPortStart   int       `json:"public_port_start"`
	PublicPortEnd     int       `json:"public_port_end"`
	DefaultUsername   string    `json:"default_username"`
	DefaultPassword   string    `json:"default_password"`
	AgentPollSeconds  int       `json:"agent_poll_seconds"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type SoftRouterFRPStatus struct {
	Installed             bool   `json:"installed"`
	InstallSupported      bool   `json:"install_supported"`
	Reason                string `json:"reason,omitempty"`
	DockerSocketAvailable bool   `json:"docker_socket_available"`
	DockerAvailable       bool   `json:"docker_available"`
	ControlPortOpen       bool   `json:"control_port_open"`
	RawRangeDeployed      bool   `json:"raw_range_deployed"`
	PublicRangeDeployed   bool   `json:"public_range_deployed"`
	NeedsRestart          bool   `json:"needs_restart"`
	ServiceName           string `json:"service_name"`
	ConfigPath            string `json:"config_path"`
	InstallMethod         string `json:"install_method"`
	ControlHost           string `json:"control_host"`
	ControlPort           int    `json:"control_port"`
	RawPortRange          string `json:"raw_port_range"`
	PublicPortRange       string `json:"public_port_range"`
	DeployedRawRange      string `json:"deployed_raw_range,omitempty"`
	DeployedPublicRange   string `json:"deployed_public_range,omitempty"`
}

type SoftRouterFRPInstallInput struct {
	PublicHost        string `json:"public_host"`
	GatewayListenHost string `json:"gateway_listen_host"`
	UpstreamHost      string `json:"upstream_host"`
	FRPServerHost     string `json:"frp_server_host"`
	FRPServerPort     int    `json:"frp_server_port"`
	FRPToken          string `json:"frp_token"`
	RawPortStart      int    `json:"raw_port_start"`
	RawPortEnd        int    `json:"raw_port_end"`
	PublicPortStart   int    `json:"public_port_start"`
	PublicPortEnd     int    `json:"public_port_end"`
	DefaultUsername   string `json:"default_username"`
	DefaultPassword   string `json:"default_password"`
	AgentPollSeconds  int    `json:"agent_poll_seconds"`
}

type SoftRouterFRPInstallResult struct {
	Status          SoftRouterFRPStatus   `json:"status"`
	Config          SoftRouterProxyConfig `json:"config"`
	RestartRequired bool                  `json:"restart_required"`
	Message         string                `json:"message"`
	Log             string                `json:"log,omitempty"`
	Metadata        map[string]string     `json:"metadata,omitempty"`
}

type SoftRouterAgent struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Token       string     `json:"token,omitempty"`
	Hostname    string     `json:"hostname"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	LastError   string     `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type SoftRouterSocksNode struct {
	ID           int64                   `json:"id"`
	AgentID      int64                   `json:"agent_id"`
	OpenWrtID    string                  `json:"openwrt_id"`
	NodeKey      string                  `json:"node_key"`
	Name         string                  `json:"name"`
	OpenWrtPort  int                     `json:"openwrt_port"`
	HTTPPort     int                     `json:"http_port"`
	NodeRef      string                  `json:"node_ref"`
	ListenStatus string                  `json:"listen_status"`
	Enabled      bool                    `json:"enabled"`
	LastSeenAt   *time.Time              `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time               `json:"created_at"`
	UpdatedAt    time.Time               `json:"updated_at"`
	Mapping      *SoftRouterProxyMapping `json:"mapping,omitempty"`
}

type SoftRouterProxyMapping struct {
	ID            int64      `json:"id"`
	AgentID       int64      `json:"agent_id"`
	NodeID        *int64     `json:"node_id,omitempty"`
	Name          string     `json:"name"`
	OpenWrtPort   int        `json:"openwrt_port"`
	RawRemotePort int        `json:"raw_remote_port"`
	PublicPort    int        `json:"public_port"`
	Username      string     `json:"username"`
	Password      string     `json:"password,omitempty"`
	Enabled       bool       `json:"enabled"`
	ProxyID       *int64     `json:"proxy_id,omitempty"`
	Status        string     `json:"status"`
	LastError     string     `json:"last_error"`
	LastTestAt    *time.Time `json:"last_test_at,omitempty"`
	LastExitIP    string     `json:"last_exit_ip"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	PublicURL     string     `json:"public_url,omitempty"`
	XiassURL      string     `json:"xiass_url,omitempty"`
	// NoWindURL is retained for API consumers that were built before the XIASS rename.
	NoWindURL  string `json:"nowind_url,omitempty"`
	EnabledSet bool   `json:"-"`
}

type SoftRouterOverview struct {
	Config   SoftRouterProxyConfig    `json:"config"`
	Agents   []SoftRouterAgent        `json:"agents"`
	Nodes    []SoftRouterSocksNode    `json:"nodes"`
	Mappings []SoftRouterProxyMapping `json:"mappings"`
	Runtime  SoftRouterRuntimeStatus  `json:"runtime"`
	FRP      SoftRouterFRPStatus      `json:"frp_status"`
}

type SoftRouterRuntimeStatus struct {
	Enabled   bool                           `json:"enabled"`
	Listeners map[int64]SoftRouterListenInfo `json:"listeners"`
}

type SoftRouterListenInfo struct {
	Running bool   `json:"running"`
	Error   string `json:"error,omitempty"`
}

type SoftRouterAgentReportInput struct {
	Hostname         string
	SnapshotComplete bool
	Nodes            []SoftRouterSocksNodeReport
}

type SoftRouterSocksNodeReport struct {
	ID           string `json:"id"`
	NodeKey      string `json:"node_key"`
	Name         string `json:"name"`
	OpenWrtPort  int    `json:"openwrt_port"`
	HTTPPort     int    `json:"http_port"`
	NodeRef      string `json:"node_ref"`
	ListenStatus string `json:"listen_status"`
	Enabled      bool   `json:"enabled"`
}

type SoftRouterAgentDesiredConfig struct {
	Enabled       bool                           `json:"enabled"`
	PanelURL      string                         `json:"panel_url,omitempty"`
	FRPServerHost string                         `json:"frp_server_host"`
	FRPServerPort int                            `json:"frp_server_port"`
	FRPToken      string                         `json:"frp_token"`
	PollSeconds   int                            `json:"poll_seconds"`
	Mappings      []SoftRouterAgentMappingConfig `json:"mappings"`
}

type SoftRouterAgentMappingConfig struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	OpenWrtPort   int    `json:"openwrt_port"`
	RawRemotePort int    `json:"raw_remote_port"`
}

type SoftRouterRepository interface {
	GetConfig(ctx context.Context) (*SoftRouterProxyConfig, error)
	UpdateConfig(ctx context.Context, cfg *SoftRouterProxyConfig) (*SoftRouterProxyConfig, error)

	ListAgents(ctx context.Context) ([]SoftRouterAgent, error)
	GetAgentByID(ctx context.Context, id int64) (*SoftRouterAgent, error)
	GetAgentByToken(ctx context.Context, token string) (*SoftRouterAgent, error)
	CreateAgent(ctx context.Context, agent *SoftRouterAgent) error
	UpdateAgent(ctx context.Context, agent *SoftRouterAgent) error
	DeleteAgent(ctx context.Context, id int64) error
	TouchAgent(ctx context.Context, id int64, hostname string, seenAt time.Time) error

	UpsertReportedNodes(ctx context.Context, agentID int64, nodes []SoftRouterSocksNodeReport, seenAt time.Time, snapshotComplete bool) error
	CleanupOrphanedGeneratedProxies(ctx context.Context) error
	ListNodes(ctx context.Context) ([]SoftRouterSocksNode, error)
	GetNodeByID(ctx context.Context, id int64) (*SoftRouterSocksNode, error)

	ListMappings(ctx context.Context) ([]SoftRouterProxyMapping, error)
	ListEnabledMappings(ctx context.Context) ([]SoftRouterProxyMapping, error)
	GetMappingByID(ctx context.Context, id int64) (*SoftRouterProxyMapping, error)
	CreateMapping(ctx context.Context, mapping *SoftRouterProxyMapping) error
	UpdateMapping(ctx context.Context, mapping *SoftRouterProxyMapping) error
	DeleteMapping(ctx context.Context, id int64) error
	UpdateMappingProxy(ctx context.Context, id int64, proxyID *int64, status, lastError string) error
}

type SoftRouterProxyService struct {
	repo         SoftRouterRepository
	proxyRepo    ProxyRepository
	runtime      SoftRouterRuntime
	frpInstaller SoftRouterFRPInstaller
}

type SoftRouterRuntime interface {
	Reconcile(ctx context.Context, cfg SoftRouterProxyConfig, mappings []SoftRouterProxyMapping) error
	Status() SoftRouterRuntimeStatus
	Stop()
}

type SoftRouterFRPInstaller interface {
	Status(ctx context.Context, cfg SoftRouterProxyConfig) SoftRouterFRPStatus
	Install(ctx context.Context, cfg SoftRouterProxyConfig) (*SoftRouterFRPInstallResult, error)
}

func NewSoftRouterProxyService(repo SoftRouterRepository, proxyRepo ProxyRepository, runtime SoftRouterRuntime) *SoftRouterProxyService {
	svc := &SoftRouterProxyService{repo: repo, proxyRepo: proxyRepo, runtime: runtime, frpInstaller: NewDockerSoftRouterFRPInstaller()}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = svc.Reconcile(ctx)
	}()
	return svc
}

func (s *SoftRouterProxyService) GetOverview(ctx context.Context) (*SoftRouterOverview, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := s.repo.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	mappings, err := s.repo.ListMappings(ctx)
	if err != nil {
		return nil, err
	}
	attachSoftRouterURLs(*cfg, mappings)
	attachMappingsToNodes(nodes, mappings)
	status := SoftRouterRuntimeStatus{}
	if s.runtime != nil {
		status = s.runtime.Status()
	}
	frpStatus := SoftRouterFRPStatus{}
	if s.frpInstaller != nil {
		frpStatus = s.frpInstaller.Status(ctx, *cfg)
	}
	return &SoftRouterOverview{
		Config:   *cfg,
		Agents:   agents,
		Nodes:    nodes,
		Mappings: mappings,
		Runtime:  status,
		FRP:      frpStatus,
	}, nil
}

func (s *SoftRouterProxyService) GetConfig(ctx context.Context) (*SoftRouterProxyConfig, error) {
	return s.repo.GetConfig(ctx)
}

func (s *SoftRouterProxyService) UpdateConfig(ctx context.Context, cfg *SoftRouterProxyConfig) (*SoftRouterProxyConfig, error) {
	normalized, err := normalizeSoftRouterConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.validateConfigPorts(ctx, *normalized); err != nil {
		return nil, err
	}
	out, err := s.repo.UpdateConfig(ctx, normalized)
	if err != nil {
		return nil, err
	}
	return out, s.Reconcile(ctx)
}

func (s *SoftRouterProxyService) GetFRPStatus(ctx context.Context) (*SoftRouterFRPStatus, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	status := SoftRouterFRPStatus{}
	if s.frpInstaller != nil {
		status = s.frpInstaller.Status(ctx, *cfg)
	}
	return &status, nil
}

func (s *SoftRouterProxyService) InstallFRP(ctx context.Context, input SoftRouterFRPInstallInput) (*SoftRouterFRPInstallResult, error) {
	if s.frpInstaller == nil {
		return nil, infraerrors.ServiceUnavailable("SOFT_ROUTER_FRP_INSTALL_UNSUPPORTED", "当前部署不支持从面板安装 FRP")
	}
	cfg := &SoftRouterProxyConfig{
		Enabled:           true,
		PublicHost:        input.PublicHost,
		GatewayListenHost: input.GatewayListenHost,
		UpstreamHost:      input.UpstreamHost,
		FRPServerHost:     input.FRPServerHost,
		FRPServerPort:     input.FRPServerPort,
		FRPToken:          input.FRPToken,
		RawPortStart:      input.RawPortStart,
		RawPortEnd:        input.RawPortEnd,
		PublicPortStart:   input.PublicPortStart,
		PublicPortEnd:     input.PublicPortEnd,
		DefaultUsername:   input.DefaultUsername,
		DefaultPassword:   input.DefaultPassword,
		AgentPollSeconds:  input.AgentPollSeconds,
	}
	normalized, err := normalizeSoftRouterConfig(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(normalized.FRPToken) == "" {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_FRP_TOKEN_REQUIRED", "FRP Token 不能为空")
	}
	if strings.TrimSpace(normalized.PublicHost) == "" {
		normalized.PublicHost = strings.TrimSpace(normalized.FRPServerHost)
	}
	if strings.TrimSpace(normalized.FRPServerHost) == "" {
		normalized.FRPServerHost = strings.TrimSpace(normalized.PublicHost)
	}
	if strings.TrimSpace(normalized.FRPServerHost) == "" {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_FRP_HOST_REQUIRED", "FRP 服务地址或公网域名/IP 不能为空")
	}
	if strings.TrimSpace(normalized.UpstreamHost) == "" || normalized.UpstreamHost == "127.0.0.1" {
		normalized.UpstreamHost = "host.docker.internal"
	}
	if strings.TrimSpace(normalized.GatewayListenHost) == "" {
		normalized.GatewayListenHost = "0.0.0.0"
	}
	if err := s.ensureInstallDoesNotCollide(ctx, *normalized); err != nil {
		return nil, err
	}
	result, err := s.frpInstaller.Install(ctx, *normalized)
	if err != nil {
		return nil, err
	}
	out, err := s.repo.UpdateConfig(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if err := s.Reconcile(ctx); err != nil {
		return nil, err
	}
	result.Config = *out
	result.RestartRequired = true
	if result.Message == "" {
		result.Message = "FRP 已安装，已保存代理节点配置。请重启或重建当前 XIASS API 容器，让新的公网 SOCKS 端口映射生效。"
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	result.Metadata["restart_hint"] = "docker compose up -d --force-recreate xiass-api"
	return result, nil
}

func (s *SoftRouterProxyService) CreateAgent(ctx context.Context, input *SoftRouterAgent) (*SoftRouterAgent, error) {
	if input == nil {
		input = &SoftRouterAgent{}
	}
	agent := &SoftRouterAgent{
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Status:      SoftRouterAgentStatusOffline,
	}
	if agent.Name == "" {
		agent.Name = "OpenWrt"
	}
	token, err := randomURLSecret(32)
	if err != nil {
		return nil, err
	}
	agent.Token = token
	if err := s.repo.CreateAgent(ctx, agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func (s *SoftRouterProxyService) UpdateAgent(ctx context.Context, id int64, input *SoftRouterAgent) (*SoftRouterAgent, error) {
	agent, err := s.repo.GetAgentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if input != nil {
		if strings.TrimSpace(input.Name) != "" {
			agent.Name = strings.TrimSpace(input.Name)
		}
		agent.Description = strings.TrimSpace(input.Description)
	}
	if err := s.repo.UpdateAgent(ctx, agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func (s *SoftRouterProxyService) DeleteAgent(ctx context.Context, id int64) error {
	mappings, err := s.repo.ListMappings(ctx)
	if err != nil {
		return err
	}
	for i := range mappings {
		if mappings[i].AgentID == id {
			if err := s.DeleteMapping(ctx, mappings[i].ID); err != nil {
				return err
			}
		}
	}
	if err := s.repo.DeleteAgent(ctx, id); err != nil {
		return err
	}
	return s.Reconcile(ctx)
}

func (s *SoftRouterProxyService) RotateAgentToken(ctx context.Context, id int64) (*SoftRouterAgent, error) {
	agent, err := s.repo.GetAgentByID(ctx, id)
	if err != nil {
		return nil, err
	}
	token, err := randomURLSecret(32)
	if err != nil {
		return nil, err
	}
	agent.Token = token
	if err := s.repo.UpdateAgent(ctx, agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func (s *SoftRouterProxyService) ReportAgent(ctx context.Context, token string, input SoftRouterAgentReportInput) error {
	agent, err := s.repo.GetAgentByToken(ctx, strings.TrimSpace(token))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.repo.TouchAgent(ctx, agent.ID, strings.TrimSpace(input.Hostname), now); err != nil {
		return err
	}
	if err := s.repo.UpsertReportedNodes(ctx, agent.ID, input.Nodes, now, input.SnapshotComplete); err != nil {
		return err
	}
	return s.Reconcile(ctx)
}

func (s *SoftRouterProxyService) GetDesiredConfig(ctx context.Context, token string) (*SoftRouterAgentDesiredConfig, error) {
	agent, err := s.repo.GetAgentByToken(ctx, strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.repo.ListMappings(ctx)
	if err != nil {
		return nil, err
	}
	mappings := make([]SoftRouterAgentMappingConfig, 0, len(all))
	for i := range all {
		m := all[i]
		if m.AgentID != agent.ID || !m.Enabled {
			continue
		}
		mappings = append(mappings, SoftRouterAgentMappingConfig{
			ID:            m.ID,
			Name:          softRouterFRPProxyName(m),
			Enabled:       m.Enabled,
			OpenWrtPort:   m.OpenWrtPort,
			RawRemotePort: m.RawRemotePort,
		})
	}
	return &SoftRouterAgentDesiredConfig{
		Enabled:       cfg.Enabled,
		FRPServerHost: cfg.FRPServerHost,
		FRPServerPort: cfg.FRPServerPort,
		FRPToken:      cfg.FRPToken,
		PollSeconds:   cfg.AgentPollSeconds,
		Mappings:      mappings,
	}, nil
}

func (s *SoftRouterProxyService) CreateMapping(ctx context.Context, input *SoftRouterProxyMapping) (*SoftRouterProxyMapping, error) {
	if input == nil {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_MAPPING_INVALID", "mapping is required")
	}
	if !input.EnabledSet {
		input.Enabled = true
		input.EnabledSet = true
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	mapping, err := s.normalizeMappingInput(ctx, *cfg, input, true)
	if err != nil {
		return nil, err
	}
	if err := s.validateMappingPorts(ctx, *cfg, mapping, nil); err != nil {
		return nil, err
	}
	if err := s.repo.CreateMapping(ctx, mapping); err != nil {
		return nil, err
	}
	if err := s.ensureProxyForMapping(ctx, *cfg, mapping); err != nil {
		return nil, err
	}
	return mapping, s.Reconcile(ctx)
}

func (s *SoftRouterProxyService) UpdateMapping(ctx context.Context, id int64, input *SoftRouterProxyMapping) (*SoftRouterProxyMapping, error) {
	current, err := s.repo.GetMappingByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if input == nil {
		input = &SoftRouterProxyMapping{}
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	merged := *current
	if input.NodeID != nil {
		merged.NodeID = input.NodeID
	}
	if strings.TrimSpace(input.Name) != "" {
		merged.Name = strings.TrimSpace(input.Name)
	}
	if input.OpenWrtPort > 0 {
		merged.OpenWrtPort = input.OpenWrtPort
	}
	if input.RawRemotePort > 0 {
		merged.RawRemotePort = input.RawRemotePort
	}
	if input.PublicPort > 0 {
		merged.PublicPort = input.PublicPort
	}
	if strings.TrimSpace(input.Username) != "" {
		merged.Username = strings.TrimSpace(input.Username)
	}
	if strings.TrimSpace(input.Password) != "" {
		merged.Password = strings.TrimSpace(input.Password)
	}
	if input.EnabledSet {
		merged.Enabled = input.Enabled
	}
	normalized, err := s.normalizeMappingInput(ctx, *cfg, &merged, false)
	if err != nil {
		return nil, err
	}
	normalized.ID = id
	normalized.ProxyID = current.ProxyID
	if err := s.validateMappingPorts(ctx, *cfg, normalized, current); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateMapping(ctx, normalized); err != nil {
		return nil, err
	}
	if err := s.ensureProxyForMapping(ctx, *cfg, normalized); err != nil {
		return nil, err
	}
	return normalized, s.Reconcile(ctx)
}

func (s *SoftRouterProxyService) DeleteMapping(ctx context.Context, id int64) error {
	mapping, err := s.repo.GetMappingByID(ctx, id)
	if err != nil {
		return err
	}
	if mapping.ProxyID != nil && *mapping.ProxyID > 0 {
		proxy, err := s.proxyRepo.GetByID(ctx, *mapping.ProxyID)
		if err != nil && !errors.Is(err, ErrProxyNotFound) {
			return err
		}
		if err == nil && proxy != nil {
			accountCount, countErr := s.proxyRepo.CountAccountsByProxyID(ctx, proxy.ID)
			if countErr != nil {
				return countErr
			}
			if accountCount == 0 {
				if err := s.proxyRepo.Delete(ctx, proxy.ID); err != nil {
					return err
				}
			} else {
				proxy.Status = StatusDisabled
				if err := s.proxyRepo.Update(ctx, proxy); err != nil {
					return err
				}
			}
		}
	}
	if err := s.repo.DeleteMapping(ctx, id); err != nil {
		return err
	}
	return s.Reconcile(ctx)
}

func (s *SoftRouterProxyService) Reconcile(ctx context.Context) error {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return err
	}
	if err := s.repo.CleanupOrphanedGeneratedProxies(ctx); err != nil {
		return err
	}
	mappings, err := s.repo.ListEnabledMappings(ctx)
	if err != nil {
		return err
	}
	for i := range mappings {
		if err := s.ensureProxyForMapping(ctx, *cfg, &mappings[i]); err != nil {
			return err
		}
	}
	if s.runtime == nil {
		return nil
	}
	return s.runtime.Reconcile(ctx, *cfg, mappings)
}

func (s *SoftRouterProxyService) normalizeMappingInput(ctx context.Context, cfg SoftRouterProxyConfig, input *SoftRouterProxyMapping, assignPorts bool) (*SoftRouterProxyMapping, error) {
	agentID := input.AgentID
	nodeID := input.NodeID
	name := strings.TrimSpace(input.Name)
	openwrtPort := input.OpenWrtPort
	if nodeID != nil && *nodeID > 0 {
		node, err := s.repo.GetNodeByID(ctx, *nodeID)
		if err != nil {
			return nil, err
		}
		agentID = node.AgentID
		if name == "" {
			name = node.Name
		}
		if openwrtPort == 0 {
			openwrtPort = node.OpenWrtPort
		}
	}
	if agentID <= 0 {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_AGENT_REQUIRED", "agent_id is required")
	}
	if openwrtPort <= 0 || openwrtPort > 65535 {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_OPENWRT_PORT_INVALID", "openwrt_port must be between 1 and 65535")
	}
	if name == "" {
		name = "OpenWrt SOCKS " + strconv.Itoa(openwrtPort)
	}
	rawPort := input.RawRemotePort
	publicPort := input.PublicPort
	if assignPorts {
		usedRaw, usedPublic, err := s.usedMappingPorts(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		if rawPort == 0 {
			rawPort = firstAvailableRawPort(cfg, usedRaw)
		}
		if publicPort == 0 {
			publicPort = firstAvailablePublicPort(cfg, usedPublic)
		}
	}
	if rawPort == 0 {
		return nil, infraerrors.Conflict("SOFT_ROUTER_RAW_PORT_UNAVAILABLE", "Raw FRP 端口范围内没有可用端口")
	}
	if publicPort == 0 {
		return nil, infraerrors.Conflict("SOFT_ROUTER_PUBLIC_PORT_UNAVAILABLE", "公网 SOCKS 端口范围内没有可用端口")
	}
	if !validPort(rawPort) || !portInRange(rawPort, cfg.RawPortStart, cfg.RawPortEnd) {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_RAW_PORT_INVALID", "Raw FRP 端口不在配置范围内")
	}
	if !validPort(publicPort) || !portInRange(publicPort, cfg.PublicPortStart, cfg.PublicPortEnd) {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_PUBLIC_PORT_INVALID", "公网 SOCKS 端口不在配置范围内")
	}
	username := strings.TrimSpace(input.Username)
	password := strings.TrimSpace(input.Password)
	if username == "" {
		username = strings.TrimSpace(cfg.DefaultUsername)
	}
	if password == "" {
		password = strings.TrimSpace(cfg.DefaultPassword)
	}
	if username == "" || password == "" {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_AUTH_REQUIRED", "username and password are required for public SOCKS access")
	}
	status := input.Status
	if status == "" {
		status = SoftRouterMappingStatusPending
	}
	if !input.Enabled {
		status = SoftRouterMappingStatusDisabled
	}
	return &SoftRouterProxyMapping{
		ID:            input.ID,
		AgentID:       agentID,
		NodeID:        nodeID,
		Name:          name,
		OpenWrtPort:   openwrtPort,
		RawRemotePort: rawPort,
		PublicPort:    publicPort,
		Username:      username,
		Password:      password,
		Enabled:       input.Enabled,
		ProxyID:       input.ProxyID,
		Status:        status,
		LastError:     strings.TrimSpace(input.LastError),
	}, nil
}

func (s *SoftRouterProxyService) usedMappingPorts(ctx context.Context, exceptID int64) (map[int]bool, map[int]bool, error) {
	mappings, err := s.repo.ListMappings(ctx)
	if err != nil {
		return nil, nil, err
	}
	raw := map[int]bool{}
	public := map[int]bool{}
	for i := range mappings {
		if mappings[i].ID == exceptID {
			continue
		}
		raw[mappings[i].RawRemotePort] = true
		public[mappings[i].PublicPort] = true
	}
	return raw, public, nil
}

func (s *SoftRouterProxyService) ensureProxyForMapping(ctx context.Context, cfg SoftRouterProxyConfig, mapping *SoftRouterProxyMapping) error {
	if mapping == nil {
		return nil
	}
	host := strings.TrimSpace(cfg.PublicHost)
	if host == "" {
		host = strings.TrimSpace(cfg.UpstreamHost)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	status := StatusActive
	if !mapping.Enabled {
		status = StatusDisabled
	}
	proxy := &Proxy{
		Name:           softRouterProxyName(mapping.Name),
		Protocol:       "socks5",
		Host:           host,
		Port:           mapping.PublicPort,
		Username:       mapping.Username,
		Password:       mapping.Password,
		Status:         status,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: 7,
	}
	if mapping.ProxyID != nil && *mapping.ProxyID > 0 {
		existing, err := s.proxyRepo.GetByID(ctx, *mapping.ProxyID)
		if err == nil && existing != nil {
			existing.Name = proxy.Name
			existing.Protocol = proxy.Protocol
			existing.Host = proxy.Host
			existing.Port = proxy.Port
			existing.Username = proxy.Username
			existing.Password = proxy.Password
			existing.Status = proxy.Status
			existing.FallbackMode = FallbackModeNone
			existing.BackupProxyID = nil
			existing.ExpiryWarnDays = 7
			if err := s.proxyRepo.Update(ctx, existing); err != nil {
				_ = s.repo.UpdateMappingProxy(ctx, mapping.ID, mapping.ProxyID, SoftRouterMappingStatusError, err.Error())
				return err
			}
			mapping.Status = mappingStatusForEnabled(mapping.Enabled)
			return s.repo.UpdateMappingProxy(ctx, mapping.ID, mapping.ProxyID, mapping.Status, "")
		}
	}
	if err := s.proxyRepo.Create(ctx, proxy); err != nil {
		_ = s.repo.UpdateMappingProxy(ctx, mapping.ID, nil, SoftRouterMappingStatusError, err.Error())
		return err
	}
	mapping.ProxyID = &proxy.ID
	mapping.Status = mappingStatusForEnabled(mapping.Enabled)
	return s.repo.UpdateMappingProxy(ctx, mapping.ID, mapping.ProxyID, mapping.Status, "")
}

func (s *SoftRouterProxyService) validateConfigPorts(ctx context.Context, cfg SoftRouterProxyConfig) error {
	if err := validateConfiguredRangeAllowed(cfg.RawPortStart, cfg.RawPortEnd, softRouterRawPortRangeEnv, "Raw FRP"); err != nil {
		return err
	}
	if err := validateConfiguredRangeAllowed(cfg.PublicPortStart, cfg.PublicPortEnd, softRouterPublicPortRangeEnv, "公网 SOCKS"); err != nil {
		return err
	}

	mappings, err := s.repo.ListMappings(ctx)
	if err != nil {
		return err
	}
	usedRaw := map[int]bool{}
	usedPublic := map[int]bool{}
	for i := range mappings {
		m := mappings[i]
		usedRaw[m.RawRemotePort] = true
		usedPublic[m.PublicPort] = true
		if !portInRange(m.RawRemotePort, cfg.RawPortStart, cfg.RawPortEnd) {
			return infraerrors.BadRequest(
				"SOFT_ROUTER_RAW_RANGE_HAS_MAPPING_OUTSIDE",
				fmt.Sprintf("已有映射 %q 使用 Raw FRP 端口 %d，不在新的端口范围内", m.Name, m.RawRemotePort),
			)
		}
		if !portInRange(m.PublicPort, cfg.PublicPortStart, cfg.PublicPortEnd) {
			return infraerrors.BadRequest(
				"SOFT_ROUTER_PUBLIC_RANGE_HAS_MAPPING_OUTSIDE",
				fmt.Sprintf("已有映射 %q 使用公网 SOCKS 端口 %d，不在新的端口范围内", m.Name, m.PublicPort),
			)
		}
	}

	for port := cfg.RawPortStart; port <= cfg.RawPortEnd; port++ {
		if usedRaw[port] {
			continue
		}
		if tcpPortOpen(cfg.UpstreamHost, port) {
			return infraerrors.Conflict(
				"SOFT_ROUTER_RAW_PORT_OCCUPIED",
				fmt.Sprintf("Raw FRP 端口 %d 已被占用，请换一个端口范围", port),
			)
		}
	}
	for port := cfg.PublicPortStart; port <= cfg.PublicPortEnd; port++ {
		if usedPublic[port] {
			continue
		}
		if err := canListenOnTCPPort(cfg.GatewayListenHost, port); err != nil {
			return infraerrors.Conflict(
				"SOFT_ROUTER_PUBLIC_PORT_OCCUPIED",
				fmt.Sprintf("公网 SOCKS 端口 %d 已被占用或无法监听，请换一个端口范围", port),
			)
		}
	}
	return nil
}

func (s *SoftRouterProxyService) ensureInstallDoesNotCollide(ctx context.Context, cfg SoftRouterProxyConfig) error {
	mappings, err := s.repo.ListMappings(ctx)
	if err != nil {
		return err
	}
	usedRaw := map[int]bool{}
	for i := range mappings {
		usedRaw[mappings[i].RawRemotePort] = true
	}
	for port := cfg.RawPortStart; port <= cfg.RawPortEnd; port++ {
		if usedRaw[port] {
			continue
		}
		if tcpPortOpen(cfg.UpstreamHost, port) {
			return infraerrors.Conflict(
				"SOFT_ROUTER_RAW_PORT_OCCUPIED",
				fmt.Sprintf("Raw FRP 端口 %d 已被占用，请换一个端口范围", port),
			)
		}
	}
	return nil
}

func (s *SoftRouterProxyService) validateMappingPorts(ctx context.Context, cfg SoftRouterProxyConfig, mapping *SoftRouterProxyMapping, current *SoftRouterProxyMapping) error {
	if mapping == nil {
		return nil
	}
	usedRaw, usedPublic, err := s.usedMappingPorts(ctx, mapping.ID)
	if err != nil {
		return err
	}
	if usedRaw[mapping.RawRemotePort] {
		return infraerrors.Conflict(
			"SOFT_ROUTER_RAW_PORT_IN_USE",
			fmt.Sprintf("Raw FRP 端口 %d 已被其他映射使用", mapping.RawRemotePort),
		)
	}
	if usedPublic[mapping.PublicPort] {
		return infraerrors.Conflict(
			"SOFT_ROUTER_PUBLIC_PORT_IN_USE",
			fmt.Sprintf("公网 SOCKS 端口 %d 已被其他映射使用", mapping.PublicPort),
		)
	}
	if err := validatePortAllowed(mapping.RawRemotePort, softRouterRawPortRangeEnv, "Raw FRP"); err != nil {
		return err
	}
	if err := validatePortAllowed(mapping.PublicPort, softRouterPublicPortRangeEnv, "公网 SOCKS"); err != nil {
		return err
	}
	if current == nil || mapping.RawRemotePort != current.RawRemotePort {
		if tcpPortOpen(cfg.UpstreamHost, mapping.RawRemotePort) {
			return infraerrors.Conflict(
				"SOFT_ROUTER_RAW_PORT_OCCUPIED",
				fmt.Sprintf("Raw FRP 端口 %d 已被占用，请换一个端口", mapping.RawRemotePort),
			)
		}
	}
	if current == nil || mapping.PublicPort != current.PublicPort {
		if err := canListenOnTCPPort(cfg.GatewayListenHost, mapping.PublicPort); err != nil {
			return infraerrors.Conflict(
				"SOFT_ROUTER_PUBLIC_PORT_OCCUPIED",
				fmt.Sprintf("公网 SOCKS 端口 %d 已被占用或无法监听，请换一个端口", mapping.PublicPort),
			)
		}
	}
	return nil
}

func normalizeSoftRouterConfig(cfg *SoftRouterProxyConfig) (*SoftRouterProxyConfig, error) {
	if cfg == nil {
		cfg = &SoftRouterProxyConfig{}
	}
	out := *cfg
	out.PublicHost = strings.TrimSpace(out.PublicHost)
	out.GatewayListenHost = strings.TrimSpace(out.GatewayListenHost)
	if out.GatewayListenHost == "" {
		out.GatewayListenHost = "0.0.0.0"
	}
	out.UpstreamHost = strings.TrimSpace(out.UpstreamHost)
	if out.UpstreamHost == "" {
		out.UpstreamHost = "127.0.0.1"
	}
	out.FRPServerHost = strings.TrimSpace(out.FRPServerHost)
	if out.FRPServerPort == 0 {
		out.FRPServerPort = 7010
	}
	if !validPort(out.FRPServerPort) {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_FRP_PORT_INVALID", "frp_server_port must be between 1 and 65535")
	}
	if out.RawPortStart == 0 {
		out.RawPortStart = 12083
	}
	if out.RawPortEnd == 0 {
		out.RawPortEnd = 12150
	}
	if out.PublicPortStart == 0 {
		out.PublicPortStart = 1101
	}
	if out.PublicPortEnd == 0 {
		out.PublicPortEnd = 1120
	}
	if !validPortRange(out.RawPortStart, out.RawPortEnd) {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_RAW_RANGE_INVALID", "Raw FRP 端口范围无效")
	}
	if !validPortRange(out.PublicPortStart, out.PublicPortEnd) {
		return nil, infraerrors.BadRequest("SOFT_ROUTER_PUBLIC_RANGE_INVALID", "公网 SOCKS 端口范围无效")
	}
	if out.AgentPollSeconds <= 0 {
		out.AgentPollSeconds = 20
	}
	if out.AgentPollSeconds < 5 {
		out.AgentPollSeconds = 5
	}
	out.DefaultUsername = strings.TrimSpace(out.DefaultUsername)
	out.DefaultPassword = strings.TrimSpace(out.DefaultPassword)
	out.FRPToken = strings.TrimSpace(out.FRPToken)
	return &out, nil
}

func attachMappingsToNodes(nodes []SoftRouterSocksNode, mappings []SoftRouterProxyMapping) {
	byNodeID := map[int64]SoftRouterProxyMapping{}
	for i := range mappings {
		if mappings[i].NodeID != nil {
			byNodeID[*mappings[i].NodeID] = mappings[i]
		}
	}
	for i := range nodes {
		if m, ok := byNodeID[nodes[i].ID]; ok {
			copy := m
			nodes[i].Mapping = &copy
		}
	}
}

func attachSoftRouterURLs(cfg SoftRouterProxyConfig, mappings []SoftRouterProxyMapping) {
	for i := range mappings {
		mappings[i].PublicURL = softRouterURL("socks5", cfg.PublicHost, mappings[i].PublicPort, mappings[i].Username, mappings[i].Password)
		mappings[i].XiassURL = softRouterURL("socks5", cfg.UpstreamHost, mappings[i].PublicPort, mappings[i].Username, mappings[i].Password)
		mappings[i].NoWindURL = mappings[i].XiassURL
	}
}

func softRouterURL(scheme, host string, port int, username, password string) string {
	host = strings.TrimSpace(host)
	if host == "" || port == 0 {
		return ""
	}
	if username != "" {
		auth := url.User(username)
		if password != "" {
			auth = url.UserPassword(username, password)
		}
		return fmt.Sprintf("%s://%s@%s", scheme, auth.String(), net.JoinHostPort(host, strconv.Itoa(port)))
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(port)))
}

func softRouterProxyName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "OpenWrt SOCKS"
	}
	return "OpenWrt - " + name
}

func softRouterFRPProxyName(mapping SoftRouterProxyMapping) string {
	base := strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, mapping.Name))
	base = strings.Trim(base, "-")
	if base == "" {
		base = "socks"
	}
	return fmt.Sprintf("nowind-%d-%s-%d", mapping.ID, base, mapping.RawRemotePort)
}

func mappingStatusForEnabled(enabled bool) string {
	if enabled {
		return SoftRouterMappingStatusRunning
	}
	return SoftRouterMappingStatusDisabled
}

func randomURLSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validPort(port int) bool {
	return port > 0 && port <= 65535
}

func validPortRange(start, end int) bool {
	return validPort(start) && validPort(end) && start <= end
}

func portInRange(port, start, end int) bool {
	return port >= start && port <= end
}

func firstAvailableRawPort(cfg SoftRouterProxyConfig, used map[int]bool) int {
	for port := cfg.RawPortStart; port <= cfg.RawPortEnd; port++ {
		if used[port] || !portAllowedByDeployment(port, softRouterRawPortRangeEnv) || tcpPortOpen(cfg.UpstreamHost, port) {
			continue
		}
		return port
	}
	return 0
}

func firstAvailablePublicPort(cfg SoftRouterProxyConfig, used map[int]bool) int {
	for port := cfg.PublicPortStart; port <= cfg.PublicPortEnd; port++ {
		if used[port] || !portAllowedByDeployment(port, softRouterPublicPortRangeEnv) {
			continue
		}
		if err := canListenOnTCPPort(cfg.GatewayListenHost, port); err != nil {
			continue
		}
		return port
	}
	return 0
}

type softRouterPortRange struct {
	start int
	end   int
}

func validateConfiguredRangeAllowed(start, end int, envName, label string) error {
	ranges, constrained, err := deploymentPortRanges(envName)
	if err != nil {
		return err
	}
	if !constrained || rangeCovered(start, end, ranges) {
		return nil
	}
	return infraerrors.BadRequest(
		"SOFT_ROUTER_PORT_RANGE_NOT_DEPLOYED",
		fmt.Sprintf("%s 端口范围 %d-%d 不在部署允许范围 %s=%s 内，请先放行或映射这些端口", label, start, end, envName, os.Getenv(envName)),
	)
}

func validatePortAllowed(port int, envName, label string) error {
	ranges, constrained, err := deploymentPortRanges(envName)
	if err != nil {
		return err
	}
	if !constrained || portInDeploymentRanges(port, ranges) {
		return nil
	}
	return infraerrors.BadRequest(
		"SOFT_ROUTER_PORT_NOT_DEPLOYED",
		fmt.Sprintf("%s 端口 %d 不在部署允许范围 %s=%s 内，请先放行或映射这个端口", label, port, envName, os.Getenv(envName)),
	)
}

func portAllowedByDeployment(port int, envName string) bool {
	ranges, constrained, err := deploymentPortRanges(envName)
	if err != nil || !constrained {
		return true
	}
	return portInDeploymentRanges(port, ranges)
}

func portInDeploymentRanges(port int, ranges []softRouterPortRange) bool {
	for i := range ranges {
		if port >= ranges[i].start && port <= ranges[i].end {
			return true
		}
	}
	return false
}

func rangeCovered(start, end int, ranges []softRouterPortRange) bool {
	for port := start; port <= end; port++ {
		covered := false
		for i := range ranges {
			if port >= ranges[i].start && port <= ranges[i].end {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func deploymentPortRanges(envName string) ([]softRouterPortRange, bool, error) {
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return nil, false, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	})
	ranges := make([]softRouterPortRange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		startText, endText, ok := strings.Cut(part, "-")
		if !ok {
			startText = part
			endText = part
		}
		start, err := strconv.Atoi(strings.TrimSpace(startText))
		if err != nil {
			return nil, true, infraerrors.BadRequest("SOFT_ROUTER_DEPLOYED_RANGE_INVALID", fmt.Sprintf("%s 配置无效: %s", envName, raw))
		}
		end, err := strconv.Atoi(strings.TrimSpace(endText))
		if err != nil {
			return nil, true, infraerrors.BadRequest("SOFT_ROUTER_DEPLOYED_RANGE_INVALID", fmt.Sprintf("%s 配置无效: %s", envName, raw))
		}
		if !validPortRange(start, end) {
			return nil, true, infraerrors.BadRequest("SOFT_ROUTER_DEPLOYED_RANGE_INVALID", fmt.Sprintf("%s 配置无效: %s", envName, raw))
		}
		ranges = append(ranges, softRouterPortRange{start: start, end: end})
	}
	return ranges, len(ranges) > 0, nil
}

func tcpPortOpen(host string, port int) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), softRouterPortCheckTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func canListenOnTCPPort(host string, port int) error {
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = ""
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return ln.Close()
}
