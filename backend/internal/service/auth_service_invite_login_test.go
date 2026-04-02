//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type inviteRedeemRepoStub struct {
	codeByCode map[string]*RedeemCode
	getErrByCode map[string]error
	useErr error
	useCalls int
	usedID int64
	usedByUserID int64
}

type inviteGroupResolverStub struct {
	groups           []Group
	groupsByPlatform map[string][]Group
	err              error
	lastPlatform string
	calls int
}

func (s *inviteGroupResolverStub) ListActiveByPlatform(_ context.Context, platform string) ([]Group, error) {
	s.calls++
	s.lastPlatform = platform
	if s.err != nil {
		return nil, s.err
	}
	if s.groupsByPlatform != nil {
		return s.groupsByPlatform[platform], nil
	}
	return s.groups, nil
}

type inviteAPIKeyProvisionerStub struct {
	err error
	calls int
	createdForUserID int64
	lastReq CreateAPIKeyRequest
}

func (s *inviteAPIKeyProvisionerStub) Create(_ context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error) {
	s.calls++
	s.createdForUserID = userID
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &APIKey{ID: 1, UserID: userID, GroupID: req.GroupID, Name: req.Name, Status: StatusActive}, nil
}

type inviteAwareUserRepoStub struct {
	userRepoStub
	addAllowedGroupCalls int
	lastAddAllowedUserID int64
	lastAddAllowedGroupID int64
	addAllowedErr error
}

func (s *inviteAwareUserRepoStub) AddGroupToAllowedGroups(_ context.Context, userID int64, groupID int64) error {
	s.addAllowedGroupCalls++
	s.lastAddAllowedUserID = userID
	s.lastAddAllowedGroupID = groupID
	if s.addAllowedErr != nil {
		return s.addAllowedErr
	}
	return nil
}

type oauthInviteUserRepoStub struct {
	usersByEmail map[string]*User
	nextID       int64
	createErr    error
	deleteErr    error
	deletedIDs   []int64
	created      []*User
}

func (s *oauthInviteUserRepoStub) Create(ctx context.Context, user *User) error {
	if s.createErr != nil {
		return s.createErr
	}
	if user.ID == 0 {
		s.nextID++
		user.ID = s.nextID
	}
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*User)
	}
	cp := *user
	s.usersByEmail[user.Email] = &cp
	s.created = append(s.created, user)
	return nil
}

func (s *oauthInviteUserRepoStub) GetByID(ctx context.Context, id int64) (*User, error) {
	for _, u := range s.usersByEmail {
		if u.ID == id {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrUserNotFound
}

func (s *oauthInviteUserRepoStub) GetByEmail(ctx context.Context, email string) (*User, error) {
	u, ok := s.usersByEmail[email]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func (s *oauthInviteUserRepoStub) GetFirstAdmin(ctx context.Context) (*User, error) {
	panic("unexpected GetFirstAdmin call")
}

func (s *oauthInviteUserRepoStub) Update(ctx context.Context, user *User) error {
	if s.usersByEmail == nil {
		s.usersByEmail = make(map[string]*User)
	}
	cp := *user
	s.usersByEmail[user.Email] = &cp
	return nil
}

func (s *oauthInviteUserRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	for email, u := range s.usersByEmail {
		if u.ID == id {
			delete(s.usersByEmail, email)
			break
		}
	}
	return s.deleteErr
}

func (s *oauthInviteUserRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *oauthInviteUserRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *oauthInviteUserRepoStub) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected UpdateBalance call")
}

func (s *oauthInviteUserRepoStub) DeductBalance(ctx context.Context, id int64, amount float64) error {
	panic("unexpected DeductBalance call")
}

func (s *oauthInviteUserRepoStub) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	panic("unexpected UpdateConcurrency call")
}

func (s *oauthInviteUserRepoStub) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	_, ok := s.usersByEmail[email]
	return ok, nil
}

func (s *oauthInviteUserRepoStub) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}

func (s *oauthInviteUserRepoStub) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}

func (s *oauthInviteUserRepoStub) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}

func (s *oauthInviteUserRepoStub) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	panic("unexpected UpdateTotpSecret call")
}

func (s *oauthInviteUserRepoStub) EnableTotp(ctx context.Context, userID int64) error {
	panic("unexpected EnableTotp call")
}

func (s *oauthInviteUserRepoStub) DisableTotp(ctx context.Context, userID int64) error {
	panic("unexpected DisableTotp call")
}

