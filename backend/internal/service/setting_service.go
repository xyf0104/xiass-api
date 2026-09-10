package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"golang.org/x/sync/singleflight"
)

const (
	GrokDefaultBaseURLModeAPI     = "api"
	GrokDefaultBaseURLModeUSEast1 = "us-east-1"
	GrokDefaultBaseURLModeUSWest2 = "us-west-2"
	GrokDefaultBaseURLModeEUWest1 = "eu-west-1"
	GrokDefaultBaseURLModeCLI     = "cli"
)

func normalizeGrokDefaultBaseURLMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case GrokDefaultBaseURLModeAPI:
		return GrokDefaultBaseURLModeAPI
	case GrokDefaultBaseURLModeUSEast1:
		return GrokDefaultBaseURLModeUSEast1
	case GrokDefaultBaseURLModeUSWest2:
		return GrokDefaultBaseURLModeUSWest2
	case GrokDefaultBaseURLModeEUWest1:
		return GrokDefaultBaseURLModeEUWest1
	case GrokDefaultBaseURLModeCLI:
		return GrokDefaultBaseURLModeCLI
	default:
		return GrokDefaultBaseURLModeCLI
	}
}

func GrokBaseURLForMode(mode string) string {
	switch normalizeGrokDefaultBaseURLMode(mode) {
	case GrokDefaultBaseURLModeAPI:
		return xai.DefaultBaseURL
	case GrokDefaultBaseURLModeUSEast1:
		return xai.DefaultUSEast1BaseURL
	case GrokDefaultBaseURLModeUSWest2:
		return xai.DefaultUSWest2BaseURL
	case GrokDefaultBaseURLModeEUWest1:
		return xai.DefaultEUWest1BaseURL
	default:
		return xai.DefaultCLIBaseURL
	}
}

func (s *SettingService) GetGrokDefaultBaseURLMode(ctx context.Context) string {
	return s.GetGrokRuntimeSettings(ctx).DefaultBaseURLMode
}

func (s *SettingService) GetGrokDefaultBaseURL(ctx context.Context) string {
	return GrokBaseURLForMode(s.GetGrokDefaultBaseURLMode(ctx))
}

// GetGrokRuntimeSettings returns the cached Grok routing policy.  A database
// failure is fail-open: the last known value is retained when available, or
// the stable CLI/grok-4.5 defaults are used for a short error TTL.
func (s *SettingService) GetGrokRuntimeSettings(ctx context.Context) GrokRuntimeSettings {
	defaults := GrokRuntimeSettings{
		DefaultTextModel:      grokDefaultResponsesModel,
		CrossClientMapEnabled: true,
		DefaultBaseURLMode:    GrokDefaultBaseURLModeCLI,
	}
	if s == nil || s.settingRepo == nil {
		return defaults
	}
	if cached, ok := s.grokRuntimeSettingsCache.Load().(*cachedGrokRuntimeSettings); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.settings
		}
	}
	result, _, _ := s.grokRuntimeSettingsSF.Do("grok_runtime_settings", func() (any, error) {
		if cached, ok := s.grokRuntimeSettingsCache.Load().(*cachedGrokRuntimeSettings); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached, nil
			}
		}
		baseCtx := ctx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), grokRuntimeSettingsDBTimeout)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{
			SettingKeyGrokDefaultTextModel,
			SettingKeyGrokCrossClientModelMapEnabled,
			SettingKeyGrokDefaultBaseURLMode,
		})
		if err != nil {
			// Retain a still-useful previous value on transient DB failure.
			if prior, ok := s.grokRuntimeSettingsCache.Load().(*cachedGrokRuntimeSettings); ok && prior != nil {
				entry := &cachedGrokRuntimeSettings{settings: prior.settings, expiresAt: time.Now().Add(grokRuntimeSettingsErrorTTL).UnixNano()}
				s.grokRuntimeSettingsCache.Store(entry)
				return entry, nil
			}
			entry := &cachedGrokRuntimeSettings{settings: defaults, expiresAt: time.Now().Add(grokRuntimeSettingsErrorTTL).UnixNano()}
			s.grokRuntimeSettingsCache.Store(entry)
			return entry, nil
		}
		settings := defaults
		if raw := strings.TrimSpace(values[SettingKeyGrokDefaultTextModel]); raw != "" {
			settings.DefaultTextModel = raw
		}
		settings.CrossClientMapEnabled = !isFalseSettingValue(values[SettingKeyGrokCrossClientModelMapEnabled])
		if raw := strings.TrimSpace(values[SettingKeyGrokDefaultBaseURLMode]); raw != "" {
			settings.DefaultBaseURLMode = normalizeGrokDefaultBaseURLMode(raw)
		}
		entry := &cachedGrokRuntimeSettings{settings: settings, expiresAt: time.Now().Add(grokRuntimeSettingsCacheTTL).UnixNano()}
		s.grokRuntimeSettingsCache.Store(entry)
		return entry, nil
	})
	if entry, ok := result.(*cachedGrokRuntimeSettings); ok && entry != nil {
		return entry.settings
	}
	return defaults
}

