package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type retryPricingRemoteClient struct {
	body, hash string
	downloads  int
}

func (c *retryPricingRemoteClient) FetchPricingJSON(context.Context, string) ([]byte, error) {
	c.downloads++
	return []byte(c.body), nil
}

func (c *retryPricingRemoteClient) FetchHashText(context.Context, string) (string, error) {
	return c.hash, nil
}

// Adapted from upstream 92d35a694: the hash endpoint can update before its CDN body.
func TestPricingSyncRetriesMismatchedCatalogUntilDataCatchesUp(t *testing.T) {
	oldBody := hotReloadCatalogJSON
	newBody := `{` + hotReloadModelJSON("remote-model", 3e-6, 6e-6) + `}`
	oldHash := fmt.Sprintf("%x", sha256.Sum256([]byte(oldBody)))
	newHash := strings.ToUpper(fmt.Sprintf("%x", sha256.Sum256([]byte(newBody))))
	remote := &retryPricingRemoteClient{body: oldBody, hash: newHash}
	svc := NewPricingService(&config.Config{Pricing: config.PricingConfig{
		DataDir: t.TempDir(), RemoteURL: "https://example.com/pricing.json",
		HashURL: "https://example.com/pricing.sha256",
	}}, remote)

	require.NoError(t, svc.ForceUpdate())
	require.InDelta(t, 1e-6, svc.GetModelPricing("remote-model").InputCostPerToken, 1e-12)
	require.Equal(t, oldHash, svc.localHash)
	savedHash, err := os.ReadFile(svc.getHashFilePath())
	require.NoError(t, err)
	require.Equal(t, oldHash+"\n", string(savedHash))

	remote.body = newBody
	require.NoError(t, svc.syncWithRemote())
	require.Equal(t, 2, remote.downloads)
	require.InDelta(t, 3e-6, svc.GetModelPricing("remote-model").InputCostPerToken, 1e-12)
	require.Equal(t, strings.ToLower(newHash), strings.ToLower(svc.localHash))
	savedBody, err := os.ReadFile(svc.getPricingFilePath())
	require.NoError(t, err)
	require.Equal(t, newBody, string(savedBody))
	savedHash, err = os.ReadFile(svc.getHashFilePath())
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(newHash)+"\n", strings.ToLower(string(savedHash)))

	require.NoError(t, svc.syncWithRemote())
	require.Equal(t, 2, remote.downloads)
}
