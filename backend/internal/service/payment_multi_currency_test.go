package service

import (
	"testing"
	"time"
)

func TestResolveFXSnapshot_ManualRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 16, 19, 0, 0, 0, time.UTC)
	cfg := &PaymentConfig{
		LedgerCurrency:           "USD",
		AllowedPaymentCurrencies: []string{"USD", "VND", "CNY"},
		ManualFXRates: map[string]float64{
			"VND": 0.000039,
		},
	}

	snapshot, err := resolveFXSnapshot("vnd", cfg, now)
	if err != nil {
		t.Fatalf("resolveFXSnapshot returned error: %v", err)
	}
	if snapshot.PaymentCurrency != "VND" {
		t.Fatalf("PaymentCurrency = %q, want %q", snapshot.PaymentCurrency, "VND")
	}
	if snapshot.LedgerCurrency != "USD" {
		t.Fatalf("LedgerCurrency = %q, want %q", snapshot.LedgerCurrency, "USD")
	}
	if snapshot.RatePaymentToLedger != 0.000039 {
		t.Fatalf("RatePaymentToLedger = %v, want %v", snapshot.RatePaymentToLedger, 0.000039)
	}
	if snapshot.Source != fxSourceManual {
		t.Fatalf("Source = %q, want %q", snapshot.Source, fxSourceManual)
	}
	if !snapshot.Timestamp.Equal(now) {
		t.Fatalf("Timestamp = %v, want %v", snapshot.Timestamp, now)
	}
}

func TestResolveFXSnapshot_IdentityRate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 16, 19, 1, 0, 0, time.UTC)
	cfg := &PaymentConfig{
		LedgerCurrency:           "USD",
		AllowedPaymentCurrencies: []string{"USD", "CNY"},
		ManualFXRates:            map[string]float64{"CNY": 0.14},
	}

	snapshot, err := resolveFXSnapshot("USD", cfg, now)
	if err != nil {
		t.Fatalf("resolveFXSnapshot returned error: %v", err)
	}
	if snapshot.RatePaymentToLedger != 1 {
		t.Fatalf("RatePaymentToLedger = %v, want 1", snapshot.RatePaymentToLedger)
	}
	if snapshot.Source != fxSourceIdentity {
		t.Fatalf("Source = %q, want %q", snapshot.Source, fxSourceIdentity)
	}
}

func TestResolveFXSnapshot_UnsupportedCurrency(t *testing.T) {
	t.Parallel()

	cfg := &PaymentConfig{
		LedgerCurrency:           "USD",
		AllowedPaymentCurrencies: []string{"USD", "CNY"},
		ManualFXRates:            map[string]float64{"CNY": 0.14},
	}

	_, err := resolveFXSnapshot("KRW", cfg, time.Now().UTC())
	if err == nil {
		t.Fatal("resolveFXSnapshot expected error for unsupported currency")
	}
}

func TestCurrencyAmountMatches(t *testing.T) {
	t.Parallel()

	if !currencyAmountMatches(10.005, 10.00, "USD") {
		t.Fatal("USD should allow 0.01 tolerance")
	}
	if currencyAmountMatches(10.02, 10.00, "USD") {
		t.Fatal("USD should reject amount over tolerance")
	}
	if !currencyAmountMatches(1000, 1001, "KRW") {
		t.Fatal("KRW should allow ±1 tolerance")
	}
	if currencyAmountMatches(1000, 1003, "KRW") {
		t.Fatal("KRW should reject amount over ±1 tolerance")
	}
}
