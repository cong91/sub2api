package service

import (
	"context"
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
	require.Equal(t, PlatformAnthropic, provider.ProviderID)
	require.Equal(t, "Anthropic", provider.ProviderName)
	require.Equal(t, "anthropic-messages", provider.APIStyle)
	require.NotEmpty(t, provider.DefaultModel)
	require.NotEmpty(t, provider.Models)
	require.NotEmpty(t, provider.Models[0].ID)
	require.NotEmpty(t, provider.Models[0].Name)
	require.Len(t, provider.Sources, 1)
	require.Equal(t, PlatformAntigravity, provider.Sources[0].SourcePlatform)
	require.Equal(t, "compatible", provider.Sources[0].ProtocolRole)
	require.Equal(t, "via_compat", provider.Resolution.SourceKind)
	require.Equal(t, []int64{202}, provider.Resolution.DerivedFromGroups)
}

func TestBuildProviderCatalogForGroups_MultiGroupAggregates(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 101, Platform: PlatformOpenAI, Hydrated: true},
		{ID: 202, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude", "gemini_text", "gemini_image"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.Len(t, resp.Providers, 3)

	providerIDs := make(map[string]struct{}, len(resp.Providers))
	for _, provider := range resp.Providers {
		providerIDs[provider.ProviderID] = struct{}{}
	}
	_, hasOpenAI := providerIDs[PlatformOpenAI]
	_, hasAnthropic := providerIDs[PlatformAnthropic]
	_, hasGemini := providerIDs[PlatformGemini]
	require.True(t, hasOpenAI)
	require.True(t, hasAnthropic)
	require.True(t, hasGemini)
}

func TestBuildProviderCatalogForGroups_AnthropicPlatformDoesNotMaterializeGeminiLaneFromScopesAlone(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 303, Name: "Antigravity", Platform: PlatformAnthropic, Hydrated: true, SupportedModelScopes: []string{"claude", "gemini_text", "gemini_image"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.Len(t, resp.Providers, 1)
	require.Equal(t, PlatformAnthropic, resp.Providers[0].ProviderID)
	require.Len(t, resp.Providers[0].Sources, 1)
	require.Equal(t, PlatformAnthropic, resp.Providers[0].Sources[0].SourcePlatform)
	require.Equal(t, []string{"claude", "gemini_text", "gemini_image"}, resp.Providers[0].Resolution.DerivedFromScopes)
}

func TestBuildProviderCatalogForGroups_AnthropicPlatformSemanticallySupportsClaudeOnly(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 304, Platform: PlatformAnthropic, Hydrated: true, SupportedModelScopes: []string{"gemini_text", "gemini_image"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.Len(t, resp.Providers, 1)
	provider := resp.Providers[0]
	require.Equal(t, PlatformAnthropic, provider.ProviderID)
	require.Equal(t, PlatformAnthropic, provider.Sources[0].SourcePlatform)
	for _, model := range provider.Models {
		require.Equal(t, "claude", model.Family)
	}
}

func TestBuildProviderCatalogForGroups_ForcedPlatformFilters(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 101, Platform: PlatformOpenAI, Hydrated: true},
		{ID: 202, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude", "gemini_text"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.NotEmpty(t, resp.Providers)

	for _, provider := range resp.Providers {
		require.Equal(t, PlatformOpenAI, provider.ProviderID)
	}
}

func TestBuildProviderCatalogForGroups_NativeAnthropicAndAntigravityClaudeMergeIntoMixedLane(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 301, Platform: PlatformAnthropic, Hydrated: true},
		{ID: 302, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"claude"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.Len(t, resp.Providers, 1)

	provider := resp.Providers[0]
	require.Equal(t, PlatformAnthropic, provider.ProviderID)
	require.Len(t, provider.Sources, 2)
	require.Equal(t, "mixed", provider.Resolution.SourceKind)
	require.ElementsMatch(t, []int64{301, 302}, provider.Resolution.DerivedFromGroups)
}

func TestBuildProviderCatalogForGroups_AntigravityGeminiLaneUsesAntigravityModelSet(t *testing.T) {
	svc := &GatewayService{}
	groups := []*Group{
		{ID: 401, Platform: PlatformAntigravity, Hydrated: true, SupportedModelScopes: []string{"gemini_text", "gemini_image"}},
	}

	resp, err := svc.BuildProviderCatalogForGroups(context.Background(), groups, "")
	require.NoError(t, err)
	require.Equal(t, "provider_catalog", resp.Object)
	require.Len(t, resp.Providers, 1)

	provider := resp.Providers[0]
	require.Equal(t, PlatformGemini, provider.ProviderID)
	require.Len(t, provider.Sources, 1)
	require.Equal(t, PlatformAntigravity, provider.Sources[0].SourcePlatform)
	require.Equal(t, "Antigravity Gemini", provider.Sources[0].SourceLabel)
	require.Equal(t, "compatible", provider.Sources[0].ProtocolRole)
	require.NotEmpty(t, provider.Models)

	modelIDs := make([]string, 0, len(provider.Models))
	for _, model := range provider.Models {
		modelIDs = append(modelIDs, model.ID)
		require.Equal(t, "gemini", model.Family)
	}
	require.Contains(t, modelIDs, "gemini-3-flash")
	require.Contains(t, modelIDs, "gemini-3-pro-high")
	require.NotContains(t, modelIDs, "gemini-2.0-flash")
}
