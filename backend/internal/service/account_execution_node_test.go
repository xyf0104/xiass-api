package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestApplyExecutionNodeForCreateDisabledPreservesLegacyBehavior(t *testing.T) {
	extra := map[string]any{AccountExecutionNodeExtraKey: "imported", "custom": true}

	gotExtra, gotProxyID := applyExecutionNodeForCreate(&config.Config{}, extra, nil)

	require.Equal(t, extra, gotExtra)
	require.Nil(t, gotProxyID)
	require.NotContains(t, gotExtra, AccountExecutionNodeExtraKey)
	require.Equal(t, true, gotExtra["custom"])
}

func TestApplyExecutionNodeForCreateUsesServerOwnedNodeAndDefaultProxy(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:        true,
		ID:             " api2 ",
		DefaultProxyID: 83,
	}

	gotExtra, gotProxyID := applyExecutionNodeForCreate(cfg, map[string]any{
		AccountExecutionNodeExtraKey: "forged-node",
		"custom":                     true,
	}, nil)

	require.Equal(t, "api2", gotExtra[AccountExecutionNodeExtraKey])
	require.Equal(t, true, gotExtra["custom"])
	require.NotNil(t, gotProxyID)
	require.Equal(t, int64(83), *gotProxyID)
}

func TestApplyExecutionNodeForCreateOverridesImportedAccountProxy(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:        true,
		ID:             "api",
		DefaultProxyID: 84,
	}
	explicitProxyID := int64(21)

	gotExtra, gotProxyID := applyExecutionNodeForCreate(cfg, nil, &explicitProxyID)

	require.Equal(t, "api", gotExtra[AccountExecutionNodeExtraKey])
	require.NotNil(t, gotProxyID)
	require.Equal(t, int64(84), *gotProxyID)
}

func TestTeamChildCreateBelongsToTheNodeThatAcceptsOAuth(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:        true,
		ID:             "api2",
		DefaultProxyID: 83,
	}
	foreignProxyID := int64(84)

	extra, proxyID := applyExecutionNodeForCreate(cfg, map[string]any{
		OpenAITeamChildExtraKey:      true,
		OpenAITeamChildEmailExtraKey: "team1004@example.test",
		AccountExecutionNodeExtraKey: "api",
	}, &foreignProxyID)

	require.Equal(t, true, extra[OpenAITeamChildExtraKey])
	require.Equal(t, "team1004@example.test", extra[OpenAITeamChildEmailExtraKey])
	require.Equal(t, "api2", extra[AccountExecutionNodeExtraKey])
	require.NotNil(t, proxyID)
	require.Equal(t, int64(83), *proxyID)
}

func TestPreserveExecutionNodeOnUpdateRejectsReplacementAndClear(t *testing.T) {
	account := &Account{Extra: map[string]any{
		AccountExecutionNodeExtraKey: "api",
		"old":                        true,
	}}

	updated := preserveExecutionNodeOnUpdate(account, map[string]any{
		AccountExecutionNodeExtraKey: "api2",
		"new":                        true,
	})
	require.Equal(t, "api", updated[AccountExecutionNodeExtraKey])
	require.Equal(t, true, updated["new"])

	cleared := preserveExecutionNodeOnUpdate(account, map[string]any{})
	require.Equal(t, "api", cleared[AccountExecutionNodeExtraKey])
}

func TestAccountExecutionNodeIDUsesLegacyOwnerForMissingOrMalformedValues(t *testing.T) {
	require.Equal(t, "api", (&Account{}).ExecutionNodeID(" api "))
	require.Equal(t, "api", (&Account{Extra: map[string]any{AccountExecutionNodeExtraKey: 7}}).ExecutionNodeID("api"))
	require.Equal(t, "api", (&Account{Extra: map[string]any{AccountExecutionNodeExtraKey: "bad node"}}).ExecutionNodeID("api"))
	require.Equal(t, "api2", (&Account{Extra: map[string]any{AccountExecutionNodeExtraKey: " api2 "}}).ExecutionNodeID("api"))
}

func TestCRSCreateUsesLocalNodeButUpdateKeepsDurableOwner(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:        true,
		ID:             "api2",
		DefaultProxyID: 83,
	}
	service := &CRSSyncService{cfg: cfg}

	createdExtra, createdProxy := service.prepareCRSExecutionNode(nil, map[string]any{
		AccountExecutionNodeExtraKey: "forged",
	}, nil)
	require.Equal(t, "api2", createdExtra[AccountExecutionNodeExtraKey])
	require.NotNil(t, createdProxy)
	require.Equal(t, int64(83), *createdProxy)

	existingProxy := int64(84)
	existing := &Account{
		ProxyID: &existingProxy,
		Extra:   map[string]any{AccountExecutionNodeExtraKey: "api"},
	}
	updatedExtra, updatedProxy := service.prepareCRSExecutionNode(existing, map[string]any{
		AccountExecutionNodeExtraKey: "api2",
	}, nil)
	require.Equal(t, "api", updatedExtra[AccountExecutionNodeExtraKey])
	require.NotNil(t, updatedProxy)
	require.Equal(t, int64(84), *updatedProxy)
}

func TestDuplicateAndShadowOwnershipStayWithSourceAccount(t *testing.T) {
	extra, err := duplicateAccountExtra(map[string]any{
		AccountExecutionNodeExtraKey: "api2",
		"custom":                     true,
	})
	require.NoError(t, err)
	require.Equal(t, "api2", extra[AccountExecutionNodeExtraKey])

	parent := &Account{Extra: map[string]any{AccountExecutionNodeExtraKey: "api"}}
	shadowExtra := preserveExecutionNodeOnUpdate(parent, map[string]any{"shadow": true})
	require.Equal(t, "api", shadowExtra[AccountExecutionNodeExtraKey])
}
