//go:build unit

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v85"
)

type stripeRefundBackend struct {
	params []*stripe.RefundCreateParams
}

func (b *stripeRefundBackend) Call(_ string, _ string, _ string, params stripe.ParamsContainer, v stripe.LastResponseSetter) error {
	b.params = append(b.params, params.(*stripe.RefundCreateParams))
	refund := v.(*stripe.Refund)
	refund.ID = "re_123"
	refund.Status = stripe.RefundStatusSucceeded
	return nil
}

func (*stripeRefundBackend) CallStreaming(string, string, string, stripe.ParamsContainer, stripe.StreamingLastResponseSetter) error {
	return nil
}

func (*stripeRefundBackend) CallRaw(string, string, string, []byte, *stripe.Params, stripe.LastResponseSetter) error {
	return nil
}

func (*stripeRefundBackend) CallMultipart(string, string, string, string, *bytes.Buffer, *stripe.Params, stripe.LastResponseSetter) error {
	return nil
}

func (*stripeRefundBackend) SetMaxNetworkRetries(int64) {}

func TestStripeRefundUsesStableAmountSpecificIdempotencyKey(t *testing.T) {
	backend := &stripeRefundBackend{}
	client := stripe.NewClient("sk_test", stripe.WithBackends(&stripe.Backends{API: backend}))
	provider := &Stripe{
		config:      map[string]string{"currency": "CNY"},
		initialized: true,
		sc:          client,
	}

	refund := func(amount string) {
		_, err := provider.Refund(context.Background(), payment.RefundRequest{
			TradeNo: "pi_123",
			OrderID: "sub2_order_456",
			Amount:  amount,
		})
		require.NoError(t, err)
	}

	refund("12.34")
	refund("12.34")
	refund("12.35")

	require.Len(t, backend.params, 3)
	require.Equal(t, int64(1234), *backend.params[0].Amount)
	require.Equal(t, "re-sub2_order_456-1234", *backend.params[0].IdempotencyKey)
	require.Equal(t, backend.params[0].IdempotencyKey, backend.params[1].IdempotencyKey)
	require.Equal(t, int64(1235), *backend.params[2].Amount)
	require.Equal(t, "re-sub2_order_456-1235", *backend.params[2].IdempotencyKey)
	require.NotEqual(t, *backend.params[0].IdempotencyKey, *backend.params[2].IdempotencyKey)
}

func TestStripeCurrencyDefaultsToCNY(t *testing.T) {
	provider := &Stripe{config: map[string]string{}}
	if got := provider.currency(); got != "CNY" {
		t.Fatalf("Stripe.currency empty = %q, want CNY", got)
	}
	provider.config["currency"] = " vnd "
	if got := provider.currency(); got != "VND" {
		t.Fatalf("Stripe.currency vnd = %q, want VND", got)
	}
}

func TestStripeIntentCurrencyDefaultsToCNY(t *testing.T) {
	if got := stripeIntentCurrency(stripe.Currency(""), ""); got != "CNY" {
		t.Fatalf("stripeIntentCurrency empty = %q, want CNY", got)
	}
	if got := stripeIntentCurrency(stripe.Currency(" vnd "), ""); got != "VND" {
		t.Fatalf("stripeIntentCurrency vnd = %q, want VND", got)
	}
}

func TestStripeParsePaymentIntentUsesEventCurrencyMinorUnits(t *testing.T) {
	intent := stripe.PaymentIntent{
		ID:       "pi_123",
		Amount:   12000,
		Currency: stripe.Currency("krw"),
		Metadata: map[string]string{"orderId": "order_123"},
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal payment intent: %v", err)
	}
	event := &stripe.Event{Data: &stripe.EventData{Raw: raw}}

	notification, err := parseStripePaymentIntent(event, payment.ProviderStatusSuccess, string(raw))
	if err != nil {
		t.Fatalf("parseStripePaymentIntent returned error: %v", err)
	}
	if notification.Currency != "KRW" {
		t.Fatalf("currency = %q, want KRW", notification.Currency)
	}
	if math.Abs(notification.Amount-12000) > 1e-9 {
		t.Fatalf("amount = %f, want 12000", notification.Amount)
	}
}

func TestStripeMinorUnitConversionSupportsLocalCurrencies(t *testing.T) {
	tests := []struct {
		currency string
		amount   string
		want     int64
	}{
		{currency: "CNY", amount: "12.34", want: 1234},
		{currency: "USD", amount: "12.34", want: 1234},
		{currency: "VND", amount: "200000", want: 200000},
		{currency: "KRW", amount: "12000", want: 12000},
	}
	for _, tt := range tests {
		t.Run(tt.currency, func(t *testing.T) {
			got, err := payment.AmountToMinorUnit(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("AmountToMinorUnit returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("minor units = %d, want %d", got, tt.want)
			}
		})
	}
}
