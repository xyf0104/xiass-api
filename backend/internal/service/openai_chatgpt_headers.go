package service

import (
	"context"
	"net/http"
)

func setOpenAIChatGPTAccountHeaders(headers http.Header, account *Account) {
	if headers == nil || account == nil || !account.IsOpenAIOAuth() {
		return
	}
	if chatgptAccountID := account.GetChatGPTAccountID(); chatgptAccountID != "" {
		headers.Set("chatgpt-account-id", chatgptAccountID)
	}
	if account.IsChatGPTAccountFedRAMP() {
		headers.Set("x-openai-fedramp", "true")
	} else {
		headers.Del("x-openai-fedramp")
	}
}

// resolveAndSetOpenAIChatGPTAccountHeaders 解析 Spark 影子或 OAuth 副本的凭据来源，
// 再调用 setOpenAIChatGPTAccountHeaders 写入 chatgpt-account-id / x-openai-fedramp 头。
// 普通独立账号为直通，行为与直接调用 setOpenAIChatGPTAccountHeaders 一致。
func resolveAndSetOpenAIChatGPTAccountHeaders(ctx context.Context, repo AccountRepository, headers http.Header, account *Account) error {
	credAccount, err := resolveCredentialAccount(ctx, repo, account)
	if err != nil {
		return err
	}
	setOpenAIChatGPTAccountHeaders(headers, credAccount)
	return nil
}
