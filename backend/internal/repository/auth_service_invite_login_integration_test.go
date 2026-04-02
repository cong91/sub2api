//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	"github.com/Wei-Shaw/sub2api/ent/userallowedgroup"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuthServiceInviteLogin_ProvisionBootstrapRuntimeRows(t *testing.T) {
	t.Run("exclusive standard openai group creates user api_key and allowed_group", func(t *testing.T) {
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
			SortOrder:        -100,
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
		require.Equal(t, openAIGroup.ID, result.BootstrapContext.DefaultGroupID)

		createdUser, err := client.User.Get(ctx, result.User.ID)
		require.NoError(t, err)
		require.Equal(t, service.StatusActive, createdUser.Status)

		createdKey, err := client.APIKey.Query().
			Where(
				apikey.UserIDEQ(result.User.ID),
				apikey.DeletedAtIsNil(),
			).
			Only(ctx)
		require.NoError(t, err)
		require.Equal(t, openAIGroup.ID, createdKey.GroupID)

		allowedCount, err := client.UserAllowedGroup.Query().
			Where(
				userallowedgroup.UserIDEQ(result.User.ID),
				userallowedgroup.GroupIDEQ(openAIGroup.ID),
			).
			Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, allowedCount)

		subCount, err := client.UserSubscription.Query().
			Where(
				usersubscription.UserIDEQ(result.User.ID),
				usersubscription.GroupIDEQ(openAIGroup.ID),
			).
			Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, subCount)
	})

	t.Run("subscription openai group creates user api_key and subscription", func(t *testing.T) {
		ctx := context.Background()
		client := testEntClient(t)
		cleanupInviteLoginTables(t, ctx, client)

		cfg := newInviteLoginIntegrationConfig()
		authService := newInviteLoginIntegrationAuthService(t, client, cfg)

		openAIGroup := mustCreateGroup(t, client, &service.Group{
			Name:                inviteLoginUniqueTestValue(t, "invite-openai-sub"),
			Platform:            service.PlatformOpenAI,
			Status:              service.StatusActive,
			SubscriptionType:    service.SubscriptionTypeSubscription,
			DefaultValidityDays: 7,
			SortOrder:           -100,
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
		require.Equal(t, openAIGroup.ID, result.BootstrapContext.DefaultGroupID)

		_, err = client.User.Get(ctx, result.User.ID)
		require.NoError(t, err)

		createdKey, err := client.APIKey.Query().
			Where(
				apikey.UserIDEQ(result.User.ID),
				apikey.DeletedAtIsNil(),
			).
			Only(ctx)
		require.NoError(t, err)
		require.Equal(t, openAIGroup.ID, createdKey.GroupID)

		sub, err := client.UserSubscription.Query().
			Where(
				usersubscription.UserIDEQ(result.User.ID),
				usersubscription.GroupIDEQ(openAIGroup.ID),
				usersubscription.StatusEQ(service.SubscriptionStatusActive),
			).
			Only(ctx)
		require.NoError(t, err)
		require.True(t, sub.ExpiresAt.After(time.Now()))
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
