package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	openAIModelPriorityCacheTTL      = 5 * time.Second
	openAIModelPriorityErrorCacheTTL = time.Second
	openAIModelPriorityDBTimeout     = 2 * time.Second
	openAIModelPriorityRefreshKey    = "openai_model_priority_settings"
	openAIModelPriorityMaxRules      = 100
	openAIModelPriorityMaxAccounts   = 10000
)

var openAIModelPriorityPattern = regexp.MustCompile(`^[A-Za-z0-9._:@/-]+\*?$`)

// OpenAIModelPriorityRule promotes the selected accounts only for models that
// match ModelPattern. Account health, group membership, model support, sticky
// sessions, concurrency, and every other ordinary eligibility check still run
// before this preference is applied.
type OpenAIModelPriorityRule struct {
	ModelPattern string  `json:"model_pattern"`
	AccountIDs   []int64 `json:"account_ids"`
	AccountOrder []int64 `json:"account_order,omitempty"`
}

type OpenAIModelPrioritySettings struct {
	Enabled bool                      `json:"enabled"`
	Rules   []OpenAIModelPriorityRule `json:"rules"`
}

type compiledOpenAIModelPriorityRule struct {
	modelPattern string
	accountIDs   map[int64]struct{}
	accountRanks map[int64]int
}

type compiledOpenAIModelPrioritySettings struct {
	enabled bool
	rules   []compiledOpenAIModelPriorityRule
}

type cachedOpenAIModelPrioritySettings struct {
	settings  OpenAIModelPrioritySettings
	compiled  compiledOpenAIModelPrioritySettings
	expiresAt int64
}

func DefaultOpenAIModelPrioritySettings() *OpenAIModelPrioritySettings {
	return &OpenAIModelPrioritySettings{Enabled: false, Rules: []OpenAIModelPriorityRule{}}
}

func cloneOpenAIModelPrioritySettings(settings OpenAIModelPrioritySettings) *OpenAIModelPrioritySettings {
	cloned := OpenAIModelPrioritySettings{Enabled: settings.Enabled, Rules: make([]OpenAIModelPriorityRule, len(settings.Rules))}
	for i, rule := range settings.Rules {
		cloned.Rules[i] = OpenAIModelPriorityRule{
			ModelPattern: rule.ModelPattern,
			AccountIDs:   append([]int64(nil), rule.AccountIDs...),
			AccountOrder: append([]int64(nil), rule.AccountOrder...),
		}
	}
	return &cloned
}

func normalizeOpenAIModelPrioritySettings(settings *OpenAIModelPrioritySettings) (*OpenAIModelPrioritySettings, error) {
	if settings == nil {
		return nil, errors.New("settings cannot be nil")
	}
	if len(settings.Rules) > openAIModelPriorityMaxRules {
		return nil, fmt.Errorf("rules cannot exceed %d", openAIModelPriorityMaxRules)
	}

	normalized := &OpenAIModelPrioritySettings{Enabled: settings.Enabled, Rules: make([]OpenAIModelPriorityRule, 0, len(settings.Rules))}
	seenPatterns := make(map[string]struct{}, len(settings.Rules))
	for i, rule := range settings.Rules {
		pattern := strings.TrimSpace(rule.ModelPattern)
		if pattern == "" {
			return nil, fmt.Errorf("rule[%d]: model_pattern cannot be empty", i)
		}
		if len(pattern) > 128 || !openAIModelPriorityPattern.MatchString(pattern) {
			return nil, fmt.Errorf("rule[%d]: invalid model_pattern %q", i, rule.ModelPattern)
		}
		patternKey := strings.ToLower(pattern)
		if _, exists := seenPatterns[patternKey]; exists {
			return nil, fmt.Errorf("rule[%d]: duplicate model_pattern %q", i, pattern)
		}
		seenPatterns[patternKey] = struct{}{}
		if len(rule.AccountIDs) == 0 {
			return nil, fmt.Errorf("rule[%d]: account_ids cannot be empty", i)
		}
		if len(rule.AccountIDs) > openAIModelPriorityMaxAccounts {
			return nil, fmt.Errorf("rule[%d]: account_ids cannot exceed %d", i, openAIModelPriorityMaxAccounts)
		}

		seenIDs := make(map[int64]struct{}, len(rule.AccountIDs))
		ids := make([]int64, 0, len(rule.AccountIDs))
		for j, accountID := range rule.AccountIDs {
			if accountID <= 0 {
				return nil, fmt.Errorf("rule[%d]: account_ids[%d] must be positive", i, j)
			}
			if _, exists := seenIDs[accountID]; exists {
				continue
			}
			seenIDs[accountID] = struct{}{}
			ids = append(ids, accountID)
		}
		sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
		if len(rule.AccountOrder) > 0 {
			if len(rule.AccountOrder) != len(ids) {
				return nil, fmt.Errorf("rule[%d]: account_order must contain every selected account exactly once", i)
			}
			ordered := make(map[int64]bool, len(ids))
			for _, id := range rule.AccountOrder {
				if _, exists := seenIDs[id]; !exists || ordered[id] {
					return nil, fmt.Errorf("rule[%d]: account_order contains an unselected or duplicate account", i)
				}
				ordered[id] = true
			}
		}
		normalized.Rules = append(normalized.Rules, OpenAIModelPriorityRule{
			ModelPattern: pattern, AccountIDs: ids, AccountOrder: append([]int64(nil), rule.AccountOrder...),
		})
	}
	return normalized, nil
}