type oauthRefreshTokenCacheStub struct{}

func (s *oauthRefreshTokenCacheStub) StoreRefreshToken(ctx context.Context, tokenHash string, data *RefreshTokenData, ttl time.Duration) error {
	return nil
}

func (s *oauthRefreshTokenCacheStub) GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshTokenData, error) {
	return nil, errors.New("not found")
}

func (s *oauthRefreshTokenCacheStub) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	return nil
}

func (s *oauthRefreshTokenCacheStub) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	return nil
}

func (s *oauthRefreshTokenCacheStub) DeleteTokenFamily(ctx context.Context, familyID string) error {
	return nil
}

func (s *oauthRefreshTokenCacheStub) AddToUserTokenSet(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) error {
	return nil
}

func (s *oauthRefreshTokenCacheStub) AddToFamilyTokenSet(ctx context.Context, familyID string, tokenHash string, ttl time.Duration) error {
	return nil
}

func (s *oauthRefreshTokenCacheStub) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	return nil, nil
}

func (s *oauthRefreshTokenCacheStub) GetFamilyTokenHashes(ctx context.Context, familyID string) ([]string, error) {
	return nil, nil
}

func (s *oauthRefreshTokenCacheStub) IsTokenInFamily(ctx context.Context, familyID string, tokenHash string) (bool, error) {
	return false, nil
}

func newAuthServiceForOAuthInviteTests(userRepo UserRepository, redeemRepo RedeemCodeRepository, settings map[string]string) *AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
		Default: config.DefaultConfig{
			UserBalance:     3.5,
			UserConcurrency: 2,
		},
	}

	settingService := NewSettingService(&settingRepoStub{values: settings}, cfg)

	return NewAuthService(
		nil,
		userRepo,
		redeemRepo,
		nil,
		nil,
		&oauthRefreshTokenCacheStub{},
		cfg,
		settingService,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func (s *inviteRedeemRepoStub) Create(ctx context.Context, code *RedeemCode) error {
	panic("unexpected Create call")
}

func (s *inviteRedeemRepoStub) CreateBatch(ctx context.Context, codes []RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (s *inviteRedeemRepoStub) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	panic("unexpected GetByID call")
}

func (s *inviteRedeemRepoStub) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	if s.getErrByCode != nil {
		if err, ok := s.getErrByCode[code]; ok {
			return nil, err
		}
	}
	if s.codeByCode == nil {
		return nil, ErrRedeemCodeNotFound
	}
	r, ok := s.codeByCode[code]
	if !ok {
		return nil, ErrRedeemCodeNotFound
	}
	return r, nil
}

func (s *inviteRedeemRepoStub) Update(ctx context.Context, code *RedeemCode) error {
	panic("unexpected Update call")
}

func (s *inviteRedeemRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *inviteRedeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	s.useCalls++
	s.usedID = id
	s.usedByUserID = userID
	return s.useErr
}

