package service

import "strconv"

// OpenAITokenCacheKey generates one cache/refresh-lock key per credential
// owner. Linked OAuth copies therefore reuse the primary account's lock and
// cached access token instead of consuming the rotating refresh token again.
func OpenAITokenCacheKey(account *Account) string {
	accountID := int64(0)
	if account != nil {
		accountID = account.OpenAIOAuthCredentialOwnerID()
	}
	return "openai:account:" + strconv.FormatInt(accountID, 10)
}

// ClaudeTokenCacheKey 生成 Claude (Anthropic) OAuth 账号的缓存键
// 格式: "claude:account:{account_id}"
func ClaudeTokenCacheKey(account *Account) string {
	return "claude:account:" + strconv.FormatInt(account.ID, 10)
}
