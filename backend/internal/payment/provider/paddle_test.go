//go:build unit

package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func TestNewPaddle(t *testing.T) {
	t.Parallel()

	if _, err := NewPaddle("inst-1", map[string]string{}); err == nil || !strings.Contains(err.Error(), "apiKey") {
		t.Fatalf("expected apiKey error, got %v", err)
	}
	p, err := NewPaddle("inst-1", map[string]string{"apiKey": "key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.instanceID != "inst-1" {
		t.Fatalf("instanceID = %q", p.instanceID)
	}
}

func TestPaddleCreatePayment(t *testing.T) {
	t.Parallel()

	var authHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.URL.Path != "/transactions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"txn_123","status":"ready","currency_code":"USD","details":{"totals":{"total":"1250"}},"custom_data":{"orderId":"sub2_abc"}}}`))
	}))
	defer ts.Close()

	p, err := NewPaddle("inst-1", map[string]string{"apiKey": "pdl_test", "apiBase": ts.URL})
	if err != nil {
		t.Fatalf("NewPaddle error: %v", err)
	}
	resp, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:         "sub2_abc",
		Amount:          "12.50",
		PaymentCurrency: "USD",
		LedgerCurrency:  "USD",
		LedgerAmount:    "12.50",
		PaymentType:     payment.TypePaddle,
		Subject:         "Wallet Recharge",
	})
	if err != nil {
		t.Fatalf("CreatePayment error: %v", err)
	}
	if authHeader != "Bearer pdl_test" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	if resp.TradeNo != "txn_123" || resp.CheckoutID != "txn_123" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestPaddleQueryOrder(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transactions/txn_123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"txn_123","status":"paid","currency_code":"USD","details":{"totals":{"total":"1250"}},"custom_data":{"orderId":"sub2_abc"},"billed_at":"2026-04-17T10:00:00Z"}}`))
	}))
	defer ts.Close()

	p, err := NewPaddle("inst-1", map[string]string{"apiKey": "pdl_test", "apiBase": ts.URL})
	if err != nil {
		t.Fatalf("NewPaddle error: %v", err)
	}
	resp, err := p.QueryOrder(context.Background(), "txn_123")
	if err != nil {
		t.Fatalf("QueryOrder error: %v", err)
	}
	if resp.Status != payment.ProviderStatusPaid || resp.Amount != 12.5 || resp.Currency != "USD" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestPaddleVerifyNotification(t *testing.T) {
	t.Parallel()

	body := `{"event_type":"transaction.completed","data":{"id":"txn_123","status":"completed","currency_code":"USD","details":{"totals":{"total":"1250"}},"custom_data":{"orderId":"sub2_abc"}}}`
	secret := "whsec_test"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + ":" + body))
	sig := hex.EncodeToString(mac.Sum(nil))

	p, err := NewPaddle("inst-1", map[string]string{"apiKey": "pdl_test", "webhookSecret": secret})
	if err != nil {
		t.Fatalf("NewPaddle error: %v", err)
	}
	n, err := p.VerifyNotification(context.Background(), body, map[string]string{"paddle-signature": "ts=" + ts + ";h1=" + sig})
	if err != nil {
		t.Fatalf("VerifyNotification error: %v", err)
	}
	if n == nil {
		t.Fatal("expected notification")
	}
	if n.OrderID != "sub2_abc" || n.TradeNo != "txn_123" || n.Amount != 12.5 || n.Currency != "USD" || n.Status != payment.ProviderStatusSuccess {
		t.Fatalf("unexpected notification: %#v", n)
	}
}
