//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type updatingProxyRepoStub struct {
	*proxyRepoStub
	proxy       *Proxy
	updateCalls int
}

func executionNodeProxyProtectionService(repo ProxyRepository) *adminServiceImpl {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	return &adminServiceImpl{
		proxyRepo: repo,
		settingService: NewSettingService(&executionNodeSettingRepo{values: map[string]string{
			SettingKeyExecutionNodeBalancingEnabled: "true",
			SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
			SettingKeyExecutionNodeProxyIDs:         `{"api":9,"api2":10}`,
		}}, cfg),
	}
}

func TestExecutionNodeMappedProxyCannotBeDisabledExpiredOrDeleted(t *testing.T) {
	repo := &updatingProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy: &Proxy{
			ID:             9,
			Protocol:       "socks5",
			Host:           "api.internal",
			Port:           1080,
			Status:         StatusActive,
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: 7,
		},
	}
	svc := executionNodeProxyProtectionService(repo)

	_, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		Status:         StatusDisabled,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: 7,
	})
	require.ErrorIs(t, err, ErrExecutionNodeProxyProtected)
	require.Zero(t, repo.updateCalls)

	expired := time.Now().Add(-time.Minute)
	_, err = svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		ExpiresAt:      &expired,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: 7,
	})
	require.ErrorIs(t, err, ErrExecutionNodeProxyProtected)
	require.Zero(t, repo.updateCalls)

	err = svc.DeleteProxy(context.Background(), 9)
	require.ErrorIs(t, err, ErrExecutionNodeProxyProtected)
	require.Empty(t, repo.deletedIDs)
}

func TestExecutionNodeMappedProxyCanBeRepairedWhileRemainingActive(t *testing.T) {
	repo := &updatingProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy: &Proxy{
			ID:             9,
			Protocol:       "socks5",
			Host:           "old.internal",
			Port:           1080,
			Status:         StatusActive,
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: 7,
		},
	}
	svc := executionNodeProxyProtectionService(repo)

	updated, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		Host:           "new.internal",
		Status:         StatusActive,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: 7,
	})

	require.NoError(t, err)
	require.Equal(t, "new.internal", updated.Host)
	require.Equal(t, 1, repo.updateCalls)
}

func (s *updatingProxyRepoStub) GetByID(context.Context, int64) (*Proxy, error) {
	copy := *s.proxy
	return &copy, nil
}

func (s *updatingProxyRepoStub) Update(_ context.Context, proxy *Proxy) error {
	s.updateCalls++
	copy := *proxy
	s.proxy = &copy
	return nil
}

func TestBothProxyUpdateServicesUseRepositoryUpdateBoundary(t *testing.T) {
	t.Run("ProxyService", func(t *testing.T) {
		repo := &updatingProxyRepoStub{
			proxyRepoStub: &proxyRepoStub{},
			proxy:         &Proxy{ID: 9, Protocol: "http", Host: "old.example", Port: 8080, Status: StatusActive},
		}
		svc := NewProxyService(repo)
		host := "new.example"

		_, err := svc.Update(context.Background(), 9, UpdateProxyRequest{Host: &host})

		require.NoError(t, err)
		require.Equal(t, 1, repo.updateCalls)
		require.Equal(t, host, repo.proxy.Host)
	})

	t.Run("adminService", func(t *testing.T) {
		repo := &updatingProxyRepoStub{
			proxyRepoStub: &proxyRepoStub{},
			proxy: &Proxy{
				ID:             9,
				Protocol:       "http",
				Host:           "old.example",
				Port:           8080,
				Status:         StatusActive,
				FallbackMode:   FallbackModeNone,
				ExpiryWarnDays: 7,
			},
		}
		svc := &adminServiceImpl{proxyRepo: repo}
		warnDays := 7

		_, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
			Host:           "new.example",
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: &warnDays,
		})

		require.NoError(t, err)
		require.Equal(t, 1, repo.updateCalls)
		require.Equal(t, "new.example", repo.proxy.Host)
	})
}
