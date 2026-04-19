//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type inviteRedeemRepoStub struct {
	code     *RedeemCode
	getErr   error
	useErr   error
	usedID   int64
	usedUser int64
}

func (s *inviteRedeemRepoStub) Create(context.Context, *RedeemCode) error       { panic("unexpected") }
func (s *inviteRedeemRepoStub) CreateBatch(context.Context, []RedeemCode) error { panic("unexpected") }
func (s *inviteRedeemRepoStub) GetByID(context.Context, int64) (*RedeemCode, error) {
	panic("unexpected")
}
func (s *inviteRedeemRepoStub) Update(context.Context, *RedeemCode) error { panic("unexpected") }
func (s *inviteRedeemRepoStub) Delete(context.Context, int64) error       { panic("unexpected") }
func (s *inviteRedeemRepoStub) List(context.Context, pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteRedeemRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteRedeemRepoStub) ListByUser(context.Context, int64, int) ([]RedeemCode, error) {
	panic("unexpected")
}
func (s *inviteRedeemRepoStub) ListByUserPaginated(context.Context, int64, pagination.PaginationParams, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteRedeemRepoStub) SumPositiveBalanceByUser(context.Context, int64) (float64, error) {
	panic("unexpected")
}
func (s *inviteRedeemRepoStub) GetByCode(_ context.Context, _ string) (*RedeemCode, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.code == nil {
		return nil, errors.New("not found")
	}
	return s.code, nil
}
func (s *inviteRedeemRepoStub) Use(_ context.Context, id, userID int64) error {
	if s.useErr != nil {
		return s.useErr
	}
	s.usedID = id
	s.usedUser = userID
	return nil
}

type inviteRefreshTokenCacheStub struct{}

func (inviteRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *RefreshTokenData, time.Duration) error {
	return nil
}
func (inviteRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	return nil, ErrRefreshTokenNotFound
}
func (inviteRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error     { return nil }
func (inviteRefreshTokenCacheStub) DeleteUserRefreshTokens(context.Context, int64) error { return nil }
func (inviteRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error      { return nil }
func (inviteRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}
func (inviteRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}
func (inviteRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}
func (inviteRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}
func (inviteRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

type inviteGroupRepoStub struct {
	groups []Group
	err    error
}

func (s *inviteGroupRepoStub) Create(context.Context, *Group) error           { panic("unexpected") }
func (s *inviteGroupRepoStub) GetByID(context.Context, int64) (*Group, error) { panic("unexpected") }
func (s *inviteGroupRepoStub) GetByIDLite(context.Context, int64) (*Group, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) Update(context.Context, *Group) error { panic("unexpected") }
func (s *inviteGroupRepoStub) Delete(context.Context, int64) error  { panic("unexpected") }
func (s *inviteGroupRepoStub) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.groups, nil
}
func (s *inviteGroupRepoStub) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected")
}
func (s *inviteGroupRepoStub) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected")
}

type inviteBootstrapAPIKeySvcStub struct {
	groups    []Group
	err       error
	keys      []*APIKey
	idx       int
	createErr error
	requests  []CreateAPIKeyRequest
}

func (s *inviteBootstrapAPIKeySvcStub) GetAvailableGroups(context.Context, int64) ([]Group, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.groups, nil
}

func (s *inviteBootstrapAPIKeySvcStub) Create(_ context.Context, _ int64, req CreateAPIKeyRequest) (*APIKey, error) {
	s.requests = append(s.requests, req)
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.idx >= len(s.keys) {
		return nil, errors.New("no key")
	}
	key := s.keys[s.idx]
	s.idx++
	if key == nil {
		return nil, errors.New("create failed")
	}
	if req.GroupID != nil {
		key.GroupID = req.GroupID
	}
	return key, nil
}

type inviteUserSubRepoStub struct {
	userSubRepoNoop
	byGroup map[int64]*UserSubscription
}

func (s *inviteUserSubRepoStub) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	if s.byGroup == nil {
		return nil, ErrSubscriptionNotFound
	}
	sub := s.byGroup[groupID]
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	clone := *sub
	clone.UserID = userID
	clone.GroupID = groupID
	return &clone, nil
}

type inviteAPIKeyRepoStub struct {
	created []*APIKey
}