func (s *SettingService) GetGrokDefaultTextModel(ctx context.Context) string {
	return s.GetGrokRuntimeSettings(ctx).DefaultTextModel
}

func (s *SettingService) GetGrokCrossClientModelMapEnabled(ctx context.Context) bool {
	return s.GetGrokRuntimeSettings(ctx).CrossClientMapEnabled
}

func (s *SettingService) ResolveGrokBaseURL(ctx context.Context, account *Account) string {
	defaultBaseURL := xai.DefaultCLIBaseURL
	if s != nil {
		defaultBaseURL = s.GetGrokDefaultBaseURL(ctx)
	}
	if account == nil {
		return defaultBaseURL
	}
	return account.GetGrokBaseURLOr(defaultBaseURL)
}

var (
	ErrRegistrationDisabled   = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrSettingNotFound        = infraerrors.NotFound("SETTING_NOT_FOUND", "setting not found")
	ErrDefaultSubGroupInvalid = infraerrors.BadRequest(
		"DEFAULT_SUBSCRIPTION_GROUP_INVALID",
		"default subscription group must exist and be subscription type",
	)
	ErrDefaultSubGroupDuplicate = infraerrors.BadRequest(
		"DEFAULT_SUBSCRIPTION_GROUP_DUPLICATE",
		"default subscription group cannot be duplicated",
	)
)

type SettingRepository interface {
	Get(ctx context.Context, key string) (*Setting, error)
	GetValue(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	GetMultiple(ctx context.Context, keys []string) (map[string]string, error)
	SetMultiple(ctx context.Context, settings map[string]string) error
	GetAll(ctx context.Context) (map[string]string, error)
	Delete(ctx context.Context, key string) error
}

// ExecutionNodeDefaultWeightsMigrator upgrades the former two-node 1:1
// default only on the authoritative source node. The repository implementation
// performs the marker claim and settings update in one transaction.
type ExecutionNodeDefaultWeightsMigrator interface {
	MigrateExecutionNodeDefaultWeights(ctx context.Context, sourceNodeID string) (bool, error)
}

// ExecutionNodeAccountPreparer performs the one-time, idempotent account
// attribution required before shared multi-node scheduling can be enabled.
type ExecutionNodeAccountPreparer interface {
	PrepareExecutionNodeRouting(ctx context.Context, legacyNodeID string, legacyProxyID int64, allowedNodeIDs []string) (int64, error)
}

// ExecutionNodeRoutingActivator atomically attributes legacy accounts and
// enables the shared routing policy. Implementations must use one database
// transaction so a failed activation cannot leave a half-migrated instance.
type ExecutionNodeRoutingActivator interface {
	PrepareAndEnableExecutionNodeRouting(ctx context.Context, legacyNodeID string, legacyProxyID int64, allowedNodeIDs []string, weights map[string]float64) (int64, error)
}

// ExecutionNodeRoutingProxyMapActivator is the strict activation contract.
// Implementations must validate every durable account against the shared
// node-to-proxy map before making the shared switch visible.
type ExecutionNodeRoutingProxyMapActivator interface {
	PrepareAndEnableExecutionNodeRoutingWithProxyIDs(ctx context.Context, legacyNodeID string, legacyProxyID int64, allowedNodeIDs []string, weights map[string]float64, proxyIDs map[string]int64) (int64, error)
}

// ExecutionNodeJoinTargetInspector prevents a source-authoritative join from
// silently replacing an already-used target ledger. A target with business
// data must be migrated explicitly before it can be joined.
type ExecutionNodeJoinTargetInspector interface {
	IsExecutionNodeJoinTargetEmpty(ctx context.Context) (bool, error)
}

// DefaultSubscriptionGroupReader validates group references used by default subscriptions.
type DefaultSubscriptionGroupReader interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
}

