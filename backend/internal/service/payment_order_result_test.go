package service

import (
	"context"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func TestBuildCreateOrderResponseDefaultsToOrderCreated(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	resp := buildCreateOrderResponse(
		&dbent.PaymentOrder{
			ID:         42,
			Amount:     12.34,
			FeeRate:    0.03,
			ExpiresAt:  expiresAt,
			OutTradeNo: "sub2_42",
		},
		CreateOrderRequest{PaymentType: payment.TypeWxpay},
		12.71,
		&payment.InstanceSelection{PaymentMode: "qrcode"},
		&payment.CreatePaymentResponse{
			TradeNo: "sub2_42",
			QRCode:  "weixin://wxpay/bizpayurl?pr=test",
		},
		payment.CreatePaymentResultOrderCreated,
	)

	if resp.ResultType != payment.CreatePaymentResultOrderCreated {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOrderCreated)
	}
	if resp.OutTradeNo != "sub2_42" {
		t.Fatalf("out_trade_no = %q, want %q", resp.OutTradeNo, "sub2_42")
	}
	if resp.QRCode != "weixin://wxpay/bizpayurl?pr=test" {
		t.Fatalf("qr_code = %q, want %q", resp.QRCode, "weixin://wxpay/bizpayurl?pr=test")
	}
	if resp.JSAPI != nil || resp.JSAPIPayload != nil {
		t.Fatal("order_created response should not include jsapi payload")
	}
	if !resp.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", resp.ExpiresAt, expiresAt)
	}
}

func TestBuildCreateOrderResponseIncludesCurrencySnapshot(t *testing.T) {
	t.Parallel()

	fxSource := fxSourceManual
	fxTimestamp := time.Date(2026, 4, 29, 9, 30, 0, 0, time.UTC)
	resp := buildCreateOrderResponse(
		&dbent.PaymentOrder{
			ID:                    91,
			Amount:                10,
			PaymentAmount:         255000,
			PaymentCurrency:       "VND",
			LedgerAmount:          10,
			LedgerCurrency:        "USD",
			FxRatePaymentToLedger: 10.0 / 255000.0,
			FxSource:              &fxSource,
			FxTimestamp:           &fxTimestamp,
			PayAmount:             262650,
			FeeRate:               3,
			ExpiresAt:             time.Date(2026, 4, 29, 9, 45, 0, 0, time.UTC),
			OutTradeNo:            "sub2_91",
		},
		CreateOrderRequest{PaymentType: payment.TypeSepay},
		262650,
		&payment.InstanceSelection{PaymentMode: "qrcode"},
		&payment.CreatePaymentResponse{TradeNo: "sub2_91", QRCode: "https://qr.example/sub2_91"},
		payment.CreatePaymentResultOrderCreated,
	)

	if resp.PaymentAmount != 255000 || resp.PaymentCurrency != "VND" {
		t.Fatalf("payment snapshot = %.2f %s, want 255000 VND", resp.PaymentAmount, resp.PaymentCurrency)
	}
	if resp.LedgerAmount != 10 || resp.LedgerCurrency != "USD" {
		t.Fatalf("ledger snapshot = %.2f %s, want 10 USD", resp.LedgerAmount, resp.LedgerCurrency)
	}
	if resp.FXRate != 10.0/255000.0 || resp.FXSource != fxSourceManual || !resp.FXTimestamp.Equal(fxTimestamp) {
		t.Fatalf("fx snapshot = rate %v source %q timestamp %v", resp.FXRate, resp.FXSource, resp.FXTimestamp)
	}
	if resp.PayAmount != 262650 {
		t.Fatalf("pay_amount = %v, want 262650", resp.PayAmount)
	}
}

