package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildProviderCatalog_OpenAIContract(t *testing.T) {
	svc := &GatewayService{}
	group := &Group{ID: 101, Platform: PlatformOpenAI, Hydrated: true}

	resp, err := svc.BuildProviderCatalog(context.Background(), group, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.Len(t, resp.Providers, 1)

	provider := resp.Providers[0]
	require.Equal(t, PlatformOpenAI, provider.ProviderID)
	require.Equal(t, "OpenAI", provider.ProviderName)
	require.Equal(t, "openai-responses", provider.APIStyle)
	require.NotEmpty(t, provider.DefaultModel)
	require.NotEmpty(t, provider.Models)
	require.NotEmpty(t, provider.Models[0].ID)
	require.NotEmpty(t, provider.Models[0].Name)
}

func TestBuildProviderCatalog_AntigravityContract(t *testing.T) {
	svc := &GatewayService{}
	group := &Group{ID: 202, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude"}}

	resp, err := svc.BuildProviderCatalog(context.Background(), group, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.NotEmpty(t, resp.Providers)

	provider := resp.Providers[0]
	require.Contains(t, provider.ProviderID, "antigravity")
	require.NotEmpty(t, provider.ProviderName)
	require.NotEmpty(t, provider.APIStyle)
	require.NotEmpty(t, provider.DefaultModel)
	require.NotEmpty(t, provider.Models)
	require.NotEmpty(t, provider.Models[0].ID)
	require.NotEmpty(t, provider.Models[0].Name)
}

func TestBuildProviderCatalogForGroups_MultiGroupAggregates(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 101, Platform: PlatformOpenAI, Hydrated: true},
		{ID: 202, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.GreaterOrEqual(t, len(resp.Providers), 2)

	providerIDs := make(map[string]struct{}, len(resp.Providers))
	for _, provider := range resp.Providers {
		providerIDs[provider.ProviderID] = struct{}{}
	}
	_, hasOpenAI := providerIDs[PlatformOpenAI]
	hasAntigravity := false
	for providerID := range providerIDs {
		if providerID == PlatformAntigravity || strings.Contains(providerID, PlatformAntigravity) {
			hasAntigravity = true
			break
		}
	}
	require.True(t, hasOpenAI)
	require.True(t, hasAntigravity)
}

func TestBuildProviderCatalogForGroups_ForcedPlatformFilters(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 101, Platform: PlatformOpenAI, Hydrated: true},
		{ID: 202, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.NotEmpty(t, resp.Providers)

	for _, provider := range resp.Providers {
		require.Equal(t, PlatformOpenAI, provider.ProviderID)
	}
}