// WebSearchManagerBuilder creates a websearch.Manager from config (injected by infra layer).
// proxyURLs maps proxy ID to resolved URL for provider-level proxy support.
type WebSearchManagerBuilder func(cfg *WebSearchEmulationConfig, proxyURLs map[int64]string)

// SettingService 系统设置服务
type SettingService struct {
	settingRepo                 SettingRepository
	defaultSubGroupReader       DefaultSubscriptionGroupReader
	proxyRepo                   ProxyRepository // for resolving websearch provider proxy URLs
	cfg                         *config.Config
	onUpdate                    func() // Callback when settings are updated (for cache invalidation)
	version                     string // Application version
	webSearchManagerBuilder     WebSearchManagerBuilder
	antigravityUAVersionCache   atomic.Value // *cachedAntigravityUserAgentVersion
	antigravityUAVersionSF      singleflight.Group
	openAICodexUACache          atomic.Value // *cachedOpenAICodexUserAgent
	openAICodexUASF             singleflight.Group
	openAICodexVersionCache     atomic.Value // *cachedOpenAICodexClientVersion
	openAICodexVersionSF        singleflight.Group
	codexRestrictionPolicyCache atomic.Value // *cachedCodexRestrictionPolicy
	codexRestrictionPolicySF    singleflight.Group

	cyberSessionBlockRuntimeCache atomic.Value // *cachedCyberSessionBlockRuntime
	cyberSessionBlockRuntimeSF    singleflight.Group

	// panelRateLimitCache 面板 API 限流配置进程内缓存（*cachedPanelRateLimitSettings）。
	// 面板每个认证请求都会读取，禁止在热路径上直接访问 DB。
	panelRateLimitCache atomic.Value
	panelRateLimitSF    singleflight.Group

	// openAIQuotaAutoPauseSettingsCache holds the most recently observed quota auto-pause
	// settings. GetOpenAIQuotaAutoPauseSettings reads this atomic.Value on the request hot
	// path without ever blocking on the DB; when the cached entry expires, a background
	// goroutine refreshes it via openAIQuotaAutoPauseSettingsSF (stale-while-revalidate).
	// This per-service field also gives tests natural isolation — each SettingService
	// instance owns its own cache, no shared package-level state.
	openAIQuotaAutoPauseSettingsCache atomic.Value // *cachedOpenAIQuotaAutoPauseSettings
	openAIQuotaAutoPauseSettingsSF    singleflight.Group

	executionNodeRoutingCache       atomic.Value // *cachedExecutionNodeRoutingSettings
	executionNodeRoutingSF          singleflight.Group
	executionNodeActivationMu       sync.Mutex
	executionNodeAccountPreparer    ExecutionNodeAccountPreparer
	executionNodeRoutingActivator   ExecutionNodeRoutingActivator
	executionNodeHealthReader       ExecutionNodeHealthReader
	executionNodeAccountStats       ExecutionNodeAccountStatsReader
	executionNodePairingState       ExecutionNodePairingStateReader
	executionNodeJoinApplier        ExecutionNodeJoinApplier
	executionNodeRuntimeInitializer ExecutionNodeRuntimeInitializer
	executionNodeJoinInspector      ExecutionNodeJoinTargetInspector

	// grokRuntimeSettingsCache keeps the Grok model-mapping and default endpoint
	// settings off the request hot path.  It is deliberately per SettingService
	// (rather than package-global) so tests and multiple app instances cannot
	// leak configuration into one another.
	grokRuntimeSettingsCache atomic.Value // *cachedGrokRuntimeSettings
	grokRuntimeSettingsSF    singleflight.Group
}

func (s *SettingService) SetExecutionNodeAccountPreparer(preparer ExecutionNodeAccountPreparer) {
	if s != nil {
		s.executionNodeAccountPreparer = preparer
	}
}

func (s *SettingService) SetExecutionNodeRoutingActivator(activator ExecutionNodeRoutingActivator) {
	if s != nil {
		s.executionNodeRoutingActivator = activator
	}
}

func (s *SettingService) SetExecutionNodeHealthReader(reader ExecutionNodeHealthReader) {
	if s != nil {
		s.executionNodeHealthReader = reader
	}
}

func (s *SettingService) SetExecutionNodeAccountStatsReader(reader ExecutionNodeAccountStatsReader) {
	if s != nil {
		s.executionNodeAccountStats = reader
	}
}

func (s *SettingService) SetExecutionNodeJoinApplier(applier ExecutionNodeJoinApplier) {
	if s != nil {
		s.executionNodeJoinApplier = applier
	}
}

