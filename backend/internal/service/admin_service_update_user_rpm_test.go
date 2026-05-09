//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// rpmUserRepoStub 复用 admin_service_update_balance_test.go 的基础 stub 结构，
// 只在 Update 时把入参克隆一份，便于断言修改后的 RPMLimit。
type rpmUserRepoStub struct {
	*userRepoStub
	lastUpdated *User
}

func (s *rpmUserRepoStub) Update(_ context.Context, user *User) error {
	if user == nil {
		return nil
	}
	clone := *user
	s.lastUpdated = &clone
	if s.userRepoStub != nil {
		s.userRepoStub.user = &clone
	}
	return nil
}

func TestAdminService_UpdateUser_InvalidatesAuthCacheOnRPMLimitChange(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", RPMLimit: 10}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}

	newRPM := 60
	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		RPMLimit: &newRPM,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 60, updated.RPMLimit)
	require.Equal(t, []int64{42}, invalidator.userIDs, "仅修改 RPMLimit 也应失效 API Key 认证缓存")
}

func TestAdminService_UpdateUser_NoInvalidateWhenRPMLimitUnchanged(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", RPMLimit: 10, Username: "old"}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}

	newName := "new"
	sameRPM := 10
	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Username: &newName,
		RPMLimit: &sameRPM,
	})
	require.NoError(t, err)
	require.Empty(t, invalidator.userIDs, "只改 username 不应触发认证缓存失效")
}

func TestAdminService_UpdateUser_ChangesRoleAndInvalidatesAuthCache(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role: RoleMarketing,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, RoleMarketing, updated.Role)
	require.Equal(t, []int64{42}, invalidator.userIDs, "修改 role 后应失效 API Key 认证缓存")
}

func TestAdminService_UpdateUser_InvalidRole(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{
		userRepo:       repo,
		redeemCodeRepo: &redeemRepoStub{},
	}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role: "sales",
	})
	require.Error(t, err)
	require.Nil(t, repo.lastUpdated)
}

type effectiveStatusUpdateUserRepoStub struct {
	*userRepoStubForListUsers
	lastUpdated              *User
	updatedEffectiveStatuses []string
}

func (s *effectiveStatusUpdateUserRepoStub) Update(_ context.Context, user *User) error {
	if user == nil {
		return nil
	}
	clone := *user
	s.lastUpdated = &clone
	if s.userRepoStubForListUsers != nil {
		s.userRepoStubForListUsers.user = &clone
	}
	return nil
}

func (s *effectiveStatusUpdateUserRepoStub) UpdateEffectiveStatus(_ context.Context, userID int64, status string) error {
	s.updatedEffectiveStatuses = append(s.updatedEffectiveStatuses, status)
	for i := range s.users {
		if s.users[i].ID == userID {
			s.users[i].Status = status
		}
	}
	return nil
}

func TestAdminService_UpdateUserStatusReturnsEffectiveStatusAndInvalidatesAuthCache(t *testing.T) {
	repo := &effectiveStatusUpdateUserRepoStub{
		userRepoStubForListUsers: &userRepoStubForListUsers{
			userRepoStub: userRepoStub{user: &User{ID: 42, Email: "u@example.com", Status: StatusActive}},
			users:        []User{{ID: 42, Email: "u@example.com", Status: StatusActive}},
		},
	}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo:             repo,
		authCacheInvalidator: invalidator,
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Status: UserDeviceStatusPendingActivation,
	})
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, UserDeviceStatusPendingActivation, updated.Status)
	require.Equal(t, []string{UserDeviceStatusPendingActivation}, repo.updatedEffectiveStatuses)
	require.NotNil(t, repo.lastUpdated)
	require.Equal(t, StatusActive, repo.lastUpdated.Status, "raw users.status must not store device activation states")
	require.Equal(t, []int64{42}, invalidator.userIDs)
}

func TestAdminService_UpdateUserRejectsAdminPromotionWithDisabledEffectiveStatus(t *testing.T) {
	repo := &effectiveStatusUpdateUserRepoStub{
		userRepoStubForListUsers: &userRepoStubForListUsers{
			userRepoStub: userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser, Status: StatusActive}},
			users:        []User{{ID: 42, Email: "u@example.com", Role: RoleUser, Status: StatusActive}},
		},
	}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:   RoleAdmin,
		Status: StatusDisabled,
	})
	require.Error(t, err)
	require.Nil(t, repo.lastUpdated)
	require.Empty(t, repo.updatedEffectiveStatuses)
}
