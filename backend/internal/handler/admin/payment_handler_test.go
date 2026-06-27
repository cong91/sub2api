package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

func TestSanitizeAdminPaymentOrderForResponseAddsCurrencyAndSanitizesProviderSnapshot(t *testing.T) {
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
			"api_key":        "example-api-token",
			"webhook_secret": "example-webhook-token",
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
		t.Fatal("expected sanitized provider snapshot")
	}
	if got.ProviderSnapshot["currency"] != "USD" {
		t.Fatalf("expected provider snapshot currency USD, got %#v", got.ProviderSnapshot["currency"])
	}
	if got.ProviderSnapshot["schema_version"] != 2 {
		t.Fatalf("expected provider snapshot schema_version 2, got %#v", got.ProviderSnapshot["schema_version"])
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal sanitized order: %v", err)
	}
	bodyText := string(body)
	if !strings.Contains(bodyText, "provider_snapshot") {
		t.Fatalf("expected sanitized provider_snapshot to be retained, got %s", bodyText)
	}
	if strings.Contains(bodyText, "api_key") || strings.Contains(bodyText, "webhook_secret") || strings.Contains(bodyText, "example-api-token") || strings.Contains(bodyText, "example-webhook-token") {
		t.Fatalf("expected provider_snapshot secrets to be omitted, got %s", bodyText)
	}
}