func (s *SettingService) SetExecutionNodeRuntimeInitializer(initializer ExecutionNodeRuntimeInitializer) {
	if s != nil {
		s.executionNodeRuntimeInitializer = initializer
	}
}

func (s *SettingService) SetExecutionNodeJoinInspector(inspector ExecutionNodeJoinTargetInspector) {
	if s != nil {
		s.executionNodeJoinInspector = inspector
	}
}

// DefaultPlatformQuotaSetting 单 platform 三档限额（nil = 沿用上层；0 = 显式禁用；>0 = 上限）
type DefaultPlatformQuotaSetting struct {
	DailyLimitUSD   *float64 `json:"daily"`
	WeeklyLimitUSD  *float64 `json:"weekly"`
	MonthlyLimitUSD *float64 `json:"monthly"`
}

type ProviderDefaultGrantSettings struct {
	Balance          float64
	Concurrency      int
	Subscriptions    []DefaultSubscriptionSetting
	GrantOnSignup    bool
	GrantOnFirstBind bool
	PlatformQuotas   map[string]*DefaultPlatformQuotaSetting // key = platform name
}

type AuthSourceDefaultSettings struct {
	Email                        ProviderDefaultGrantSettings
	LinuxDo                      ProviderDefaultGrantSettings
	OIDC                         ProviderDefaultGrantSettings
	WeChat                       ProviderDefaultGrantSettings
	GitHub                       ProviderDefaultGrantSettings
	Google                       ProviderDefaultGrantSettings
	DingTalk                     ProviderDefaultGrantSettings
	ForceEmailOnThirdPartySignup bool
}

type authSourceDefaultKeySet struct {
	// source 是 auth source 标识（如 "email"、"github"），仅用于 parse 时
	// slog.Warn 诊断输出，不再参与 key 拼接（platformQuotas 字段已存完整 key）。
	source           string
	balance          string
	concurrency      string
	subscriptions    string
	grantOnSignup    string
	grantOnFirstBind string
	platformQuotas   string // SettingKeyAuthSourcePlatformQuotas(source)
}

var (
	emailAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "email",
		balance:          SettingKeyAuthSourceDefaultEmailBalance,
		concurrency:      SettingKeyAuthSourceDefaultEmailConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultEmailSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultEmailGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultEmailGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("email"),
	}
	linuxDoAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "linuxdo",
		balance:          SettingKeyAuthSourceDefaultLinuxDoBalance,
		concurrency:      SettingKeyAuthSourceDefaultLinuxDoConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultLinuxDoSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("linuxdo"),
	}
	oidcAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "oidc",
		balance:          SettingKeyAuthSourceDefaultOIDCBalance,
		concurrency:      SettingKeyAuthSourceDefaultOIDCConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultOIDCSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultOIDCGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("oidc"),
	}
	weChatAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "wechat",
		balance:          SettingKeyAuthSourceDefaultWeChatBalance,
		concurrency:      SettingKeyAuthSourceDefaultWeChatConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultWeChatSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultWeChatGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("wechat"),
	}
	gitHubAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "github",
		balance:          SettingKeyAuthSourceDefaultGitHubBalance,
		concurrency:      SettingKeyAuthSourceDefaultGitHubConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultGitHubSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultGitHubGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("github"),
	}
	googleAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "google",
		balance:          SettingKeyAuthSourceDefaultGoogleBalance,
		concurrency:      SettingKeyAuthSourceDefaultGoogleConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultGoogleSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultGoogleGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("google"),
	}
	dingTalkAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "dingtalk",
		balance:          SettingKeyAuthSourceDefaultDingTalkBalance,
		concurrency:      SettingKeyAuthSourceDefaultDingTalkConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultDingTalkSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultDingTalkGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("dingtalk"),
	}
)

const (
	defaultAuthSourceBalance     = 0
	defaultAuthSourceConcurrency = 5
	defaultWeChatConnectMode     = "open"
	defaultWeChatConnectScopes   = "snsapi_login"
	defaultWeChatConnectFrontend = "/auth/wechat/callback"
	defaultGitHubOAuthAuthorize  = "https://github.com/login/oauth/authorize"
	defaultGitHubOAuthToken      = "https://github.com/login/oauth/access_token"
	defaultGitHubOAuthUserInfo   = "https://api.github.com/user"
	defaultGitHubOAuthEmails     = "https://api.github.com/user/emails"
	defaultGitHubOAuthScopes     = "read:user user:email"
	defaultGitHubOAuthFrontend   = "/auth/oauth/callback"
	defaultGoogleOAuthAuthorize  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleOAuthToken      = "https://oauth2.googleapis.com/token"
	defaultGoogleOAuthUserInfo   = "https://openidconnect.googleapis.com/v1/userinfo"
	defaultGoogleOAuthScopes     = "openid email profile"
	defaultGoogleOAuthFrontend   = "/auth/oauth/callback"
	defaultLoginAgreementMode    = "modal"
	defaultLoginAgreementDate    = "2026-03-31"
)

