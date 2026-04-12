//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type inviteRedeemRepoStub struct {
	*redeemRepoStub
	codes  map[string]*RedeemCode
	used   map[int64]int64
	useErr error
}

func (s *inviteRedeemRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	if s.codes == nil {
		return nil, errors.New("not found")
	}
	item, ok := s.codes[code]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := *item
	return &copy, nil
}

func (s *inviteRedeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	if s.useErr != nil {
		return s.useErr
	}
	if s.used == nil {
		s.used = make(map[int64]int64)
	}
	s.used[id] = userID
	return nil
}

type inviteGroupRepoStub struct {
	*mockGroupRepoForGemini
	groupsByPlatform map[string][]Group
}

func (s *inviteGroupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]Group, error) {
	items := s.groupsByPlatform[platform]
	out := make([]Group, 0, len(items))
	for i := range items {
		out = append(out, items[i])
	}
	return out, nil
}

type inviteAccountRepoStub struct {
	*mockAccountRepoForGemini
	accountsByGroup map[int64][]Account
}

func (s *inviteAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	items := s.accountsByGroup[groupID]
	out := make([]Account, 0, len(items))
	for i := range items {
		if items[i].Platform == platform && items[i].IsSchedulable() {
			out = append(out, items[i])
		}
	}
	return out, nil
}

type inviteAPIKeyCreatorStub struct {
	nextID     int64
	failByName map[string]error
	requests   []CreateAPIKeyRequest
}

func (s *inviteAPIKeyCreatorStub) Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	s.requests = append(s.requests, req)
	if s.failByName != nil {
		if err, ok := s.failByName[req.Name]; ok {
			return nil, err
		}
	}
	s.nextID++
	keyGroupID := int64(0)
	if req.GroupID != nil {
		keyGroupID = *req.GroupID
	}
	return &APIKey{
		ID:      s.nextID,
		UserID:  userID,
		Name:    req.Name,
		Key:     fmt.Sprintf("sk-%d-%d", userID, s.nextID),
		GroupID: req.GroupID,
		Status:  StatusActive,
		Group:   &Group{ID: keyGroupID},
	}, nil
}

type inviteRefreshTokenCacheStub struct{}

func (s *inviteRefreshTokenCacheStub) StoreRefreshToken(ctx context.Context, tokenHash string, data *RefreshTokenData, ttl time.Duration) error {
	return nil
}

func (s *inviteRefreshTokenCacheStub) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshTokenData, error) {
	return nil, ErrRefreshTokenNotFound
}

func (s *inviteRefreshTokenCacheStub) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	return nil
}

func (s *inviteRefreshTokenCacheStub) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	return nil
}

func (s *inviteRefreshTokenCacheStub) DeleteTokenFamily(ctx context.Context, familyID string) error {
	return nil
}

func (s *inviteRefreshTokenCacheStub) AddToUserTokenSet(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) error {
	return nil
}

func (s *inviteRefreshTokenCacheStub) AddToFamilyTokenSet(ctx context.Context, familyID string, tokenHash string, ttl time.Duration) error {
	return nil
}

func (s *inviteRefreshTokenCacheStub) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	return nil, nil
}

func (s *inviteRefreshTokenCacheStub) GetFamilyTokenHashes(ctx context.Context, familyID string) ([]string, error) {
	return nil, nil
}

func (s *inviteRefreshTokenCacheStub) IsTokenInFamily(ctx context.Context, familyID string, tokenHash string) (bool, error) {
	return false, nil
}

func newAuthServiceForInviteLogin(
	userRepo *userRepoStub,
	redeemRepo RedeemCodeRepository,
	groupRepo GroupRepository,
	accountRepo AccountRepository,
	apiKeyCreator InviteAPIKeyCreator,
) *AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "invite-login-test-secret",
			ExpireHour: 1,
		},
		Default: config.DefaultConfig{
			UserBalance:     2,
			UserConcurrency: 1,
		},
	}

	settingService := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}}, cfg)

	svc := NewAuthService(
		nil,
		userRepo,
		redeemRepo,
		&inviteRefreshTokenCacheStub{},
		cfg,
		settingService,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	svc.ConfigureInviteLoginDependencies(groupRepo, accountRepo, apiKeyCreator, nil)
	return svc
}

