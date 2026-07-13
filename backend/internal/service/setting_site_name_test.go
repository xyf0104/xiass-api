//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type siteNameSettingRepoStub struct {
	values      map[string]string
	initialized map[string]string
}

func (s *siteNameSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *siteNameSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (s *siteNameSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *siteNameSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *siteNameSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	s.initialized = settings
	return nil
}

func (s *siteNameSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *siteNameSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestSettingService_DefaultSiteNameIsXIASSAPI(t *testing.T) {
	t.Run("initialization", func(t *testing.T) {
		repo := &siteNameSettingRepoStub{values: map[string]string{}}
		svc := NewSettingService(repo, &config.Config{})

		require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
		require.Equal(t, "XIASS API", repo.initialized[SettingKeySiteName])
	})

	t.Run("system_settings_parse", func(t *testing.T) {
		svc := NewSettingService(&siteNameSettingRepoStub{}, &config.Config{})

		require.Equal(t, "XIASS API", svc.parseSettings(map[string]string{}).SiteName)
	})

	t.Run("public_settings", func(t *testing.T) {
		svc := NewSettingService(&siteNameSettingRepoStub{values: map[string]string{}}, &config.Config{})

		settings, err := svc.GetPublicSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, "XIASS API", settings.SiteName)
	})

	t.Run("site_name_accessor", func(t *testing.T) {
		svc := NewSettingService(&siteNameSettingRepoStub{values: map[string]string{}}, &config.Config{})

		require.Equal(t, "XIASS API", svc.GetSiteName(context.Background()))
	})

	t.Run("custom_name_is_preserved", func(t *testing.T) {
		svc := NewSettingService(&siteNameSettingRepoStub{values: map[string]string{
			SettingKeySiteName: "Custom Gateway",
		}}, &config.Config{})

		require.Equal(t, "Custom Gateway", svc.GetSiteName(context.Background()))
		settings, err := svc.GetPublicSettings(context.Background())
		require.NoError(t, err)
		require.Equal(t, "Custom Gateway", settings.SiteName)
	})
}
