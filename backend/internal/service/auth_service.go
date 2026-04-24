package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"

	"entgo.io/ent/dialect"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials         = infraerrors.Unauthorized("INVALID_CREDENTIALS", "invalid email or password")
	ErrUserNotActive              = infraerrors.Forbidden("USER_NOT_ACTIVE", "user is not active")
	ErrEmailExists                = infraerrors.Conflict("EMAIL_EXISTS", "email already exists")
	ErrEmailReserved              = infraerrors.BadRequest("EMAIL_RESERVED", "email is reserved")
	ErrInvalidToken               = infraerrors.Unauthorized("INVALID_TOKEN", "invalid token")
	ErrTokenExpired               = infraerrors.Unauthorized("TOKEN_EXPIRED", "token has expired")
	ErrAccessTokenExpired         = infraerrors.Unauthorized("ACCESS_TOKEN_EXPIRED", "access token has expired")
	ErrTokenTooLarge              = infraerrors.BadRequest("TOKEN_TOO_LARGE", "token too large")
	ErrTokenRevoked               = infraerrors.Unauthorized("TOKEN_REVOKED", "token has been revoked")
	ErrRefreshTokenInvalid        = infraerrors.Unauthorized("REFRESH_TOKEN_INVALID", "invalid refresh token")
	ErrRefreshTokenExpired        = infraerrors.Unauthorized("REFRESH_TOKEN_EXPIRED", "refresh token has expired")
	ErrRefreshTokenReused         = infraerrors.Unauthorized("REFRESH_TOKEN_REUSED", "refresh token has been reused")
	ErrEmailVerifyRequired        = infraerrors.BadRequest("EMAIL_VERIFY_REQUIRED", "email verification is required")
	ErrEmailSuffixNotAllowed      = infraerrors.BadRequest("EMAIL_SUFFIX_NOT_ALLOWED", "email suffix is not allowed")
	ErrRegDisabled                = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrServiceUnavailable         = infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "service temporarily unavailable")
	ErrInvitationCodeRequired     = infraerrors.BadRequest("INVITATION_CODE_REQUIRED", "invitation code is required")
	ErrInvitationCodeInvalid      = infraerrors.BadRequest("INVITATION_CODE_INVALID", "invalid or used invitation code")
	ErrOAuthInvitationRequired    = infraerrors.Forbidden("OAUTH_INVITATION_REQUIRED", "invitation code required to complete oauth registration")
	ErrBootstrapAPIKeyUnavailable = infraerrors.ServiceUnavailable("BOOTSTRAP_API_KEY_UNAVAILABLE", "failed to provision bootstrap api keys")
)

// maxTokenLength 限制 token 大小，避免超长 header 触发解析时的异常内存分配。
const maxTokenLength = 8192

// refreshTokenPrefix is the prefix for refresh tokens to distinguish them from access tokens.
const refreshTokenPrefix = "rt_"