func (s *inviteAPIKeyRepoStub) Create(_ context.Context, key *APIKey) error {
	clone := *key
	clone.ID = int64(len(s.created) + 1)
	key.ID = clone.ID
	s.created = append(s.created, &clone)
	return nil
}
func (s *inviteAPIKeyRepoStub) GetByID(context.Context, int64) (*APIKey, error) { panic("unexpected") }
func (s *inviteAPIKeyRepoStub) GetKeyAndOwnerID(context.Context, int64) (string, int64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) GetByKey(context.Context, string) (*APIKey, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) GetByKeyForAuth(context.Context, string) (*APIKey, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) Update(context.Context, *APIKey) error { panic("unexpected") }
func (s *inviteAPIKeyRepoStub) Delete(context.Context, int64) error   { panic("unexpected") }
func (s *inviteAPIKeyRepoStub) ListByUserID(context.Context, int64, pagination.PaginationParams, APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) VerifyOwnership(context.Context, int64, []int64) ([]int64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) CountByUserID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) ExistsByKey(context.Context, string) (bool, error) { return false, nil }
func (s *inviteAPIKeyRepoStub) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) SearchAPIKeys(context.Context, int64, string, int) ([]APIKey, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) ClearGroupIDByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) CountByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) ListKeysByUserID(context.Context, int64) ([]string, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) ListKeysByGroupID(context.Context, int64) ([]string, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) IncrementQuotaUsed(context.Context, int64, float64) (float64, error) {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) UpdateLastUsed(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) IncrementRateLimitUsage(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) ResetRateLimitWindows(context.Context, int64) error {
	panic("unexpected")
}
func (s *inviteAPIKeyRepoStub) GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error) {
	panic("unexpected")
}

func newAuthServiceForInviteLoginTest(userRepo *userRepoStub, redeemRepo RedeemCodeRepository) *AuthService {
	return NewAuthService(
		nil,
		userRepo,
		redeemRepo,
		inviteRefreshTokenCacheStub{},
		&config.Config{
			JWT:     config.JWTConfig{Secret: "invite-login-secret", ExpireHour: 1, RefreshTokenExpireDays: 7},
			Default: config.DefaultConfig{UserBalance: 1.5, UserConcurrency: 2, APIKeyPrefix: "sk-"},
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

func TestSelectInviteBootstrapGroupsForRedeem_BalanceChoosesCheapestPerPlatform(t *testing.T) {
	redeemCode := &RedeemCode{Type: RedeemTypeBalance}
	groups := []Group{
		{ID: 10, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 2.0, SortOrder: 30},
		{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1.0, SortOrder: 40},
		{ID: 12, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1.0, SortOrder: 20},
		{ID: 20, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, RateMultiplier: 0.0, SortOrder: 1},
		{ID: 21, Platform: PlatformAnthropic, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.5, SortOrder: 99},
		{ID: 22, Platform: PlatformAnthropic, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.5, SortOrder: 1},
		{ID: 31, Platform: PlatformGemini, Status: StatusActive, ActiveAccountCount: 0, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.1, SortOrder: 1},
	}

	selected := selectInviteBootstrapGroupsForRedeem(redeemCode, groups)
	require.Len(t, selected, 2)
	require.Equal(t, int64(12), selected[PlatformOpenAI].ID)
	require.Equal(t, int64(22), selected[PlatformAnthropic].ID)
}

func TestSelectInviteBootstrapGroupsForRedeem_InvitationUsesSubscriptionGroups(t *testing.T) {
	redeemCode := &RedeemCode{Type: RedeemTypeInvitation}
	groups := []Group{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.1, SortOrder: 1},
		{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 9},
		{ID: 3, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 60, SortOrder: 8},
		{ID: 4, Platform: PlatformAnthropic, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 15, SortOrder: 3},
	}

	selected := selectInviteBootstrapGroupsForRedeem(redeemCode, groups)
	require.Len(t, selected, 2)
	require.Equal(t, int64(3), selected[PlatformOpenAI].ID)
	require.Equal(t, int64(4), selected[PlatformAnthropic].ID)
}

func TestAuthService_InviteLogin_CodeRequired(t *testing.T) {
	svc := newAuthServiceForInviteLoginTest(&userRepoStub{}, &inviteRedeemRepoStub{})

	_, _, _, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "   "})
	require.ErrorIs(t, err, ErrInvitationCodeRequired)
}

