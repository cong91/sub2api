package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentproviderinstance"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
)

// --- Order Status Constants ---

const (
	OrderStatusPending           = payment.OrderStatusPending
	OrderStatusPaid              = payment.OrderStatusPaid
	OrderStatusRecharging        = payment.OrderStatusRecharging
	OrderStatusCompleted         = payment.OrderStatusCompleted
	OrderStatusExpired           = payment.OrderStatusExpired
	OrderStatusCancelled         = payment.OrderStatusCancelled
	OrderStatusFailed            = payment.OrderStatusFailed
	OrderStatusRefundRequested   = payment.OrderStatusRefundRequested
	OrderStatusRefunding         = payment.OrderStatusRefunding
	OrderStatusPartiallyRefunded = payment.OrderStatusPartiallyRefunded
	OrderStatusRefunded          = payment.OrderStatusRefunded
	OrderStatusRefundFailed      = payment.OrderStatusRefundFailed
)

const (
	// defaultMaxPendingOrders and defaultOrderTimeoutMin are defined in
	// payment_config_service.go alongside other payment configuration defaults.
	paymentGraceMinutes = 5

	defaultPageSize    = 20
	maxPageSize        = 100
	topUsersLimit      = 10
	amountToleranceCNY = 0.01

	orderIDPrefix              = "sub2_"
	paymentResumeSigningKeyEnv = "PAYMENT_RESUME_SIGNING_KEY"
)

// --- Types ---

// generateOutTradeNo creates a unique external order ID for payment providers.
// Format: sub2_20250409aB3kX9mQ (prefix + date + 8-char random)
func generateOutTradeNo() string {
	date := time.Now().Format("20060102")
	rnd := generateRandomString(8)
	return orderIDPrefix + date + rnd
}

func generateRandomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.IntN(len(charset))]
	}
	return string(b)
}

type CreateOrderRequest struct {
	UserID          int64
	Amount          float64
	PaymentCurrency string
	PaymentType     string
	ClientIP        string
	IsMobile        bool
	IsWeChatBrowser bool
	OpenID          string
	SrcHost         string
	SrcURL          string
	OrderType       string
	PlanID          int64
}

type CreateOrderResponse struct {
	OrderID         int64                           `json:"order_id"`
	Amount          float64                         `json:"amount"`
	PaymentAmount   float64                         `json:"payment_amount"`
	PaymentCurrency string                          `json:"payment_currency"`
	LedgerAmount    float64                         `json:"ledger_amount"`
	LedgerCurrency  string                          `json:"ledger_currency"`
	FXRate          float64                         `json:"fx_rate"`
	FXSource        string                          `json:"fx_source"`
	FXTimestamp     time.Time                       `json:"fx_timestamp"`
	PayAmount       float64                         `json:"pay_amount"`
	FeeRate         float64                         `json:"fee_rate"`
	Status          string                          `json:"status"`
	ResultType      payment.CreatePaymentResultType `json:"result_type,omitempty"`
	PaymentType     string                          `json:"payment_type"`
	OutTradeNo      string                          `json:"out_trade_no,omitempty"`
	PayURL          string                          `json:"pay_url,omitempty"`
	QRCode          string                          `json:"qr_code,omitempty"`
	ClientSecret    string                          `json:"client_secret,omitempty"`
	CheckoutID      string                          `json:"checkout_id,omitempty"`
	OAuth           *payment.WechatOAuthInfo        `json:"oauth,omitempty"`
	JSAPI           *payment.WechatJSAPIPayload     `json:"jsapi,omitempty"`
	JSAPIPayload    *payment.WechatJSAPIPayload     `json:"jsapi_payload,omitempty"`
	ExpiresAt       time.Time                       `json:"expires_at"`
	PaymentMode     string                          `json:"payment_mode,omitempty"`
	ResumeToken     string                          `json:"resume_token,omitempty"`
}

type OrderListParams struct {
	Page        int
	PageSize    int
	Status      string
	OrderType   string
	PaymentType string
	Keyword     string
}

type RefundPlan struct {
	OrderID         int64
	Order           *dbent.PaymentOrder
	RefundAmount    float64
	GatewayAmount   float64
	Reason          string
	Force           bool
	DeductBalance   bool
	DeductionType   string
	BalanceToDeduct float64
	SubDaysToDeduct int
	SubscriptionID  int64
}

type RefundResult struct {
	Success         bool    `json:"success"`
	Warning         string  `json:"warning,omitempty"`
	RequireForce    bool    `json:"require_force,omitempty"`
	BalanceDeducted float64 `json:"balance_deducted,omitempty"`
	SubDaysDeducted int     `json:"subscription_days_deducted,omitempty"`
}