func TestAuthService_InviteLogin_SuccessCreatesBootstrapUserAndKeys(t *testing.T) {
	ctx := context.Background()
	userRepo := &userRepoStub{nextID: 101}
	redeemRepo := &inviteRedeemRepoStub{
		redeemRepoStub: &redeemRepoStub{},
		codes: map[string]*RedeemCode{
			"invite-ok": {ID: 7, Code: "invite-ok", Type: RedeemTypeInvitation, Status: StatusUnused},
		},
	}
	groupRepo := &inviteGroupRepoStub{
		mockGroupRepoForGemini: &mockGroupRepoForGemini{},
		groupsByPlatform: map[string][]Group{
			PlatformOpenAI: {
				{ID: 11, Name: "openai-free", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0, SubscriptionType: SubscriptionTypeStandard},
				{ID: 12, Name: "openai-paid", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0.4, SubscriptionType: SubscriptionTypeStandard},
			},
			PlatformAnthropic: {
				{ID: 21, Name: "anthropic-paid", Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 0.3, SubscriptionType: SubscriptionTypeStandard},
			},
		},
	}
	accountRepo := &inviteAccountRepoStub{
		mockAccountRepoForGemini: &mockAccountRepoForGemini{},
		accountsByGroup: map[int64][]Account{
			11: {{ID: 1001, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 1}},
			12: {{ID: 1002, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 1}},
			21: {{ID: 2001, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 1}},
		},
	}
	keyCreator := &inviteAPIKeyCreatorStub{}

	svc := newAuthServiceForInviteLogin(userRepo, redeemRepo, groupRepo, accountRepo, keyCreator)
	result, err := svc.InviteLogin(ctx, "invite-ok")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.User)
	require.NotNil(t, result.TokenPair)
	require.NotEmpty(t, result.TokenPair.AccessToken)
	require.NotEmpty(t, result.TokenPair.RefreshToken)
	require.True(t, strings.HasSuffix(result.User.Email, "@bootstrap.local"))
	require.GreaterOrEqual(t, len(result.BootstrapAPIKeys), 1)

	require.Equal(t, int64(101), result.User.ID)
	require.Equal(t, int64(101), redeemRepo.used[7])

	var openAIKey *InviteBootstrapAPIKey
	for i := range result.BootstrapAPIKeys {
		item := result.BootstrapAPIKeys[i]
		if item.Platform == PlatformOpenAI {
			openAIKey = &item
		}
		require.Positive(t, item.GroupID)
	}
	require.NotNil(t, openAIKey)
	require.Equal(t, int64(11), openAIKey.GroupID)
	for _, req := range keyCreator.requests {
		require.NotNil(t, req.GroupID)
	}
}

func TestAuthService_InviteLogin_MissingInvitationCodeRejected(t *testing.T) {
	ctx := context.Background()
	userRepo := &userRepoStub{}
	redeemRepo := &inviteRedeemRepoStub{redeemRepoStub: &redeemRepoStub{}}
	svc := newAuthServiceForInviteLogin(userRepo, redeemRepo, &inviteGroupRepoStub{mockGroupRepoForGemini: &mockGroupRepoForGemini{}}, &inviteAccountRepoStub{mockAccountRepoForGemini: &mockAccountRepoForGemini{}}, &inviteAPIKeyCreatorStub{})

	_, err := svc.InviteLogin(ctx, "   ")
	require.ErrorIs(t, err, ErrInvitationCodeRequired)
	require.Empty(t, userRepo.created)
}

func TestAuthService_InviteLogin_InvalidInvitationTypeOrStatusRejected(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		redeemCode *RedeemCode
	}{
		{name: "wrong type", redeemCode: &RedeemCode{ID: 1, Code: "x", Type: RedeemTypeBalance, Status: StatusUnused}},
		{name: "used", redeemCode: &RedeemCode{ID: 2, Code: "y", Type: RedeemTypeInvitation, Status: StatusUsed}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userRepo := &userRepoStub{}
			redeemRepo := &inviteRedeemRepoStub{
				redeemRepoStub: &redeemRepoStub{},
				codes:          map[string]*RedeemCode{tt.redeemCode.Code: tt.redeemCode},
			}
			svc := newAuthServiceForInviteLogin(userRepo, redeemRepo, &inviteGroupRepoStub{mockGroupRepoForGemini: &mockGroupRepoForGemini{}}, &inviteAccountRepoStub{mockAccountRepoForGemini: &mockAccountRepoForGemini{}}, &inviteAPIKeyCreatorStub{})

			_, err := svc.InviteLogin(ctx, tt.redeemCode.Code)
			require.ErrorIs(t, err, ErrInvitationCodeInvalid)
			require.Empty(t, userRepo.created)
		})
	}
}

