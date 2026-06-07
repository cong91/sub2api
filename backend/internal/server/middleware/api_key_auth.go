package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// NewAPIKeyAuthMiddleware 创建 API Key 认证中间件
func NewAPIKeyAuthMiddleware(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, cfg *config.Config) APIKeyAuthMiddleware {
	return NewAPIKeyAuthMiddlewareWithEntitlements(apiKeyService, subscriptionService, nil, cfg)
}

// NewAPIKeyAuthMiddlewareWithEntitlements creates the gateway API-key middleware with
// server-side entitlement auto-switch enabled for quota/limit failures. This is the
// real provider traffic surface; renderer/titlebar hooks do not see these calls.
func NewAPIKeyAuthMiddlewareWithEntitlements(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, entitlementService *service.EntitlementService, cfg *config.Config) APIKeyAuthMiddleware {
	return APIKeyAuthMiddleware(apiKeyAuthWithSubscription(apiKeyService, subscriptionService, entitlementService, cfg))
}

// apiKeyAuthWithSubscription API Key认证中间件（支持订阅验证）
//
// 中间件职责分为两层：
//   - 鉴权（Authentication）：验证 Key 有效性、用户状态、IP 限制 —— 始终执行
//   - 计费执行（Billing Enforcement）：过期/配额/订阅/余额检查 —— skipBilling 时整块跳过
//
// /v1/usage 端点只需鉴权，不需要计费执行（允许过期/配额耗尽的 Key 查询自身用量）。
func apiKeyAuthWithSubscription(apiKeyService *service.APIKeyService, subscriptionService *service.SubscriptionService, entitlementService *service.EntitlementService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ── 1. 提取 API Key ──────────────────────────────────────────

		queryKey := strings.TrimSpace(c.Query("key"))
		queryApiKey := strings.TrimSpace(c.Query("api_key"))
		if queryKey != "" || queryApiKey != "" {
			AbortWithError(c, 400, "api_key_in_query_deprecated", "API key in query parameter is deprecated. Please use Authorization header instead.")
			return
		}

		// 尝试从Authorization header中提取API key (Bearer scheme)
		authHeader := c.GetHeader("Authorization")
		var apiKeyString string

		if authHeader != "" {
			// 验证Bearer scheme
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				apiKeyString = strings.TrimSpace(parts[1])
			}
		}

		// 如果Authorization header中没有，尝试从x-api-key header中提取
		if apiKeyString == "" {
			apiKeyString = c.GetHeader("x-api-key")
		}

		// 如果x-api-key header中没有，尝试从x-goog-api-key header中提取（Google/Gemini CLI兼容）
		if apiKeyString == "" {
			apiKeyString = c.GetHeader("x-goog-api-key")
		}

		// 如果所有header都没有API key
		if apiKeyString == "" {
			AbortWithError(c, 401, "API_KEY_REQUIRED", "API key is required in Authorization header (Bearer scheme), x-api-key header, or x-goog-api-key header")
			return
		}

		// ── 2. 验证 Key 存在 ─────────────────────────────────────────

		apiKey, err := apiKeyService.GetByKey(c.Request.Context(), apiKeyString)
		if err != nil {
			if errors.Is(err, service.ErrAPIKeyNotFound) {
				AbortWithError(c, 401, "INVALID_API_KEY", "Invalid API key")
				return
			}
			AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to validate API key")
			return
		}

		// apiKey 已加载（含 User/Group）。即便后续因分组停用/Key 停用/用户停用/
		// IP 限制等早退中断，也让 Ops 错误日志能回退取到 user/group/platform。
		SetOpsFallbackAPIKey(c, apiKey)

		// ── 3. 基础鉴权（始终执行） ─────────────────────────────────

		// disabled / 未知状态 → 无条件拦截（expired 和 quota_exhausted 留给计费阶段）
		if !apiKey.IsActive() &&
			apiKey.Status != service.StatusAPIKeyExpired &&
			apiKey.Status != service.StatusAPIKeyQuotaExhausted {
			AbortWithError(c, 401, "API_KEY_DISABLED", "API key is disabled")
			return
		}

		// 检查 IP 限制（白名单/黑名单）
		// 注意：错误信息故意模糊，避免暴露具体的 IP 限制机制
		if len(apiKey.IPWhitelist) > 0 || len(apiKey.IPBlacklist) > 0 {
			clientIP := ip.GetTrustedClientIP(c)
			if cfg.TrustForwardedIPForAPIKeyACL() {
				clientIP = ip.GetClientIP(c)
			}
			allowed, _ := ip.CheckIPRestrictionWithCompiledRules(clientIP, apiKey.CompiledIPWhitelist, apiKey.CompiledIPBlacklist)
			if !allowed {
				if clientIP == "" {
					clientIP = "unknown"
				}
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonIPRestriction)
				AbortWithError(c, 403, "ACCESS_DENIED", fmt.Sprintf("Access denied. Your IP is %s", clientIP))
				return
			}
		}

		// 检查关联的用户
		if apiKey.User == nil {
			AbortWithError(c, 401, "USER_NOT_FOUND", "User associated with API key not found")
			return
		}

		// 检查用户状态
		if !apiKey.User.IsActive() {
			AbortWithError(c, 401, "USER_INACTIVE", "User account is not active")
			return
		}
		if abortIfAPIKeyGroupUnavailable(c, apiKey) {
			return
		}
		if abortIfAPIKeyGroupNotAllowed(c, apiKey) {
			return
		}

		// ── 4. SimpleMode → early return ─────────────────────────────

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

		// ── 5. 加载订阅（订阅模式时始终加载） ───────────────────────

		// skipBilling: /v1/usage 只需鉴权，跳过所有计费执行
		skipBilling := c.Request.URL.Path == "/v1/usage"

		var subscription *service.UserSubscription
		isSubscriptionType := apiKey.Group != nil && apiKey.Group.IsSubscriptionType()

		if isSubscriptionType && subscriptionService != nil {
			sub, subErr := subscriptionService.GetActiveSubscription(
				c.Request.Context(),
				apiKey.User.ID,
				apiKey.Group.ID,
			)
			if subErr != nil {
				if !skipBilling {
					if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "subscription_not_found", "SUBSCRIPTION_NOT_FOUND", false); ok {
						apiKey = switchedKey
						subscription = nil
						isSubscriptionType = apiKey.Group != nil && apiKey.Group.IsSubscriptionType()
						if isSubscriptionType && subscriptionService != nil {
							sub, subErr = subscriptionService.GetActiveSubscription(c.Request.Context(), apiKey.User.ID, apiKey.Group.ID)
							if subErr != nil {
								AbortWithError(c, 403, "SUBSCRIPTION_NOT_FOUND", "No active subscription found for this group")
								return
							}
							subscription = sub
						}
					} else {
						AbortWithError(c, 403, "SUBSCRIPTION_NOT_FOUND", "No active subscription found for this group")
						return
					}
				}
				// skipBilling: 订阅不存在也放行，handler 会返回可用的数据
			} else {
				subscription = sub
			}
		}

		// ── 6. 计费执行（skipBilling 时整块跳过） ────────────────────

		if !skipBilling {
			// Key 状态检查
			switch apiKey.Status {
			case service.StatusAPIKeyQuotaExhausted:
				if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "api_key_quota_exhausted", "API_KEY_QUOTA_EXHAUSTED", false); ok {
					apiKey = switchedKey
					subscription = nil
					break
				}
				AbortWithError(c, 429, "API_KEY_QUOTA_EXHAUSTED", "API key 额度已用完")
				return
			case service.StatusAPIKeyExpired:
				AbortWithError(c, 403, "API_KEY_EXPIRED", "API key 已过期")
				return
			}

			// 运行时过期/配额检查（即使状态是 active，也要检查时间和用量）
			if apiKey.IsExpired() {
				AbortWithError(c, 403, "API_KEY_EXPIRED", "API key 已过期")
				return
			}
			if apiKey.IsQuotaExhausted() {
				if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "api_key_quota_exhausted", "API_KEY_QUOTA_EXHAUSTED", false); ok {
					apiKey = switchedKey
					subscription = nil
				} else {
					AbortWithError(c, 429, "API_KEY_QUOTA_EXHAUSTED", "API key 额度已用完")
					return
				}
			}

			// 订阅模式：验证订阅限额
			if subscription != nil {
				needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(subscription, apiKey.Group)
				if validateErr != nil {
					code := "SUBSCRIPTION_INVALID"
					status := 403
					if errors.Is(validateErr, service.ErrDailyLimitExceeded) ||
						errors.Is(validateErr, service.ErrWeeklyLimitExceeded) ||
						errors.Is(validateErr, service.ErrMonthlyLimitExceeded) {
						code = "USAGE_LIMIT_EXCEEDED"
						status = 429
						if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "subscription_limit_exceeded", code, true); ok {
							apiKey = switchedKey
							subscription = nil
						} else {
							AbortWithError(c, status, code, validateErr.Error())
							return
						}
					} else {
						AbortWithError(c, status, code, validateErr.Error())
						return
					}
				}

				// 窗口维护异步化（不阻塞请求）
				if subscription != nil && needsMaintenance {
					maintenanceCopy := *subscription
					subscriptionService.DoWindowMaintenance(&maintenanceCopy)
				}
			}
			if subscription == nil {
				// 非订阅模式 或 订阅模式但 subscriptionService 未注入：回退到余额检查。
				// If the user's hidden provider key is still bound to a balance group and the
				// wallet is empty, try to bind the same key to an active subscription group
				// before rejecting the request. Users do not manage these API keys directly,
				// so this keeps manual/admin subscription grants from interrupting traffic.
				if apiKey.User.Balance <= 0 {
					if switchedKey, ok := tryAutoSwitchAPIKey(c, apiKeyService, entitlementService, apiKey, "balance_insufficient", "INSUFFICIENT_BALANCE", false); ok {
						apiKey = switchedKey
						subscription = nil
						if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() && subscriptionService != nil {
							sub, subErr := subscriptionService.GetActiveSubscription(c.Request.Context(), apiKey.User.ID, apiKey.Group.ID)
							if subErr != nil {
								AbortWithError(c, 403, "SUBSCRIPTION_NOT_FOUND", "No active subscription found for this group")
								return
							}
							needsMaintenance, validateErr := subscriptionService.ValidateAndCheckLimits(sub, apiKey.Group)
							if validateErr != nil {
								code := "SUBSCRIPTION_INVALID"
								status := 403
								if errors.Is(validateErr, service.ErrDailyLimitExceeded) ||
									errors.Is(validateErr, service.ErrWeeklyLimitExceeded) ||
									errors.Is(validateErr, service.ErrMonthlyLimitExceeded) {
									code = "USAGE_LIMIT_EXCEEDED"
									status = 429
								}
								AbortWithError(c, status, code, validateErr.Error())
								return
							}
							if needsMaintenance {
								maintenanceCopy := *sub
								subscriptionService.DoWindowMaintenance(&maintenanceCopy)
							}
							subscription = sub
						}
					} else {
						AbortWithError(c, 403, "INSUFFICIENT_BALANCE", "Insufficient account balance")
						return
					}
				}
			}
		}

		// ── 7. 设置上下文 → Next ─────────────────────────────────────

		if subscription != nil {
			c.Set(string(ContextKeySubscription), subscription)
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

func tryAutoSwitchAPIKey(c *gin.Context, apiKeyService *service.APIKeyService, entitlementService *service.EntitlementService, apiKey *service.APIKey, reason, errorCode string, allowProviderChange bool) (*service.APIKey, bool) {
	if c == nil || apiKeyService == nil || entitlementService == nil || apiKey == nil || apiKey.User == nil {
		return nil, false
	}
	currentAPIKeyID := apiKey.ID
	var currentGroupID *int64
	if apiKey.GroupID != nil {
		groupID := *apiKey.GroupID
		currentGroupID = &groupID
	}
	providerID := ""
	if apiKey.Group != nil {
		providerID = providerIDForAutoSwitchPlatform(apiKey.Group.Platform)
	}
	result, err := entitlementService.AutoSwitchEntitlement(c.Request.Context(), apiKey.User.ID, service.AutoSwitchEntitlementRequest{
		Reason:              reason,
		ErrorCode:           errorCode,
		CurrentAPIKeyID:     &currentAPIKeyID,
		CurrentGroupID:      currentGroupID,
		ProviderID:          providerID,
		AllowAPIKeyChange:   false,
		AllowProviderChange: allowProviderChange,
		PreferCurrentAPIKey: true,
	})
	if err != nil || result == nil || !result.Switched || result.Target == nil {
		return nil, false
	}
	switchedKey, err := apiKeyService.GetByID(c.Request.Context(), result.Target.APIKeyID)
	if err != nil || switchedKey == nil || switchedKey.User == nil || switchedKey.User.ID != apiKey.User.ID {
		return nil, false
	}
	if !switchedKey.IsActive() || switchedKey.IsExpired() || switchedKey.IsQuotaExhausted() {
		return nil, false
	}
	if _, _, ok := validateAPIKeyGroupAvailable(switchedKey); !ok {
		return nil, false
	}
	c.Header("X-Sub2API-Auto-Switched", "true")
	c.Header("X-Sub2API-Auto-Switch-Action", result.Action)
	c.Header("X-Sub2API-Auto-Switch-Target-Group", result.Target.GroupName)
	return switchedKey, true
}

func providerIDForAutoSwitchPlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		return ""
	}
	return "v-claw-" + platform
}

