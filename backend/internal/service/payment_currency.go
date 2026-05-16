package service

import (
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func paymentProviderConfigCurrency(providerKey string, cfg map[string]string) string {
	switch strings.TrimSpace(providerKey) {
	case payment.TypeStripe, payment.TypeAirwallex:
		currency, err := payment.NormalizePaymentCurrency(cfg["currency"])
		if err == nil {
			return currency
		}
	}
	return payment.DefaultPaymentCurrency
}

func PaymentOrderCurrency(order *dbent.PaymentOrder) string {
	// Prefer the explicit payment_currency field on the order itself.
	if order.PaymentCurrency != "" {
		if currency, err := payment.NormalizePaymentCurrency(order.PaymentCurrency); err == nil {
			return currency
		}
	}
	if snapshot := psOrderProviderSnapshot(order); snapshot != nil {
		if currency, err := payment.NormalizePaymentCurrency(snapshot.Currency); err == nil {
			return currency
		}
	}
	return payment.DefaultPaymentCurrency
}