func TestAuthService_InviteLogin_DeterministicTieBreakPrefersLowerGroupID(t *testing.T) {
	ctx := context.Background()
	userRepo := &userRepoStub{nextID: 102}
	redeemRepo := &inviteRedeemRepoStub{
		redeemRepoStub: &redeemRepoStub{},
		codes: map[string]*RedeemCode{
			"invite-tie": {ID: 8, Code: "invite-tie", Type: RedeemTypeInvitation, Status: StatusUnused},
		},
	}
	groupRepo := &inviteGroupRepoStub{
		mockGroupRepoForGemini: &mockGroupRepoForGemini{},
		groupsByPlatform: map[string][]Group{
			PlatformGemini: {
				{ID: 31, Name: "gemini-a", Platform: PlatformGemini, Status: StatusActive, RateMultiplier: 0.2, SubscriptionType: SubscriptionTypeStandard},
				{ID: 32, Name: "gemini-b", Platform: PlatformGemini, Status: StatusActive, RateMultiplier: 0.2, SubscriptionType: SubscriptionTypeStandard},
			},
		},
	}
	accountRepo := &inviteAccountRepoStub{
		mockAccountRepoForGemini: &mockAccountRepoForGemini{},
		accountsByGroup: map[int64][]Account{
			31: {{ID: 9001, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 1}},
			32: {{ID: 8001, Platform: PlatformGemini, Status: StatusActive, Schedulable: true, Priority: 1}},
		},
	}
	keyCreator := &inviteAPIKeyCreatorStub{}

	svc := newAuthServiceForInviteLogin(userRepo, redeemRepo, groupRepo, accountRepo, keyCreator)
	result, err := svc.InviteLogin(ctx, "invite-tie")
	require.NoError(t, err)
	require.Len(t, result.BootstrapAPIKeys, 1)
	require.Equal(t, PlatformGemini, result.BootstrapAPIKeys[0].Platform)
	require.Equal(t, int64(31), result.BootstrapAPIKeys[0].GroupID)
}

func TestAuthService_InviteLogin_FailsWhenNoBootstrapKeyProvisioned(t *testing.T) {
	ctx := context.Background()
	userRepo := &userRepoStub{nextID: 103}
	redeemRepo := &inviteRedeemRepoStub{
		redeemRepoStub: &redeemRepoStub{},
		codes: map[string]*RedeemCode{
			"invite-nokey": {ID: 9, Code: "invite-nokey", Type: RedeemTypeInvitation, Status: StatusUnused},
		},
	}
	groupRepo := &inviteGroupRepoStub{
		mockGroupRepoForGemini: &mockGroupRepoForGemini{},
		groupsByPlatform: map[string][]Group{
			PlatformOpenAI: {
				{ID: 41, Name: "openai-one", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0, SubscriptionType: SubscriptionTypeStandard},
			},
		},
	}
	accountRepo := &inviteAccountRepoStub{
		mockAccountRepoForGemini: &mockAccountRepoForGemini{},
		accountsByGroup: map[int64][]Account{
			41: {{ID: 4101, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 1}},
		},
	}
	keyCreator := &inviteAPIKeyCreatorStub{failByName: map[string]error{"bootstrap-openai": errors.New("create failed")}}

	svc := newAuthServiceForInviteLogin(userRepo, redeemRepo, groupRepo, accountRepo, keyCreator)
	_, err := svc.InviteLogin(ctx, "invite-nokey")
	require.ErrorIs(t, err, ErrServiceUnavailable)
}

func TestAuthService_InviteLogin_PartialProvisioningSucceeds(t *testing.T) {
	ctx := context.Background()
	userRepo := &userRepoStub{nextID: 104}
	redeemRepo := &inviteRedeemRepoStub{
		redeemRepoStub: &redeemRepoStub{},
		codes: map[string]*RedeemCode{
			"invite-partial": {ID: 10, Code: "invite-partial", Type: RedeemTypeInvitation, Status: StatusUnused},
		},
	}
	groupRepo := &inviteGroupRepoStub{
		mockGroupRepoForGemini: &mockGroupRepoForGemini{},
		groupsByPlatform: map[string][]Group{
			PlatformOpenAI: {
				{ID: 51, Name: "openai-free", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 0, SubscriptionType: SubscriptionTypeStandard},
			},
			PlatformAnthropic: {
				{ID: 61, Name: "anthropic-paid", Platform: PlatformAnthropic, Status: StatusActive, RateMultiplier: 0.2, SubscriptionType: SubscriptionTypeStandard},
			},
		},
	}
	accountRepo := &inviteAccountRepoStub{
		mockAccountRepoForGemini: &mockAccountRepoForGemini{},
		accountsByGroup: map[int64][]Account{
			51: {{ID: 5101, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Priority: 1}},
			61: {{ID: 6101, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Priority: 1}},
		},
	}
	keyCreator := &inviteAPIKeyCreatorStub{failByName: map[string]error{"bootstrap-anthropic": errors.New("down")}}

	svc := newAuthServiceForInviteLogin(userRepo, redeemRepo, groupRepo, accountRepo, keyCreator)
	result, err := svc.InviteLogin(ctx, "invite-partial")
	require.NoError(t, err)
	require.Len(t, result.BootstrapAPIKeys, 1)
	require.Equal(t, PlatformOpenAI, result.BootstrapAPIKeys[0].Platform)
	require.Equal(t, int64(51), result.BootstrapAPIKeys[0].GroupID)
}