func compileOpenAIModelPrioritySettings(settings OpenAIModelPrioritySettings) compiledOpenAIModelPrioritySettings {
	compiled := compiledOpenAIModelPrioritySettings{enabled: settings.Enabled, rules: make([]compiledOpenAIModelPriorityRule, 0, len(settings.Rules))}
	for _, rule := range settings.Rules {
		accountIDs := make(map[int64]struct{}, len(rule.AccountIDs))
		for _, accountID := range rule.AccountIDs {
			accountIDs[accountID] = struct{}{}
		}
		ranks := make(map[int64]int, len(rule.AccountOrder))
		for rank, accountID := range rule.AccountOrder {
			ranks[accountID] = rank
		}
		compiled.rules = append(compiled.rules, compiledOpenAIModelPriorityRule{
			modelPattern: strings.ToLower(rule.ModelPattern),
			accountIDs:   accountIDs,
			accountRanks: ranks,
		})
	}
	return compiled
}

func (settings compiledOpenAIModelPrioritySettings) accountIDsForModel(model string) map[int64]struct{} {
	if rule := settings.ruleForModel(model); rule != nil {
		return rule.accountIDs
	}
	return nil
}

func (settings compiledOpenAIModelPrioritySettings) ruleForModel(model string) *compiledOpenAIModelPriorityRule {
	if !settings.enabled || len(settings.rules) == 0 {
		return nil
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return nil
	}
	for index, rule := range settings.rules {
		if rule.modelPattern == model {
			return &settings.rules[index]
		}
	}
	bestIndex := -1
	bestPrefixLength := -1
	for i, rule := range settings.rules {
		if !strings.HasSuffix(rule.modelPattern, "*") || !matchModelPattern(rule.modelPattern, model) {
			continue
		}
		prefixLength := len(strings.TrimSuffix(rule.modelPattern, "*"))
		if prefixLength > bestPrefixLength {
			bestIndex = i
			bestPrefixLength = prefixLength
		}
	}
	if bestIndex >= 0 {
		return &settings.rules[bestIndex]
	}
	return nil
}

func (s *SettingService) loadOpenAIModelPrioritySettings(ctx context.Context) (*cachedOpenAIModelPrioritySettings, error) {
	if s == nil || s.settingRepo == nil {
		defaults := DefaultOpenAIModelPrioritySettings()
		return &cachedOpenAIModelPrioritySettings{settings: *defaults, compiled: compileOpenAIModelPrioritySettings(*defaults), expiresAt: time.Now().Add(openAIModelPriorityCacheTTL).UnixNano()}, nil
	}
	s.openAIModelPriorityMu.Lock()
	defer s.openAIModelPriorityMu.Unlock()
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), openAIModelPriorityDBTimeout)
	defer cancel()

	settings := DefaultOpenAIModelPrioritySettings()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIModelPrioritySettings)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, fmt.Errorf("get OpenAI model priority settings: %w", err)
	}
	if err == nil && strings.TrimSpace(raw) != "" {
		if unmarshalErr := json.Unmarshal([]byte(raw), settings); unmarshalErr != nil {
			return nil, fmt.Errorf("decode OpenAI model priority settings: %w", unmarshalErr)
		}
	}
	normalized, err := normalizeOpenAIModelPrioritySettings(settings)
	if err != nil {
		return nil, fmt.Errorf("validate OpenAI model priority settings: %w", err)
	}
	entry := &cachedOpenAIModelPrioritySettings{
		settings:  *normalized,
		compiled:  compileOpenAIModelPrioritySettings(*normalized),
		expiresAt: time.Now().Add(openAIModelPriorityCacheTTL).UnixNano(),
	}
	s.openAIModelPriorityCache.Store(entry)
	return entry, nil
}

