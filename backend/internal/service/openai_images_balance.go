package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const (
	OpenAIImagesInsufficientBalanceReason  GatewayFailureReason = "openai_images_insufficient_balance"
	OpenAIImagesInsufficientBalanceCode                         = providerInsufficientBalanceCode
	OpenAIImagesInsufficientBalanceMessage                      = "Upstream image account has insufficient balance"
	openAIImagesBalanceCooldown                                 = 5 * time.Minute
	openAIImagesBalanceRateLimitReason                          = "openai_images_insufficient_balance"
)

// isOpenAIImagesInsufficientBalance applies the shared provider-error parser
// to image failover handling.
func isOpenAIImagesInsufficientBalance(body []byte) bool {
	return hasStructuredProviderBalanceCode(body)
}

func (s *OpenAIGatewayService) coolOpenAIImagesInsufficientBalance(ctx context.Context, account *Account) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	now := time.Now()
	resetAt := now.Add(openAIImagesBalanceCooldown)
	setAccountModelRateLimitSnapshot(account, openAIImageGenerationRateLimitKey, resetAt, openAIImagesBalanceRateLimitReason, now)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, openAIImageGenerationRateLimitKey, resetAt, openAIImagesBalanceRateLimitReason); err != nil {
		slog.Warn("openai_images_balance_cooldown_failed", "account_id", account.ID, "error", err)
	}
}

func newOpenAIImagesInsufficientBalanceFailoverError(statusCode int, headers http.Header, body []byte) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:        statusCode,
		ResponseHeaders:   headers.Clone(),
		ResponseBody:      body,
		Reason:            OpenAIImagesInsufficientBalanceReason,
		ClientStatusCode:  402,
		ClientMessage:     OpenAIImagesInsufficientBalanceMessage,
		NextAccountAction: NextAccountRetry,
	}
}
