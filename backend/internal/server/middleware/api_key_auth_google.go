package middleware

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/clienterror"
	"github.com/Wei-Shaw/sub2api/internal/pkg/googleapi"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// APIKeyAuthGoogle is a Google-style error wrapper for API key auth.
func APIKeyAuthGoogle(apiKeyService *service.APIKeyService, cfg *config.Config) gin.HandlerFunc {
	return APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg)
}

// APIKeyAuthWithSubscriptionGoogle behaves like ApiKeyAuthWithSubscription but returns Google-style errors:
// {"error":{"code":401,"message":"...","status":"UNAUTHENTICATED"}}
//
// It is intended for Gemini native endpoints (/v1beta) to match Gemini SDK expectations.
func APIKeyAuthWithSubscriptionGoogle(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return APIKeyAuthWithSubscriptionGoogleAndEntitlements(apiKeyService, subscriptionService, nil, cfg)
}

// APIKeyAuthWithSubscriptionGoogleAndEntitlements enables server-side entitlement auto-switch
// for Gemini-native gateway traffic while preserving Google-style errors when no fallback exists.
func APIKeyAuthWithSubscriptionGoogleAndEntitlements(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, entitlementService *service.EntitlementService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v := strings.TrimSpace(c.Query("api_key")); v != "" {
			abortWithGoogleError(c, 400, "Query parameter api_key is deprecated. Use Authorization header or key instead.")
			return
		}
		apiKeyString := extractAPIKeyForGoogle(c)
		if apiKeyString == "" {
			abortWithGoogleError(c, 401, "API key is required")
			return
		}

		apiKey, err := apiKeyService.GetByKey(c.Request.Context(), apiKeyString)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				abortWithGoogleError(c, 401, "Invalid API key")
				return
			}
			abortWithGoogleError(c, 500, "Failed to validate API key")
			return
		}

		// 同 api_key_auth.go：早退中断前也写入 Ops 回退 key，便于错误日志展示
		// user/group/platform。
		SetOpsFallbackAPIKey(c, apiKey)

		if !apiKey.IsActive() &&
			apiKey.Status != service.StatusAPIKeyExpired &&
			apiKey.Status != service.StatusAPIKeyQuotaExhausted {
			abortWithGoogleError(c, 401, "API key is disabled")
			return
		}
		if apiKey.User == nil {
			abortWithGoogleError(c, 401, "User associated with API key not found")
			return
		}
		if !apiKey.User.IsActive() {
			abortWithGoogleError(c, 401, "User account is not active")
			return
		}
		if _, message, ok := validateAPIKeyGroupAvailable(apiKey); !ok {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
			abortWithGoogleError(c, 403, message)
			return
		}

		// 简易模式：跳过余额和订阅检查
		if cfg.RunMode == config.RunModeSimple {
			c.Set(string(ContextKeyAPIKey), apiKey)
			c.Set(string(ContextKeyUser), AuthSubject{
				UserID:      apiKey.User.ID,
				Concurrency: apiKey.User.Concurrency,
			})
			c.Set(string(ContextKeyUserRole), apiKey.User.Role)
			setGroupContext(c, apiKey.Group)
			_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
			c.Next()
			return
		}

		isSubscriptionType := apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
		if apiKey.Status == service.StatusAPIKeyQuotaExhausted {
			if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "api_key_quota_exhausted", "API_KEY_QUOTA_EXHAUSTED", false); ok {
				apiKey = switchedKey
				isSubscriptionType = apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
			} else {
				abortWithGoogleBillingError(c, 429, "API_KEY_QUOTA_EXHAUSTED", "API key 额度已用完")
				return
			}
		}
		if apiKey.Status == service.StatusAPIKeyExpired || apiKey.IsExpired() {
			abortWithGoogleError(c, 403, "API key 已过期")
			return
		}
		if apiKey.IsQuotaExhausted() {
			if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "api_key_quota_exhausted", "API_KEY_QUOTA_EXHAUSTED", false); ok {
				apiKey = switchedKey
				isSubscriptionType = apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
			} else {
				abortWithGoogleBillingError(c, 429, "API_KEY_QUOTA_EXHAUSTED", "API key 额度已用完")
				return
			}
		}
		var subscription *service.UserSubscription
		if isSubscriptionType && subscriptionService != nil {
			subscription, err = subscriptionService.GetActiveSubscription(
				c.Request.Context(),
				apiKey.User.ID,
				apiKey.Group.ID,
			)
			if err != nil {
				if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "subscription_not_found", "SUBSCRIPTION_NOT_FOUND", false); ok {
					apiKey = switchedKey
					isSubscriptionType = apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
					if isSubscriptionType && subscriptionService != nil {
						subscription, err = subscriptionService.GetActiveSubscription(c.Request.Context(), apiKey.User.ID, apiKey.Group.ID)
						if err != nil {
							abortWithGoogleError(c, 403, "No active subscription found for this group")
							return
						}
					}
				} else {
					abortWithGoogleError(c, 403, "No active subscription found for this group")
					return
				}
			}

			if subscription != nil {
				needsMaintenance, err := subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
				if err != nil {
					status := 403
					code := "SUBSCRIPTION_INVALID"
					reason := "subscription_invalid"
					allowProviderChange := false
					if errors.Is(err, service.ErrDailyLimitExceeded) ||
						errors.Is(err, service.ErrWeeklyLimitExceeded) ||
						errors.Is(err, service.ErrMonthlyLimitExceeded) {
						status = 429
						code = "USAGE_LIMIT_EXCEEDED"
						reason = "subscription_limit_exceeded"
						allowProviderChange = true
					}
					if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, reason, code, allowProviderChange); ok {
						apiKey = switchedKey
						subscription = nil
						if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() && subscriptionService != nil {
							sub, subErr := subscriptionService.GetActiveSubscription(c.Request.Context(), apiKey.User.ID, apiKey.Group.ID)
							if subErr != nil {
								abortWithGoogleError(c, 403, "No active subscription found for this group")
								return
							}
							needsMaintenance, err = subscriptionService.ValidateAndCheckLimits(sub, apiKey.Group)
							if err != nil {
								switchedStatus := 403
								switchedCode := "SUBSCRIPTION_INVALID"
								if errors.Is(err, service.ErrDailyLimitExceeded) ||
									errors.Is(err, service.ErrWeeklyLimitExceeded) ||
									errors.Is(err, service.ErrMonthlyLimitExceeded) {
									switchedStatus = 429
									switchedCode = "USAGE_LIMIT_EXCEEDED"
								}
								abortWithGoogleBillingError(c, switchedStatus, switchedCode, err.Error())
								return
							}
							subscription = sub
						}
					} else {
						abortWithGoogleBillingError(c, status, code, err.Error())
						return
					}
				}

				if subscription != nil {
					c.Set(string(ContextKeySubscription), subscription)
				}

				if subscription != nil && needsMaintenance {
					maintenanceCopy := *subscription
					subscriptionService.DoWindowMaintenance(&maintenanceCopy)
				}
			}
		}
		if subscription == nil {
			if apiKey.User.Balance <= 0 {
				abortWithGoogleBillingError(c, 403, "INSUFFICIENT_BALANCE", "Insufficient account balance")
				return
			}
		}

		c.Set(string(ContextKeyAPIKey), apiKey)
		c.Set(string(ContextKeyUser), AuthSubject{
			UserID:      apiKey.User.ID,
			Concurrency: apiKey.User.Concurrency,
		})
		c.Set(string(ContextKeyUserRole), apiKey.User.Role)
		setGroupContext(c, apiKey.Group)
		_ = apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
		c.Next()
	}
}