func (s *SettingService) GetOpenAIModelPrioritySettings(ctx context.Context) (*OpenAIModelPrioritySettings, error) {
	if s == nil {
		return DefaultOpenAIModelPrioritySettings(), nil
	}
	if cached, _ := s.openAIModelPriorityCache.Load().(*cachedOpenAIModelPrioritySettings); cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cloneOpenAIModelPrioritySettings(cached.settings), nil
	}
	loaded, err, _ := s.openAIModelPrioritySF.Do(openAIModelPriorityRefreshKey, func() (any, error) {
		return s.loadOpenAIModelPrioritySettings(ctx)
	})
	if err != nil {
		return nil, err
	}
	entry, ok := loaded.(*cachedOpenAIModelPrioritySettings)
	if !ok || entry == nil {
		return nil, errors.New("invalid OpenAI model priority cache entry")
	}
	return cloneOpenAIModelPrioritySettings(entry.settings), nil
}

func (s *SettingService) SetOpenAIModelPrioritySettings(ctx context.Context, settings *OpenAIModelPrioritySettings) error {
	if s == nil || s.settingRepo == nil {
		return errors.New("setting repository is unavailable")
	}
	normalized, err := normalizeOpenAIModelPrioritySettings(settings)
	if err != nil {
		return err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal OpenAI model priority settings: %w", err)
	}
	s.openAIModelPriorityMu.Lock()
	if err := s.settingRepo.Set(ctx, SettingKeyOpenAIModelPrioritySettings, string(data)); err != nil {
		s.openAIModelPriorityMu.Unlock()
		return fmt.Errorf("save OpenAI model priority settings: %w", err)
	}
	s.openAIModelPrioritySF.Forget(openAIModelPriorityRefreshKey)
	s.openAIModelPriorityCache.Store(&cachedOpenAIModelPrioritySettings{
		settings:  *normalized,
		compiled:  compileOpenAIModelPrioritySettings(*normalized),
		expiresAt: time.Now().Add(openAIModelPriorityCacheTTL).UnixNano(),
	})
	s.openAIModelPriorityMu.Unlock()
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

// ResolveOpenAIModelPriorityAccountIDs is intentionally non-blocking. It never
// waits for the database: a stale immutable snapshot is served while one
// singleflight refresh runs in the background.
func (s *SettingService) ResolveOpenAIModelPriorityAccountIDs(ctx context.Context, model string) map[int64]struct{} {
	return s.resolveOpenAIModelPriorityPreference(ctx, model).accountIDs
}

func (s *SettingService) resolveOpenAIModelPriorityPreference(ctx context.Context, model string) openAIModelRoutingPreference {
	if s == nil || strings.TrimSpace(model) == "" {
		return openAIModelRoutingPreference{}
	}
	cached, _ := s.openAIModelPriorityCache.Load().(*cachedOpenAIModelPrioritySettings)
	if (cached == nil || time.Now().UnixNano() >= cached.expiresAt) && s.openAIModelPriorityRefreshing.CompareAndSwap(false, true) {
		go func() {
			defer s.openAIModelPriorityRefreshing.Store(false)
			_, err, _ := s.openAIModelPrioritySF.Do(openAIModelPriorityRefreshKey, func() (any, error) {
				return s.loadOpenAIModelPrioritySettings(ctx)
			})
			if err != nil {
				current, _ := s.openAIModelPriorityCache.Load().(*cachedOpenAIModelPrioritySettings)
				now := time.Now()
				if current != nil && current.expiresAt <= now.UnixNano() {
					s.openAIModelPriorityCache.CompareAndSwap(current, &cachedOpenAIModelPrioritySettings{
						settings: current.settings, compiled: current.compiled,
						expiresAt: now.Add(openAIModelPriorityErrorCacheTTL).UnixNano(),
					})
				}
			}
		}()
	}
	if cached == nil {
		return openAIModelRoutingPreference{}
	}
	if rule := cached.compiled.ruleForModel(model); rule != nil {
		return openAIModelRoutingPreference{accountIDs: rule.accountIDs, accountRanks: rule.accountRanks}
	}
	return openAIModelRoutingPreference{}
}