// GetAPIKeyFromContext 从上下文中获取API key
func GetAPIKeyFromContext(c *gin.Context) (*service.APIKey, bool) {
	value, exists := c.Get(string(ContextKeyAPIKey))
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*service.APIKey)
	return apiKey, ok
}

// SetOpsFallbackAPIKey 记录已加载的 API Key，供 Ops 错误日志在鉴权早退时回退使用。
// 与 ContextKeyAPIKey 区分：写入它不代表请求已通过鉴权，因此不影响 handler、
// 审计日志等对“已鉴权”的判断。
func SetOpsFallbackAPIKey(c *gin.Context, apiKey *service.APIKey) {
	if c == nil || apiKey == nil {
		return
	}
	c.Set(string(ContextKeyOpsFallbackAPIKey), apiKey)
}

// GetOpsFallbackAPIKey 读取 Ops 错误日志专用的回退 API Key。
func GetOpsFallbackAPIKey(c *gin.Context) (*service.APIKey, bool) {
	value, exists := c.Get(string(ContextKeyOpsFallbackAPIKey))
	if !exists {
		return nil, false
	}
	apiKey, ok := value.(*service.APIKey)
	return apiKey, ok
}

// GetSubscriptionFromContext 从上下文中获取订阅信息
func GetSubscriptionFromContext(c *gin.Context) (*service.UserSubscription, bool) {
	value, exists := c.Get(string(ContextKeySubscription))
	if !exists {
		return nil, false
	}
	subscription, ok := value.(*service.UserSubscription)
	return subscription, ok
}

