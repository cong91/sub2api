//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type vclawUserDeviceRepoStub struct {
	byHash                map[string]*UserDevice
	byLoginRedeemCodeID   map[int64]*UserDevice
	createErr             error
	updateLastClaimedErr  error
	updateLastLoginErr    error
	created               []*UserDevice
	updatedLastClaimedIDs []int64
	updatedLastLoginIDs   []int64
}

func (s *vclawUserDeviceRepoStub) GetByDeviceHash(_ context.Context, deviceHash string) (*UserDevice, error) {
	if s.byHash == nil {
		return nil, ErrUserDeviceNotFound
	}
	device := s.byHash[deviceHash]
	if device == nil {
		return nil, ErrUserDeviceNotFound
	}
	clone := *device
	return &clone, nil
}

func (s *vclawUserDeviceRepoStub) GetByLoginRedeemCodeID(_ context.Context, codeID int64) (*UserDevice, error) {
	if s.byLoginRedeemCodeID == nil {
		return nil, ErrUserDeviceNotFound
	}
	device := s.byLoginRedeemCodeID[codeID]
	if device == nil {
		return nil, ErrUserDeviceNotFound
	}
	clone := *device
	return &clone, nil
}

func (s *vclawUserDeviceRepoStub) Create(_ context.Context, device *UserDevice) error {
	if s.createErr != nil {
		return s.createErr
	}
	clone := *device
	if clone.ID == 0 {
		clone.ID = int64(len(s.created) + 1)
		device.ID = clone.ID
	}
	s.created = append(s.created, &clone)
	return nil
}

func (s *vclawUserDeviceRepoStub) UpdateLastClaimedAt(_ context.Context, id int64, at time.Time) error {
	if s.updateLastClaimedErr != nil {
		return s.updateLastClaimedErr
	}
	s.updatedLastClaimedIDs = append(s.updatedLastClaimedIDs, id)
	_ = at
	return nil
}

func (s *vclawUserDeviceRepoStub) UpdateLastLoginAt(_ context.Context, id int64, at time.Time) error {
	if s.updateLastLoginErr != nil {
		return s.updateLastLoginErr
	}
	s.updatedLastLoginIDs = append(s.updatedLastLoginIDs, id)
	_ = at
	return nil
}

type vclawRedeemRepoStub struct {
	inviteRedeemRepoStub
	created []*RedeemCode
}

func (s *vclawRedeemRepoStub) GetByID(_ context.Context, codeID int64) (*RedeemCode, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.code != nil && s.code.ID == codeID {
		return s.code, nil
	}
	return nil, ErrRedeemCodeNotFound
}

func (s *vclawRedeemRepoStub) Create(_ context.Context, code *RedeemCode) error {
	clone := *code
	if clone.ID == 0 {
		clone.ID = int64(len(s.created) + 100)
		code.ID = clone.ID
	}
	s.created = append(s.created, &clone)
	return nil
}

func newVClawClaimServiceForTest(userRepo UserRepository, redeemRepo RedeemCodeRepository, userDeviceRepo UserDeviceRepository) *VClawClaimService {
	return newVClawClaimServiceForTestWithSettings(userRepo, redeemRepo, userDeviceRepo, nil)
}

func newVClawClaimServiceForTestWithSettings(userRepo UserRepository, redeemRepo RedeemCodeRepository, userDeviceRepo UserDeviceRepository, settingService *SettingService) *VClawClaimService {
	return NewVClawClaimService(
		nil,
		userRepo,
		redeemRepo,
		userDeviceRepo,
		&config.Config{Default: config.DefaultConfig{UserBalance: 1.5, UserConcurrency: 2}},
		settingService,
	)
}