type DashboardStats struct {
	TodayAmount   float64 `json:"today_amount"`
	TotalAmount   float64 `json:"total_amount"`
	TodayCount    int     `json:"today_count"`
	TotalCount    int     `json:"total_count"`
	AvgAmount     float64 `json:"avg_amount"`
	PendingOrders int     `json:"pending_orders"`

	DailySeries    []DailyStats        `json:"daily_series"`
	PaymentMethods []PaymentMethodStat `json:"payment_methods"`
	TopUsers       []TopUserStat       `json:"top_users"`
}

type DailyStats struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

type PaymentMethodStat struct {
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

type TopUserStat struct {
	UserID int64   `json:"user_id"`
	Email  string  `json:"email"`
	Amount float64 `json:"amount"`
}

// --- Service ---

type PaymentService struct {
	providerMu      sync.Mutex
	providersLoaded bool
	entClient       *dbent.Client
	registry        *payment.Registry
	loadBalancer    payment.LoadBalancer
	redeemService   *RedeemService
	subscriptionSvc *SubscriptionService
	configService   *PaymentConfigService
	userRepo        UserRepository
	groupRepo       GroupRepository
	resumeService   *PaymentResumeService
}

func NewPaymentService(entClient *dbent.Client, registry *payment.Registry, loadBalancer payment.LoadBalancer, redeemService *RedeemService, subscriptionSvc *SubscriptionService, configService *PaymentConfigService, userRepo UserRepository, groupRepo GroupRepository) *PaymentService {
	svc := &PaymentService{entClient: entClient, registry: registry, loadBalancer: newVisibleMethodLoadBalancer(loadBalancer, configService), redeemService: redeemService, subscriptionSvc: subscriptionSvc, configService: configService, userRepo: userRepo, groupRepo: groupRepo}
	svc.resumeService = psNewPaymentResumeService(configService)
	return svc
}

// --- Provider Registry ---

// EnsureProviders lazily initializes the provider registry on first call.
func (s *PaymentService) EnsureProviders(ctx context.Context) {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	if !s.providersLoaded {
		s.loadProviders(ctx)
		s.providersLoaded = true
	}
}

// RefreshProviders clears and re-registers all providers from the database.
func (s *PaymentService) RefreshProviders(ctx context.Context) {
	s.providerMu.Lock()
	defer s.providerMu.Unlock()
	s.registry.Clear()
	s.loadProviders(ctx)
	s.providersLoaded = true
}

func (s *PaymentService) GetWebhookProvider(ctx context.Context, providerKey, outTradeNo string) (payment.Provider, error) {
	if s == nil || s.registry == nil {
		return nil, fmt.Errorf("payment registry not ready")
	}
	s.EnsureProviders(ctx)
	provider, err := s.registry.GetProviderByKey(providerKey)
	if err != nil {
		return nil, err
	}
	return provider, nil
}

func (s *PaymentService) loadProviders(ctx context.Context) {
	instances, err := s.entClient.PaymentProviderInstance.Query().
		Where(paymentproviderinstance.Enabled(true)).
		All(ctx)
	if err != nil {
		slog.Error("[PaymentService] failed to query provider instances", "error", err)
		return
	}

	var loaded int
	for _, inst := range instances {
		cfg := map[string]string{}
		if strings.TrimSpace(inst.Config) != "" {
			decrypted, err := s.configService.decryptConfig(inst.Config)
			if err != nil {
				slog.Error("[PaymentService] failed to decrypt provider config", "provider", inst.ProviderKey, "instance", inst.ID, "error", err)
				continue
			}
			if decrypted != nil {
				cfg = decrypted
			}
		}
		instanceID := fmt.Sprintf("%d", inst.ID)
		p, err := provider.CreateProvider(inst.ProviderKey, instanceID, cfg)
		if err != nil {
			slog.Error("[PaymentService] failed to create provider", "provider", inst.ProviderKey, "instance", instanceID, "error", err)
			continue
		}
		s.registry.Register(p)
		loaded++
		slog.Info("[PaymentService] provider loaded", "provider", inst.ProviderKey, "instance", instanceID)
	}
	slog.Info("[PaymentService] providers initialized", "loaded", loaded)
}

func psIsRefundStatus(s string) bool {
	switch s {
	case OrderStatusRefundRequested, OrderStatusRefunding, OrderStatusPartiallyRefunded, OrderStatusRefunded, OrderStatusRefundFailed:
		return true
	}
	return false
}

func psErrMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func psNilIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func (s *PaymentService) usesOfficialWxpayVisibleMethod(ctx context.Context) bool {
	if s == nil || s.configService == nil || s.configService.entClient == nil {
		return false
	}

	instances, err := s.configService.entClient.PaymentProviderInstance.Query().
		Where(paymentproviderinstance.EnabledEQ(true)).
		All(ctx)
	if err != nil {
		return false
	}

	hasOfficial := false
	hasEasyPay := false
	for _, inst := range instances {
		switch inst.ProviderKey {
		case payment.TypeWxpay:
			if inst.SupportedTypes == "" || payment.InstanceSupportsType(inst.SupportedTypes, payment.TypeWxpay) || payment.InstanceSupportsType(inst.SupportedTypes, payment.TypeWxpayDirect) {
				hasOfficial = true
			}
		case payment.TypeEasyPay:
			for _, supportedType := range splitTypes(inst.SupportedTypes) {
				if NormalizeVisibleMethod(supportedType) == payment.TypeWxpay {
					hasEasyPay = true
					break
				}
			}
		}
	}

	if !hasOfficial {
		return false
	}
	if !hasEasyPay {
		return true
	}

	source := ""
	if s.configService.settingRepo != nil {
		if raw, err := s.configService.settingRepo.GetValue(ctx, SettingPaymentVisibleMethodWxpaySource); err == nil {
			source = raw
		}
	}
	return NormalizeVisibleMethodSource(payment.TypeWxpay, source) == VisibleMethodSourceOfficialWechat
}

func psSliceContains(sl []string, s string) bool {
	for _, v := range sl {
		if v == s {
			return true
		}
	}
	return false
}

func psComputeValidityDays(val int, unit string) int {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "day", "days", "d", "":
		return val
	case "week", "weeks", "w":
		return val * 7
	case "month", "months", "m":
		return val * 30
	case "year", "years", "y":
		return val * 365
	default:
		return val
	}
}

func psStartOfDayUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func applyPagination(pageSize, page int) (size, pg int) {
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if page <= 0 {
		page = 1
	}
	return pageSize, page
}

func (s *PaymentService) paymentResume() *PaymentResumeService {
	if s.resumeService != nil {
		return s.resumeService
	}
	s.resumeService = psNewPaymentResumeService(s.configService)
	return s.resumeService
}

func NewLegacyAwarePaymentResumeService(legacyKey []byte) *PaymentResumeService {
	return newLegacyAwarePaymentResumeService(legacyKey)
}

func psNewPaymentResumeService(configService *PaymentConfigService) *PaymentResumeService {
	return newLegacyAwarePaymentResumeService(psResumeLegacyVerificationKey(configService))
}

func newLegacyAwarePaymentResumeService(legacyKey []byte) *PaymentResumeService {
	signingKey, verifyFallbacks := resolvePaymentResumeSigningKeys(legacyKey)
	return NewPaymentResumeService(signingKey, verifyFallbacks...)
}

func psResumeLegacyVerificationKey(configService *PaymentConfigService) []byte {
	if configService == nil {
		return nil
	}
	return configService.encryptionKey
}

func resolvePaymentResumeSigningKeys(legacyKey []byte) ([]byte, [][]byte) {
	signingKey := parsePaymentResumeSigningKey(os.Getenv(paymentResumeSigningKeyEnv))
	if len(signingKey) == 0 {
		if len(legacyKey) == 0 {
			return nil, nil
		}
		return legacyKey, nil
	}
	if len(legacyKey) == 0 || bytes.Equal(legacyKey, signingKey) {
		return signingKey, nil
	}
	return signingKey, [][]byte{legacyKey}
}

func parsePaymentResumeSigningKey(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if len(raw) >= 64 && len(raw)%2 == 0 {
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) > 0 {
			return decoded
		}
	}
	return []byte(raw)
}

func psBuildResumeTokenLegacySecret() []byte {
	legacy := strings.TrimSpace(os.Getenv(paymentResumeSigningKeyEnv))
	if legacy == "" {
		return nil
	}
	if raw, err := hex.DecodeString(legacy); err == nil {
		switch len(raw) {
		case 32, 48, 64:
			cp := make([]byte, len(raw))
			copy(cp, raw)
			return cp
		}
	}
	buf := bytes.Repeat([]byte(legacy), 32/len(legacy)+1)
	out := make([]byte, 32)
	copy(out, buf[:32])
	return out
}