// extractAPIKeyForGoogle extracts API key for Google/Gemini endpoints.
// Priority: x-goog-api-key > Authorization: Bearer > x-api-key > query key
// This allows OpenClaw and other clients using Bearer auth to work with Gemini endpoints.
func extractAPIKeyForGoogle(c *gin.Context) string {
	// 1) preferred: Gemini native header
	if k := strings.TrimSpace(c.GetHeader("x-goog-api-key")); k != "" {
		return k
	}

	// 2) fallback: Authorization: Bearer <key>
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			if k := strings.TrimSpace(parts[1]); k != "" {
				return k
			}
		}
	}

	// 3) x-api-key header (backward compatibility)
	if k := strings.TrimSpace(c.GetHeader("x-api-key")); k != "" {
		return k
	}

	// 4) query parameter key (for specific paths)
	if allowGoogleQueryKey(c.Request.URL.Path) {
		if v := strings.TrimSpace(c.Query("key")); v != "" {
			return v
		}
	}

	return ""
}

func allowGoogleQueryKey(path string) bool {
	return strings.HasPrefix(path, "/v1beta") || strings.HasPrefix(path, "/antigravity/v1beta")
}

func abortWithGoogleError(c *gin.Context, status int, message string) {
	message = clienterror.Message(status, message)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  googleapi.HTTPStatusToGoogleStatus(status),
		},
	})
	c.Abort()
}

func abortWithGoogleBillingError(c *gin.Context, status int, code, message string) {
	setBillingErrorHeaders(c, code)
	message = clienterror.Message(status, message)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    status,
			"message": message,
			"status":  googleapi.HTTPStatusToGoogleStatus(status),
		},
		"metadata": billingErrorMetadata(code),
	})
	c.Abort()
}
