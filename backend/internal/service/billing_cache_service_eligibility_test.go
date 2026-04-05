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

type eligibilityUserRepoStub struct {
	balance float64
	err     error
}

func (s *eligibilityUserRepoStub) Create(context.Context, *User) error { panic("unexpected") }
func (s *eligibilityUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &User{ID: id, Balance: s.balance}, nil
}
func (s *eligibilityUserRepoStub) GetByEmail(context.Context, string) (*User, error) {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) GetFirstAdmin(context.Context) (*User, error) { panic("unexpected") }
func (s *eligibilityUserRepoStub) Update(context.Context, *User) error          { panic("unexpected") }
func (s *eligibilityUserRepoStub) Delete(context.Context, int64) error          { panic("unexpected") }
func (s *eligibilityUserRepoStub) List(context.Context, pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) DeductBalance(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) ExistsByEmail(context.Context, string) (bool, error) {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected")
}
func (s *eligibilityUserRepoStub) EnableTotp(context.Context, int64) error  { panic("unexpected") }
func (s *eligibilityUserRepoStub) DisableTotp(context.Context, int64) error { panic("unexpected") }

type eligibilitySubscriptionRepoStub struct {
	active *UserSubscription
	err    error
}

func (s *eligibilitySubscriptionRepoStub) Create(context.Context, *UserSubscription) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) GetByID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) GetByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.active == nil || s.active.UserID != userID || s.active.GroupID != groupID {
		return nil, ErrSubscriptionNotFound
	}
	cp := *s.active
	return &cp, nil
}
func (s *eligibilitySubscriptionRepoStub) Update(context.Context, *UserSubscription) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) Delete(context.Context, int64) error { panic("unexpected") }
func (s *eligibilitySubscriptionRepoStub) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) List(context.Context, pagination.PaginationParams, *int64, *int64, string, string, string, string) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ExistsByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ExtendExpiry(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) UpdateStatus(context.Context, int64, string) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) UpdateNotes(context.Context, int64, string) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ActivateWindows(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ResetDailyUsage(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ResetWeeklyUsage(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) ResetMonthlyUsage(context.Context, int64, time.Time) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) IncrementUsage(context.Context, int64, float64) error {
	panic("unexpected")
}
func (s *eligibilitySubscriptionRepoStub) BatchUpdateExpiredStatus(context.Context) (int64, error) {
	panic("unexpected")
}

type eligibilityRateLoaderStub struct{}

func (eligibilityRateLoaderStub) GetRateLimitData(context.Context, int64) (*APIKeyRateLimitData, error) {
	return &APIKeyRateLimitData{}, nil
}

func TestBillingCacheServiceCheckBillingEligibility_UsesGroupForSubscriptionMode(t *testing.T) {
	userRepo := &eligibilityUserRepoStub{balance: 0}
	subRepo := &eligibilitySubscriptionRepoStub{
		active: &UserSubscription{
			UserID:        100,
			GroupID:       7,
			Status:        SubscriptionStatusActive,
			ExpiresAt:     time.Now().Add(24 * time.Hour),
			DailyUsageUSD: 0,
		},
	}
	svc := NewBillingCacheService(nil, userRepo, subRepo, nil, &config.Config{})
	svc.apiKeyRateLimitLoader = eligibilityRateLoaderStub{}

	err := svc.CheckBillingEligibility(
		context.Background(),
		&User{ID: 100},
		&APIKey{ID: 1, RateLimit1d: 0},
		&Group{ID: 7, SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive, Platform: PlatformAnthropic, Hydrated: true},
		nil,
	)

	require.NoError(t, err)
}

func TestBillingCacheServiceCheckBillingEligibility_BalanceModeWithoutGroup(t *testing.T) {
	userRepo := &eligibilityUserRepoStub{balance: 10}
	svc := NewBillingCacheService(nil, userRepo, nil, nil, &config.Config{})

	err := svc.CheckBillingEligibility(context.Background(), &User{ID: 200}, &APIKey{ID: 2}, nil, nil)
	require.NoError(t, err)
}

func TestBillingCacheServiceCheckBillingEligibility_SubscriptionMissingForEffectiveGroup(t *testing.T) {
	userRepo := &eligibilityUserRepoStub{balance: 999}
	subRepo := &eligibilitySubscriptionRepoStub{err: errors.New("missing")}
	svc := NewBillingCacheService(nil, userRepo, subRepo, nil, &config.Config{})

	err := svc.CheckBillingEligibility(
		context.Background(),
		&User{ID: 300},
		&APIKey{ID: 3},
		&Group{ID: 77, SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive, Platform: PlatformAnthropic, Hydrated: true},
		nil,
	)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
}
