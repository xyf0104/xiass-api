//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updatingProxyRepoStub struct {
	*proxyRepoStub
	proxy       *Proxy
	updateCalls int
}

func TestProxyCanBeDisabledExpiredAndDeletedWhenUnused(t *testing.T) {
	warnDays := 7
	repo := &updatingProxyRepoStub{
		proxyRepoStub: &proxyRepoStub{},
		proxy: &Proxy{
			ID:             9,
			Protocol:       "socks5",
			Host:           "proxy.example",
			Port:           1080,
			Status:         StatusActive,
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: 7,
		},
	}
	svc := &adminServiceImpl{proxyRepo: repo}

	updated, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		Status:         StatusDisabled,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: &warnDays,
	})
	require.NoError(t, err)
	require.Equal(t, StatusDisabled, updated.Status)
	require.Equal(t, 1, repo.updateCalls)

	expired := time.Now().Add(-time.Minute)
	updated, err = svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		ExpiresAt:      &expired,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: &warnDays,
	})
	require.NoError(t, err)
	require.Equal(t, &expired, updated.ExpiresAt)
	require.Equal(t, StatusDisabled, updated.Status)
	require.Equal(t, 2, repo.updateCalls)

	repo.accountCount = 1
	err = svc.DeleteProxy(context.Background(), 9)
	require.ErrorIs(t, err, ErrProxyInUse)
	require.Empty(t, repo.deletedIDs)
	repo.accountCount = 0
	require.NoError(t, svc.DeleteProxy(context.Background(), 9))
	require.Equal(t, []int64{9}, repo.deletedIDs)
}

func TestProxyCanBeRepairedWhileRemainingActive(t *testing.T) {
	warnDays := 7
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
	svc := &adminServiceImpl{proxyRepo: repo}

	updated, err := svc.UpdateProxy(context.Background(), 9, &UpdateProxyInput{
		Host:           "new.internal",
		Status:         StatusActive,
		FallbackMode:   FallbackModeNone,
		ExpiryWarnDays: &warnDays,
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
