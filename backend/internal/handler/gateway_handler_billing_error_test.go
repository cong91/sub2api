package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBillingErrorDetails_MapsGroupRPMExceededToTooManyRequests(t *testing.T) {
	status, code, msg, retryAfter := billingErrorDetails(service.ErrGroupRPMExceeded)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_exceeded", code)
	require.NotEmpty(t, msg)
	require.Greater(t, retryAfter, 0, "RPM exceeded should return positive Retry-After")
	require.LessOrEqual(t, retryAfter, 60)
}

func TestBillingErrorDetails_MapsUserRPMExceededToTooManyRequests(t *testing.T) {
	status, code, msg, retryAfter := billingErrorDetails(service.ErrUserRPMExceeded)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_exceeded", code)
	require.NotEmpty(t, msg)
	require.Greater(t, retryAfter, 0, "RPM exceeded should return positive Retry-After")
	require.LessOrEqual(t, retryAfter, 60)
}

func TestBillingErrorDetails_APIKeyRateLimitStillMaps(t *testing.T) {
	// 回归保护：加 RPM 分支后不应影响已有 APIKey rate limit 的映射。
	for _, err := range []error{
		service.ErrAPIKeyRateLimit5hExceeded,
		service.ErrAPIKeyRateLimit1dExceeded,
		service.ErrAPIKeyRateLimit7dExceeded,
	} {
		status, code, _, _ := billingErrorDetails(err)
		require.Equal(t, http.StatusTooManyRequests, status, "status for %v", err)
		require.Equal(t, "rate_limit_exceeded", code)
	}
}

func TestBillingErrorDetails_BillingServiceUnavailableMapsTo503(t *testing.T) {
	status, code, _, retryAfter := billingErrorDetails(service.ErrBillingServiceUnavailable)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "billing_service_error", code)
	require.Equal(t, 0, retryAfter, "non-RPM errors should not set Retry-After")
}

func TestBillingErrorDetails_UnknownErrorFallsBackTo403(t *testing.T) {
	status, code, msg, _ := billingErrorDetails(errors.New("unknown billing failure"))
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "billing_error", code)
	require.NotEmpty(t, msg)
}

func TestBillingErrorDetails_MapsSubscriptionLimitsToSwitchableQuotaError(t *testing.T) {
	for _, err := range []error{
		service.ErrDailyLimitExceeded,
		service.ErrWeeklyLimitExceeded,
		service.ErrMonthlyLimitExceeded,
	} {
		status, code, msg, retryAfter := billingErrorDetails(err)
		require.Equal(t, http.StatusTooManyRequests, status, "status for %v", err)
		require.Equal(t, "subscription_limit_exceeded", code)
		require.NotEmpty(t, msg)
		require.Equal(t, 0, retryAfter)
	}
}

func TestBillingErrorDetails_MapsInsufficientBalance(t *testing.T) {
	status, code, msg, retryAfter := billingErrorDetails(service.ErrInsufficientBalance)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "insufficient_balance", code)
	require.NotEmpty(t, msg)
	require.Equal(t, 0, retryAfter)
}

func TestRespondBillingAsAssistantMessageIncludesStructuredAutoSwitchMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	require.True(t, respondBillingAsAssistantMessage(ctx, service.ErrDailyLimitExceeded, "anthropic"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "subscription_limit_exceeded", rec.Header().Get("X-Sub2API-Billing-Code"))
	require.Equal(t, "true", rec.Header().Get("X-Sub2API-Auto-Switchable"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok, "assistant message should expose billing metadata for clients that inspect 200 responses")
	require.Equal(t, "subscription_limit_exceeded", metadata["billing_code"])
	require.Equal(t, true, metadata["auto_switchable"])
}

func TestRespondBillingAsAssistantMessageMarksInsufficientBalanceAsNotSwitchable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	require.True(t, respondBillingAsAssistantMessage(ctx, service.ErrInsufficientBalance, "openai"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "insufficient_balance", rec.Header().Get("X-Sub2API-Billing-Code"))
	require.Equal(t, "false", rec.Header().Get("X-Sub2API-Auto-Switchable"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "insufficient_balance", metadata["billing_code"])
	require.Equal(t, false, metadata["auto_switchable"])
}