func TestVClawClaimService_Claim_ResumeExistingDeviceReturnsExistingLoginCode(t *testing.T) {
	t.Parallel()

	loginUserID := int64(55)
	loginCodeID := int64(88)
	resumeHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	deviceRepo := &vclawUserDeviceRepoStub{
		byHash: map[string]*UserDevice{
			resumeHash: {
				ID:                9,
				UserID:            loginUserID,
				DeviceHash:        resumeHash,
				Status:            UserDeviceStatusActive,
				LoginRedeemCodeID: loginCodeID,
			},
		},
	}
	redeemRepo := &vclawRedeemRepoStub{
		inviteRedeemRepoStub: inviteRedeemRepoStub{code: &RedeemCode{ID: loginCodeID, Code: "DLG-AAAA-BBBB-CCCC", Type: RedeemTypeDeviceLogin, Status: StatusUsed, UsedBy: &loginUserID}},
	}
	svc := newVClawClaimServiceForTest(&userRepoStub{}, redeemRepo, deviceRepo)

	result, err := svc.Claim(context.Background(), VClawClaimRequest{
		Device: VClawDeviceInput{
			DeviceHash:         resumeHash,
			FingerprintVersion: 1,
			Platform:           "win32",
			Arch:               "x64",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "resume", result.Mode)
	require.Equal(t, "DLG-AAAA-BBBB-CCCC", result.DeviceLoginCode)
	require.Equal(t, []int64{9}, deviceRepo.updatedLastClaimedIDs)
}

func TestVClawClaimService_Claim_FirstClaimCreatesBindingAndLoginCode(t *testing.T) {
	t.Parallel()

	claimCodeID := int64(77)
	claimUserID := int64(501)
	redeemRepo := &vclawRedeemRepoStub{
		inviteRedeemRepoStub: inviteRedeemRepoStub{code: &RedeemCode{ID: claimCodeID, Code: "DCL-AAAA-BBBB-CCCC", Type: RedeemTypeDeviceClaim, Status: StatusUnused}},
	}
	userRepo := &userRepoStub{nextID: claimUserID}
	deviceRepo := &vclawUserDeviceRepoStub{}
	svc := newVClawClaimServiceForTest(userRepo, redeemRepo, deviceRepo)

	result, err := svc.Claim(context.Background(), VClawClaimRequest{
		ClaimCode: "DCL-AAAA-BBBB-CCCC",
		Device: VClawDeviceInput{
			DeviceHash:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			FingerprintVersion: 1,
			InstallID:          "install-1",
			Platform:           "win32",
			Arch:               "x64",
			AppVersion:         "1.0.0",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "first_claim", result.Mode)
	require.Equal(t, claimUserID, result.UserID)
	require.Len(t, userRepo.created, 1)
	require.Len(t, redeemRepo.created, 1)
	require.Equal(t, RedeemTypeDeviceLogin, redeemRepo.created[0].Type)
	require.Equal(t, StatusUsed, redeemRepo.created[0].Status)
	require.NotEmpty(t, result.DeviceLoginCode)
	require.Len(t, deviceRepo.created, 1)
	require.Equal(t, claimUserID, deviceRepo.created[0].UserID)
	require.Equal(t, redeemRepo.created[0].ID, deviceRepo.created[0].LoginRedeemCodeID)
	require.Equal(t, claimCodeID, redeemRepo.usedID)
	require.Equal(t, claimUserID, redeemRepo.usedUser)
}

func TestVClawClaimService_Claim_FirstLaunchWithoutClaimCodeAutoCreatesBindingAndLoginCode(t *testing.T) {
	t.Parallel()

	userRepo := &userRepoStub{nextID: 777}
	deviceRepo := &vclawUserDeviceRepoStub{}
	redeemRepo := &vclawRedeemRepoStub{}
	svc := newVClawClaimServiceForTest(userRepo, redeemRepo, deviceRepo)

	result, err := svc.Claim(context.Background(), VClawClaimRequest{
		Device: VClawDeviceInput{
			DeviceHash:         "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			FingerprintVersion: 1,
			InstallID:          "install-auto-1",
			Platform:           "win32",
			Arch:               "x64",
			AppVersion:         "1.0.0",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "first_claim", result.Mode)
	require.Equal(t, int64(777), result.UserID)
	require.NotEmpty(t, result.DeviceLoginCode)
	require.Len(t, userRepo.created, 1)
	require.Len(t, redeemRepo.created, 1)
	require.Equal(t, RedeemTypeDeviceLogin, redeemRepo.created[0].Type)
	require.Equal(t, StatusUsed, redeemRepo.created[0].Status)
	require.Len(t, deviceRepo.created, 1)
	require.Equal(t, int64(777), deviceRepo.created[0].UserID)
	require.Nil(t, deviceRepo.created[0].ClaimRedeemCodeID)
	require.Equal(t, redeemRepo.created[0].ID, deviceRepo.created[0].LoginRedeemCodeID)
}

func TestVClawClaimService_Claim_FirstLaunchAppliesConfiguredDeviceClaimBonus(t *testing.T) {
	t.Parallel()

	userRepo := &userRepoStub{nextID: 778}
	deviceRepo := &vclawUserDeviceRepoStub{}
	redeemRepo := &vclawRedeemRepoStub{}
	settingService := NewSettingService(&settingRepoStub{values: map[string]string{SettingKeyDeviceClaimBonusBalance: "3.25"}}, &config.Config{})
	svc := newVClawClaimServiceForTestWithSettings(userRepo, redeemRepo, deviceRepo, settingService)

	result, err := svc.Claim(context.Background(), VClawClaimRequest{
		Device: VClawDeviceInput{
			DeviceHash:         "edededededededededededededededededededededededededededededededed",
			FingerprintVersion: 1,
			InstallID:          "install-bonus-1",
			Platform:           "win32",
			Arch:               "x64",
			AppVersion:         "1.0.0",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "first_claim", result.Mode)
	require.Equal(t, []float64{3.25}, userRepo.balanceUpdates)
	require.Len(t, redeemRepo.created, 2)
	require.Equal(t, AdjustmentTypeAdminBalance, redeemRepo.created[0].Type)
	require.Equal(t, 3.25, redeemRepo.created[0].Value)
	require.Equal(t, RedeemTypeDeviceLogin, redeemRepo.created[1].Type)
}

func TestAuthService_InviteLogin_DeviceLoginUsesExistingBoundUser(t *testing.T) {
	t.Parallel()

	userID := int64(900)
	loginCodeID := int64(333)
	deviceHash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	userRepo := &userRepoStub{user: &User{ID: userID, Email: "device@example.com", Role: RoleUser, Status: StatusActive}}
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: loginCodeID, Code: "DLG-AAAA-BBBB-CCCC", Type: RedeemTypeDeviceLogin, Status: StatusUsed, UsedBy: &userID}}
	deviceRepo := &vclawUserDeviceRepoStub{
		byLoginRedeemCodeID: map[int64]*UserDevice{
			loginCodeID: {
				ID:                22,
				UserID:            userID,
				DeviceHash:        deviceHash,
				Status:            UserDeviceStatusActive,
				LoginRedeemCodeID: loginCodeID,
			},
		},
	}
	bootstrapSvc := &inviteBootstrapAPIKeySvcStub{groups: []Group{{ID: 7, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeStandard, RateMultiplier: 1, SortOrder: 1}}, keys: []*APIKey{{ID: 10, Name: "bootstrap-openai", Key: "sk-openai"}}}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.SetUserDeviceRepository(deviceRepo)
	svc.SetInviteBootstrapAPIKeyService(bootstrapSvc)

	tokenPair, user, keys, err := svc.InviteLogin(context.Background(), InviteLoginInput{
		InvitationCode: "DLG-AAAA-BBBB-CCCC",
		DeviceHash:     deviceHash,
	})
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.Equal(t, userID, user.ID)
	require.Len(t, keys, 1)
	require.Empty(t, userRepo.created)
	require.Zero(t, redeemRepo.usedID)
	require.Equal(t, []int64{22}, deviceRepo.updatedLastLoginIDs)
}

func TestAuthService_InviteLogin_DeviceLoginRejectsDeviceMismatch(t *testing.T) {
	t.Parallel()

	userID := int64(901)
	loginCodeID := int64(334)
	userRepo := &userRepoStub{user: &User{ID: userID, Email: "device@example.com", Role: RoleUser, Status: StatusActive}}
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: loginCodeID, Code: "DLG-ZZZZ-YYYY-XXXX", Type: RedeemTypeDeviceLogin, Status: StatusUsed, UsedBy: &userID}}
	deviceRepo := &vclawUserDeviceRepoStub{
		byLoginRedeemCodeID: map[int64]*UserDevice{
			loginCodeID: {
				ID:                23,
				UserID:            userID,
				DeviceHash:        "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
				Status:            UserDeviceStatusActive,
				LoginRedeemCodeID: loginCodeID,
			},
		},
	}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.SetUserDeviceRepository(deviceRepo)

	_, _, _, err := svc.InviteLogin(context.Background(), InviteLoginInput{
		InvitationCode: "DLG-ZZZZ-YYYY-XXXX",
		DeviceHash:     "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	})
	require.ErrorIs(t, err, ErrDeviceMismatch)
}

func TestAuthService_RedeemLogin_SucceedsWithBootstrapInvitation(t *testing.T) {
	t.Parallel()

	userRepo := &userRepoStub{user: &User{ID: 43, Email: "web@example.com", Role: RoleUser, Status: StatusActive}, nextID: 43}
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 335, Code: "INV-QQQQ-WWWW-EEEE", Type: RedeemTypeInvitation, Status: StatusUnused, UsedBy: nil}}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.defaultSubAssigner = &inviteDefaultSubAssignerStub{}
	svc.SetInviteBootstrapGroupRepository(&inviteGroupRepoStub{groups: []Group{{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, ActiveAccountCount: 1, SubscriptionType: SubscriptionTypeSubscription, DefaultValidityDays: 30, SortOrder: 1}}})
	svc.SetInviteBootstrapAPIKeyService(&inviteBootstrapAPIKeySvcStub{keys: []*APIKey{{ID: 1, Name: "bootstrap-openai", Key: "sk-openai"}}})

	tokenPair, user, keys, err := svc.RedeemLogin(context.Background(), "INV-QQQQ-WWWW-EEEE")
	require.NoError(t, err)
	require.NotNil(t, tokenPair)
	require.NotNil(t, user)
	require.Equal(t, int64(43), user.ID)
	require.Len(t, keys, 1)
}

func TestAuthService_RedeemLogin_RejectsDeviceLoginCode(t *testing.T) {
	t.Parallel()

	userRepo := &userRepoStub{user: &User{ID: 44, Email: "web@example.com", Role: RoleUser, Status: StatusActive}}
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: 336, Code: "DLG-RRRR-TTTT-YYYY", Type: RedeemTypeDeviceLogin, Status: StatusUsed}}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)

	_, _, _, err := svc.RedeemLogin(context.Background(), "DLG-RRRR-TTTT-YYYY")
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
}

func TestAuthService_InviteLogin_DeviceLoginStillRequiresDeviceHashOutsideWeb(t *testing.T) {

	t.Parallel()

	userID := int64(903)
	loginCodeID := int64(336)
	userRepo := &userRepoStub{user: &User{ID: userID, Email: "device@example.com", Role: RoleUser, Status: StatusActive}}
	redeemRepo := &inviteRedeemRepoStub{code: &RedeemCode{ID: loginCodeID, Code: "DLG-RRRR-TTTT-YYYY", Type: RedeemTypeDeviceLogin, Status: StatusUsed, UsedBy: &userID}}
	deviceRepo := &vclawUserDeviceRepoStub{
		byLoginRedeemCodeID: map[int64]*UserDevice{
			loginCodeID: {
				ID:                25,
				UserID:            userID,
				DeviceHash:        "abababababababababababababababababababababababababababababababab",
				Status:            UserDeviceStatusActive,
				LoginRedeemCodeID: loginCodeID,
			},
		},
	}
	svc := newAuthServiceForInviteLoginTest(userRepo, redeemRepo)
	svc.SetUserDeviceRepository(deviceRepo)

	_, _, _, err := svc.InviteLogin(context.Background(), InviteLoginInput{InvitationCode: "DLG-RRRR-TTTT-YYYY"})
	require.ErrorIs(t, err, ErrDeviceHashRequired)
}

func TestVClawClaimService_Claim_ResumeRejectsRevokedDevice(t *testing.T) {
	t.Parallel()

	revokedHash := "1111111111111111111111111111111111111111111111111111111111111111"
	deviceRepo := &vclawUserDeviceRepoStub{
		byHash: map[string]*UserDevice{
			revokedHash: {ID: 5, DeviceHash: revokedHash, Status: UserDeviceStatusRevoked, LoginRedeemCodeID: 7},
		},
	}
	svc := newVClawClaimServiceForTest(&userRepoStub{}, &vclawRedeemRepoStub{}, deviceRepo)

	_, err := svc.Claim(context.Background(), VClawClaimRequest{Device: VClawDeviceInput{DeviceHash: revokedHash, FingerprintVersion: 1, Platform: "win32", Arch: "x64"}})
	require.ErrorIs(t, err, ErrDeviceRevoked)
}

func TestVClawClaimService_Claim_PropagatesMissingLoginCodeOnResume(t *testing.T) {
	t.Parallel()

	resumeMissingHash := "2222222222222222222222222222222222222222222222222222222222222222"
	deviceRepo := &vclawUserDeviceRepoStub{byHash: map[string]*UserDevice{resumeMissingHash: {ID: 9, DeviceHash: resumeMissingHash, Status: UserDeviceStatusActive, LoginRedeemCodeID: 999}}}
	redeemRepo := &vclawRedeemRepoStub{inviteRedeemRepoStub: inviteRedeemRepoStub{getErr: ErrRedeemCodeNotFound}}
	svc := newVClawClaimServiceForTest(&userRepoStub{}, redeemRepo, deviceRepo)

	_, err := svc.Claim(context.Background(), VClawClaimRequest{Device: VClawDeviceInput{DeviceHash: resumeMissingHash, FingerprintVersion: 1, Platform: "win32", Arch: "x64"}})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrClaimCodeRequired))
}