func TestAuthService_InviteLogin_SuccessWithPartialBootstrapKeys(t *testing.T) {
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 88, Type: RedeemTypeInvitation, Status: StatusUnused}}
	userRepo := &userRepoStub{nextID: 101}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.defaultSubAssigner = &inviteDefaultSubAssignerStub{}
	svc.SetInviteBootstrapAPIKeyService(&inviteBootstrapAPIKeySvcStub{
		groups: []Group{
			{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 20},
			{ID: 5, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 90, SortOrder: 10},
			{ID: 2, Platform: PlatformAnthropic, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 7, SortOrder: 1},
		},
		keys: []*APIKey{
			{ID: 3001, Name: "bootstrap-anthropic", Key: "sk-anthropic"},
			nil,
		},
	})
	svc.SetInviteBootstrapGroupRepository(&inviteGroupRepoStub{groups: []Group{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 20},
		{ID: 5, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 90, SortOrder: 10},
		{ID: 2, Platform: PlatformAnthropic, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 7, SortOrder: 1},
	}})

	tokenPair, user, keys, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "INVITE-001"})
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.NotNil(t, user)
	require.Equal(t, int64(101), user.ID)
	require.Equal(t, int64(88), redeemRepo.usedID)
	require.Equal(t, int64(101), redeemRepo.usedUser)
	require.Len(t, keys, 1)
	require.Equal(t, int64(2), keys[0].GroupID)
	require.Equal(t, PlatformAnthropic, keys[0].Platform)
}

func TestAuthService_InviteLogin_FailsWhenNoBootstrapKeyCreated(t *testing.T) {
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 66, Type: RedeemTypeInvitation, Status: StatusUnused}}
	userRepo := &userRepoStub{nextID: 202}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.defaultSubAssigner = &inviteDefaultSubAssignerStub{}
	svc.SetInviteBootstrapAPIKeyService(&inviteBootstrapAPIKeySvcStub{
		groups: []Group{{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, RateMultiplier: 0.2, SortOrder: 1}},
		keys:   []*APIKey{nil},
	})
	svc.SetInviteBootstrapGroupRepository(&inviteGroupRepoStub{groups: []Group{{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 1}}})

	_, _, _, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "INVITE-002"})
	require.ErrorIs(t, err, ErrBootstrapAPIKeyUnavailable)
}

func TestAuthService_InviteLogin_BalanceUsesNonSubscriptionBootstrapGroups(t *testing.T) {
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 77, Type: RedeemTypeBalance, Status: StatusUnused, Value: 25}}
	userRepo := &userRepoStub{nextID: 303, user: &User{ID: 303, Balance: 1.5, Concurrency: 2}}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.SetInviteBootstrapAPIKeyService(&inviteBootstrapAPIKeySvcStub{
		groups: []Group{
			{ID: 9, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.8, SortOrder: 3},
			{ID: 8, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 0.8, SortOrder: 2},
			{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, RateMultiplier: 0.0, SortOrder: 1},
		},
		keys: []*APIKey{{ID: 4001, Name: "bootstrap-openai", Key: "sk-openai"}},
	})

	_, user, keys, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "BALANCE-001"})
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, int64(8), keys[0].GroupID)
	require.Equal(t, PlatformOpenAI, keys[0].Platform)
	require.NotNil(t, user)
	require.Equal(t, 26.5, user.Balance)
	require.Equal(t, []float64{25}, userRepo.balanceUpdates)
}