func setGroupContext(c *gin.Context, group *service.Group) {
	if !service.IsGroupContextValid(group) {
		return
	}
	if existing, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group); ok && existing != nil && existing.ID == group.ID && service.IsGroupContextValid(existing) {
		return
	}
	ctx := context.WithValue(c.Request.Context(), ctxkey.Group, group)
	c.Request = c.Request.WithContext(ctx)
}

func abortIfAPIKeyGroupUnavailable(c *gin.Context, apiKey *service.APIKey) bool {
	code, message, ok := validateAPIKeyGroupAvailable(apiKey)
	if ok {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
	AbortWithError(c, 403, code, message)
	return true
}

func abortIfAPIKeyGroupNotAllowed(c *gin.Context, apiKey *service.APIKey) bool {
	if validateAPIKeyGroupAllowed(apiKey) {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
	AbortWithError(c, 403, "GROUP_NOT_ALLOWED", "API Key 所属专属分组不再允许当前用户使用")
	return true
}

func validateAPIKeyGroupAllowed(apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.GroupID == nil || apiKey.User == nil || apiKey.Group == nil {
		return true
	}
	group := apiKey.Group
	if group.IsSubscriptionType() {
		return true
	}
	return apiKey.User.CanBindGroup(group.ID, group.IsExclusive)
}

func validateAPIKeyGroupAvailable(apiKey *service.APIKey) (string, string, bool) {
	if apiKey == nil || apiKey.GroupID == nil {
		return "", "", true
	}
	group := apiKey.Group
	if group == nil || strings.EqualFold(group.Status, "deleted") {
		return "GROUP_DELETED", "API Key 所属分组已删除", false
	}
	if !group.IsActive() {
		return "GROUP_DISABLED", "API Key 所属分组已停用", false
	}
	return "", "", true
}
