package service

import (
	"context"
	"fmt"
)

var openAIOAuthSourceOwnedCredentialKeys = map[string]struct{}{
	"access_token":               {},
	"refresh_token":              {},
	"id_token":                   {},
	"expires_at":                 {},
	"expires_in":                 {},
	"email":                      {},
	"chatgpt_account_id":         {},
	"chatgpt_user_id":            {},
	"organization_id":            {},
	"plan_type":                  {},
	"subscription_expires_at":    {},
	"client_id":                  {},
	"token_type":                 {},
	"scope":                      {},
	"auth_mode":                  {},
	"openai_auth_mode":           {},
	"chatgpt_account_is_fedramp": {},
	"_token_version":             {},
}

func mergeOpenAIOAuthSourceCredentials(target, source map[string]any) map[string]any {
	merged := shallowCopyMap(target)
	if merged == nil {
		merged = make(map[string]any)
	}
	for key := range openAIOAuthSourceOwnedCredentialKeys {
		value, exists := source[key]
		if !exists {
			delete(merged, key)
			continue
		}
		merged[key] = value
	}
	return merged
}

func stripOpenAIOAuthSourceCredentialUpdates(credentials map[string]any) map[string]any {
	if credentials == nil {
		return nil
	}
	filtered := make(map[string]any, len(credentials))
	for key, value := range credentials {
		if _, sourceOwned := openAIOAuthSourceOwnedCredentialKeys[key]; sourceOwned {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func preserveOpenAIOAuthSourceCredentialSnapshot(existing, updates map[string]any) map[string]any {
	filtered := stripOpenAIOAuthSourceCredentialUpdates(updates)
	if filtered == nil {
		filtered = make(map[string]any)
	}
	for key := range openAIOAuthSourceOwnedCredentialKeys {
		if value, exists := existing[key]; exists {
			filtered[key] = value
		}
	}
	return filtered
}

func containsOpenAIOAuthSourceCredentialUpdates(credentials map[string]any) bool {
	for key := range credentials {
		if _, sourceOwned := openAIOAuthSourceOwnedCredentialKeys[key]; sourceOwned {
			return true
		}
	}
	return false
}

// resolveCredentialAccount resolves linked OpenAI accounts for credential use.
// Spark shadows inherit the complete parent account, including its proxy.
// OpenAI OAuth copies inherit only the source credentials while retaining the
// copy's own proxy, fingerprint, scheduling and billing configuration.
// 设计为包级函数（非任何 service 的方法），以便 OpenAIGatewayService / OpenAIQuotaService /
// AccountUsageService 等不同接收者共享同一实现。
func resolveCredentialAccount(ctx context.Context, repo AccountRepository, account *Account) (*Account, error) {
	if account == nil {
		return account, nil
	}
	if account.IsShadow() {
		parent, err := repo.GetByID(ctx, *account.ParentAccountID)
		if err != nil {
			return nil, fmt.Errorf("resolve spark shadow parent %d: %w", *account.ParentAccountID, err)
		}
		if parent == nil {
			return nil, fmt.Errorf("spark shadow parent %d not found", *account.ParentAccountID)
		}
		// 防御:创建路径已禁二级影子(G6),此处再挡一层——畸形数据/手工 DB 写出的影子→影子链
		// 会让凭据解析停在无凭据的一级影子(只解一层),fail-closed 比静默返回坏母更安全(外审第6轮)。
		if parent.IsShadow() {
			return nil, fmt.Errorf("spark shadow parent %d is itself a shadow", parent.ID)
		}
		if !parent.IsOpenAIOAuth() {
			return nil, fmt.Errorf("spark shadow parent %d is not OpenAI OAuth", parent.ID)
		}
		return parent, nil
	}

	source, err := resolveOpenAIOAuthCredentialSourceAccount(ctx, repo, account)
	if err != nil || source == account {
		return source, err
	}
	resolved := *account
	resolved.Credentials = mergeOpenAIOAuthSourceCredentials(account.Credentials, source.Credentials)
	return &resolved, nil
}

// resolveOpenAIOAuthCredentialSourceAccount returns the primary row that owns
// token rotation for an OAuth copy. Chains are forbidden so one refresh lock
// and one durable credential document remain authoritative.
func resolveOpenAIOAuthCredentialSourceAccount(ctx context.Context, repo AccountRepository, account *Account) (*Account, error) {
	if account == nil {
		return nil, nil
	}
	sourceID := account.OpenAIOAuthCredentialSourceID()
	if sourceID <= 0 {
		return account, nil
	}
	if repo == nil {
		return nil, fmt.Errorf("resolve OpenAI OAuth credential source %d: account repository is not configured", sourceID)
	}
	source, err := repo.GetByID(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("resolve OpenAI OAuth credential source %d: %w", sourceID, err)
	}
	if source == nil {
		return nil, fmt.Errorf("OpenAI OAuth credential source %d not found", sourceID)
	}
	if !source.IsOpenAIOAuth() || source.IsCredentialShadow() {
		return nil, fmt.Errorf("OpenAI OAuth credential source %d is not a primary OpenAI OAuth account", sourceID)
	}
	if source.IsOpenAIOAuthCredentialCopy() {
		return nil, fmt.Errorf("OpenAI OAuth credential source %d is itself a credential copy", sourceID)
	}
	return source, nil
}
