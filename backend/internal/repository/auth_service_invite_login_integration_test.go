//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/apikeygroup"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/ent/userallowedgroup"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuthServiceInviteLogin_ProvisionBootstrapRuntimeRows(t *testing.T) {
	t.Run("multi-provider bootstrap creates per-provider api keys", func(t *testing.T) {
		ctx := context.Background()
		client := testEntClient(t)
		cleanupInviteLoginTables(t, ctx, client)

		cfg := newInviteLoginIntegrationConfig()
		authService := newInviteLoginIntegrationAuthService(t, client, cfg)

		openAIGroup := mustCreateGroup(t, client, &service.Group{
			Name:             inviteLoginUniqueTestValue(t, "invite-openai-standard"),
			Platform:         service.PlatformOpenAI,
			Status:           service.StatusActive,
			SubscriptionType: service.SubscriptionTypeStandard,
			IsExclusive:      true,
			RateMultiplier:   1.0,
		})

		anthropicGroup := mustCreateGroup(t, client, &service.Group{
			Name:             inviteLoginUniqueTestValue(t, "invite-anthropic-standard"),
			Platform:         service.PlatformAnthropic,
			Status:           service.StatusActive,
			SubscriptionType: service.SubscriptionTypeStandard,
			RateMultiplier:   0.8,
		})

		antigravityGroup := mustCreateGroup(t, client, &service.Group{
			Name:             inviteLoginUniqueTestValue(t, "invite-antigravity-standard"),
			Platform:         service.PlatformAntigravity,
			Status:           service.StatusActive,
			SubscriptionType: service.SubscriptionTypeStandard,
			RateMultiplier:   0.9,
		})

		inviteCode := mustCreateRedeemCode(t, client, &service.RedeemCode{
			Code:   inviteLoginUniqueTestValue(t, "INVITE-STD"),
			Type:   service.RedeemTypeInvitation,
			Status: service.StatusUnused,
		})

		result, err := authService.InviteLogin(ctx, inviteCode.Code)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		require.Equal(t, service.PlatformOpenAI, result.BootstrapContext.DefaultProviderID)
		require.Len(t, result.BootstrapContext.Providers, 4)

		createdUser, err := client.User.Get(ctx, result.User.ID)
		require.NoError(t, err)
		require.Equal(t, service.StatusActive, createdUser.Status)

		createdKeys, err := client.APIKey.Query().
			Where(
				apikey.UserIDEQ(result.User.ID),
				apikey.DeletedAtIsNil(),
			).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, createdKeys, 4)

		keysByName := make(map[string]int64, len(createdKeys))
		keysByNameToID := make(map[string]int64, len(createdKeys))
		for _, key := range createdKeys {
			require.NotNil(t, key.GroupID)
			keysByName[key.Name] = *key.GroupID
			keysByNameToID[key.Name] = key.ID
		}
		require.Equal(t, map[string]int64{
			"default-openai":             openAIGroup.ID,
			"default-anthropic":          anthropicGroup.ID,
			"default-antigravity-claude": antigravityGroup.ID,
			"default-antigravity-gemini": antigravityGroup.ID,
		}, keysByName)

		type expectedGrant struct {
			name     string
			groupIDs []int64
		}
		expectedGrants := []expectedGrant{
			{name: "default-openai", groupIDs: []int64{openAIGroup.ID}},
			{name: "default-anthropic", groupIDs: []int64{openAIGroup.ID, anthropicGroup.ID}},
			{name: "default-antigravity-claude", groupIDs: []int64{openAIGroup.ID, anthropicGroup.ID, antigravityGroup.ID}},
			{name: "default-antigravity-gemini", groupIDs: []int64{openAIGroup.ID, anthropicGroup.ID, antigravityGroup.ID}},
		}
		for _, expected := range expectedGrants {
			keyID, ok := keysByNameToID[expected.name]
			require.True(t, ok, "missing key %s", expected.name)
			links, err := client.APIKeyGroup.Query().
				Where(apikeygroup.APIKeyIDEQ(keyID)).
				All(ctx)
			require.NoError(t, err)
			actualIDs := make([]int64, 0, len(links))
			for _, link := range links {
				actualIDs = append(actualIDs, link.GroupID)
			}
			require.ElementsMatch(t, expected.groupIDs, actualIDs)
		}

		allowedCount, err := client.UserAllowedGroup.Query().
			Where(
				userallowedgroup.UserIDEQ(result.User.ID),
				userallowedgroup.GroupIDEQ(openAIGroup.ID),
			).
			Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, allowedCount)

		anthropicAllowedCount, err := client.UserAllowedGroup.Query().
			Where(
				userallowedgroup.UserIDEQ(result.User.ID),
				userallowedgroup.GroupIDEQ(anthropicGroup.ID),
			).
			Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, anthropicAllowedCount)

		subCount, err := client.UserSubscription.Query().
			Where(
				usersubscription.UserIDEQ(result.User.ID),
				usersubscription.GroupIDIn(openAIGroup.ID, anthropicGroup.ID, antigravityGroup.ID),
			).
			Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, subCount)
	})

	t.Run("subscription groups are ignored by invite bootstrap selection", func(t *testing.T) {
		ctx := context.Background()
		client := testEntClient(t)
		cleanupInviteLoginTables(t, ctx, client)

		cfg := newInviteLoginIntegrationConfig()
		authService := newInviteLoginIntegrationAuthService(t, client, cfg)

		_ = mustCreateGroup(t, client, &service.Group{
			Name:                inviteLoginUniqueTestValue(t, "invite-openai-sub"),
			Platform:            service.PlatformOpenAI,
			Status:              service.StatusActive,
			SubscriptionType:    service.SubscriptionTypeSubscription,
			DefaultValidityDays: 7,
			RateMultiplier:      0.5,
		})

		openAIStandardGroup := mustCreateGroup(t, client, &service.Group{
			Name:             inviteLoginUniqueTestValue(t, "invite-openai-std"),
			Platform:         service.PlatformOpenAI,
			Status:           service.StatusActive,
			SubscriptionType: service.SubscriptionTypeStandard,
			RateMultiplier:   1.2,
		})

		inviteCode := mustCreateRedeemCode(t, client, &service.RedeemCode{
			Code:   inviteLoginUniqueTestValue(t, "INVITE-SUB"),
			Type:   service.RedeemTypeInvitation,
			Status: service.StatusUnused,
		})

		result, err := authService.InviteLogin(ctx, inviteCode.Code)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.User)
		require.Equal(t, service.PlatformOpenAI, result.BootstrapContext.DefaultProviderID)
		require.Len(t, result.BootstrapContext.Providers, 1)
		require.Equal(t, openAIStandardGroup.ID, result.BootstrapContext.Providers[0].DefaultGroupID)

		_, err = client.User.Get(ctx, result.User.ID)
		require.NoError(t, err)

		createdKey, err := client.APIKey.Query().
			Where(
				apikey.UserIDEQ(result.User.ID),
				apikey.DeletedAtIsNil(),
			).
			Only(ctx)
		require.NoError(t, err)
		require.NotNil(t, createdKey.GroupID)
		require.Equal(t, openAIStandardGroup.ID, *createdKey.GroupID)
		require.Equal(t, "default-openai", createdKey.Name)

		subCount, err := client.UserSubscription.Query().
			Where(
				usersubscription.UserIDEQ(result.User.ID),
				usersubscription.StatusEQ(service.SubscriptionStatusActive),
			).
			Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, subCount)
	})
}