func TestAPIKeyService_Create_SubscriptionGroupRequiresActiveSubscription(t *testing.T) {
	groupID := int64(12)
	apiKeyRepo := &inviteAPIKeyRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&userRepoStub{user: &User{ID: 99, Role: RoleUser}},
		&inviteGroupRepoStub{groups: []Group{{ID: groupID}}, err: nil},
		&inviteUserSubRepoStub{},
		nil,
		nil,
		&config.Config{Default: config.DefaultConfig{APIKeyPrefix: "sk-"}},
	)
	groupRepo := &inviteGroupRepoStub{}
	groupRepoWithGet := &inviteCreateGroupRepoStub{group: &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription}}
	svc.groupRepo = groupRepoWithGet

	_, err := svc.Create(context.Background(), 99, CreateAPIKeyRequest{Name: "bootstrap-openai", GroupID: &groupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Empty(t, apiKeyRepo.created)
	_ = groupRepo
}

func TestAPIKeyService_Create_SubscriptionGroupAllowsWhenActiveSubscriptionExists(t *testing.T) {
	groupID := int64(12)
	apiKeyRepo := &inviteAPIKeyRepoStub{}
	svc := NewAPIKeyService(
		apiKeyRepo,
		&userRepoStub{user: &User{ID: 99, Role: RoleUser}},
		&inviteCreateGroupRepoStub{group: &Group{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription}},
		&inviteUserSubRepoStub{byGroup: map[int64]*UserSubscription{groupID: {ID: 1, Status: SubscriptionStatusActive}}},
		nil,
		nil,
		&config.Config{Default: config.DefaultConfig{APIKeyPrefix: "sk-"}},
	)

	created, err := svc.Create(context.Background(), 99, CreateAPIKeyRequest{Name: "bootstrap-openai", GroupID: &groupID})
	require.NoError(t, err)
	require.NotNil(t, created)
	require.Len(t, apiKeyRepo.created, 1)
	require.Equal(t, groupID, *apiKeyRepo.created[0].GroupID)
}

func TestAuthService_InviteLogin_SubscriptionRedeemAssignsSubscriptionBeforeCreate(t *testing.T) {
	groupID := int64(42)
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 90, Code: "SUB-001", Type: RedeemTypeSubscription, Status: StatusUnused, GroupID: &groupID, ValidityDays: 45}}
	userRepo := &userRepoStub{nextID: 404, user: &User{ID: 404, Balance: 1.5, Concurrency: 2}}
	assigner := &inviteDefaultSubAssignerStub{}
	bootstrapSvc := &inviteBootstrapAPIKeySvcStub{keys: []*APIKey{{ID: 5001, Name: "bootstrap-openai", Key: "sk-openai"}}}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.defaultSubAssigner = assigner
	svc.SetInviteBootstrapAPIKeyService(bootstrapSvc)
	svc.SetInviteBootstrapGroupRepository(&inviteGroupRepoStub{groups: []Group{{ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 1}}})

	_, user, keys, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "SUB-001"})
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Len(t, keys, 1)
	require.Len(t, assigner.calls, 2)
	require.Equal(t, int64(404), assigner.calls[0].UserID)
	require.Equal(t, groupID, assigner.calls[0].GroupID)
	require.Equal(t, 45, assigner.calls[0].ValidityDays)
	require.Equal(t, int64(404), assigner.calls[1].UserID)
	require.Equal(t, groupID, assigner.calls[1].GroupID)
	require.Equal(t, 45, assigner.calls[1].ValidityDays)
	require.Len(t, bootstrapSvc.requests, 1)
	require.NotNil(t, bootstrapSvc.requests[0].GroupID)
	require.Equal(t, groupID, *bootstrapSvc.requests[0].GroupID)
}

func TestAuthService_InviteLogin_InvitationRedeemAssignsSubscriptionBeforeCreate(t *testing.T) {
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 91, Type: RedeemTypeInvitation, Status: StatusUnused}}
	userRepo := &userRepoStub{nextID: 405}
	assigner := &inviteDefaultSubAssignerStub{}
	bootstrapSvc := &inviteBootstrapAPIKeySvcStub{keys: []*APIKey{{ID: 5002, Name: "bootstrap-openai", Key: "sk-openai"}}}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.defaultSubAssigner = assigner
	svc.SetInviteBootstrapAPIKeyService(bootstrapSvc)
	svc.SetInviteBootstrapGroupRepository(&inviteGroupRepoStub{groups: []Group{{ID: 77, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 1}}})

	_, user, keys, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "INV-ASSIGN-001"})
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Len(t, keys, 1)
	require.Len(t, assigner.calls, 1)
	require.Equal(t, int64(405), assigner.calls[0].UserID)
	require.Equal(t, int64(77), assigner.calls[0].GroupID)
	require.Equal(t, 30, assigner.calls[0].ValidityDays)
}

type inviteDefaultSubAssignerStub struct {
	calls []*AssignSubscriptionInput
	err   error
}

func (s *inviteDefaultSubAssignerStub) AssignOrExtendSubscription(_ context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error) {
	clone := *input
	s.calls = append(s.calls, &clone)
	if s.err != nil {
		return nil, false, s.err
	}
	return &UserSubscription{ID: int64(len(s.calls)), UserID: input.UserID, GroupID: input.GroupID, Status: SubscriptionStatusActive}, false, nil
}

type inviteCreateGroupRepoStub struct {
	inviteGroupRepoStub
	group *Group
}

func (s *inviteCreateGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	if s.group == nil || s.group.ID != id {
		return nil, errors.New("group not found")
	}
	clone := *s.group
	return &clone, nil
}
