package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// ProviderBalanceAlerts delivers bounded admin alerts for API-key providers'
// structured insufficient-balance responses.
type ProviderBalanceAlerts struct {
	telegram *TelegramNotifyService
	claims   *IdempotencyCoordinator
}

func NewProviderBalanceAlerts(telegram *TelegramNotifyService, claims *IdempotencyCoordinator) *ProviderBalanceAlerts {
	return &ProviderBalanceAlerts{telegram: telegram, claims: claims}
}

// newAPIQuotaError matches the observed structured new-api account quota errors.
// Require the complete numeric message shape to avoid classifying generic 403s.
var newAPIQuotaError = regexp.MustCompile(`^(?:預扣費額度失敗，使用者剩餘額度：＄-?[0-9]+(?:\.[0-9]+)?，需要預扣費額度：＄[0-9]+(?:\.[0-9]+)?|使用者額度不足，剩餘額度：＄-?[0-9]+(?:\.[0-9]+)?)(?: \(request id: [a-zA-Z0-9_-]+\))?$`)

const providerInsufficientBalanceCode = "insufficient_balance"

// hasStructuredProviderBalanceCode recognizes explicit balance codes only
// within structured upstream response fields, not echoed request data or arbitrary text.
func hasStructuredProviderBalanceCode(body []byte) bool {
	var value any
	if len(body) == 0 || json.Unmarshal(body, &value) != nil {
		return false
	}
	return containsStructuredProviderBalanceCode(value, 0)
}

func containsStructuredProviderBalanceCode(value any, depth int) bool {
	if depth > 6 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			if text, ok := child.(string); ok {
				normalizedValue := strings.ToLower(strings.Trim(strings.TrimSpace(text), "."))
				switch normalizedKey {
				case "errorkey", "error_key", "code":
					if normalizedValue == providerInsufficientBalanceCode {
						return true
					}
				}
			}
			if isUpstreamErrorContainer(normalizedKey) && containsStructuredProviderBalanceCode(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsStructuredProviderBalanceCode(child, depth+1) {
				return true
			}
		}
	}
	return false
}

func isUpstreamErrorContainer(key string) bool {
	switch key {
	case "error", "errors", "cause", "detail", "details", "inner", "innererror", "inner_error", "response":
		return true
	default:
		return false
	}
}

// providerInsufficientBalance matches only structured account-level
// balance signals, never generic 403/HTML/CDN failures or a customer's wallet.
func providerInsufficientBalance(account *Account, statusCode int, body []byte) bool {
	if account == nil || !domain.IsConcretePlatform(account.Platform) || account.Type != AccountTypeAPIKey || !json.Valid(body) {
		return false
	}
	if statusCode < http.StatusBadRequest || statusCode > 599 {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(extractUpstreamErrorCode(body)))
	// A specific outer code wins over a contradictory nested value or message.
	// Generic wrapper codes can legitimately contain a more specific cause.
	switch code {
	case "", "insufficient_balance", "upstream_error", "api_error", "server_error", "internal_error":
	default:
		return false
	}
	// Reuse the existing structured-error parser, which only traverses error
	// containers and does not classify echoed requests or arbitrary messages.
	// Explicit codes can survive a provider's 5xx wrapper (e.g. images).
	if hasStructuredProviderBalanceCode(body) {
		return true
	}
	// Preserve the previous direct-code handling for 402/403 responses.
	if (statusCode == http.StatusForbidden || statusCode == http.StatusPaymentRequired) && code == "insufficient_balance" {
		return true
	}
	// Never override an explicit code with a less reliable message match.
	if code != "" {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if statusCode == http.StatusBadRequest && account.Platform == PlatformAnthropic &&
		strings.HasPrefix(message, "your credit balance is too low to access the anthropic api") {
		return true
	}
	if (statusCode == http.StatusForbidden || statusCode == http.StatusPaymentRequired) &&
		(message == "insufficient balance" || strings.HasPrefix(message, "insufficient account balance")) {
		return true
	}
	if statusCode != http.StatusForbidden {
		return false
	}
	var response struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &response) == nil && response.Type == "error" &&
		response.Error.Type == "new_api_error" && newAPIQuotaError.MatchString(strings.TrimSpace(response.Error.Message))
}

// Observe does not change the upstream error policy. One durable claim per
// account/hour limits repeated failed requests across instances; a failed send
// is retryable on the next matching response after coordinator backoff.
func (s *ProviderBalanceAlerts) Observe(ctx context.Context, account *Account, statusCode int, body []byte) {
	if !providerInsufficientBalance(account, statusCode, body) {
		return
	}
	s.Deliver(ctx, account.ID, account.Name, account.Platform)
}

// Deliver is called only after classifying the raw upstream provider response.
// Its arguments are copied scalars, so it is safe to invoke from the error
// handler asynchronously without retaining a response buffer or account object.
func (s *ProviderBalanceAlerts) Deliver(ctx context.Context, accountID int64, accountName, platform string) {
	if s == nil || s.telegram == nil || s.claims == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer cancel()
	if !s.telegram.isEnabled(ctx, SettingTelegramNotifyProviderBalance) {
		return
	}
	key := fmt.Sprintf("provider-balance:%d:%s", accountID, time.Now().UTC().Format("2006010215"))
	_, err := s.claims.Execute(ctx, IdempotencyExecuteOptions{
		Scope: "telegram_provider_balance", ActorScope: fmt.Sprintf("account:%d", accountID),
		Method: "POST", Route: "telegram/admin/provider-balance",
		IdempotencyKey: key, Payload: map[string]any{"account_id": accountID, "platform": platform, "status": "exhausted"},
		RequireKey: true, TTL: 2 * time.Hour,
	}, func(sendCtx context.Context) (any, error) {
		cfg, configErr := s.telegram.getConfig(sendCtx)
		if configErr != nil {
			return nil, configErr
		}
		if cfg == nil {
			return nil, fmt.Errorf("admin telegram bot is not configured")
		}
		text := fmt.Sprintf("<b>Provider hết số dư — cần nạp tiền</b>\n\nAccount: <code>%d</code> %s\nProvider: <b>%s</b>\nLỗi upstream: Insufficient account balance\nThời gian: %s",
			accountID, escapeHTML(truncateTelegram(accountName, 128)), escapeHTML(truncateTelegram(platform, 64)), time.Now().Format("2006-01-02 15:04:05"))
		if sendErr := s.telegram.sendWithConfig(sendCtx, cfg, cfg.ChatID, text); sendErr != nil {
			// HTTP transport errors may contain the bot token in the URL. Never
			// persist them in the claim or emit them to the application log.
			return nil, fmt.Errorf("admin telegram delivery failed")
		}
		return nil, nil
	})
	if err != nil && err != ErrIdempotencyInProgress && err != ErrIdempotencyRetryBackoff {
		slog.Warn("provider_balance_alert_failed", "account_id", accountID, "platform", platform, "error", err)
	}
}