func TestBuildProviderCreatePaymentRequestUsesPayAmountAndCurrencySnapshot(t *testing.T) {
	t.Parallel()

	req := buildProviderCreatePaymentRequest(
		CreateOrderRequest{PaymentType: payment.TypeSepay, ReturnURL: "https://app.example/payment/result"},
		&payment.InstanceSelection{SupportedTypes: string(payment.TypeSepay)},
		&dbent.PaymentOrder{
			OutTradeNo:      "sub2_vnd",
			PaymentAmount:   255000,
			PayAmount:       262650,
			PaymentCurrency: "VND",
			LedgerAmount:    10,
			LedgerCurrency:  "USD",
		},
		"Sub2API 10 USD",
	)

	if req.Amount != "262650" {
		t.Fatalf("provider amount = %q, want local pay_amount after fee", req.Amount)
	}
	if req.PaymentCurrency != "VND" {
		t.Fatalf("payment currency = %q, want VND", req.PaymentCurrency)
	}
	if req.LedgerAmount != "10.00" || req.LedgerCurrency != "USD" {
		t.Fatalf("ledger snapshot = %q %s, want 10.00 USD", req.LedgerAmount, req.LedgerCurrency)
	}
}

func TestBuildCreateOrderResponseCopiesJSAPIPayload(t *testing.T) {
	t.Parallel()

	jsapiPayload := &payment.WechatJSAPIPayload{
		AppID:     "wx123",
		TimeStamp: "1712345678",
		NonceStr:  "nonce-123",
		Package:   "prepay_id=wx123",
		SignType:  "RSA",
		PaySign:   "signed-payload",
	}
	resp := buildCreateOrderResponse(
		&dbent.PaymentOrder{
			ID:         88,
			Amount:     66.88,
			FeeRate:    0.01,
			ExpiresAt:  time.Date(2026, 4, 16, 13, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_88",
		},
		CreateOrderRequest{PaymentType: payment.TypeWxpay},
		67.55,
		&payment.InstanceSelection{PaymentMode: "popup"},
		&payment.CreatePaymentResponse{
			TradeNo:    "sub2_88",
			ResultType: payment.CreatePaymentResultJSAPIReady,
			JSAPI:      jsapiPayload,
		},
		payment.CreatePaymentResultJSAPIReady,
	)

	if resp.ResultType != payment.CreatePaymentResultJSAPIReady {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultJSAPIReady)
	}
	if resp.JSAPI == nil || resp.JSAPIPayload == nil {
		t.Fatal("expected jsapi payload aliases to be populated")
	}
	if resp.JSAPI != jsapiPayload || resp.JSAPIPayload != jsapiPayload {
		t.Fatal("expected jsapi aliases to preserve the original pointer")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponse(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	svc := newWeChatPaymentOAuthTestService(map[string]string{
		SettingKeyWeChatConnectEnabled:             "true",
		SettingKeyWeChatConnectAppID:               "wx123456",
		SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		SettingKeyWeChatConnectMode:                "mp",
		SettingKeyWeChatConnectScopes:              "snsapi_base",
		SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, 12.88, 0.03)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected oauth_required response, got nil")
	}
	if resp.ResultType != payment.CreatePaymentResultOAuthRequired {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOAuthRequired)
	}
	if resp.OAuth == nil {
		t.Fatal("expected oauth payload, got nil")
	}
	if resp.OAuth.AppID != "wx123456" {
		t.Fatalf("appid = %q, want %q", resp.OAuth.AppID, "wx123456")
	}
	if resp.OAuth.Scope != "snsapi_base" {
		t.Fatalf("scope = %q, want %q", resp.OAuth.Scope, "snsapi_base")
	}
	if resp.OAuth.RedirectURL != "/auth/wechat/payment/callback" {
		t.Fatalf("redirect_url = %q, want %q", resp.OAuth.RedirectURL, "/auth/wechat/payment/callback")
	}
	if resp.OAuth.AuthorizeURL != "/api/v1/auth/oauth/wechat/payment/start?amount=12.5&order_type=balance&payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat&scope=snsapi_base" {
		t.Fatalf("authorize_url = %q", resp.OAuth.AuthorizeURL)
	}
}

func TestBuildWeChatPaymentOAuthStartURLPreservesCurrencyAmountMode(t *testing.T) {
	t.Parallel()

	got, err := buildWeChatPaymentOAuthStartURL(CreateOrderRequest{
		Amount:          255000,
		AmountMode:      PaymentAmountModePayment,
		PaymentCurrency: "vnd",
		PaymentType:     payment.TypeWxpay,
		OrderType:       payment.OrderTypeBalance,
		SrcURL:          "https://merchant.example/payment?from=wechat",
	}, "snsapi_base")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/api/v1/auth/oauth/wechat/payment/start?amount=255000&amount_mode=payment&order_type=balance&payment_currency=VND&payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat&scope=snsapi_base"
	if got != want {
		t.Fatalf("authorize URL = %q, want %q", got, want)
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseRequiresMPConfigInWeChat(t *testing.T) {
	t.Parallel()

	svc := newWeChatPaymentOAuthTestService(nil)

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, 12.88, 0.03)
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	appErr := infraerrors.FromError(err)
	if appErr.Reason != "WECHAT_PAYMENT_MP_NOT_CONFIGURED" {
		t.Fatalf("reason = %q, want %q", appErr.Reason, "WECHAT_PAYMENT_MP_NOT_CONFIGURED")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseRequiresResumeSigningKey(t *testing.T) {
	t.Parallel()

	svc := &PaymentService{
		configService: &PaymentConfigService{
			settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
				SettingKeyWeChatConnectEnabled:             "true",
				SettingKeyWeChatConnectAppID:               "wx123456",
				SettingKeyWeChatConnectAppSecret:           "wechat-secret",
				SettingKeyWeChatConnectMode:                "mp",
				SettingKeyWeChatConnectScopes:              "snsapi_base",
				SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
				SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
			}},
			// Intentionally missing payment resume signing key.
			encryptionKey: nil,
		},
	}

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, 12.88, 0.03)
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	appErr := infraerrors.FromError(err)
	if appErr.Reason != "PAYMENT_RESUME_NOT_CONFIGURED" {
		t.Fatalf("reason = %q, want %q", appErr.Reason, "PAYMENT_RESUME_NOT_CONFIGURED")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseFallsBackToConfiguredLegacySigningKey(t *testing.T) {
	svc := &PaymentService{
		configService: &PaymentConfigService{
			settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
				SettingKeyWeChatConnectEnabled:             "true",
				SettingKeyWeChatConnectAppID:               "wx123456",
				SettingKeyWeChatConnectAppSecret:           "wechat-secret",
				SettingKeyWeChatConnectMode:                "mp",
				SettingKeyWeChatConnectScopes:              "snsapi_base",
				SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
				SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
			}},
			// Legacy stable signing key remains available for no-config upgrade compatibility.
			encryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		},
	}

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, 12.88, 0.03)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected oauth-required response, got nil")
	}
	if resp.ResultType != payment.CreatePaymentResultOAuthRequired {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOAuthRequired)
	}
	if resp.OAuth == nil || strings.TrimSpace(resp.OAuth.AuthorizeURL) == "" {
		t.Fatalf("expected oauth redirect payload, got %+v", resp.OAuth)
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseForSelectionSkipsEasyPayProvider(t *testing.T) {
	svc := newWeChatPaymentOAuthTestService(map[string]string{
		SettingKeyWeChatConnectEnabled:             "true",
		SettingKeyWeChatConnectAppID:               "wx123456",
		SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		SettingKeyWeChatConnectMode:                "mp",
		SettingKeyWeChatConnectScopes:              "snsapi_base",
		SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponseForSelection(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, 12.88, 0.03, &payment.InstanceSelection{
		ProviderKey: payment.TypeEasyPay,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
}

func newWeChatPaymentOAuthTestService(values map[string]string) *PaymentService {
	return &PaymentService{
		configService: &PaymentConfigService{
			settingRepo:   &paymentConfigSettingRepoStub{values: values},
			encryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		},
	}
}