// JWTClaims JWT载荷数据
type JWTClaims struct {
	UserID       int64  `json:"user_id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	TokenVersion int64  `json:"token_version"` // Used to invalidate tokens on password change
	jwt.RegisteredClaims
}

// AuthService 认证服务
type AuthService struct {
	entClient                *dbent.Client
	userRepo                 UserRepository
	redeemRepo               RedeemCodeRepository
	refreshTokenCache        RefreshTokenCache
	cfg                      *config.Config
	settingService           *SettingService
	emailService             *EmailService
	turnstileService         *TurnstileService
	emailQueueService        *EmailQueueService
	promoService             *PromoService
	affiliateService         *AffiliateService
	defaultSubAssigner       DefaultSubscriptionAssigner
	inviteBootstrapAPIKeySvc InviteBootstrapAPIKeyService
	groupRepo                GroupRepository
	userDeviceRepo           UserDeviceRepository
}

type DefaultSubscriptionAssigner interface {
	AssignOrExtendSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error)
}

type signupGrantPlan struct {
	Balance       float64
	Concurrency   int
	Subscriptions []DefaultSubscriptionSetting
}

type InviteBootstrapAPIKey struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Key      string `json:"key"`
	GroupID  int64  `json:"group_id"`
	Platform string `json:"platform"`
}

type InviteBootstrapAPIKeyService interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
	Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error)
}

// NewAuthService 创建认证服务实例
func NewAuthService(
	entClient *dbent.Client,
	userRepo UserRepository,
	redeemRepo RedeemCodeRepository,
	refreshTokenCache RefreshTokenCache,
	cfg *config.Config,
	settingService *SettingService,
	emailService *EmailService,
	turnstileService *TurnstileService,
	emailQueueService *EmailQueueService,
	promoService *PromoService,
	defaultSubAssigner DefaultSubscriptionAssigner,
	affiliateService *AffiliateService,
) *AuthService {
	return &AuthService{
		entClient:          entClient,
		userRepo:           userRepo,
		redeemRepo:         redeemRepo,
		refreshTokenCache:  refreshTokenCache,
		cfg:                cfg,
		settingService:     settingService,
		emailService:       emailService,
		turnstileService:   turnstileService,
		emailQueueService:  emailQueueService,
		promoService:       promoService,
		affiliateService:   affiliateService,
		defaultSubAssigner: defaultSubAssigner,
		groupRepo:          nil,
	}
}

// SetInviteBootstrapAPIKeyService sets the API key service used by invite-login bootstrap flow.
func (s *AuthService) SetInviteBootstrapAPIKeyService(svc InviteBootstrapAPIKeyService) {
	s.inviteBootstrapAPIKeySvc = svc
}

func (s *AuthService) SetInviteBootstrapGroupRepository(repo GroupRepository) {
	s.groupRepo = repo
}
func (s *AuthService) SetAffiliateService(affiliateService *AffiliateService) {
	if s == nil {
		return
	}
	s.affiliateService = affiliateService
}

// SetUserDeviceRepository sets the optional user-device repository used by device claim/login flows.
func (s *AuthService) SetUserDeviceRepository(repo UserDeviceRepository) {
	s.userDeviceRepo = repo
}

// EntClient exposes the underlying ent client for flows that need direct ent-backed services.
func (s *AuthService) EntClient() *dbent.Client {
	if s == nil {
		return nil
	}
	return s.entClient
}

// Register 用户注册，返回token和用户
func (s *AuthService) Register(ctx context.Context, email, password string) (string, *User, error) {
	return s.RegisterWithVerification(ctx, email, password, "", "", "", "")
}

// RegisterWithVerification 用户注册（支持邮件验证、优惠码、邀请码和邀请返利码），返回token和用户。
func (s *AuthService) RegisterWithVerification(ctx context.Context, email, password, verifyCode, promoCode, invitationCode, affiliateCode string) (string, *User, error) {
	// 检查是否开放注册（默认关闭：settingService 未配置时不允许注册）
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		return "", nil, ErrRegDisabled
	}

	// 防止用户注册 LinuxDo OAuth 合成邮箱，避免第三方登录与本地账号发生碰撞。
	if isReservedEmail(email) {
		return "", nil, ErrEmailReserved
	}
	if err := s.validateRegistrationEmailPolicy(ctx, email); err != nil {
		return "", nil, err
	}

	// 检查是否需要邀请码
	var invitationRedeemCode *RedeemCode
	if s.settingService != nil && s.settingService.IsInvitationCodeEnabled(ctx) {
		if invitationCode == "" {
			return "", nil, ErrInvitationCodeRequired
		}
		// 验证邀请码
		redeemCode, err := s.redeemRepo.GetByCode(ctx, invitationCode)
		if err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Invalid invitation code: %s, error: %v", invitationCode, err)
			return "", nil, ErrInvitationCodeInvalid
		}
		// 检查类型和状态
		if redeemCode.Type != RedeemTypeInvitation || redeemCode.Status != StatusUnused {
			logger.LegacyPrintf("service.auth", "[Auth] Invitation code invalid: type=%s, status=%s", redeemCode.Type, redeemCode.Status)
			return "", nil, ErrInvitationCodeInvalid
		}
		invitationRedeemCode = redeemCode
	}

	// 检查是否需要邮件验证
	if s.settingService != nil && s.settingService.IsEmailVerifyEnabled(ctx) {
		// 如果邮件验证已开启但邮件服务未配置，拒绝注册
		// 这是一个配置错误，不应该允许绕过验证
		if s.emailService == nil {
			logger.LegacyPrintf("service.auth", "%s", "[Auth] Email verification enabled but email service not configured, rejecting registration")
			return "", nil, ErrServiceUnavailable
		}
		if verifyCode == "" {
			return "", nil, ErrEmailVerifyRequired
		}
		// 验证邮箱验证码
		if err := s.emailService.VerifyCode(ctx, email, verifyCode); err != nil {
			return "", nil, fmt.Errorf("verify code: %w", err)
		}
	}

	// 检查邮箱是否已存在
	existsEmail, err := s.userRepo.ExistsByEmail(ctx, email)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return "", nil, ErrServiceUnavailable
	}
	if existsEmail {
		return "", nil, ErrEmailExists
	}

	// 密码哈希
	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return "", nil, fmt.Errorf("hash password: %w", err)
	}

	// 获取默认配置
	defaultBalance := s.cfg.Default.UserBalance
	defaultConcurrency := s.cfg.Default.UserConcurrency
	if s.settingService != nil {
		defaultBalance = s.settingService.GetDefaultBalance(ctx)
		defaultConcurrency = s.settingService.GetDefaultConcurrency(ctx)
	}

	// 创建用户
	user := &User{
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      defaultBalance,
		Concurrency:  defaultConcurrency,
		Status:       StatusActive,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		// 优先检查邮箱冲突错误（竞态条件下可能发生）
		if errors.Is(err, ErrEmailExists) {
			return "", nil, ErrEmailExists
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error creating user: %v", err)
		return "", nil, ErrServiceUnavailable
	}
	s.postAuthUserBootstrap(ctx, user, "email", true)
	s.assignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
	if s.affiliateService != nil {
		if _, err := s.affiliateService.EnsureUserAffiliate(ctx, user.ID); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to initialize affiliate profile for user %d: %v", user.ID, err)
		}
		if code := strings.TrimSpace(affiliateCode); code != "" {
			if err := s.affiliateService.BindInviterByCode(ctx, user.ID, code); err != nil {
				// 邀请返利码绑定失败不影响注册，只记录日志
				logger.LegacyPrintf("service.auth", "[Auth] Failed to bind affiliate inviter for user %d: %v", user.ID, err)
			}
		}
	}
	if len(grantPlan.Subscriptions) == 0 {
		s.assignDefaultSubscriptions(ctx, user.ID)
	}

	// 标记邀请码为已使用（如果使用了邀请码）
	if invitationRedeemCode != nil {
		if err := s.redeemRepo.Use(ctx, invitationRedeemCode.ID, user.ID); err != nil {
			// 邀请码标记失败不影响注册，只记录日志
			logger.LegacyPrintf("service.auth", "[Auth] Failed to mark invitation code as used for user %d: %v", user.ID, err)
		}
	}
	// 应用优惠码（如果提供且功能已启用）
	if promoCode != "" && s.promoService != nil && s.settingService != nil && s.settingService.IsPromoCodeEnabled(ctx) {
		if err := s.promoService.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
			// 优惠码应用失败不影响注册，只记录日志
			logger.LegacyPrintf("service.auth", "[Auth] Failed to apply promo code for user %d: %v", user.ID, err)
		} else {
			// 重新获取用户信息以获取更新后的余额
			if updatedUser, err := s.userRepo.GetByID(ctx, user.ID); err == nil {
				user = updatedUser
			}
		}
	}

	// 生成token
	token, err := s.GenerateToken(user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

type InviteLoginInput struct {
	InvitationCode string
	DeviceHash     string
	InstallID      string
}

// InviteLogin handles device-bound invite login and resume flows.
func (s *AuthService) InviteLogin(ctx context.Context, input InviteLoginInput) (*TokenPair, *User, []InviteBootstrapAPIKey, error) {
	code := strings.TrimSpace(input.InvitationCode)
	if code == "" {
		return nil, nil, nil, ErrInvitationCodeRequired
	}
	if s.redeemRepo == nil {
		return nil, nil, nil, ErrServiceUnavailable
	}

	redeemCode, err := s.redeemRepo.GetByCode(ctx, code)
	if err != nil {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if redeemCode == nil {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if redeemCode.Type == RedeemTypeDeviceLogin {
		return s.inviteLoginWithDeviceCode(ctx, redeemCode, input)
	}
	if redeemCode.Status != StatusUnused {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if !isInviteLoginBootstrapRedeemType(redeemCode.Type) {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}

	return s.completeInviteBootstrapLogin(ctx, redeemCode)
}

// RedeemLogin handles the web redeem login flow.
func (s *AuthService) RedeemLogin(ctx context.Context, invitationCode string) (*TokenPair, *User, []InviteBootstrapAPIKey, error) {
	code := strings.TrimSpace(invitationCode)
	if code == "" {
		return nil, nil, nil, ErrInvitationCodeRequired
	}
	if s.redeemRepo == nil {
		return nil, nil, nil, ErrServiceUnavailable
	}

	redeemCode, err := s.redeemRepo.GetByCode(ctx, code)
	if err != nil || redeemCode == nil {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if redeemCode.Type == RedeemTypeDeviceLogin {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if redeemCode.Status != StatusUnused {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if !isInviteLoginBootstrapRedeemType(redeemCode.Type) {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}

	return s.completeInviteBootstrapLogin(ctx, redeemCode)
}

func (s *AuthService) createInviteBootstrapUser(ctx context.Context, invitationRedeemCode *RedeemCode) (*User, error) {
	return createInviteBootstrapUserWithRedeem(ctx, s.entClient, s.userRepo, s.redeemRepo, s.cfg, s.settingService, invitationRedeemCode)
}

func (s *AuthService) inviteLoginWithDeviceCode(ctx context.Context, redeemCode *RedeemCode, input InviteLoginInput) (*TokenPair, *User, []InviteBootstrapAPIKey, error) {
	if redeemCode == nil || redeemCode.Type != RedeemTypeDeviceLogin {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if s.userDeviceRepo == nil {
		return nil, nil, nil, ErrServiceUnavailable
	}
	deviceHash := normalizeDeviceHash(input.DeviceHash)
	if deviceHash == "" {
		return nil, nil, nil, ErrDeviceHashRequired
	}
	binding, err := s.userDeviceRepo.GetByLoginRedeemCodeID(ctx, redeemCode.ID)
	if err != nil || binding == nil {
		return nil, nil, nil, ErrInvitationCodeInvalid
	}
	if binding.Status != UserDeviceStatusActive {
		return nil, nil, nil, ErrDeviceRevoked
	}
	if normalizeDeviceHash(binding.DeviceHash) != deviceHash {
		return nil, nil, nil, ErrDeviceMismatch
	}
	user, err := s.userRepo.GetByID(ctx, binding.UserID)
	if err != nil || user == nil {
		return nil, nil, nil, ErrServiceUnavailable
	}
	bootstrapKeys, err := s.provisionInviteBootstrapAPIKeys(ctx, user.ID, redeemCode)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := s.userDeviceRepo.UpdateLastLoginAt(ctx, binding.ID, time.Now().UTC()); err != nil {
		return nil, nil, nil, ErrServiceUnavailable
	}
	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, bootstrapKeys, nil
}

func (s *AuthService) completeInviteBootstrapLogin(ctx context.Context, redeemCode *RedeemCode) (*TokenPair, *User, []InviteBootstrapAPIKey, error) {
	user, err := s.createInviteBootstrapUser(ctx, redeemCode)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := s.applyInviteLoginEntitlement(ctx, user.ID, redeemCode); err != nil {
		return nil, nil, nil, err
	}
	if updatedUser, err := s.userRepo.GetByID(ctx, user.ID); err == nil {
		user = updatedUser
	}

	bootstrapKeys, err := s.provisionInviteBootstrapAPIKeys(ctx, user.ID, redeemCode)
	if err != nil {
		return nil, nil, nil, err
	}

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, bootstrapKeys, nil
}

func (s *AuthService) provisionInviteBootstrapAPIKeys(ctx context.Context, userID int64, redeemCode *RedeemCode) ([]InviteBootstrapAPIKey, error) {
	if s.inviteBootstrapAPIKeySvc == nil || redeemCode == nil {
		return nil, ErrBootstrapAPIKeyUnavailable
	}

	groups, err := s.loadInviteBootstrapGroups(ctx, userID, redeemCode)
	if err != nil {
		return nil, ErrBootstrapAPIKeyUnavailable
	}
	selectedGroups := selectInviteBootstrapGroupsForRedeem(redeemCode, groups)
	if err := s.ensureInviteBootstrapSubscriptions(ctx, userID, redeemCode, selectedGroups); err != nil {
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] ensure subscription failed: redeem_type=%s user_id=%d err=%v", redeemCode.Type, userID, err)
		return nil, ErrBootstrapAPIKeyUnavailable
	}
	if len(selectedGroups) == 0 {
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] no selected groups: redeem_type=%s user_id=%d candidate_count=%d", redeemCode.Type, userID, len(groups))
		return nil, ErrBootstrapAPIKeyUnavailable
	}
	for platform, group := range selectedGroups {
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] selected group: redeem_type=%s user_id=%d platform=%s group_id=%d subscription_type=%s active_accounts=%d", redeemCode.Type, userID, platform, group.ID, group.SubscriptionType, group.ActiveAccountCount)
	}

	platforms := make([]string, 0, len(selectedGroups))
	for platform := range selectedGroups {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	keys := make([]InviteBootstrapAPIKey, 0, len(platforms))
	for _, platform := range platforms {
		group := selectedGroups[platform]
		groupID := group.ID
		created, createErr := s.inviteBootstrapAPIKeySvc.Create(ctx, userID, CreateAPIKeyRequest{
			Name:    "bootstrap-" + sanitizePlatformForBootstrapKey(platform),
			GroupID: &groupID,
		})
		if createErr != nil {
			logger.LegacyPrintf("service.auth", "[InviteBootstrap] create api key failed: redeem_type=%s user_id=%d platform=%s group_id=%d err=%v", redeemCode.Type, userID, platform, groupID, createErr)
			continue
		}
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] created api key: redeem_type=%s user_id=%d platform=%s group_id=%d api_key_id=%d", redeemCode.Type, userID, platform, groupID, created.ID)
		keys = append(keys, InviteBootstrapAPIKey{
			ID:       created.ID,
			Name:     created.Name,
			Key:      created.Key,
			GroupID:  group.ID,
			Platform: group.Platform,
		})
	}

	if len(keys) == 0 {
		return nil, ErrBootstrapAPIKeyUnavailable
	}
	return keys, nil
}

func (s *AuthService) ensureInviteBootstrapSubscriptions(ctx context.Context, userID int64, redeemCode *RedeemCode, selectedGroups map[string]Group) error {
	if redeemCode == nil {
		return ErrBootstrapAPIKeyUnavailable
	}
	if redeemCode.Type != RedeemTypeSubscription && redeemCode.Type != RedeemTypeInvitation {
		return nil
	}
	if len(selectedGroups) == 0 {
		return ErrBootstrapAPIKeyUnavailable
	}
	if s.defaultSubAssigner == nil {
		return ErrBootstrapAPIKeyUnavailable
	}

	validityDays := inviteBootstrapValidityDays(redeemCode)
	platforms := make([]string, 0, len(selectedGroups))
	for platform := range selectedGroups {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	for _, platform := range platforms {
		group := selectedGroups[platform]
		if _, _, err := s.defaultSubAssigner.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:       userID,
			GroupID:      group.ID,
			ValidityDays: validityDays,
			AssignedBy:   0,
			Notes:        "invite-login bootstrap subscription",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *AuthService) applyInviteLoginEntitlement(ctx context.Context, userID int64, redeemCode *RedeemCode) error {
	if redeemCode == nil {
		return ErrInvitationCodeInvalid
	}
	if userID <= 0 {
		return ErrServiceUnavailable
	}

	switch redeemCode.Type {
	case RedeemTypeBalance:
		if redeemCode.Value != 0 {
			if err := s.userRepo.UpdateBalance(ctx, userID, redeemCode.Value); err != nil {
				return ErrServiceUnavailable
			}
		}
		return nil
	case RedeemTypeSubscription:
		if redeemCode.GroupID == nil || s.defaultSubAssigner == nil {
			return ErrServiceUnavailable
		}
		_, _, err := s.defaultSubAssigner.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:       userID,
			GroupID:      *redeemCode.GroupID,
			ValidityDays: inviteBootstrapValidityDays(redeemCode),
			AssignedBy:   0,
			Notes:        fmt.Sprintf("invite-login redeem subscription code %s", redeemCode.Code),
		})
		if err != nil {
			return ErrServiceUnavailable
		}
		return nil
	case RedeemTypeInvitation:
		return nil
	default:
		return ErrInvitationCodeInvalid
	}
}

func inviteBootstrapValidityDays(redeemCode *RedeemCode) int {
	if redeemCode == nil {
		return 30
	}
	if redeemCode.ValidityDays > 0 {
		return redeemCode.ValidityDays
	}
	return 30
}

func isInviteLoginBootstrapRedeemType(redeemType string) bool {
	switch redeemType {
	case RedeemTypeInvitation, RedeemTypeSubscription, RedeemTypeBalance, RedeemTypeDeviceLogin:
		return true
	default:
		return false
	}
}

func (s *AuthService) loadInviteBootstrapGroups(ctx context.Context, userID int64, redeemCode *RedeemCode) ([]Group, error) {
	if redeemCode == nil || s.inviteBootstrapAPIKeySvc == nil {
		return nil, ErrBootstrapAPIKeyUnavailable
	}
	if redeemCode.Type == RedeemTypeSubscription || redeemCode.Type == RedeemTypeInvitation {
		if s.groupRepo == nil {
			return nil, ErrBootstrapAPIKeyUnavailable
		}
		groups, err := s.groupRepo.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] active groups direct load: redeem_type=%s user_id=%d count=%d", redeemCode.Type, userID, len(groups))
		groups = loadInviteBootstrapSubscriptionCandidates(groups, redeemCode)
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] subscription candidates after override: redeem_type=%s user_id=%d count=%d", redeemCode.Type, userID, len(groups))
		return groups, nil
	}
	groups, err := s.inviteBootstrapAPIKeySvc.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	logger.LegacyPrintf("service.auth", "[InviteBootstrap] available groups before override: redeem_type=%s user_id=%d count=%d", redeemCode.Type, userID, len(groups))
	return groups, nil
}

func loadInviteBootstrapSubscriptionCandidates(groups []Group, redeemCode *RedeemCode) []Group {
	if redeemCode == nil {
		return groups
	}
	if redeemCode.Type != RedeemTypeSubscription && redeemCode.Type != RedeemTypeInvitation {
		return groups
	}
	if len(groups) == 0 {
		return groups
	}
	candidates := make([]Group, 0, len(groups))
	for _, group := range groups {
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] inspect group for subscription lane: redeem_type=%s group_id=%d platform=%s subscription_type=%s active=%v active_accounts=%d", redeemCode.Type, group.ID, group.Platform, group.SubscriptionType, group.IsActive(), group.ActiveAccountCount)
		if !group.IsActive() {
			logger.LegacyPrintf("service.auth", "[InviteBootstrap] drop group: reason=inactive redeem_type=%s group_id=%d", redeemCode.Type, group.ID)
			continue
		}
		if strings.TrimSpace(group.Platform) == "" {
			logger.LegacyPrintf("service.auth", "[InviteBootstrap] drop group: reason=empty_platform redeem_type=%s group_id=%d", redeemCode.Type, group.ID)
			continue
		}
		if !group.IsSubscriptionType() {
			logger.LegacyPrintf("service.auth", "[InviteBootstrap] drop group: reason=not_subscription redeem_type=%s group_id=%d subscription_type=%s", redeemCode.Type, group.ID, group.SubscriptionType)
			continue
		}
		if redeemCode.Type == RedeemTypeSubscription && redeemCode.GroupID != nil && group.ID != *redeemCode.GroupID {
			logger.LegacyPrintf("service.auth", "[InviteBootstrap] drop group: reason=group_id_mismatch redeem_type=%s expected_group_id=%d actual_group_id=%d", redeemCode.Type, *redeemCode.GroupID, group.ID)
			continue
		}
		logger.LegacyPrintf("service.auth", "[InviteBootstrap] keep group: redeem_type=%s group_id=%d platform=%s", redeemCode.Type, group.ID, group.Platform)
		candidates = append(candidates, group)
	}
	return candidates
}

func selectInviteBootstrapGroupsForRedeem(redeemCode *RedeemCode, groups []Group) map[string]Group {
	if redeemCode == nil {
		return nil
	}
	selected := make(map[string]Group)
	for _, group := range groups {
		platform := strings.TrimSpace(group.Platform)
		if platform == "" || !group.IsActive() || group.ActiveAccountCount <= 0 {
			continue
		}
		if !isGroupEligibleForInviteBootstrap(redeemCode, group) {
			continue
		}
		current, exists := selected[platform]
		if !exists || isInviteBootstrapGroupBetter(redeemCode, group, current) {
			selected[platform] = group
		}
	}
	return selected
}

func isGroupEligibleForInviteBootstrap(redeemCode *RedeemCode, group Group) bool {
	switch redeemCode.Type {
	case RedeemTypeSubscription, RedeemTypeInvitation:
		return group.IsSubscriptionType()
	case RedeemTypeBalance:
		return !group.IsSubscriptionType()
	case RedeemTypeDeviceLogin:
		return true
	default:
		return false
	}
}

func isInviteBootstrapGroupBetter(redeemCode *RedeemCode, a, b Group) bool {
	switch redeemCode.Type {
	case RedeemTypeBalance:
		if a.RateMultiplier != b.RateMultiplier {
			return a.RateMultiplier < b.RateMultiplier
		}
	case RedeemTypeSubscription, RedeemTypeInvitation:
		if a.DefaultValidityDays != b.DefaultValidityDays {
			return a.DefaultValidityDays > b.DefaultValidityDays
		}
	case RedeemTypeDeviceLogin:
		if a.IsSubscriptionType() != b.IsSubscriptionType() {
			return a.IsSubscriptionType()
		}
	}
	if a.SortOrder != b.SortOrder {
		return a.SortOrder < b.SortOrder
	}
	return a.ID < b.ID
}

func sanitizePlatformForBootstrapKey(platform string) string {
	pl := strings.ToLower(strings.TrimSpace(platform))
	if pl == "" {
		return "unknown"
	}
	var out []rune
	lastDash := false
	for _, r := range pl {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
			lastDash = false
			continue
		}
		if !lastDash {
			out = append(out, '-')
			lastDash = true
		}
	}
	trimmed := strings.Trim(string(out), "-")
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

// SendVerifyCodeResult 发送验证码返回结果
type SendVerifyCodeResult struct {
	Countdown int `json:"countdown"` // 倒计时秒数
}

// SendVerifyCode 发送邮箱验证码（同步方式）
func (s *AuthService) SendVerifyCode(ctx context.Context, email string) error {
	// 检查是否开放注册（默认关闭）
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		return ErrRegDisabled
	}

	if isReservedEmail(email) {
		return ErrEmailReserved
	}
	if err := s.validateRegistrationEmailPolicy(ctx, email); err != nil {
		return err
	}

	// 检查邮箱是否已存在
	existsEmail, err := s.userRepo.ExistsByEmail(ctx, email)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return ErrServiceUnavailable
	}
	if existsEmail {
		return ErrEmailExists
	}

	// 发送验证码
	if s.emailService == nil {
		return errors.New("email service not configured")
	}

	// 获取网站名称
	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	return s.emailService.SendVerifyCode(ctx, email, siteName)
}

// SendVerifyCodeAsync 异步发送邮箱验证码并返回倒计时
func (s *AuthService) SendVerifyCodeAsync(ctx context.Context, email string) (*SendVerifyCodeResult, error) {
	logger.LegacyPrintf("service.auth", "[Auth] SendVerifyCodeAsync called for email: %s", email)

	// 检查是否开放注册（默认关闭）
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Registration is disabled")
		return nil, ErrRegDisabled
	}

	if isReservedEmail(email) {
		return nil, ErrEmailReserved
	}
	if err := s.validateRegistrationEmailPolicy(ctx, email); err != nil {
		return nil, err
	}

	// 检查邮箱是否已存在
	existsEmail, err := s.userRepo.ExistsByEmail(ctx, email)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return nil, ErrServiceUnavailable
	}
	if existsEmail {
		logger.LegacyPrintf("service.auth", "[Auth] Email already exists: %s", email)
		return nil, ErrEmailExists
	}

	// 检查邮件队列服务是否配置
	if s.emailQueueService == nil {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Email queue service not configured")
		return nil, errors.New("email queue service not configured")
	}

	// 获取网站名称
	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	// 异步发送
	logger.LegacyPrintf("service.auth", "[Auth] Enqueueing verify code for: %s", email)
	if err := s.emailQueueService.EnqueueVerifyCode(email, siteName); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to enqueue: %v", err)
		return nil, fmt.Errorf("enqueue verify code: %w", err)
	}

	logger.LegacyPrintf("service.auth", "[Auth] Verify code enqueued successfully for: %s", email)
	return &SendVerifyCodeResult{
		Countdown: 60, // 60秒倒计时
	}, nil
}

// VerifyTurnstileForRegister 在注册场景下验证 Turnstile。
// 当邮箱验证开启且已提交验证码时，说明验证码发送阶段已完成 Turnstile 校验，
// 此处跳过二次校验，避免一次性 token 在注册提交时重复使用导致误报失败。
func (s *AuthService) VerifyTurnstileForRegister(ctx context.Context, token, remoteIP, verifyCode string) error {
	if s.IsEmailVerifyEnabled(ctx) && strings.TrimSpace(verifyCode) != "" {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Email verify flow detected, skip duplicate Turnstile check on register")
		return nil
	}
	return s.VerifyTurnstile(ctx, token, remoteIP)
}

// VerifyTurnstile 验证Turnstile token
func (s *AuthService) VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	required := s.cfg != nil && s.cfg.Server.Mode == "release" && s.cfg.Turnstile.Required

	if required {
		if s.settingService == nil {
			logger.LegacyPrintf("service.auth", "%s", "[Auth] Turnstile required but settings service is not configured")
			return ErrTurnstileNotConfigured
		}
		enabled := s.settingService.IsTurnstileEnabled(ctx)
		secretConfigured := s.settingService.GetTurnstileSecretKey(ctx) != ""
		if !enabled || !secretConfigured {
			logger.LegacyPrintf("service.auth", "[Auth] Turnstile required but not configured (enabled=%v, secret_configured=%v)", enabled, secretConfigured)
			return ErrTurnstileNotConfigured
		}
	}

	if s.turnstileService == nil {
		if required {
			logger.LegacyPrintf("service.auth", "%s", "[Auth] Turnstile required but service not configured")
			return ErrTurnstileNotConfigured
		}
		return nil // 服务未配置则跳过验证
	}

	if !required && s.settingService != nil && s.settingService.IsTurnstileEnabled(ctx) && s.settingService.GetTurnstileSecretKey(ctx) == "" {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Turnstile enabled but secret key not configured")
	}

	return s.turnstileService.VerifyToken(ctx, token, remoteIP)
}

// IsTurnstileEnabled 检查是否启用Turnstile验证
func (s *AuthService) IsTurnstileEnabled(ctx context.Context) bool {
	if s.turnstileService == nil {
		return false
	}
	return s.turnstileService.IsEnabled(ctx)
}

// IsRegistrationEnabled 检查是否开放注册
func (s *AuthService) IsRegistrationEnabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false // 安全默认：settingService 未配置时关闭注册
	}
	return s.settingService.IsRegistrationEnabled(ctx)
}

// IsEmailVerifyEnabled 检查是否开启邮件验证
func (s *AuthService) IsEmailVerifyEnabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false
	}
	return s.settingService.IsEmailVerifyEnabled(ctx)
}

// Login 用户登录，返回JWT token
func (s *AuthService) assignSubscriptions(ctx context.Context, userID int64, items []DefaultSubscriptionSetting, notes string) {
	if s.settingService == nil || s.defaultSubAssigner == nil || userID <= 0 {
		return
	}
	for _, item := range items {
		if _, _, err := s.defaultSubAssigner.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:       userID,
			GroupID:      item.GroupID,
			ValidityDays: item.ValidityDays,
			Notes:        notes,
		}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to assign default subscription: user_id=%d group_id=%d err=%v", userID, item.GroupID, err)
		}
	}
}

func (s *AuthService) resolveSignupGrantPlan(ctx context.Context, signupSource string) signupGrantPlan {
	plan := signupGrantPlan{}
	if s != nil && s.cfg != nil {
		plan.Balance = s.cfg.Default.UserBalance
		plan.Concurrency = s.cfg.Default.UserConcurrency
	}
	if s == nil || s.settingService == nil {
		return plan
	}

	plan.Balance = s.settingService.GetDefaultBalance(ctx)
	plan.Concurrency = s.settingService.GetDefaultConcurrency(ctx)
	plan.Subscriptions = s.settingService.GetDefaultSubscriptions(ctx)

	resolved, enabled, err := s.settingService.ResolveAuthSourceGrantSettings(ctx, signupSource, false)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to load auth source signup defaults for %s: %v", signupSource, err)
		return plan
	}
	if !enabled {
		return plan
	}

	plan.Balance = resolved.Balance
	plan.Concurrency = resolved.Concurrency
	plan.Subscriptions = resolved.Subscriptions
	return plan
}

func (s *AuthService) touchUserLogin(ctx context.Context, userID int64) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return
	}
	now := time.Now().UTC()
	if err := s.entClient.User.UpdateOneID(userID).
		SetLastLoginAt(now).
		SetLastActiveAt(now).
		Exec(ctx); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to touch login timestamps: user_id=%d err=%v", userID, err)
	}
}

func (s *AuthService) backfillEmailIdentityOnSuccessfulLogin(ctx context.Context, user *User) {
	if s == nil || user == nil || user.ID <= 0 {
		return
	}
	identity, created := s.ensureEmailAuthIdentity(ctx, user, "auth_service_login_backfill")
	if s.shouldApplyEmailFirstBindDefaults(ctx, user.ID, identity, created) {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, user.ID, "email"); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to apply email first bind defaults: user_id=%d err=%v", user.ID, err)
		}
	}
}

func (s *AuthService) shouldApplyEmailFirstBindDefaults(
	ctx context.Context,
	userID int64,
	identity *dbent.AuthIdentity,
	created bool,
) bool {
	source := emailAuthIdentitySource(identity.Metadata)
	if source == "auth_service_login_backfill" {
		return false
	}
	if created {
		return true
	}
	if s == nil || s.entClient == nil || userID <= 0 || identity == nil || identity.UserID != userID {
		return false
	}
	if source != "auth_service_dual_write" {
		return false
	}

	hasGrant, err := s.hasProviderGrantRecord(ctx, userID, "email", "first_bind")
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to inspect email first bind grant state: user_id=%d err=%v", userID, err)
		return false
	}
	return !hasGrant
}

func emailAuthIdentitySource(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["source"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func (s *AuthService) hasProviderGrantRecord(
	ctx context.Context,
	userID int64,
	providerType string,
	grantReason string,
) (bool, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return false, nil
	}

	providerType = strings.TrimSpace(providerType)
	grantReason = strings.TrimSpace(grantReason)
	if providerType == "" || grantReason == "" {
		return false, nil
	}

	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}

	query := `SELECT 1 FROM user_provider_default_grants WHERE user_id = ? AND provider_type = ? AND grant_reason = ? LIMIT 1`
	args := []any{userID, providerType, grantReason}
	if drv := client.Driver(); drv != nil && drv.Dialect() == dialect.Postgres {
		query = `SELECT 1 FROM user_provider_default_grants WHERE user_id = $1 AND provider_type = $2 AND grant_reason = $3 LIMIT 1`
	}

	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

func (s *AuthService) ensureEmailAuthIdentity(ctx context.Context, user *User, source string) (*dbent.AuthIdentity, bool) {
	if s == nil || s.entClient == nil || user == nil || user.ID <= 0 {
		return nil, false
	}

	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" || isReservedEmail(email) {
		return nil, false
	}
	if strings.TrimSpace(source) == "" {
		source = "auth_service_dual_write"
	}

	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}

	buildQuery := func() *dbent.AuthIdentityQuery {
		return client.AuthIdentity.Query().Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(email),
		)
	}

	existed, err := buildQuery().Exist(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to inspect email auth identity: user_id=%d email=%s err=%v", user.ID, email, err)
		return nil, false
	}

	if !existed {
		if err := client.AuthIdentity.Create().
			SetUserID(user.ID).
			SetProviderType("email").
			SetProviderKey("email").
			SetProviderSubject(email).
			SetVerifiedAt(time.Now().UTC()).
			SetMetadata(map[string]any{
				"source": strings.TrimSpace(source),
			}).
			OnConflictColumns(
				authidentity.FieldProviderType,
				authidentity.FieldProviderKey,
				authidentity.FieldProviderSubject,
			).
			DoNothing().
			Exec(ctx); err != nil {
			if isSQLNoRowsError(err) {
				return nil, false
			}
		}
		if err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to ensure email auth identity: user_id=%d email=%s err=%v", user.ID, email, err)
			return nil, false
		}
	}

	identity, err := buildQuery().Only(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to reload email auth identity: user_id=%d email=%s err=%v", user.ID, email, err)
		return nil, false
	}
	if identity.UserID != user.ID {
		logger.LegacyPrintf("service.auth", "[Auth] Email auth identity ownership mismatch: user_id=%d email=%s owner_id=%d", user.ID, email, identity.UserID)
		return nil, false
	}

	return identity, !existed
}

func (s *AuthService) Login(ctx context.Context, email, password string) (string, *User, error) {
	// 查找用户
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", nil, ErrInvalidCredentials
		}
		// 记录数据库错误但不暴露给用户
		logger.LegacyPrintf("service.auth", "[Auth] Database error during login: %v", err)
		return "", nil, ErrServiceUnavailable
	}

	// 验证密码
	if !s.CheckPassword(password, user.PasswordHash) {
		return "", nil, ErrInvalidCredentials
	}

	// 检查用户状态
	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	// 生成JWT token
	token, err := s.GenerateToken(user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// LoginOrRegisterOAuth 用于第三方 OAuth/SSO 登录：
// - 如果邮箱已存在：直接登录（不需要本地密码）
// - 如果邮箱不存在：创建新用户并登录
//
// 注意：该函数用于 LinuxDo OAuth 登录场景（不同于上游账号的 OAuth，例如 Claude/OpenAI/Gemini）。
// 为了满足现有数据库约束（需要密码哈希），新用户会生成随机密码并进行哈希保存。
func (s *AuthService) LoginOrRegisterOAuth(ctx context.Context, email, username string) (string, *User, error) {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return "", nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	username = strings.TrimSpace(username)
	if len([]rune(username)) > 100 {
		username = string([]rune(username)[:100])
	}

	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// OAuth 首次登录视为注册（fail-close：settingService 未配置时不允许注册）
			if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
				return "", nil, ErrRegDisabled
			}

			randomPassword, err := randomHexString(32)
			if err != nil {
				logger.LegacyPrintf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return "", nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return "", nil, fmt.Errorf("hash password: %w", err)
			}

			// 新用户默认值。
			defaultBalance := s.cfg.Default.UserBalance
			defaultConcurrency := s.cfg.Default.UserConcurrency
			if s.settingService != nil {
				defaultBalance = s.settingService.GetDefaultBalance(ctx)
				defaultConcurrency = s.settingService.GetDefaultConcurrency(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      defaultBalance,
				Concurrency:  defaultConcurrency,
				Status:       StatusActive,
				SignupSource: "email",
			}

			if err := s.userRepo.Create(ctx, newUser); err != nil {
				if errors.Is(err, ErrEmailExists) {
					// 并发场景：GetByEmail 与 Create 之间用户被创建。
					user, err = s.userRepo.GetByEmail(ctx, email)
					if err != nil {
						logger.LegacyPrintf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
						return "", nil, ErrServiceUnavailable
					}
				} else {
					logger.LegacyPrintf("service.auth", "[Auth] Database error creating oauth user: %v", err)
					return "", nil, ErrServiceUnavailable
				}
			} else {
				user = newUser
				s.assignDefaultSubscriptions(ctx, user.ID)
			}
		} else {
			logger.LegacyPrintf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return "", nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	// 尽力补全：当用户名为空时，使用第三方返回的用户名回填。
	if user.Username == "" && username != "" {
		user.Username = username
		if err := s.userRepo.Update(ctx, user); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}

	token, err := s.GenerateToken(user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	return token, user, nil
}

// LoginOrRegisterOAuthWithTokenPair 用于第三方 OAuth/SSO 登录，返回完整的 TokenPair。
// 与 LoginOrRegisterOAuth 功能相同，但返回 TokenPair 而非单个 token。
// invitationCode 仅在邀请码注册模式下新用户注册时使用；已有账号登录时忽略。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode string) (*TokenPair, *User, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, nil, errors.New("refresh token cache not configured")
	}

	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	username = strings.TrimSpace(username)
	if len([]rune(username)) > 100 {
		username = string([]rune(username)[:100])
	}

	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// OAuth 首次登录视为注册
			if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
				return nil, nil, ErrRegDisabled
			}

			// 检查是否需要邀请码
			var invitationRedeemCode *RedeemCode
			if s.settingService != nil && s.settingService.IsInvitationCodeEnabled(ctx) {
				if invitationCode == "" {
					return nil, nil, ErrOAuthInvitationRequired
				}
				redeemCode, err := s.redeemRepo.GetByCode(ctx, invitationCode)
				if err != nil {
					return nil, nil, ErrInvitationCodeInvalid
				}
				if redeemCode.Type != RedeemTypeInvitation || redeemCode.Status != StatusUnused {
					return nil, nil, ErrInvitationCodeInvalid
				}
				invitationRedeemCode = redeemCode
			}

			randomPassword, err := randomHexString(32)
			if err != nil {
				logger.LegacyPrintf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return nil, nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return nil, nil, fmt.Errorf("hash password: %w", err)
			}

			defaultBalance := s.cfg.Default.UserBalance
			defaultConcurrency := s.cfg.Default.UserConcurrency
			if s.settingService != nil {
				defaultBalance = s.settingService.GetDefaultBalance(ctx)
				defaultConcurrency = s.settingService.GetDefaultConcurrency(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      defaultBalance,
				Concurrency:  defaultConcurrency,
				Status:       StatusActive,
				SignupSource: "email",
			}

			if s.entClient != nil && invitationRedeemCode != nil {
				tx, err := s.entClient.Tx(ctx)
				if err != nil {
					logger.LegacyPrintf("service.auth", "[Auth] Failed to begin transaction for oauth registration: %v", err)
					return nil, nil, ErrServiceUnavailable
				}
				defer func() { _ = tx.Rollback() }()
				txCtx := dbent.NewTxContext(ctx, tx)

				if err := s.userRepo.Create(txCtx, newUser); err != nil {
					if errors.Is(err, ErrEmailExists) {
						user, err = s.userRepo.GetByEmail(ctx, email)
						if err != nil {
							logger.LegacyPrintf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
							return nil, nil, ErrServiceUnavailable
						}
					} else {
						logger.LegacyPrintf("service.auth", "[Auth] Database error creating oauth user: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
				} else {
					if err := s.redeemRepo.Use(txCtx, invitationRedeemCode.ID, newUser.ID); err != nil {
						return nil, nil, ErrInvitationCodeInvalid
					}
					if err := tx.Commit(); err != nil {
						logger.LegacyPrintf("service.auth", "[Auth] Failed to commit oauth registration transaction: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
					user = newUser
					s.assignDefaultSubscriptions(ctx, user.ID)
				}
			} else {
				if err := s.userRepo.Create(ctx, newUser); err != nil {
					if errors.Is(err, ErrEmailExists) {
						user, err = s.userRepo.GetByEmail(ctx, email)
						if err != nil {
							logger.LegacyPrintf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
							return nil, nil, ErrServiceUnavailable
						}
					} else {
						logger.LegacyPrintf("service.auth", "[Auth] Database error creating oauth user: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
				} else {
					user = newUser
					s.assignDefaultSubscriptions(ctx, user.ID)
					if invitationRedeemCode != nil {
						if err := s.redeemRepo.Use(ctx, invitationRedeemCode.ID, user.ID); err != nil {
							return nil, nil, ErrInvitationCodeInvalid
						}
					}
				}
			}
		} else {
			logger.LegacyPrintf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return nil, nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}

	if user.Username == "" && username != "" {
		user.Username = username
		if err := s.userRepo.Update(ctx, user); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

// pendingOAuthTokenTTL is the validity period for pending OAuth tokens.
const pendingOAuthTokenTTL = 10 * time.Minute

// pendingOAuthPurpose is the purpose claim value for pending OAuth registration tokens.
const pendingOAuthPurpose = "pending_oauth_registration"

type pendingOAuthClaims struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Purpose  string `json:"purpose"`
	jwt.RegisteredClaims
}

// CreatePendingOAuthToken generates a short-lived JWT that carries the OAuth identity
// while waiting for the user to supply an invitation code.
func (s *AuthService) CreatePendingOAuthToken(email, username string) (string, error) {
	now := time.Now()
	claims := &pendingOAuthClaims{
		Email:    email,
		Username: username,
		Purpose:  pendingOAuthPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(pendingOAuthTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWT.Secret))
}

// VerifyPendingOAuthToken validates a pending OAuth token and returns the embedded identity.
// Returns ErrInvalidToken when the token is invalid or expired.
func (s *AuthService) VerifyPendingOAuthToken(tokenStr string) (email, username string, err error) {
	if len(tokenStr) > maxTokenLength {
		return "", "", ErrInvalidToken
	}
	parser := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}))
	token, parseErr := parser.ParseWithClaims(tokenStr, &pendingOAuthClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWT.Secret), nil
	})
	if parseErr != nil {
		return "", "", ErrInvalidToken
	}
	claims, ok := token.Claims.(*pendingOAuthClaims)
	if !ok || !token.Valid {
		return "", "", ErrInvalidToken
	}
	if claims.Purpose != pendingOAuthPurpose {
		return "", "", ErrInvalidToken
	}
	return claims.Email, claims.Username, nil
}

func (s *AuthService) assignDefaultSubscriptions(ctx context.Context, userID int64) {
	if s.settingService == nil || s.defaultSubAssigner == nil || userID <= 0 {
		return
	}
	items := s.settingService.GetDefaultSubscriptions(ctx)
	for _, item := range items {
		if _, _, err := s.defaultSubAssigner.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:       userID,
			GroupID:      item.GroupID,
			ValidityDays: item.ValidityDays,
			Notes:        "auto assigned by default user subscriptions setting",
		}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to assign default subscription: user_id=%d group_id=%d err=%v", userID, item.GroupID, err)
		}
	}
}

func (s *AuthService) validateRegistrationEmailPolicy(ctx context.Context, email string) error {
	if s.settingService == nil {
		return nil
	}
	whitelist := s.settingService.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixAllowed(email, whitelist) {
		return buildEmailSuffixNotAllowedError(whitelist)
	}
	return nil
}

func buildEmailSuffixNotAllowedError(whitelist []string) error {
	if len(whitelist) == 0 {
		return ErrEmailSuffixNotAllowed
	}

	allowed := strings.Join(whitelist, ", ")
	return infraerrors.BadRequest(
		"EMAIL_SUFFIX_NOT_ALLOWED",
		fmt.Sprintf("email suffix is not allowed, allowed suffixes: %s", allowed),
	).WithMetadata(map[string]string{
		"allowed_suffixes":     strings.Join(whitelist, ","),
		"allowed_suffix_count": strconv.Itoa(len(whitelist)),
	})
}

// ValidateToken 验证JWT token并返回用户声明
func (s *AuthService) ValidateToken(tokenString string) (*JWTClaims, error) {
	// 先做长度校验，尽早拒绝异常超长 token，降低 DoS 风险。
	if len(tokenString) > maxTokenLength {
		return nil, ErrTokenTooLarge
	}

	// 使用解析器并限制可接受的签名算法，防止算法混淆。
	parser := jwt.NewParser(jwt.WithValidMethods([]string{
		jwt.SigningMethodHS256.Name,
		jwt.SigningMethodHS384.Name,
		jwt.SigningMethodHS512.Name,
	}))

	// 保留默认 claims 校验（exp/nbf），避免放行过期或未生效的 token。
	token, err := parser.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (any, error) {
		// 验证签名方法
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWT.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			// token 过期但仍返回 claims（用于 RefreshToken 等场景）
			// jwt-go 在解析时即使遇到过期错误，token.Claims 仍会被填充
			if claims, ok := token.Claims.(*JWTClaims); ok {
				return claims, ErrTokenExpired
			}
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

func randomHexString(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 16
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isReservedEmail(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return strings.HasSuffix(normalized, LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, OIDCConnectSyntheticEmailDomain)
}

// GenerateToken 生成JWT access token
// 使用新的access_token_expire_minutes配置项（如果配置了），否则回退到expire_hour
func (s *AuthService) GenerateToken(user *User) (string, error) {
	now := time.Now()
	var expiresAt time.Time
	if s.cfg.JWT.AccessTokenExpireMinutes > 0 {
		expiresAt = now.Add(time.Duration(s.cfg.JWT.AccessTokenExpireMinutes) * time.Minute)
	} else {
		// 向后兼容：使用旧的expire_hour配置
		expiresAt = now.Add(time.Duration(s.cfg.JWT.ExpireHour) * time.Hour)
	}

	claims := &JWTClaims{
		UserID:       user.ID,
		Email:        user.Email,
		Role:         user.Role,
		TokenVersion: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.cfg.JWT.Secret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return tokenString, nil
}

// GetAccessTokenExpiresIn 返回Access Token的有效期（秒）
// 用于前端设置刷新定时器
func (s *AuthService) GetAccessTokenExpiresIn() int {
	if s.cfg.JWT.AccessTokenExpireMinutes > 0 {
		return s.cfg.JWT.AccessTokenExpireMinutes * 60
	}
	return s.cfg.JWT.ExpireHour * 3600
}

// HashPassword 使用bcrypt加密密码
func (s *AuthService) HashPassword(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}

// CheckPassword 验证密码是否匹配
func (s *AuthService) CheckPassword(password, hashedPassword string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// RefreshToken 刷新token
func (s *AuthService) RefreshToken(ctx context.Context, oldTokenString string) (string, error) {
	// 验证旧token（即使过期也允许，用于刷新）
	claims, err := s.ValidateToken(oldTokenString)
	if err != nil && !errors.Is(err, ErrTokenExpired) {
		return "", err
	}

	// 获取最新的用户信息
	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", ErrInvalidToken
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error refreshing token: %v", err)
		return "", ErrServiceUnavailable
	}

	// 检查用户状态
	if !user.IsActive() {
		return "", ErrUserNotActive
	}

	// Security: Check TokenVersion to prevent refreshing revoked tokens
	// This ensures tokens issued before a password change cannot be refreshed
	if claims.TokenVersion != user.TokenVersion {
		return "", ErrTokenRevoked
	}

	// 生成新token
	return s.GenerateToken(user)
}

// IsPasswordResetEnabled 检查是否启用密码重置功能
// 要求：必须同时开启邮件验证且 SMTP 配置正确
func (s *AuthService) IsPasswordResetEnabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false
	}
	// Must have email verification enabled and SMTP configured
	if !s.settingService.IsEmailVerifyEnabled(ctx) {
		return false
	}
	return s.settingService.IsPasswordResetEnabled(ctx)
}

// preparePasswordReset validates the password reset request and returns necessary data
// Returns (siteName, resetURL, shouldProceed)
// shouldProceed is false when we should silently return success (to prevent enumeration)
func (s *AuthService) preparePasswordReset(ctx context.Context, email, frontendBaseURL string) (string, string, bool) {
	// Check if user exists (but don't reveal this to the caller)
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Security: Log but don't reveal that user doesn't exist
			logger.LegacyPrintf("service.auth", "[Auth] Password reset requested for non-existent email: %s", email)
			return "", "", false
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email for password reset: %v", err)
		return "", "", false
	}

	// Check if user is active
	if !user.IsActive() {
		logger.LegacyPrintf("service.auth", "[Auth] Password reset requested for inactive user: %s", email)
		return "", "", false
	}

	// Get site name
	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	// Build reset URL base
	resetURL := fmt.Sprintf("%s/reset-password", strings.TrimSuffix(frontendBaseURL, "/"))

	return siteName, resetURL, true
}

// RequestPasswordReset 请求密码重置（同步发送）
// Security: Returns the same response regardless of whether the email exists (prevent user enumeration)
func (s *AuthService) RequestPasswordReset(ctx context.Context, email, frontendBaseURL string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.emailService == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.preparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	if err := s.emailService.SendPasswordResetEmail(ctx, email, siteName, resetURL); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to send password reset email to %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	logger.LegacyPrintf("service.auth", "[Auth] Password reset email sent to: %s", email)
	return nil
}

// RequestPasswordResetAsync 异步请求密码重置（队列发送）
// Security: Returns the same response regardless of whether the email exists (prevent user enumeration)
func (s *AuthService) RequestPasswordResetAsync(ctx context.Context, email, frontendBaseURL string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.emailQueueService == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.preparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	if err := s.emailQueueService.EnqueuePasswordReset(email, siteName, resetURL); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to enqueue password reset email for %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	logger.LegacyPrintf("service.auth", "[Auth] Password reset email enqueued for: %s", email)
	return nil
}

// ResetPassword 重置密码
// Security: Increments TokenVersion to invalidate all existing JWT tokens
func (s *AuthService) ResetPassword(ctx context.Context, email, token, newPassword string) error {
	// Check if password reset is enabled
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}

	if s.emailService == nil {
		return ErrServiceUnavailable
	}

	// Verify and consume the reset token (one-time use)
	if err := s.emailService.ConsumePasswordResetToken(ctx, email, token); err != nil {
		return err
	}

	// Get user
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrInvalidResetToken // Token was valid but user was deleted
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error getting user for password reset: %v", err)
		return ErrServiceUnavailable
	}

	// Check if user is active
	if !user.IsActive() {
		return ErrUserNotActive
	}

	// Hash new password
	hashedPassword, err := s.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Update password and increment TokenVersion
	user.PasswordHash = hashedPassword
	user.TokenVersion++ // Invalidate all existing tokens

	if err := s.userRepo.Update(ctx, user); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error updating password for user %d: %v", user.ID, err)
		return ErrServiceUnavailable
	}

	// Also revoke all refresh tokens for this user
	if err := s.RevokeAllUserSessions(ctx, user.ID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke refresh tokens for user %d: %v", user.ID, err)
		// Don't return error - password was already changed successfully
	}

	logger.LegacyPrintf("service.auth", "[Auth] Password reset successful for user: %s", email)
	return nil
}

// ==================== Refresh Token Methods ====================

// TokenPair 包含Access Token和Refresh Token
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // Access Token有效期（秒）
}

// TokenPairWithUser extends TokenPair with user role for backend mode checks
type TokenPairWithUser struct {
	TokenPair
	UserRole string
}

// GenerateTokenPair 生成Access Token和Refresh Token对
// familyID: 可选的Token家族ID，用于Token轮转时保持家族关系
func (s *AuthService) GenerateTokenPair(ctx context.Context, user *User, familyID string) (*TokenPair, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, errors.New("refresh token cache not configured")
	}

	// 生成Access Token
	accessToken, err := s.GenerateToken(user)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	// 生成Refresh Token
	refreshToken, err := s.generateRefreshToken(ctx, user, familyID)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    s.GetAccessTokenExpiresIn(),
	}, nil
}

// generateRefreshToken 生成并存储Refresh Token
func (s *AuthService) generateRefreshToken(ctx context.Context, user *User, familyID string) (string, error) {
	// 生成随机Token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawToken := refreshTokenPrefix + hex.EncodeToString(tokenBytes)

	// 计算Token哈希（存储哈希而非原始Token）
	tokenHash := hashToken(rawToken)

	// 如果没有提供familyID，生成新的
	if familyID == "" {
		familyBytes := make([]byte, 16)
		if _, err := rand.Read(familyBytes); err != nil {
			return "", fmt.Errorf("generate family id: %w", err)
		}
		familyID = hex.EncodeToString(familyBytes)
	}

	now := time.Now()
	ttl := time.Duration(s.cfg.JWT.RefreshTokenExpireDays) * 24 * time.Hour

	data := &RefreshTokenData{
		UserID:       user.ID,
		TokenVersion: user.TokenVersion,
		FamilyID:     familyID,
		CreatedAt:    now,
		ExpiresAt:    now.Add(ttl),
	}

	// 存储Token数据
	if err := s.refreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl); err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}

	// 添加到用户Token集合
	if err := s.refreshTokenCache.AddToUserTokenSet(ctx, user.ID, tokenHash, ttl); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to add token to user set: %v", err)
		// 不影响主流程
	}

	// 添加到家族Token集合
	if err := s.refreshTokenCache.AddToFamilyTokenSet(ctx, familyID, tokenHash, ttl); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to add token to family set: %v", err)
		// 不影响主流程
	}

	return rawToken, nil
}

// RefreshTokenPair 使用Refresh Token刷新Token对
// 实现Token轮转：每次刷新都会生成新的Refresh Token，旧Token立即失效
func (s *AuthService) RefreshTokenPair(ctx context.Context, refreshToken string) (*TokenPairWithUser, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, ErrRefreshTokenInvalid
	}

	// 验证Token格式
	if !strings.HasPrefix(refreshToken, refreshTokenPrefix) {
		return nil, ErrRefreshTokenInvalid
	}

	tokenHash := hashToken(refreshToken)

	// 获取Token数据
	data, err := s.refreshTokenCache.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			// Token不存在，可能是已被使用（Token轮转）或已过期
			logger.LegacyPrintf("service.auth", "[Auth] Refresh token not found, possible reuse attack")
			return nil, ErrRefreshTokenInvalid
		}
		logger.LegacyPrintf("service.auth", "[Auth] Error getting refresh token: %v", err)
		return nil, ErrServiceUnavailable
	}

	// 检查Token是否过期
	if time.Now().After(data.ExpiresAt) {
		// 删除过期Token
		_ = s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
		return nil, ErrRefreshTokenExpired
	}

	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, data.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 用户已删除，撤销整个Token家族
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			return nil, ErrRefreshTokenInvalid
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error getting user for token refresh: %v", err)
		return nil, ErrServiceUnavailable
	}

	// 检查用户状态
	if !user.IsActive() {
		// 用户被禁用，撤销整个Token家族
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		return nil, ErrUserNotActive
	}

	// 检查TokenVersion（密码更改后所有Token失效）
	if data.TokenVersion != user.TokenVersion {
		// TokenVersion不匹配，撤销整个Token家族
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		return nil, ErrTokenRevoked
	}

	// Token轮转：立即使旧Token失效
	if err := s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to delete old refresh token: %v", err)
		// 继续处理，不影响主流程
	}

	// 生成新的Token对，保持同一个家族ID
	pair, err := s.GenerateTokenPair(ctx, user, data.FamilyID)
	if err != nil {
		return nil, err
	}
	return &TokenPairWithUser{
		TokenPair: *pair,
		UserRole:  user.Role,
	}, nil
}

// RevokeRefreshToken 撤销单个Refresh Token
func (s *AuthService) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	if s.refreshTokenCache == nil {
		return nil // No-op if cache not configured
	}
	if !strings.HasPrefix(refreshToken, refreshTokenPrefix) {
		return ErrRefreshTokenInvalid
	}

	tokenHash := hashToken(refreshToken)
	return s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
}

// RevokeAllUserSessions 撤销用户的所有会话（所有Refresh Token）
// 用于密码更改或用户主动登出所有设备
func (s *AuthService) RevokeAllUserSessions(ctx context.Context, userID int64) error {
	if s.refreshTokenCache == nil {
		return nil // No-op if cache not configured
	}
	return s.refreshTokenCache.DeleteUserRefreshTokens(ctx, userID)
}

// RevokeAllUserTokens invalidates both stateless access tokens and refresh sessions.
// Access/refresh token verification both depend on TokenVersion, so bumping it provides
// immediate revocation even if refresh-token cache cleanup later fails.
func (s *AuthService) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	user.TokenVersion++
	if err := s.userRepo.Update(ctx, user); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if err := s.RevokeAllUserSessions(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke refresh sessions after token invalidation for user %d: %v", userID, err)
	}
	return nil
}

// hashToken 计算Token的SHA256哈希
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