// NewSettingService 创建系统设置服务实例
func NewSettingService(settingRepo SettingRepository, cfg *config.Config) *SettingService {
	return &SettingService{
		settingRepo: settingRepo,
		cfg:         cfg,
	}
}

// SetDefaultSubscriptionGroupReader injects an optional group reader for default subscription validation.
func (s *SettingService) SetDefaultSubscriptionGroupReader(reader DefaultSubscriptionGroupReader) {
	s.defaultSubGroupReader = reader
}

// SetProxyRepository injects a proxy repo for resolving websearch provider proxy URLs.
func (s *SettingService) SetProxyRepository(repo ProxyRepository) {
	s.proxyRepo = repo
}

func (s *SettingService) LoadForwardedClientIPSettings(ctx context.Context) error {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return nil
	}

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyAPIKeyACLTrustForwardedIP,
		SettingKeyForwardedClientIPHeaders,
		settingKeyForwardedClientIPModeV2,
	})
	if err != nil {
		s.cfg.SetForwardedClientIPSettings(false, nil)
		return fmt.Errorf("get forwarded client ip settings: %w", err)
	}

	enabled := s.cfg.Security.TrustForwardedIPForAPIKeyACL
	headers := s.cfg.ForwardedClientIPSettings().Headers
	storedValue, hasStoredValue := values[SettingKeyAPIKeyACLTrustForwardedIP]
	if hasStoredValue {
		enabled = storedValue == "true"
	}

	var headersErr error
	if storedHeaders, ok := values[SettingKeyForwardedClientIPHeaders]; ok {
		headers, headersErr = parseForwardedClientIPHeadersSetting(storedHeaders)
		if headersErr != nil {
			enabled = false
			headers = []string{}
			headersErr = fmt.Errorf("load forwarded client ip headers: %w", headersErr)
		}
	}

	updates := make(map[string]string)
	if _, hasStoredHeaders := values[SettingKeyForwardedClientIPHeaders]; !hasStoredHeaders {
		headersJSON, marshalErr := json.Marshal(headers)
		if marshalErr != nil {
			headers = []string{}
			headersErr = errors.Join(headersErr, fmt.Errorf("marshal forwarded client ip headers: %w", marshalErr))
			headersJSON = []byte("[]")
		}
		updates[SettingKeyForwardedClientIPHeaders] = string(headersJSON)
	}
	if values[settingKeyForwardedClientIPModeV2] != "true" {
		updates[settingKeyForwardedClientIPModeV2] = "true"
		// Before this migration, new installations persisted false by default.
		// Restore compatibility only when no trusted-proxy policy was configured.
		if headersErr == nil && hasStoredValue && !enabled && !s.cfg.Server.TrustedProxiesConfigured {
			enabled = true
			updates[SettingKeyAPIKeyACLTrustForwardedIP] = "true"
		}
	}
	if len(updates) > 0 {
		if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
			s.cfg.SetForwardedClientIPSettings(enabled, headers)
			return errors.Join(headersErr, fmt.Errorf("migrate forwarded client ip setting: %w", err))
		}
	}

	s.cfg.SetForwardedClientIPSettings(enabled, headers)
	return headersErr
}

// GetAllSettings 获取所有系统设置
func (s *SettingService) GetAllSettings(ctx context.Context) (*SystemSettings, error) {
	settings, err := s.settingRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all settings: %w", err)
	}

	return s.parseSettings(settings), nil
}

// SetOnUpdateCallback sets a callback function to be called when settings are updated
// This is used for cache invalidation (e.g., HTML cache in frontend server)
func (s *SettingService) SetOnUpdateCallback(callback func()) {
	s.onUpdate = callback
}

// SetVersion sets the application version for injection into public settings
func (s *SettingService) SetVersion(version string) {
	s.version = version
}

// getStringOrDefault 获取字符串值或默认值
func (s *SettingService) getStringOrDefault(settings map[string]string, key, defaultValue string) string {
	if value, ok := settings[key]; ok && value != "" {
		return value
	}
	return defaultValue
}
