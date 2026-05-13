package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

func TestSanitizeAdminPaymentOrderForResponseAddsCurrency(t *testing.T) {
	now := time.Now()
	order := &dbent.PaymentOrder{
		ID:          1,
		UserID:      2,
		Amount:      100,
		PayAmount:   108,
		FeeRate:     8,
		OutTradeNo:  "sub2_202606250001",
		PaymentType: "stripe",
		OrderType:   "subscription",
		Status:      "COMPLETED",
		ExpiresAt:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"currency":       "USD",
			"payment_mode":   "manual",
			"api_key":        "secret-value",
		},
	}

	got := sanitizeAdminPaymentOrderForResponse(order)
	if got == nil {
		t.Fatal("expected sanitized order")
	}
	if got.Currency != "USD" {
		t.Fatalf("expected currency USD, got %q", got.Currency)
	}
	if got.ProviderSnapshot == nil {
		t.Fatal("expected safe provider snapshot metadata")
	}
	if got.ProviderSnapshot["payment_mode"] != "manual" {
		t.Fatalf("expected payment_mode manual, got %#v", got.ProviderSnapshot["payment_mode"])
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal sanitized order: %v", err)
	}
	if !strings.Contains(string(body), "provider_snapshot") {
		t.Fatalf("expected safe provider_snapshot metadata, got %s", string(body))
	}
	if strings.Contains(string(body), "api_key") || strings.Contains(string(body), "secret-value") {
		t.Fatalf("expected provider_snapshot secrets to be omitted, got %s", string(body))
	}
}