func (s *inviteRedeemRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *inviteRedeemRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *inviteRedeemRepoStub) ListByUser(ctx context.Context, userID int64, limit int) ([]RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (s *inviteRedeemRepoStub) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *inviteRedeemRepoStub) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func TestAuthService_InviteLogin_ValidCodeCreatesUserAndConsumesCode(t *testing.T) {
	userRepo := &userRepoStub{nextID: 42}
	groupResolver := &inviteGroupResolverStub{groups: []Group{
		{ID: 77, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
		{ID: 88, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
	}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-OK": {
				ID: 99,
				Code: "INVITE-OK",
				Type: RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyInvitationCodeEnabled:  "true",
		SettingKeyAPIBaseURL:             "https://api.sub2api.dev",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	result, err := service.InviteLogin(context.Background(), "INVITE-OK")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Token)
	user := result.User
	require.NotNil(t, user)
	require.Equal(t, int64(42), user.ID)
	require.Equal(t, RoleUser, user.Role)
	require.Equal(t, StatusActive, user.Status)
	require.Len(t, userRepo.created, 1)
	require.Equal(t, 1, redeemRepo.useCalls)
	require.Equal(t, int64(99), redeemRepo.usedID)
	require.Equal(t, int64(42), redeemRepo.usedByUserID)
	require.Equal(t, 1, groupResolver.calls)
	require.Equal(t, PlatformOpenAI, groupResolver.lastPlatform)
	require.Equal(t, 1, apiKeyProvisioner.calls)
	require.Equal(t, int64(42), apiKeyProvisioner.createdForUserID)
	require.Equal(t, "default", apiKeyProvisioner.lastReq.Name)
	require.NotNil(t, apiKeyProvisioner.lastReq.GroupID)
	require.Equal(t, int64(77), *apiKeyProvisioner.lastReq.GroupID)
	require.Contains(t, user.Email, "@bootstrap.local")
	require.NotEmpty(t, user.Username)
	require.Equal(t, PlatformOpenAI, result.BootstrapContext.ProviderID)
	require.Equal(t, "OpenAI", result.BootstrapContext.ProviderName)
	require.Equal(t, "https://api.sub2api.dev", result.BootstrapContext.BaseURL)
	require.Equal(t, "openai-completions", result.BootstrapContext.APIStyle)
	require.NotEmpty(t, result.BootstrapContext.Models)
	require.Equal(t, openai.DefaultModels[0].ID, result.BootstrapContext.DefaultModel)
	require.Equal(t, openai.DefaultModels[0].ID, result.BootstrapContext.Models[0].ID)
	require.Equal(t, "default", result.BootstrapContext.DefaultAPIKeyName)
	require.Equal(t, int64(77), result.BootstrapContext.DefaultGroupID)
}

func TestAuthService_InviteLogin_ProvisionFailsReturnsUnavailable(t *testing.T) {
	userRepo := &userRepoStub{nextID: 101}
	groupResolver := &inviteGroupResolverStub{groups: []Group{{ID: 77, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{err: errors.New("create key failed")}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-FAIL": {
				ID: 103,
				Code: "INVITE-FAIL",
				Type: RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	_, err := service.InviteLogin(context.Background(), "INVITE-FAIL")
	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Equal(t, 1, redeemRepo.useCalls)
	require.Equal(t, 1, apiKeyProvisioner.calls)
}

func TestAuthService_InviteLogin_NoActiveSupportedPlatformGroupFailsUnavailable(t *testing.T) {
	userRepo := &userRepoStub{nextID: 102}
	groupResolver := &inviteGroupResolverStub{groupsByPlatform: map[string][]Group{}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-NO-GROUP": {
				ID:     104,
				Code:   "INVITE-NO-GROUP",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	_, err := service.InviteLogin(context.Background(), "INVITE-NO-GROUP")
	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Equal(t, 3, groupResolver.calls)
	require.Equal(t, PlatformAntigravity, groupResolver.lastPlatform)
	require.Equal(t, 0, apiKeyProvisioner.calls)
}

func TestAuthService_InviteLogin_AnthropicGroupBuildsAnthropicBootstrapContext(t *testing.T) {
	userRepo := &userRepoStub{nextID: 120}
	groupResolver := &inviteGroupResolverStub{groupsByPlatform: map[string][]Group{
		PlatformAnthropic: {
			{ID: 301, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true},
		},
	}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-ANTHROPIC": {
				ID:     205,
				Code:   "INVITE-ANTHROPIC",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
		SettingKeyAPIBaseURL:            "https://api.sub2api.dev",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	result, err := service.InviteLogin(context.Background(), "INVITE-ANTHROPIC")
	require.NoError(t, err)
	require.Equal(t, 2, groupResolver.calls)
	require.Equal(t, PlatformAnthropic, groupResolver.lastPlatform)
	require.Equal(t, PlatformAnthropic, result.BootstrapContext.ProviderID)
	require.Equal(t, "Anthropic", result.BootstrapContext.ProviderName)
	require.Equal(t, "anthropic-messages", result.BootstrapContext.APIStyle)
	require.NotEmpty(t, result.BootstrapContext.Models)
	require.Equal(t, claude.DefaultModels[0].ID, result.BootstrapContext.Models[0].ID)
	require.Equal(t, claude.DefaultModels[0].ID, result.BootstrapContext.DefaultModel)
	require.Equal(t, int64(301), result.BootstrapContext.DefaultGroupID)
}

func TestAuthService_InviteLogin_AntigravityGroupBuildsAntigravityBootstrapContext(t *testing.T) {
	userRepo := &userRepoStub{nextID: 121}
	groupResolver := &inviteGroupResolverStub{groupsByPlatform: map[string][]Group{
		PlatformAntigravity: {
			{ID: 401, Platform: PlatformAntigravity, Status: StatusActive, Hydrated: true},
		},
	}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-ANTI": {
				ID:     206,
				Code:   "INVITE-ANTI",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
		SettingKeyAPIBaseURL:            "https://api.sub2api.dev",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	result, err := service.InviteLogin(context.Background(), "INVITE-ANTI")
	require.NoError(t, err)
	require.Equal(t, 3, groupResolver.calls)
	require.Equal(t, PlatformAntigravity, groupResolver.lastPlatform)
	require.Equal(t, "antigravity-gemini", result.BootstrapContext.ProviderID)
	require.Equal(t, "Antigravity Gemini", result.BootstrapContext.ProviderName)
	require.Equal(t, "openai-completions", result.BootstrapContext.APIStyle)
	require.NotEmpty(t, result.BootstrapContext.Models)
	require.True(t, strings.HasPrefix(result.BootstrapContext.Models[0].ID, "gemini-"))
	require.True(t, strings.HasPrefix(result.BootstrapContext.DefaultModel, "gemini-"))
	require.Equal(t, int64(401), result.BootstrapContext.DefaultGroupID)
}

func TestAuthService_InviteLogin_AntigravityClaudeScopeBuildsClaudeFlavorBootstrapContext(t *testing.T) {
	userRepo := &userRepoStub{nextID: 122}
	groupResolver := &inviteGroupResolverStub{groupsByPlatform: map[string][]Group{
		PlatformAntigravity: {
			{
				ID:                   402,
				Platform:             PlatformAntigravity,
				Status:               StatusActive,
				Hydrated:             true,
				SupportedModelScopes: []string{"claude"},
				DefaultMappedModel:   "claude-opus-4-6-thinking",
			},
		},
	}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-ANTI-CLAUDE": {
				ID:     207,
				Code:   "INVITE-ANTI-CLAUDE",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:      "true",
		SettingKeyInvitationCodeEnabled:    "true",
		SettingKeyAPIBaseURL:               "https://api.sub2api.dev",
		SettingKeyFallbackModelAntigravity: "gemini-2.5-flash",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	result, err := service.InviteLogin(context.Background(), "INVITE-ANTI-CLAUDE")
	require.NoError(t, err)
	require.Equal(t, "antigravity-claude", result.BootstrapContext.ProviderID)
	require.Equal(t, "Antigravity Claude", result.BootstrapContext.ProviderName)
	require.Equal(t, "anthropic-messages", result.BootstrapContext.APIStyle)
	require.NotEmpty(t, result.BootstrapContext.Models)
	for _, m := range result.BootstrapContext.Models {
		require.True(t, strings.HasPrefix(m.ID, "claude-"))
	}
	require.Equal(t, "claude-opus-4-6-thinking", result.BootstrapContext.DefaultModel)
	require.Equal(t, int64(402), result.BootstrapContext.DefaultGroupID)
}

func TestAuthService_InviteLogin_AntigravityGeminiScopeUsesFallbackDefaultModel(t *testing.T) {
	userRepo := &userRepoStub{nextID: 123}
	groupResolver := &inviteGroupResolverStub{groupsByPlatform: map[string][]Group{
		PlatformAntigravity: {
			{
				ID:                   403,
				Platform:             PlatformAntigravity,
				Status:               StatusActive,
				Hydrated:             true,
				SupportedModelScopes: []string{"gemini_text"},
			},
		},
	}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-ANTI-GEMINI": {
				ID:     208,
				Code:   "INVITE-ANTI-GEMINI",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:      "true",
		SettingKeyInvitationCodeEnabled:    "true",
		SettingKeyAPIBaseURL:               "https://api.sub2api.dev",
		SettingKeyFallbackModelAntigravity: "gemini-3-flash",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	result, err := service.InviteLogin(context.Background(), "INVITE-ANTI-GEMINI")
	require.NoError(t, err)
	require.Equal(t, "antigravity-gemini", result.BootstrapContext.ProviderID)
	require.Equal(t, "Antigravity Gemini", result.BootstrapContext.ProviderName)
	require.Equal(t, "openai-completions", result.BootstrapContext.APIStyle)
	require.NotEmpty(t, result.BootstrapContext.Models)
	for _, m := range result.BootstrapContext.Models {
		require.True(t, strings.HasPrefix(m.ID, "gemini-"))
	}
	require.Equal(t, "gemini-3-flash", result.BootstrapContext.DefaultModel)
	require.Equal(t, int64(403), result.BootstrapContext.DefaultGroupID)
}

func TestAuthService_InviteLogin_SubscriptionGroupAssignsSubscription(t *testing.T) {
	userRepo := &userRepoStub{nextID: 103}
	groupResolver := &inviteGroupResolverStub{groups: []Group{{
		ID:                 91,
		Platform:           PlatformOpenAI,
		Status:             StatusActive,
		Hydrated:           true,
		SubscriptionType:   SubscriptionTypeSubscription,
		DefaultValidityDays: 15,
	}}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	subAssigner := &defaultSubscriptionAssignerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-SUB": {
				ID:     105,
				Code:   "INVITE-SUB",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner
	service.defaultSubAssigner = subAssigner

	result, err := service.InviteLogin(context.Background(), "INVITE-SUB")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, apiKeyProvisioner.calls)
	require.Len(t, subAssigner.calls, 1)
	require.Equal(t, int64(103), subAssigner.calls[0].UserID)
	require.Equal(t, int64(91), subAssigner.calls[0].GroupID)
	require.Equal(t, 15, subAssigner.calls[0].ValidityDays)
	require.Contains(t, subAssigner.calls[0].Notes, "invite bootstrap")
}

func TestAuthService_InviteLogin_ExclusiveStandardGroupAddsAllowedGroup(t *testing.T) {
	userRepo := &inviteAwareUserRepoStub{userRepoStub: userRepoStub{nextID: 104}}
	baseUserRepo := &userRepoStub{nextID: 104}
	groupResolver := &inviteGroupResolverStub{groups: []Group{{
		ID:               201,
		Platform:         PlatformOpenAI,
		Status:           StatusActive,
		Hydrated:         true,
		IsExclusive:      true,
		SubscriptionType: SubscriptionTypeStandard,
	}}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-EXCLUSIVE": {
				ID:     106,
				Code:   "INVITE-EXCLUSIVE",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(baseUserRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.userRepo = userRepo
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	result, err := service.InviteLogin(context.Background(), "INVITE-EXCLUSIVE")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, userRepo.addAllowedGroupCalls)
	require.Equal(t, int64(104), userRepo.lastAddAllowedUserID)
	require.Equal(t, int64(201), userRepo.lastAddAllowedGroupID)
	require.Equal(t, 1, apiKeyProvisioner.calls)
	require.NotNil(t, apiKeyProvisioner.lastReq.GroupID)
	require.Equal(t, int64(201), *apiKeyProvisioner.lastReq.GroupID)
}

func TestAuthService_InviteLogin_ExclusiveStandardGroupAddAllowedGroupFails(t *testing.T) {
	userRepo := &inviteAwareUserRepoStub{
		userRepoStub:  userRepoStub{nextID: 105},
		addAllowedErr: errors.New("grant failed"),
	}
	baseUserRepo := &userRepoStub{nextID: 105}
	groupResolver := &inviteGroupResolverStub{groups: []Group{{
		ID:               202,
		Platform:         PlatformOpenAI,
		Status:           StatusActive,
		Hydrated:         true,
		IsExclusive:      true,
		SubscriptionType: SubscriptionTypeStandard,
	}}}
	apiKeyProvisioner := &inviteAPIKeyProvisionerStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE-EXCLUSIVE-FAIL": {
				ID:     107,
				Code:   "INVITE-EXCLUSIVE-FAIL",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(baseUserRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.userRepo = userRepo
	service.redeemRepo = redeemRepo
	service.groupRepo = groupResolver
	service.apiKeyProvisioner = apiKeyProvisioner

	_, err := service.InviteLogin(context.Background(), "INVITE-EXCLUSIVE-FAIL")
	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Equal(t, 1, userRepo.addAllowedGroupCalls)
	require.Equal(t, 0, apiKeyProvisioner.calls)
}

func TestAuthService_InviteLogin_InvalidCodeFailsCleanly(t *testing.T) {
	userRepo := &userRepoStub{}
	redeemRepo := &inviteRedeemRepoStub{
		getErrByCode: map[string]error{
			"BAD": ErrRedeemCodeNotFound,
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyInvitationCodeEnabled:  "true",
	}, nil)
	service.redeemRepo = redeemRepo

	_, err := service.InviteLogin(context.Background(), "BAD")
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
	require.Empty(t, userRepo.created)
	require.Equal(t, 0, redeemRepo.useCalls)
}

func TestAuthService_InviteLogin_UsedCodeFailsCleanly(t *testing.T) {
	userRepo := &userRepoStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"USED": {
				ID: 100,
				Code: "USED",
				Type: RedeemTypeInvitation,
				Status: StatusUsed,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyInvitationCodeEnabled:  "true",
	}, nil)
	service.redeemRepo = redeemRepo

	_, err := service.InviteLogin(context.Background(), "USED")
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
	require.Empty(t, userRepo.created)
	require.Equal(t, 0, redeemRepo.useCalls)
}

func TestAuthService_InviteLogin_ConsumeRaceReturnsInvalid(t *testing.T) {
	userRepo := &userRepoStub{nextID: 7}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"RACE": {
				ID: 101,
				Code: "RACE",
				Type: RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
		useErr: ErrRedeemCodeUsed,
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyInvitationCodeEnabled:  "true",
	}, nil)
	service.redeemRepo = redeemRepo

	_, err := service.InviteLogin(context.Background(), "RACE")
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
	require.Equal(t, 1, redeemRepo.useCalls)
}

func TestAuthService_InviteLogin_Disabled(t *testing.T) {
	userRepo := &userRepoStub{}
	redeemRepo := &inviteRedeemRepoStub{}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyInvitationCodeEnabled:  "false",
	}, nil)
	service.redeemRepo = redeemRepo

	_, err := service.InviteLogin(context.Background(), "ANY")
	require.ErrorIs(t, err, ErrInvitationCodeDisabled)
}

func TestAuthService_InviteLogin_CreateFailureReturnsUnavailable(t *testing.T) {
	userRepo := &userRepoStub{createErr: errors.New("db error")}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"INVITE": {
				ID: 102,
				Code: "INVITE",
				Type: RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:    "true",
		SettingKeyInvitationCodeEnabled:  "true",
	}, nil)
	service.redeemRepo = redeemRepo

	_, err := service.InviteLogin(context.Background(), "INVITE")
	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Equal(t, 0, redeemRepo.useCalls)
}

func TestAuthService_RegisterWithVerification_InvitationConsumeRaceRollsBackUser(t *testing.T) {
	userRepo := &userRepoStub{nextID: 88}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"REG-RACE": {
				ID:     201,
				Code:   "REG-RACE",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
		useErr: ErrRedeemCodeUsed,
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.redeemRepo = redeemRepo

	_, _, err := service.RegisterWithVerification(
		context.Background(),
		"register-race@test.com",
		"password123",
		"",
		"",
		"REG-RACE",
	)
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
	require.Len(t, userRepo.created, 1)
	require.Equal(t, []int64{88}, userRepo.deletedIDs)
	require.Equal(t, 1, redeemRepo.useCalls)
}

func TestAuthService_RegisterWithVerification_InvitationConsumeFailureReturnsUnavailable(t *testing.T) {
	userRepo := &userRepoStub{nextID: 89}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"REG-FAIL": {
				ID:     202,
				Code:   "REG-FAIL",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
		useErr: errors.New("db unavailable"),
	}
	service := newAuthService(userRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	}, nil)
	service.redeemRepo = redeemRepo

	_, _, err := service.RegisterWithVerification(
		context.Background(),
		"register-fail@test.com",
		"password123",
		"",
		"",
		"REG-FAIL",
	)
	require.ErrorIs(t, err, ErrServiceUnavailable)
	require.Len(t, userRepo.created, 1)
	require.Equal(t, []int64{89}, userRepo.deletedIDs)
	require.Equal(t, 1, redeemRepo.useCalls)
}

func TestAuthService_LoginOrRegisterOAuthWithTokenPair_InvitationConsumeRaceRollsBackUser(t *testing.T) {
	userRepo := &oauthInviteUserRepoStub{}
	redeemRepo := &inviteRedeemRepoStub{
		codeByCode: map[string]*RedeemCode{
			"OAUTH-RACE": {
				ID:     301,
				Code:   "OAUTH-RACE",
				Type:   RedeemTypeInvitation,
				Status: StatusUnused,
			},
		},
		useErr: ErrRedeemCodeUsed,
	}
	service := newAuthServiceForOAuthInviteTests(userRepo, redeemRepo, map[string]string{
		SettingKeyRegistrationEnabled:   "true",
		SettingKeyInvitationCodeEnabled: "true",
	})

	_, _, err := service.LoginOrRegisterOAuthWithTokenPair(
		context.Background(),
		"oauth-race@test.com",
		"oauth_user",
		"OAUTH-RACE",
	)
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
	require.Equal(t, 1, redeemRepo.useCalls)
	require.Len(t, userRepo.deletedIDs, 1)
	_, lookupErr := userRepo.GetByEmail(context.Background(), "oauth-race@test.com")
	require.ErrorIs(t, lookupErr, ErrUserNotFound)
}
