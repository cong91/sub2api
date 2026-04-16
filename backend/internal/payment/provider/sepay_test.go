//go:build unit

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func TestNewSepay(t *testing.T) {
	t.Parallel()

	valid := map[string]string{"apiToken": "token", "bankAccountId": "123456", "notifyUrl": "https://example.com/webhook"}
	tests := []struct {
		name      string
		config    map[string]string
		wantErr   bool
		errSubstr string
	}{
		{name: "valid config", config: valid},
		{name: "missing api token", config: map[string]string{"bankAccountId": "123456", "notifyUrl": "https://example.com/webhook"}, wantErr: true, errSubstr: "apiToken"},
		{name: "missing bank account", config: map[string]string{"apiToken": "token", "notifyUrl": "https://example.com/webhook"}, wantErr: true, errSubstr: "bankAccountId"},
		{name: "missing notify url", config: map[string]string{"apiToken": "token", "bankAccountId": "123456"}, wantErr: true, errSubstr: "notifyUrl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewSepay("inst-1", tt.config)
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil || got.instanceID != "inst-1" {
				t.Fatalf("unexpected provider: %#v", got)
			}
		})
	}
}

func TestSepayCreatePayment(t *testing.T) {
	t.Parallel()

	var authHeader string
	var requestedPaths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		requestedPaths = append(requestedPaths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bankaccounts/details/778899":
			_, _ = w.Write([]byte(`{"bankaccount":{"id":"778899","account_number":"333999333333","bank_short_name":"MBBank"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer ts.Close()

	p, err := NewSepay("inst-1", map[string]string{
		"apiToken":      "token-123",
		"bankAccountId": "778899",
		"notifyUrl":     "https://example.com/webhook/sepay",
		"apiBase":       ts.URL,
	})
	if err != nil {
		t.Fatalf("NewSepay error: %v", err)
	}
	resp, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "PAY-1", Amount: "100000", PaymentCurrency: "VND"})
	if err != nil {
		t.Fatalf("CreatePayment error: %v", err)
	}
	if len(requestedPaths) != 1 || requestedPaths[0] != "/bankaccounts/details/778899" {
		t.Fatalf("unexpected requests: %v", requestedPaths)
	}
	if authHeader != "Bearer token-123" {
		t.Fatalf("unexpected auth header: %q", authHeader)
	}
	if resp.TradeNo != "PAY-1" {
		t.Fatalf("TradeNo = %q, want PAY-1", resp.TradeNo)
	}
	if resp.QRCode != "https://qr.sepay.vn/img?acc=333999333333&amount=100000&bank=MBBank&des=PAY-1" {
		t.Fatalf("unexpected QR code url: %q", resp.QRCode)
	}
}

func TestSepayQueryOrder(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/bankaccounts/details/778899":
			_, _ = w.Write([]byte(`{"bankaccount":{"id":"778899","account_number":"333999333333","bank_short_name":"MBBank"}}`))
		case "/transactions/list":
			if got := r.URL.Query().Get("account_number"); got != "333999333333" {
				t.Fatalf("account_number = %q", got)
			}
			_, _ = w.Write([]byte(`{"transactions":[{"id":"92704","amount_in":100000,"transaction_content":"NAP TIEN PAY-1","code":"PAY-1","reference_number":"MBVCB.3278907687","transaction_date":"2026-04-16 22:00:00"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	p, err := NewSepay("inst-1", map[string]string{
		"apiToken":      "token-123",
		"bankAccountId": "778899",
		"notifyUrl":     "https://example.com/webhook/sepay",
		"apiBase":       ts.URL,
	})
	if err != nil {
		t.Fatalf("NewSepay error: %v", err)
	}
	resp, err := p.QueryOrder(context.Background(), "PAY-1")
	if err != nil {
		t.Fatalf("QueryOrder error: %v", err)
	}
	if resp.Status != payment.ProviderStatusPaid || resp.TradeNo != "MBVCB.3278907687" || resp.Amount != 100000 {
		t.Fatalf("unexpected query response: %#v", resp)
	}
}

func TestSepayVerifyNotification(t *testing.T) {
	t.Parallel()

	p, err := NewSepay("inst-1", map[string]string{
		"apiToken":      "token-123",
		"bankAccountId": "778899",
		"notifyUrl":     "https://example.com/webhook/sepay",
		"webhookApiKey": "webhook-secret",
	})
	if err != nil {
		t.Fatalf("NewSepay error: %v", err)
	}
	n, err := p.VerifyNotification(context.Background(), `{"id":92704,"code":"PAY-1","transferType":"in","transferAmount":100000,"referenceCode":"MBVCB.3278907687"}`,
		map[string]string{"authorization": "Apikey webhook-secret"})
	if err != nil {
		t.Fatalf("VerifyNotification error: %v", err)
	}
	if n == nil {
		t.Fatal("expected notification")
	}
	if n.OrderID != "PAY-1" || n.TradeNo != "MBVCB.3278907687" || n.Amount != 100000 || n.Currency != "VND" || n.Status != payment.ProviderStatusSuccess {
		t.Fatalf("unexpected notification: %#v", n)
	}

	ignored, err := p.VerifyNotification(context.Background(), `{"id":1,"code":"","transferType":"in","transferAmount":1000}`,
		map[string]string{"authorization": "Apikey webhook-secret"})
	if err != nil {
		t.Fatalf("unexpected error on ignored webhook: %v", err)
	}
	if ignored != nil {
		t.Fatalf("expected nil notification, got %#v", ignored)
	}
}
