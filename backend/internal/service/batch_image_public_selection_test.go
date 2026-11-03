//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestBatchImageAccountSelectionPreservesPriorityEgressAndOwnerScope(t *testing.T) {
	for _, mode := range []string{"selected proxy", "direct", "unavailable", "unsupported model", "restricted", "denied", "empty allowlist"} {
		t.Run(mode, func(t *testing.T) {
			fallback := testBatchImageAccount(9771, AccountTypeAPIKey)
			preferred := testBatchImageAccount(9772, AccountTypeAPIKey)
			fallback.Priority, preferred.Priority = 100, 1
			proxyID := int64(83)
			preferred.ProxyID = &proxyID
			preferred.Proxy = &Proxy{ID: proxyID, Protocol: "socks5", Host: "selected.example", Port: 1080, Status: StatusActive}
			switch mode {
			case "direct":
				preferred.ProxyID, preferred.Proxy = nil, nil
			case "unavailable":
				preferred.Schedulable = false
			case "unsupported model":
				preferred.Credentials["model_mapping"] = map[string]any{"unrelated-model": "unrelated-model"}
			}
			svc, _, _, gemini, vertex := newTestBatchImagePublicService(true)
			svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{fallback, preferred}}
			policy := &publicBatchImageAccountPolicy{allowedIDs: map[int64]struct{}{fallback.ID: {}, preferred.ID: {}}}
			switch mode {
			case "restricted":
				delete(policy.allowedIDs, preferred.ID)
			case "denied":
				policy.err = ErrUserGroupAccountNotAllowed
			case "empty allowlist":
				policy.allowedIDs = map[int64]struct{}{}
			}
			svc.AccountPolicy = policy
			ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(999))
			ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 999})
			provider, got, err := svc.selectProviderAndAccount(ctx, testBatchImageOwner(), "", "gemini-2.5-flash-image")
			if mode == "denied" || mode == "empty allowlist" {
				if mode == "denied" {
					require.ErrorIs(t, err, ErrUserGroupAccountNotAllowed)
				} else {
					require.ErrorIs(t, err, ErrBatchImageNoAccountAvailable)
				}
				require.Nil(t, got)
				require.Nil(t, provider)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				require.Equal(t, BatchImageProviderGeminiAPI, provider.Name())
				switch mode {
				case "unavailable", "unsupported model", "restricted":
					require.Equal(t, fallback.ID, got.ID)
				case "direct":
					require.Equal(t, preferred.ID, got.ID)
					require.Empty(t, got.requestProxyURL())
				default:
					require.Equal(t, preferred.ID, got.ID)
					require.Equal(t, preferred.Proxy.URL(), got.requestProxyURL())
				}
			}
			require.Empty(t, gemini.submits)
			require.Empty(t, vertex.submits)
			require.NotEmpty(t, policy.calls)
			for _, call := range policy.calls {
				require.Equal(t, testBatchImageOwner().UserID, call.userID)
				require.Zero(t, call.groupID)
				require.Zero(t, call.ambientGroupID)
			}
		})
	}
}

func TestBatchImageAccountSelectionPreservesProviderPreference(t *testing.T) {
	geminiAccount := testBatchImageAccount(9781, AccountTypeAPIKey)
	vertexAccount := testBatchImageAccount(9782, AccountTypeServiceAccount)
	geminiAccount.Priority, vertexAccount.Priority = 100, 1
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{vertexAccount, geminiAccount}}
	svc.ProviderRegistry = NewBatchImageProviderRegistry(&GeminiAPIBatchImageProvider{}, &publicBatchImageProvider{name: BatchImageProviderVertex})
	provider, got, err := svc.selectProviderAndAccount(context.Background(), testBatchImageOwner(), "", "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, BatchImageProviderGeminiAPI, provider.Name())
	require.Equal(t, geminiAccount.ID, got.ID, "provider preference must precede priority across provider types")
	provider, got, err = svc.selectProviderAndAccount(context.Background(), testBatchImageOwner(), BatchImageProviderVertex, "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, BatchImageProviderVertex, provider.Name())
	require.Equal(t, vertexAccount.ID, got.ID)
}