func newInviteLoginIntegrationConfig() *config.Config {
	return &config.Config{
		JWT: config.JWTConfig{
			Secret:     "integration-test-secret",
			ExpireHour: 1,
		},
		Default: config.DefaultConfig{
			UserBalance:     0,
			UserConcurrency: 1,
		},
	}
}

func newInviteLoginIntegrationAuthService(t *testing.T, client *dbent.Client, cfg *config.Config) *service.AuthService {
	t.Helper()

	userRepo := NewUserRepository(client, integrationDB)
	redeemRepo := NewRedeemCodeRepository(client)
	groupRepo := NewGroupRepository(client, integrationDB)
	apiKeyRepo := NewAPIKeyRepository(client, integrationDB)
	userSubRepo := NewUserSubscriptionRepository(client)
	settingRepo := NewSettingRepository(client)

	require.NoError(t, settingRepo.Set(context.Background(), service.SettingKeyInvitationCodeEnabled, "true"))

	settingService := service.NewSettingService(settingRepo, cfg)
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, userRepo, groupRepo, userSubRepo, nil, nil, cfg)
	subscriptionService := service.NewSubscriptionService(groupRepo, userSubRepo, nil, client, cfg)

	return service.NewAuthService(
		client,
		userRepo,
		redeemRepo,
		groupRepo,
		apiKeyService,
		nil,
		cfg,
		settingService,
		nil,
		nil,
		nil,
		nil,
		subscriptionService,
	)
}

func cleanupInviteLoginTables(t *testing.T, ctx context.Context, client *dbent.Client) {
	t.Helper()

	_, err := client.APIKey.Delete().Where(apikey.Not(apikey.IDEQ(0))).Exec(ctx)
	require.NoError(t, err)
	_, err = client.UserSubscription.Delete().Where(usersubscription.Not(usersubscription.IDEQ(0))).Exec(ctx)
	require.NoError(t, err)
	_, err = client.UserAllowedGroup.Delete().Exec(ctx)
	require.NoError(t, err)
	_, err = client.RedeemCode.Delete().Where(redeemcode.Not(redeemcode.IDEQ(0))).Exec(ctx)
	require.NoError(t, err)
	_, err = client.Group.Delete().Where(group.Not(group.IDEQ(0))).Exec(ctx)
	require.NoError(t, err)
	_, err = client.User.Delete().Exec(ctx)
	require.NoError(t, err)
}

func inviteLoginUniqueTestValue(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
